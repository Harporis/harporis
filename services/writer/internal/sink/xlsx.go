// Streaming XLSX sink. Renders findings as an Excel workbook (one
// file per scan_id at <rootDir>/<scan_id>.xlsx). Uses
// excelize.StreamWriter so the writer doesn't hold every cell in
// memory at once — for large scans this drops peak RSS from "O(N)
// cell-objects" to "O(W) row buffer".
//
// Streaming model:
//
//   * On the FIRST Write for a scan_id, the sink opens an in-memory
//     excelize.File + StreamWriter for the "Findings" sheet, writes
//     the header row, and stamps the styles.
//   * Each subsequent Write calls sw.SetRow on the next physical row
//     index. StreamWriter internally batches into XLSX shared-string
//     deltas + a row-stream temp; no per-Write tempfile rename.
//   * Per-Write the sink also bumps in-memory severity + rule
//     counters so the Summary sheet can be written at Finalize
//     without re-reading the Findings sheet.
//   * Finalize calls sw.Flush(), writes the Summary sheet (non-stream
//     — only a few rows), f.SaveAs(tempfile), then renames onto the
//     final path.
//   * Close() drains every still-open scan the same way at shutdown.

package sink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	xlsxlib "github.com/xuri/excelize/v2"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

const xlsxSinkLabel = "xlsx_file"

const XLSXDefaultMaxPerScan = 10_000

const xlsxMinIdleTimeout = 30 * time.Second

const xlsxFinalizedCap = 4096

// XLSX writes one streaming .xlsx workbook per scan_id.
type XLSX struct {
	rootDir     string
	maxPerScan  int
	idleTimeout time.Duration
	replicaID   string

	mu             sync.Mutex
	closed         bool
	scans          map[string]*xlsxScanState
	finalized      map[string]struct{}
	finalizedOrder []string

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type xlsxScanState struct {
	mu             sync.Mutex
	file           *xlsxlib.File
	sw             *xlsxlib.StreamWriter
	tmpPath        string
	finalPath      string
	rowCount       int   // physical row index (excluding the 1-indexed header)
	closed         bool
	lastWrite      time.Time
	headerStyle    int
	severityStyles map[string]int
	sevCounts      map[string]int
	ruleCounts     map[string]int
}

func NewXLSX(rootDir string) (*XLSX, error) {
	return NewXLSXN(rootDir, XLSXDefaultMaxPerScan)
}

func NewXLSXN(rootDir string, maxPerScan int) (*XLSX, error) {
	return NewXLSXConfig(rootDir, BatchConfig{MaxPerScan: maxPerScan})
}

func NewXLSXConfig(rootDir string, cfg BatchConfig) (*XLSX, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if cfg.MaxPerScan <= 0 {
		cfg.MaxPerScan = XLSXDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	metrics.Init()
	idleTimeout := cfg.FlushInterval
	if idleTimeout > 0 && idleTimeout < xlsxMinIdleTimeout {
		idleTimeout = xlsxMinIdleTimeout
	}
	x := &XLSX{
		rootDir:     rootDir,
		maxPerScan:  cfg.MaxPerScan,
		idleTimeout: idleTimeout,
		scans:       make(map[string]*xlsxScanState),
		finalized:   make(map[string]struct{}, xlsxFinalizedCap),
		stopCh:      make(chan struct{}),
	}
	if x.idleTimeout > 0 {
		x.wg.Add(1)
		go x.runIdleSweeper()
	}
	return x, nil
}

func (x *XLSX) Name() string { return xlsxSinkLabel }

func (x *XLSX) SetReplicaID(id string) { x.replicaID = id }

func (x *XLSX) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}

	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return ErrSinkClosed
	}
	if _, done := x.finalized[f.ScanId]; done {
		x.mu.Unlock()
		metrics.SinkPostFinalizeDropped.WithLabelValues(xlsxSinkLabel).Inc()
		return nil
	}
	st, ok := x.scans[f.ScanId]
	if !ok {
		var err error
		st, err = x.newScanState(f.ScanId)
		if err != nil {
			x.mu.Unlock()
			return err
		}
		x.scans[f.ScanId] = st
	}
	x.mu.Unlock()

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return ErrSinkClosed
	}
	if st.rowCount >= x.maxPerScan {
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, x.maxPerScan)
	}
	if err := st.appendRow(f); err != nil {
		return fmt.Errorf("sink: xlsx row: %w", err)
	}
	st.rowCount++
	st.lastWrite = time.Now()
	metrics.SinkPendingFindings.WithLabelValues(xlsxSinkLabel).Inc()
	return nil
}

func (x *XLSX) Finalize(_ context.Context, scanID string) error {
	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return ErrSinkClosed
	}
	st, ok := x.scans[scanID]
	if !ok {
		x.mu.Unlock()
		return nil
	}
	delete(x.scans, scanID)
	x.markFinalizedLocked(scanID)
	x.mu.Unlock()
	return st.closeAndRename("terminal")
}

func (x *XLSX) markFinalizedLocked(scanID string) {
	if _, exists := x.finalized[scanID]; exists {
		return
	}
	x.finalized[scanID] = struct{}{}
	x.finalizedOrder = append(x.finalizedOrder, scanID)
	for len(x.finalizedOrder) > xlsxFinalizedCap {
		evict := x.finalizedOrder[0]
		x.finalizedOrder = x.finalizedOrder[1:]
		delete(x.finalized, evict)
	}
}

func (x *XLSX) Close() error {
	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return nil
	}
	x.closed = true
	states := make([]*xlsxScanState, 0, len(x.scans))
	for _, st := range x.scans {
		states = append(states, st)
	}
	x.scans = nil
	x.mu.Unlock()

	close(x.stopCh)
	x.wg.Wait()

	var firstErr error
	for _, st := range states {
		if err := st.closeAndRename("close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (x *XLSX) Flush() error { return nil }

func (x *XLSX) runIdleSweeper() {
	defer x.wg.Done()
	tickEvery := x.idleTimeout / 2
	if tickEvery < time.Second {
		tickEvery = time.Second
	}
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-x.stopCh:
			return
		case <-t.C:
			x.finalizeIdleLocked()
		}
	}
}

func (x *XLSX) finalizeIdleLocked() {
	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return
	}
	now := time.Now()
	var toFinalize []*xlsxScanState
	for id, st := range x.scans {
		st.mu.Lock()
		idle := !st.lastWrite.IsZero() && now.Sub(st.lastWrite) >= x.idleTimeout
		st.mu.Unlock()
		if idle {
			toFinalize = append(toFinalize, st)
			delete(x.scans, id)
			x.markFinalizedLocked(id)
		}
	}
	x.mu.Unlock()

	for _, st := range toFinalize {
		_ = st.closeAndRename("idle")
	}
}

func (x *XLSX) newScanState(scanID string) (*xlsxScanState, error) {
	finalName := scanID + ".xlsx"
	if x.replicaID != "" {
		finalName = scanID + "." + x.replicaID + ".xlsx"
	}
	finalPath := filepath.Join(x.rootDir, finalName)
	rootClean := filepath.Clean(x.rootDir)
	if !strings.HasPrefix(filepath.Clean(finalPath), rootClean+string(filepath.Separator)) {
		return nil, fmt.Errorf("sink: path %q escapes rootDir %q", finalPath, x.rootDir)
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpPath := filepath.Join(x.rootDir, fmt.Sprintf(".%s.%s.xlsx", scanID, hex.EncodeToString(nonce[:])))

	f := xlsxlib.NewFile()
	const sheet = "Findings"
	if _, err := f.NewSheet(sheet); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sink: new sheet: %w", err)
	}
	if _, err := f.NewSheet("Summary"); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sink: new summary sheet: %w", err)
	}
	_ = f.DeleteSheet("Sheet1")
	if idx, err := f.GetSheetIndex(sheet); err == nil {
		f.SetActiveSheet(idx)
	}
	headerStyle, _ := f.NewStyle(&xlsxlib.Style{
		Font: &xlsxlib.Font{Bold: true},
		Fill: xlsxlib.Fill{Type: "pattern", Color: []string{"#E5E7EB"}, Pattern: 1},
	})
	severityStyles := map[string]int{}
	for sev, hexColor := range map[string]string{
		"CRITICAL": "#FDE8E8",
		"HIGH":     "#FEF3C7",
		"MEDIUM":   "#FFFBEB",
		"LOW":      "#E0F2FE",
	} {
		st, _ := f.NewStyle(&xlsxlib.Style{Fill: xlsxlib.Fill{Type: "pattern", Color: []string{hexColor}, Pattern: 1}})
		severityStyles[sev] = st
	}

	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sink: stream writer: %w", err)
	}
	headers := []any{"severity", "rule_id", "file_path", "line", "secret", "finding_id", "ctx_before", "ctx_after"}
	if err := sw.SetRow("A1", headers, xlsxlib.RowOpts{StyleID: headerStyle}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sink: header row: %w", err)
	}
	return &xlsxScanState{
		file:           f,
		sw:             sw,
		tmpPath:        tmpPath,
		finalPath:      finalPath,
		headerStyle:    headerStyle,
		severityStyles: severityStyles,
		sevCounts:      make(map[string]int),
		ruleCounts:     make(map[string]int),
	}, nil
}

// appendRow writes one finding to the streaming "Findings" sheet and
// bumps the in-memory summary counters.
func (st *xlsxScanState) appendRow(f *v1.Finding) error {
	path := f.FilePath
	if path == "" && len(f.Refs) > 0 {
		path = f.Refs[0].Path
	}
	row := []any{
		f.Severity.String(),
		f.RuleId,
		path,
		f.LineNumber,
		string(f.MatchedSecret),
		f.FindingId,
		joinBytesLines(f.ContextBefore),
		joinBytesLines(f.ContextAfter),
	}
	cell := fmt.Sprintf("A%d", st.rowCount+2) // +2 = 1 (1-indexed) + 1 (header)
	opts := []xlsxlib.RowOpts{}
	if sty, ok := st.severityStyles[f.Severity.String()]; ok {
		opts = append(opts, xlsxlib.RowOpts{StyleID: sty})
	}
	if err := st.sw.SetRow(cell, row, opts...); err != nil {
		return err
	}
	st.sevCounts[f.Severity.String()]++
	st.ruleCounts[f.RuleId]++
	return nil
}

func (st *xlsxScanState) closeAndRename(trigger string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
		return nil
	}
	st.closed = true
	rowCount := st.rowCount
	defer func() {
		if rowCount > 0 {
			metrics.SinkPendingFindings.WithLabelValues(xlsxSinkLabel).Sub(float64(rowCount))
		}
		metrics.SinkFlushTotal.WithLabelValues(xlsxSinkLabel, trigger).Inc()
	}()
	cleanup := func() {
		_ = st.file.Close()
		_ = os.Remove(st.tmpPath)
	}
	// Reasonable column widths on the streamed Findings sheet — must be
	// done before sw.Flush() since Flush finalises the sheet XML.
	const sheet = "Findings"
	_ = st.file.SetColWidth(sheet, "A", "A", 12)
	_ = st.file.SetColWidth(sheet, "B", "B", 26)
	_ = st.file.SetColWidth(sheet, "C", "C", 48)
	_ = st.file.SetColWidth(sheet, "D", "D", 8)
	_ = st.file.SetColWidth(sheet, "E", "E", 60)
	_ = st.file.SetColWidth(sheet, "F", "F", 38)
	_ = st.file.SetColWidth(sheet, "G", "H", 60)
	if err := st.sw.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("sink: xlsx stream flush: %w", err)
	}
	// Freeze the header row on the streamed sheet. After Flush we can no
	// longer use the StreamWriter, but f.SetPanes targets the sheet XML
	// directly and works post-flush.
	_ = st.file.SetPanes(sheet, &xlsxlib.Panes{
		Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
	})
	// Build the Summary sheet from in-memory counters (a few rows only,
	// no need to stream).
	if err := writeXLSXSummaryFromCounters(st.file, st.sevCounts, st.ruleCounts, st.headerStyle); err != nil {
		cleanup()
		return fmt.Errorf("sink: write summary: %w", err)
	}
	if err := st.file.SaveAs(st.tmpPath); err != nil {
		cleanup()
		return fmt.Errorf("sink: save xlsx: %w", err)
	}
	if err := st.file.Close(); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: close xlsx: %w", err)
	}
	if err := os.Rename(st.tmpPath, st.finalPath); err != nil {
		_ = os.Remove(st.tmpPath)
		return fmt.Errorf("sink: rename to %s: %w", st.finalPath, err)
	}
	return nil
}

// writeXLSXSummaryFromCounters populates the Summary sheet from
// pre-accumulated counters (vs. the legacy reader which iterated a
// findings slice). Layout matches the previous accumulator-based
// summary: severity table → blank row → per-rule breakdown.
func writeXLSXSummaryFromCounters(f *xlsxlib.File, sevCounts, ruleCounts map[string]int, headerStyle int) error {
	const sheet = "Summary"
	_ = f.SetCellValue(sheet, "A1", "severity")
	_ = f.SetCellValue(sheet, "B1", "count")
	_ = f.SetCellStyle(sheet, "A1", "B1", headerStyle)
	row := 2
	total := 0
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "SEVERITY_UNSPECIFIED"} {
		c := sevCounts[sev]
		total += c
		if c == 0 {
			continue
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), sev)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), c)
		row++
	}
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "TOTAL")
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), total)
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("B%d", row), headerStyle)
	row += 2

	type ruleCount struct {
		rule string
		n    int
	}
	ranked := make([]ruleCount, 0, len(ruleCounts))
	for k, v := range ruleCounts {
		ranked = append(ranked, ruleCount{k, v})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].n != ranked[j].n {
			return ranked[i].n > ranked[j].n
		}
		return ranked[i].rule < ranked[j].rule
	})

	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "rule_id")
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), "count")
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("B%d", row), headerStyle)
	row++
	for _, rc := range ranked {
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), rc.rule)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), rc.n)
		row++
	}
	_ = f.SetColWidth(sheet, "A", "A", 28)
	_ = f.SetColWidth(sheet, "B", "B", 10)
	return nil
}

// joinBytesLines glues a slice of raw line bytes with literal newlines
// for embedding into a single XLSX cell. Returns "" when the slice is
// empty so the cell stays visually empty rather than holding a stray
// newline character.
func joinBytesLines(lines [][]byte) string {
	if len(lines) == 0 {
		return ""
	}
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}
