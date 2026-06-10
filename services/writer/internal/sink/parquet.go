// Parquet sink. Renders findings as a Parquet workbook (one file per
// scan_id at <rootDir>/<scan_id>.parquet) for SIEM / data-warehouse
// ingestion (Athena, BigQuery, Spark, DuckDB, Polars all read
// Parquet natively). Pure-Go encoder via github.com/parquet-go —
// no CGO, no Arrow runtime, no JVM.
//
// Memory + atomic-write model mirrors HTML/SARIF/XLSX/PDF: in-memory
// accumulator per scan_id, rewritten atomically (crypto/rand
// tempfile suffix + rename) on every Write. NDJSON is the only sink
// that streams append-only.
//
// Schema is deliberately flat — context_before / context_after are
// joined with "\n" into one column each rather than emitted as Parquet
// LIST types, so downstream consumers (DuckDB `SELECT *`, Pandas
// `read_parquet`) get one row per finding with no nesting.

package sink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/parquet-go/parquet-go"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
)

const ParquetDefaultMaxPerScan = 10_000

// Parquet writes one .parquet workbook per scan_id.
type Parquet struct {
	rootDir string
	acc     *BatchedAccumulator
}

// parquetRow is the on-disk row schema. Struct tag names match the
// Finding proto field names (snake_case) so SELECT * matches what an
// operator sees in NDJSON / SARIF. Compression is zstd on the
// high-cardinality / large-text columns; default snappy on the rest.
type parquetRow struct {
	ScanID          string  `parquet:"scan_id"`
	FindingID       string  `parquet:"finding_id"`
	RuleID          string  `parquet:"rule_id"`
	Severity        string  `parquet:"severity"`
	FilePath        string  `parquet:"file_path,zstd"`
	LineNumber      int32   `parquet:"line_number"`
	LineNumberEnd   int32   `parquet:"line_number_end"`
	ByteOffset      int64   `parquet:"byte_offset"`
	MatchedSecret   string  `parquet:"matched_secret,zstd"`
	MatchedLine     string  `parquet:"matched_line,zstd"`
	EntropyScore    float64 `parquet:"entropy_score"`
	DetectedAtMs    int64   `parquet:"detected_at_ms"`
	DetectorVersion string  `parquet:"detector_version"`
	ContextBefore   string  `parquet:"context_before,zstd"`
	ContextAfter    string  `parquet:"context_after,zstd"`
}

func NewParquet(rootDir string) (*Parquet, error) {
	return NewParquetN(rootDir, ParquetDefaultMaxPerScan)
}

func NewParquetN(rootDir string, maxPerScan int) (*Parquet, error) {
	return NewParquetConfig(rootDir, BatchConfig{MaxPerScan: maxPerScan})
}

func NewParquetConfig(rootDir string, cfg BatchConfig) (*Parquet, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if cfg.MaxPerScan <= 0 {
		cfg.MaxPerScan = ParquetDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	cfg.SinkLabel = "parquet_file"
	p := &Parquet{rootDir: rootDir}
	p.acc = NewBatchedAccumulator(cfg, p.flush)
	return p, nil
}

func (p *Parquet) Name() string { return "parquet_file" }

func (p *Parquet) Write(ctx context.Context, f *v1.Finding) error {
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}
	return p.acc.Add(ctx, f)
}

func (p *Parquet) Close() error { return p.acc.Close() }

func (p *Parquet) Flush() error { return p.acc.Flush() }

func (p *Parquet) flush(scanID string, findings []*v1.Finding) error {
	path := filepath.Join(p.rootDir, scanID+".parquet")
	rootClean := filepath.Clean(p.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		return fmt.Errorf("sink: path %q escapes rootDir %q", path, p.rootDir)
	}

	rows := make([]parquetRow, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, parquetRow{
			ScanID:          f.ScanId,
			FindingID:       f.FindingId,
			RuleID:          f.RuleId,
			Severity:        f.Severity.String(),
			FilePath:        f.FilePath,
			LineNumber:      f.LineNumber,
			LineNumberEnd:   f.LineNumberEnd,
			ByteOffset:      f.ByteOffset,
			MatchedSecret:   string(f.MatchedSecret),
			MatchedLine:     string(f.MatchedLine),
			EntropyScore:    f.EntropyScore,
			DetectedAtMs:    f.DetectedAtMs,
			DetectorVersion: f.DetectorVersion,
			ContextBefore:   joinBytesLines(f.ContextBefore),
			ContextAfter:    joinBytesLines(f.ContextAfter),
		})
	}

	// Atomic write via random-suffix tempfile + rename. The
	// parquet-go writer needs a true os.File (it seeks during close),
	// so build the tempfile first then rename onto the final path.
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpName := filepath.Join(p.rootDir, fmt.Sprintf(".%s.%s.parquet", scanID, hex.EncodeToString(nonce[:])))
	tmp, err := os.Create(tmpName)
	if err != nil {
		return fmt.Errorf("sink: tempfile: %w", err)
	}
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := parquet.Write(tmp, rows); err != nil {
		cleanup()
		return fmt.Errorf("sink: write parquet: %w", err)
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
