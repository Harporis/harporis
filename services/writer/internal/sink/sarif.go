// SARIF v2.1.0 sink. Each scan_id materializes to <rootDir>/<scan_id>.sarif
// containing a SARIF document with one `run` and N `result` entries. On
// every Write the in-memory accumulator is rewritten to disk atomically
// (tempfile + rename) so the file is always a parseable SARIF report.
//
// Tradeoffs:
//   - Memory: bounded by maxPerScan findings per active scan_id. Past
//     that, Write fails and the message Naks (with metric bump) instead
//     of letting the writer OOM under a runaway producer.
//   - I/O: each Write triggers a full re-serialize + tempfile + rename
//     for the affected scan. Typical secret-scan output sizes (≤ a few
//     thousand findings) keep this in the low-ms range. For pathological
//     scans the operator should disable SARIF and rely on NDJSON.
//   - Security: scan_id is validated via kit/scan, same as NDJSON. The
//     containment check in flush() is belt-and-suspenders.

package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
)

// SARIFDefaultMaxPerScan caps the in-memory findings accumulator per scan.
// Past this, Write returns an error (the caller will Nak with a metric
// bump). 10k is comfortable for typical secret-scan output and well
// below the 64-bit JSON array limit any tool consumes.
const SARIFDefaultMaxPerScan = 10_000

// SARIF emits one SARIF v2.1.0 report per scan_id to rootDir. The file
// is rewritten on every Write so a partial scan is always inspectable.
type SARIF struct {
	rootDir    string
	maxPerScan int

	mu     sync.Mutex
	closed bool
	scans  map[string][]*v1.Finding
}

// NewSARIF constructs a SARIF sink with the default per-scan cap.
func NewSARIF(rootDir string) (*SARIF, error) {
	return NewSARIFN(rootDir, SARIFDefaultMaxPerScan)
}

// NewSARIFN exposes the per-scan accumulator cap for tests.
func NewSARIFN(rootDir string, maxPerScan int) (*SARIF, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if maxPerScan <= 0 {
		maxPerScan = SARIFDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &SARIF{
		rootDir:    rootDir,
		maxPerScan: maxPerScan,
		scans:      make(map[string][]*v1.Finding),
	}, nil
}

// Name returns the sink identifier used as a Prometheus label.
func (s *SARIF) Name() string { return "sarif_file" }

// Write appends f to the per-scan accumulator and rewrites the SARIF
// file. Returns ErrSinkClosed after Close has run.
func (s *SARIF) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSinkClosed
	}
	findings := append(s.scans[f.ScanId], f)
	if len(findings) > s.maxPerScan {
		s.mu.Unlock()
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, s.maxPerScan)
	}
	s.scans[f.ScanId] = findings
	snapshot := make([]*v1.Finding, len(findings))
	copy(snapshot, findings)
	s.mu.Unlock()

	return s.flush(f.ScanId, snapshot)
}

// Close discards in-memory accumulators. Files on disk are left in their
// last-written state (already valid SARIF reports). Idempotent.
func (s *SARIF) Close() error {
	s.mu.Lock()
	s.closed = true
	s.scans = nil
	s.mu.Unlock()
	return nil
}

func (s *SARIF) flush(scanID string, findings []*v1.Finding) error {
	path := filepath.Join(s.rootDir, scanID+".sarif")
	rootClean := filepath.Clean(s.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		return fmt.Errorf("sink: path %q escapes rootDir %q", path, s.rootDir)
	}
	doc := buildSARIF(findings)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("sink: marshal sarif: %w", err)
	}
	tmp, err := os.CreateTemp(s.rootDir, scanID+".sarif.tmp-*")
	if err != nil {
		return fmt.Errorf("sink: tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("sink: write tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sink: sync tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: close tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: rename to %s: %w", path, err)
	}
	return nil
}

// --- SARIF v2.1.0 wire types ---

type sarifReport struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string `json:"name"`
	InformationURI string `json:"informationUri,omitempty"`
	Version        string `json:"version,omitempty"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId,omitempty"`
	Level               string            `json:"level"` // note/warning/error
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           *sarifRegion          `json:"region,omitempty"`
	ContextRegion    *sarifRegion          `json:"contextRegion,omitempty"`
}

type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine int32          `json:"startLine,omitempty"`
	EndLine   int32          `json:"endLine,omitempty"`
	Snippet   *sarifSnippet  `json:"snippet,omitempty"`
}

type sarifSnippet struct {
	Text string `json:"text"`
}

func buildSARIF(findings []*v1.Finding) sarifReport {
	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		results = append(results, findingToSARIF(f))
	}
	return sarifReport{
		Schema:  "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "harporis",
				InformationURI: "https://github.com/Harporis/harporis",
			}},
			Results: results,
		}},
	}
}

func findingToSARIF(f *v1.Finding) sarifResult {
	r := sarifResult{
		RuleID:  f.RuleId,
		Level:   sarifLevel(f.Severity),
		Message: sarifMessage{Text: f.RuleId + " match"},
		PartialFingerprints: map[string]string{
			"finding_id": f.FindingId,
		},
	}
	region := buildRegion(f.LineNumber, f.LineNumberEnd)
	context := buildContextRegion(f)
	// Prefer the DIFF_WINDOW shape (FilePath + LineNumber directly on
	// the Finding); fall back to BLOB-source refs (one location per ref).
	if f.FilePath != "" {
		r.Locations = []sarifLocation{{
			PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifactLocation{URI: f.FilePath},
				Region:           region,
				ContextRegion:    context,
			},
		}}
	} else if len(f.Refs) > 0 {
		r.Locations = make([]sarifLocation, 0, len(f.Refs))
		for _, ref := range f.Refs {
			if ref == nil || ref.Path == "" {
				continue
			}
			r.Locations = append(r.Locations, sarifLocation{
				PhysicalLocation: sarifPhysicalLocation{
					ArtifactLocation: sarifArtifactLocation{URI: ref.Path},
					Region:           region,
					ContextRegion:    context,
				},
			})
		}
	}
	return r
}

func buildRegion(start, end int32) *sarifRegion {
	if start <= 0 {
		return nil
	}
	r := &sarifRegion{StartLine: start}
	if end > start {
		r.EndLine = end
	}
	return r
}

// buildContextRegion folds the harvested context_before + matched_line
// + context_after into a single SARIF contextRegion. Returns nil when
// no context was harvested (preserves the pre-context-feature shape).
func buildContextRegion(f *v1.Finding) *sarifRegion {
	if len(f.ContextBefore) == 0 && len(f.ContextAfter) == 0 {
		return nil
	}
	// startLine of the context window = line_number - len(context_before),
	// clamped to 1 because line numbers are 1-based and the harvester
	// may not have padded all the way (chunk-edge truncation).
	start := f.LineNumber - int32(len(f.ContextBefore))
	if start < 1 {
		start = 1
	}
	lines := make([]string, 0, len(f.ContextBefore)+1+len(f.ContextAfter))
	for _, ln := range f.ContextBefore {
		lines = append(lines, string(ln))
	}
	lines = append(lines, string(f.MatchedLine))
	for _, ln := range f.ContextAfter {
		lines = append(lines, string(ln))
	}
	return &sarifRegion{
		StartLine: start,
		Snippet:   &sarifSnippet{Text: strings.Join(lines, "\n")},
	}
}

func sarifLevel(sev v1.Severity) string {
	switch sev {
	case v1.Severity_CRITICAL, v1.Severity_HIGH:
		return "error"
	case v1.Severity_MEDIUM:
		return "warning"
	default:
		return "note"
	}
}
