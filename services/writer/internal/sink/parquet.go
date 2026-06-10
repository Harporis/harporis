// Streaming Parquet sink. Writes findings as a Parquet workbook (one
// file per scan_id at <rootDir>/<scan_id>.parquet) for SIEM /
// data-warehouse ingestion (Athena, BigQuery, Spark, DuckDB, Polars
// all read Parquet natively). Pure-Go encoder via parquet-go.
//
// Streaming model:
//
//   * On the FIRST Write for a scan_id, the sink opens a tempfile and
//     a parquet.GenericWriter — both stay open for the lifetime of the
//     scan.
//   * Each subsequent Write appends one row to the GenericWriter. The
//     writer batches rows into row groups internally and flushes them
//     to disk as they fill (no per-Finding rewrite of the whole file).
//   * Finalize(scanID) — invoked by the writer's STATUS-consumer when
//     a terminal ScanState event arrives — closes the GenericWriter
//     (which writes the Parquet footer/index), fsyncs the file, and
//     renames the tempfile onto the final path. Only then does the
//     file become a valid Parquet document any reader can open.
//   * Close() drains every still-open scan in the same fashion on
//     writer shutdown.
//
// Asymptotic cost:
//   * Per Write: O(1) amortized (append to row buffer + occasional
//     row-group flush to disk).
//   * Total per scan: O(N) bytes written.
//   * Trade-off: the file is INCOMPLETE (no footer) while the scan
//     is running. A reader opening `<scan_id>.parquet.tmp-*` mid-scan
//     gets "invalid Parquet" until Finalize. SARIF/HTML/XLSX/PDF
//     keep the partial-parseable accumulator pattern for that reason.
//
// Single-writer-replica caveat:
//
// With multiple writer replicas (`docker compose up -d --scale writer=2`),
// findings for a single scan_id can land on DIFFERENT replicas via
// the WorkQueuePolicy round-robin. Each replica would open its own
// tempfile + GenericWriter for the same scan_id and they'd both try
// to rename onto the same final path. Last-writer-wins, with most
// findings lost. This is a known limitation; deploy with one writer
// replica (the default) until per-scan affinity / merge-on-finalize
// arrives. Same caveat already applies to the accumulator sinks.
//
// Schema is deliberately flat — context_before/context_after are
// joined with "\n" into one column each rather than emitted as
// Parquet LIST types, so downstream consumers (DuckDB `SELECT *`,
// Pandas `read_parquet`) get one row per finding with no nesting.

package sink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

// parquetSinkLabel is the {sink} label value used on the shared
// flush metrics (writer_sink_flush_*) so streaming Parquet shows
// up alongside the accumulator sinks in Prometheus.
const parquetSinkLabel = "parquet_file"

const ParquetDefaultMaxPerScan = 10_000

// Parquet writes one .parquet workbook per scan_id via streaming
// GenericWriter. Compatible with the legacy constructors (NewParquet /
// NewParquetN / NewParquetConfig). Reuses BatchConfig.FlushInterval as
// the IDLE TIMEOUT — when a scan has not received a Write for this
// long, the sink finalises it (closes the GenericWriter, writes
// footer, atomically renames). Default 5 s.
//
// The terminal-status event from HARPORIS_STATUS can ALSO trigger
// Finalize directly (via the Finalizer interface), but Parquet does
// not depend on it: even if the status event is delayed or missed,
// idle detection eventually closes the file.
type Parquet struct {
	rootDir     string
	maxPerScan  int
	idleTimeout time.Duration
	// replicaID disambiguates the per-scan final filename across writer
	// replicas. Empty string keeps the legacy single-file shape
	// (`<scan_id>.parquet`); non-empty stamps `<scan_id>.<replica_id>
	// .parquet` so each replica's output lives in its own file and the
	// "two replicas race to rename onto the same path" bug from
	// multi-replica deployments goes away. Operators with N>1 writer
	// replicas DuckDB-UNION the per-replica files for the full view:
	// `SELECT * FROM read_parquet('<scan_id>.*.parquet')`.
	replicaID string

	mu     sync.Mutex
	closed bool
	scans  map[string]*parquetScanState

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// SetReplicaID stamps replica_id into the per-scan final filename.
// Call once before any Write; never thread-safe vs. concurrent Writes.
func (p *Parquet) SetReplicaID(id string) { p.replicaID = id }

// parquetScanState owns one open GenericWriter + its tempfile for one
// scan_id. mu serialises Write/Close (parquet-go's GenericWriter is
// not safe for concurrent use on a single instance).
type parquetScanState struct {
	mu        sync.Mutex
	writer    *parquet.GenericWriter[parquetRow]
	file      *os.File
	tmpPath   string
	finalPath string
	rowCount  int
	closed    bool
	lastWrite time.Time
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

// NewParquetConfig accepts a BatchConfig. MaxPerScan caps in-memory
// rows per scan; FlushInterval is reused as the IDLE TIMEOUT for the
// per-scan finalisation sweeper. 0 disables the sweeper (only the
// HARPORIS_STATUS terminal Finalize or sink Close drains).
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
	// Ensure the Prometheus collectors exist before Write() reaches for
	// SinkPendingFindings (the accumulator constructor does the same in
	// its own NewBatchedAccumulator — keep parity so any caller, e.g.
	// writer-rebuild, works without a separate metrics setup step).
	metrics.Init()
	p := &Parquet{
		rootDir:     rootDir,
		maxPerScan:  cfg.MaxPerScan,
		idleTimeout: cfg.FlushInterval,
		scans:       make(map[string]*parquetScanState),
		stopCh:      make(chan struct{}),
	}
	if p.idleTimeout > 0 {
		p.wg.Add(1)
		go p.runIdleSweeper()
	}
	return p, nil
}

func (p *Parquet) Name() string { return "parquet_file" }

// Write appends one row to the open GenericWriter for f.ScanId. Opens
// a new writer + tempfile on first sighting of a scan_id.
func (p *Parquet) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}

	// Acquire / create the per-scan state under the sink lock so a
	// concurrent first-Write for the same scan_id doesn't race to open
	// two tempfiles.
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrSinkClosed
	}
	s, ok := p.scans[f.ScanId]
	if !ok {
		var err error
		s, err = p.newScanState(f.ScanId)
		if err != nil {
			p.mu.Unlock()
			return err
		}
		p.scans[f.ScanId] = s
	}
	p.mu.Unlock()

	// Serialize Writes/Close for this scan_id.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSinkClosed
	}
	if s.rowCount >= p.maxPerScan {
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, p.maxPerScan)
	}
	row := findingToParquetRow(f)
	if _, err := s.writer.Write([]parquetRow{row}); err != nil {
		return fmt.Errorf("sink: parquet write: %w", err)
	}
	s.rowCount++
	s.lastWrite = time.Now()
	metrics.SinkPendingFindings.WithLabelValues(parquetSinkLabel).Inc()
	return nil
}

// Finalize closes the per-scan GenericWriter (writing the Parquet
// footer + index), fsyncs the file, and atomically renames onto the
// final path. After Finalize the file is a valid Parquet document any
// reader can open. No-op for unknown scan_ids.
func (p *Parquet) Finalize(_ context.Context, scanID string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrSinkClosed
	}
	s, ok := p.scans[scanID]
	if !ok {
		p.mu.Unlock()
		return nil
	}
	delete(p.scans, scanID)
	p.mu.Unlock()
	return s.closeAndRename("terminal")
}

// Close drains every still-open scan (writes Parquet footers + renames
// every tempfile) and marks the sink as terminated. Idempotent.
func (p *Parquet) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	states := make([]*parquetScanState, 0, len(p.scans))
	for _, s := range p.scans {
		states = append(states, s)
	}
	p.scans = nil
	p.mu.Unlock()

	close(p.stopCh)
	p.wg.Wait()

	var firstErr error
	for _, s := range states {
		if err := s.closeAndRename("close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// runIdleSweeper ticks every idleTimeout/2 (with a sane lower bound)
// and finalises any scan whose last Write is older than idleTimeout.
// Catches the "scanner kept emitting findings past getter's COMPLETED"
// race that pure terminal-status finalisation suffers from.
func (p *Parquet) runIdleSweeper() {
	defer p.wg.Done()
	tickEvery := p.idleTimeout / 2
	if tickEvery < time.Second {
		tickEvery = time.Second
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.finalizeIdleLocked()
		}
	}
}

func (p *Parquet) finalizeIdleLocked() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	now := time.Now()
	var toFinalize []*parquetScanState
	for id, s := range p.scans {
		s.mu.Lock()
		idle := !s.lastWrite.IsZero() && now.Sub(s.lastWrite) >= p.idleTimeout
		s.mu.Unlock()
		if idle {
			toFinalize = append(toFinalize, s)
			delete(p.scans, id)
		}
	}
	p.mu.Unlock()

	for _, s := range toFinalize {
		_ = s.closeAndRename("idle")
	}
}

// Flush is a no-op for the streaming Parquet sink: row groups flush on
// their own size threshold; a partial scan has no valid file to flush
// to until Finalize / Close. Kept on the type so it satisfies the
// Sink-with-Flush convention used by the accumulator sinks (some
// tests poke it generically).
func (p *Parquet) Flush() error { return nil }

func (p *Parquet) newScanState(scanID string) (*parquetScanState, error) {
	finalName := scanID + ".parquet"
	if p.replicaID != "" {
		finalName = scanID + "." + p.replicaID + ".parquet"
	}
	finalPath := filepath.Join(p.rootDir, finalName)
	rootClean := filepath.Clean(p.rootDir)
	if !strings.HasPrefix(filepath.Clean(finalPath), rootClean+string(filepath.Separator)) {
		return nil, fmt.Errorf("sink: path %q escapes rootDir %q", finalPath, p.rootDir)
	}
	// Tempfile name pattern matches the accumulator-sink minted shape
	// (`.<scan_id>.<hex>.parquet`) so writer's SweepOrphanTempfiles
	// already recognises it and cleans up after crashes mid-scan.
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpPath := filepath.Join(p.rootDir, fmt.Sprintf(".%s.%s.parquet", scanID, hex.EncodeToString(nonce[:])))
	file, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("sink: parquet tempfile: %w", err)
	}
	writer := parquet.NewGenericWriter[parquetRow](file)
	return &parquetScanState{
		writer:    writer,
		file:      file,
		tmpPath:   tmpPath,
		finalPath: finalPath,
	}, nil
}

func (s *parquetScanState) closeAndRename(trigger string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	// Drain the pending gauge regardless of success — the rows already
	// went out of the in-memory buffer one way or another (either onto
	// disk or into a tempfile we're about to remove).
	rowCount := s.rowCount
	defer func() {
		if rowCount > 0 {
			metrics.SinkPendingFindings.WithLabelValues(parquetSinkLabel).Sub(float64(rowCount))
		}
	}()
	// Always try to clean up the tempfile on any error path; the
	// rename is the success edge.
	cleanup := func() {
		_ = s.file.Close()
		_ = os.Remove(s.tmpPath)
	}
	start := time.Now()
	if err := s.writer.Close(); err != nil {
		cleanup()
		return fmt.Errorf("sink: parquet writer close: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sink: parquet sync: %w", err)
	}
	if err := s.file.Close(); err != nil {
		_ = os.Remove(s.tmpPath)
		return fmt.Errorf("sink: parquet close: %w", err)
	}
	if err := os.Rename(s.tmpPath, s.finalPath); err != nil {
		_ = os.Remove(s.tmpPath)
		return fmt.Errorf("sink: parquet rename to %s: %w", s.finalPath, err)
	}
	// Emit on the shared flush metrics so streaming Parquet shows up
	// alongside the accumulator sinks. batchSize = rowCount sized this
	// finalization. trigger ∈ {terminal,idle,close}.
	metrics.ObserveFlush(parquetSinkLabel, rowCount, trigger, time.Since(start).Seconds())
	return nil
}

func findingToParquetRow(f *v1.Finding) parquetRow {
	return parquetRow{
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
	}
}
