// Streaming SARIF sink. Writes a SARIF v2.1.0 report per scan_id to
// <rootDir>/<scan_id>.sarif. Mirrors the Parquet streaming model:
//
//   * On the FIRST Write for a scan_id, the sink opens a tempfile and
//     writes the SARIF document prefix up to the opening of
//     `runs[0].results: [`.
//   * Each subsequent Write appends one JSON-encoded `sarifResult` to
//     the open tempfile (with comma separators). O(1) amortised per
//     finding; O(N) total bytes written per scan.
//   * Finalize(scanID) closes the results array + runs entry + runs
//     array + outer object, fsyncs, and atomically renames the
//     tempfile onto `<scan_id>.sarif`. Only then is the file a valid
//     SARIF document.
//   * Close() drains every still-open scan the same way on writer
//     shutdown.
//
// Asymptotic cost:
//   * Per Write: O(1) — one JSON marshal of one result + a write(2).
//   * Total per scan: O(N) bytes.
//   * Trade-off: the file is INCOMPLETE (no closing brackets) while
//     the scan is running. SARIF readers opening `.<scan>.<hex>.sarif`
//     mid-scan get parse errors until Finalize. The accumulator
//     pattern previously used here gave partial-parseable files at
//     the cost of O(N²/B) bytes written.

package sink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

const sarifSinkLabel = "sarif_file"

// SARIFDefaultMaxPerScan caps the streamed results per scan. Past this
// limit Write returns an error (NAK + metric bump upstream).
const SARIFDefaultMaxPerScan = 10_000

// sarifMinIdleTimeout — same rationale as parquetMinIdleTimeout: the
// idle sweeper is a BACKSTOP for missing terminal events, while
// HARPORIS_STATUS terminal + finalize_grace_ms is the primary path.
const sarifMinIdleTimeout = 30 * time.Second

// sarifFinalizedCap bounds the post-finalize FIFO so stragglers from
// a finalized scan are dropped via metric (not silently clobbering).
const sarifFinalizedCap = 4096

// SARIF emits one streaming .sarif file per scan_id.
type SARIF struct {
	rootDir     string
	maxPerScan  int
	idleTimeout time.Duration
	replicaID   string

	mu             sync.Mutex
	closed         bool
	scans          map[string]*sarifScanState
	finalized      map[string]struct{}
	finalizedOrder []string

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type sarifScanState struct {
	mu        sync.Mutex
	file      *os.File
	bw        *bufferedJSONWriter
	tmpPath   string
	finalPath string
	rowCount  int
	closed    bool
	lastWrite time.Time
}

// bufferedJSONWriter is a thin wrapper that tracks whether the first
// result has been written, so subsequent results know to prefix a
// comma. Keeping this on the state means Write doesn't have to consult
// rowCount under a lock.
type bufferedJSONWriter struct {
	w     io.Writer
	first bool // true until the first result is appended
}

// NewSARIF constructs a streaming SARIF sink. Defaults are identical
// to the previous accumulator-backed constructor; existing callers
// (writer/main.go, writer-rebuild, tests) keep working unchanged.
func NewSARIF(rootDir string) (*SARIF, error) {
	return NewSARIFN(rootDir, SARIFDefaultMaxPerScan)
}

// NewSARIFN is the legacy explicit-cap constructor.
func NewSARIFN(rootDir string, maxPerScan int) (*SARIF, error) {
	return NewSARIFConfig(rootDir, BatchConfig{MaxPerScan: maxPerScan})
}

// NewSARIFConfig accepts a BatchConfig. MaxPerScan caps in-memory
// row count per scan; FlushInterval becomes the IDLE TIMEOUT for the
// per-scan finalisation sweeper (floored at sarifMinIdleTimeout to
// keep the sweeper a backstop). 0 disables the sweeper entirely.
func NewSARIFConfig(rootDir string, cfg BatchConfig) (*SARIF, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if cfg.MaxPerScan <= 0 {
		cfg.MaxPerScan = SARIFDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	metrics.Init()
	idleTimeout := cfg.FlushInterval
	if idleTimeout > 0 && idleTimeout < sarifMinIdleTimeout {
		idleTimeout = sarifMinIdleTimeout
	}
	s := &SARIF{
		rootDir:     rootDir,
		maxPerScan:  cfg.MaxPerScan,
		idleTimeout: idleTimeout,
		scans:       make(map[string]*sarifScanState),
		finalized:   make(map[string]struct{}, sarifFinalizedCap),
		stopCh:      make(chan struct{}),
	}
	if s.idleTimeout > 0 {
		s.wg.Add(1)
		go s.runIdleSweeper()
	}
	return s, nil
}

// Name returns the Prometheus sink label.
func (s *SARIF) Name() string { return sarifSinkLabel }

// SetReplicaID stamps replica_id into the per-scan final filename so
// multiple writer replicas don't race to rename onto the same path.
// Empty (default) keeps `<scan_id>.sarif`.
func (s *SARIF) SetReplicaID(id string) { s.replicaID = id }

// Write appends one SARIF result to the open tempfile for f.ScanId.
// Opens a new tempfile on the first sighting of a scan_id.
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
	if _, done := s.finalized[f.ScanId]; done {
		s.mu.Unlock()
		metrics.SinkPostFinalizeDropped.WithLabelValues(sarifSinkLabel).Inc()
		return nil
	}
	st, ok := s.scans[f.ScanId]
	if !ok {
		var err error
		st, err = s.newScanState(f.ScanId)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.scans[f.ScanId] = st
	}
	s.mu.Unlock()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ErrSinkClosed
	}
	if st.rowCount >= s.maxPerScan {
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, s.maxPerScan)
	}
	body, err := json.Marshal(findingToSARIF(f))
	if err != nil {
		return fmt.Errorf("sink: marshal sarif result: %w", err)
	}
	if !st.bw.first {
		if _, err := st.bw.w.Write([]byte(",\n  ")); err != nil {
			return fmt.Errorf("sink: write separator: %w", err)
		}
	} else {
		if _, err := st.bw.w.Write([]byte("\n  ")); err != nil {
			return fmt.Errorf("sink: write first indent: %w", err)
		}
		st.bw.first = false
	}
	if _, err := st.bw.w.Write(body); err != nil {
		return fmt.Errorf("sink: write result: %w", err)
	}
	st.rowCount++
	st.lastWrite = time.Now()
	metrics.SinkPendingFindings.WithLabelValues(sarifSinkLabel).Inc()
	return nil
}

// Finalize closes the SARIF document for scanID, fsyncs, and renames
// the tempfile onto the final path. No-op for unknown scan_ids.
func (s *SARIF) Finalize(_ context.Context, scanID string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSinkClosed
	}
	st, ok := s.scans[scanID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.scans, scanID)
	s.markFinalizedLocked(scanID)
	s.mu.Unlock()
	return st.closeAndRename("terminal")
}

func (s *SARIF) markFinalizedLocked(scanID string) {
	if _, exists := s.finalized[scanID]; exists {
		return
	}
	s.finalized[scanID] = struct{}{}
	s.finalizedOrder = append(s.finalizedOrder, scanID)
	for len(s.finalizedOrder) > sarifFinalizedCap {
		evict := s.finalizedOrder[0]
		s.finalizedOrder = s.finalizedOrder[1:]
		delete(s.finalized, evict)
	}
}

// Close drains every still-open scan (writes the closing brackets,
// renames). Idempotent.
func (s *SARIF) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	states := make([]*sarifScanState, 0, len(s.scans))
	for _, st := range s.scans {
		states = append(states, st)
	}
	s.scans = nil
	s.mu.Unlock()

	close(s.stopCh)
	s.wg.Wait()

	var firstErr error
	for _, st := range states {
		if err := st.closeAndRename("close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Flush is a no-op for the streaming SARIF sink — same shape as the
// streaming Parquet sink.
func (s *SARIF) Flush() error { return nil }

func (s *SARIF) runIdleSweeper() {
	defer s.wg.Done()
	tickEvery := s.idleTimeout / 2
	if tickEvery < time.Second {
		tickEvery = time.Second
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.finalizeIdleLocked()
		}
	}
}

func (s *SARIF) finalizeIdleLocked() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	var toFinalize []*sarifScanState
	for id, st := range s.scans {
		st.mu.Lock()
		idle := !st.lastWrite.IsZero() && now.Sub(st.lastWrite) >= s.idleTimeout
		st.mu.Unlock()
		if idle {
			toFinalize = append(toFinalize, st)
			delete(s.scans, id)
			s.markFinalizedLocked(id)
		}
	}
	s.mu.Unlock()

	for _, st := range toFinalize {
		_ = st.closeAndRename("idle")
	}
}

func (s *SARIF) newScanState(scanID string) (*sarifScanState, error) {
	finalName := scanID + ".sarif"
	if s.replicaID != "" {
		finalName = scanID + "." + s.replicaID + ".sarif"
	}
	finalPath := filepath.Join(s.rootDir, finalName)
	rootClean := filepath.Clean(s.rootDir)
	if !strings.HasPrefix(filepath.Clean(finalPath), rootClean+string(filepath.Separator)) {
		return nil, fmt.Errorf("sink: path %q escapes rootDir %q", finalPath, s.rootDir)
	}
	// Tempfile name pattern matches SweepOrphanTempfiles' mint shape so
	// orphan tempfiles from a crash mid-scan are cleaned up on startup.
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpPath := filepath.Join(s.rootDir, fmt.Sprintf(".%s.%s.sarif", scanID, hex.EncodeToString(nonce[:])))
	file, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("sink: sarif tempfile: %w", err)
	}
	// Write the SARIF document prefix up to the start of the results
	// array. The closing brackets (`]}]}`) land on Finalize/Close.
	prefix := []byte(`{` + "\n" +
		`  "$schema": "https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json",` + "\n" +
		`  "version": "2.1.0",` + "\n" +
		`  "runs": [{` + "\n" +
		`    "tool": {"driver": {"name": "harporis", "informationUri": "https://github.com/Harporis/harporis"}},` + "\n" +
		`    "results": [`)
	if _, err := file.Write(prefix); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("sink: sarif prefix: %w", err)
	}
	return &sarifScanState{
		file:      file,
		bw:        &bufferedJSONWriter{w: file, first: true},
		tmpPath:   tmpPath,
		finalPath: finalPath,
	}, nil
}

func (st *sarifScanState) closeAndRename(trigger string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.closed = true
	rowCount := st.rowCount
	defer func() {
		if rowCount > 0 {
			metrics.SinkPendingFindings.WithLabelValues(sarifSinkLabel).Sub(float64(rowCount))
		}
		metrics.SinkFlushTotal.WithLabelValues(sarifSinkLabel, trigger).Inc()
	}()
	cleanup := func() {
		_ = st.file.Close()
		_ = os.Remove(st.tmpPath)
	}
	// Close results array + runs entry + runs array + outer object.
	closer := []byte("\n  ]\n  }]\n}\n")
	if _, err := st.file.Write(closer); err != nil {
		cleanup()
		return fmt.Errorf("sink: sarif closer: %w", err)
	}
	if err := st.file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sink: sarif sync: %w", err)
	}
	if err := st.file.Close(); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: sarif close: %w", err)
	}
	if err := os.Rename(st.tmpPath, st.finalPath); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: sarif rename to %s: %w", st.finalPath, err)
	}
	return nil
}

// --- SARIF v2.1.0 wire types (kept for downstream readers + tests) ---

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
	Level               string            `json:"level"`
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
	StartLine int32         `json:"startLine,omitempty"`
	EndLine   int32         `json:"endLine,omitempty"`
	Snippet   *sarifSnippet `json:"snippet,omitempty"`
}

type sarifSnippet struct {
	Text string `json:"text"`
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

func buildContextRegion(f *v1.Finding) *sarifRegion {
	if len(f.ContextBefore) == 0 && len(f.ContextAfter) == 0 {
		return nil
	}
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
