// XLSX sink. Renders findings as an Excel workbook (one file per
// scan_id at <rootDir>/<scan_id>.xlsx). Security/audit teams typically
// triage findings in spreadsheets; this lets them open the report
// directly without an NDJSON-to-CSV step.
//
// Memory + atomic-write model mirrors HTML/SARIF: in-memory
// accumulator per scan_id, rewritten atomically on every Write.
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

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	xlsxlib "github.com/xuri/excelize/v2"

	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

const XLSXDefaultMaxPerScan = 10_000

// XLSX writes one .xlsx workbook per scan_id.
type XLSX struct {
	rootDir    string
	maxPerScan int

	mu     sync.Mutex
	closed bool
	scans  map[string][]*v1.Finding
}

func NewXLSX(rootDir string) (*XLSX, error) {
	return NewXLSXN(rootDir, XLSXDefaultMaxPerScan)
}

func NewXLSXN(rootDir string, maxPerScan int) (*XLSX, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if maxPerScan <= 0 {
		maxPerScan = XLSXDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &XLSX{
		rootDir:    rootDir,
		maxPerScan: maxPerScan,
		scans:      make(map[string][]*v1.Finding),
	}, nil
}

func (x *XLSX) Name() string { return "xlsx_file" }

// writeXLSXSummary populates the Summary sheet with per-severity totals
// + a per-rule breakdown. Layout: two stacked tables separated by a
// blank row so a copy-paste into a doc carries the structure.
func writeXLSXSummary(f *xlsxlib.File, findings []*v1.Finding, headerStyle int) error {
	const sheet = "Summary"

	// Per-severity totals.
	sevCounts := map[string]int{}
	for _, fnd := range findings {
		sevCounts[fnd.Severity.String()]++
	}
	_ = f.SetCellValue(sheet, "A1", "severity")
	_ = f.SetCellValue(sheet, "B1", "count")
	_ = f.SetCellStyle(sheet, "A1", "B1", headerStyle)
	row := 2
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "SEVERITY_UNSPECIFIED"} {
		if sevCounts[sev] == 0 {
			continue
		}
		_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), sev)
		_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), sevCounts[sev])
		row++
	}
	// Total row (bold via header style).
	_ = f.SetCellValue(sheet, fmt.Sprintf("A%d", row), "TOTAL")
	_ = f.SetCellValue(sheet, fmt.Sprintf("B%d", row), len(findings))
	_ = f.SetCellStyle(sheet, fmt.Sprintf("A%d", row), fmt.Sprintf("B%d", row), headerStyle)
	row += 2

	// Per-rule breakdown.
	type ruleCount struct {
		rule string
		n    int
	}
	ruleMap := map[string]int{}
	for _, fnd := range findings {
		ruleMap[fnd.RuleId]++
	}
	ranked := make([]ruleCount, 0, len(ruleMap))
	for k, v := range ruleMap {
		ranked = append(ranked, ruleCount{k, v})
	}
	// Highest count first; ties broken alphabetically for deterministic output.
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
	findings := append(x.scans[f.ScanId], f)
	if len(findings) > x.maxPerScan {
		x.mu.Unlock()
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, x.maxPerScan)
	}
	x.scans[f.ScanId] = findings
	snapshot := make([]*v1.Finding, len(findings))
	copy(snapshot, findings)
	x.mu.Unlock()
	start := time.Now()
	err := x.flush(f.ScanId, snapshot)
	metrics.ObserveFlush(x.Name(), 1, "batch", time.Since(start).Seconds())
	return err
}

func (x *XLSX) Close() error {
	x.mu.Lock()
	x.closed = true
	x.scans = nil
	x.mu.Unlock()
	return nil
}

func (x *XLSX) flush(scanID string, findings []*v1.Finding) error {
	path := filepath.Join(x.rootDir, scanID+".xlsx")
	rootClean := filepath.Clean(x.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		return fmt.Errorf("sink: path %q escapes rootDir %q", path, x.rootDir)
	}

	f := xlsxlib.NewFile()
	defer f.Close()
	sheet := "Findings"
	if _, err := f.NewSheet(sheet); err != nil {
		return fmt.Errorf("sink: new sheet: %w", err)
	}
	if _, err := f.NewSheet("Summary"); err != nil {
		return fmt.Errorf("sink: new summary sheet: %w", err)
	}
	_ = f.DeleteSheet("Sheet1")
	// Findings is the primary sheet — open the workbook on it.
	if idx, err := f.GetSheetIndex(sheet); err == nil {
		f.SetActiveSheet(idx)
	}

	// Header row: bold, frozen.
	headers := []string{"severity", "rule_id", "file_path", "line", "secret", "finding_id", "ctx_before", "ctx_after"}
	for i, h := range headers {
		cell, _ := xlsxlib.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return fmt.Errorf("sink: set header: %w", err)
		}
	}
	headerStyle, _ := f.NewStyle(&xlsxlib.Style{
		Font: &xlsxlib.Font{Bold: true},
		Fill: xlsxlib.Fill{Type: "pattern", Color: []string{"#E5E7EB"}, Pattern: 1},
	})
	_ = f.SetCellStyle(sheet, "A1", "H1", headerStyle)
	_ = f.SetPanes(sheet, &xlsxlib.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	// Per-severity row fill — quick visual triage.
	severityStyle := map[string]int{}
	for sev, hex := range map[string]string{
		"CRITICAL": "#FDE8E8",
		"HIGH":     "#FEF3C7",
		"MEDIUM":   "#FFFBEB",
		"LOW":      "#E0F2FE",
	} {
		st, _ := f.NewStyle(&xlsxlib.Style{Fill: xlsxlib.Fill{Type: "pattern", Color: []string{hex}, Pattern: 1}})
		severityStyle[sev] = st
	}

	for i, fnd := range findings {
		row := i + 2
		path := fnd.FilePath
		if path == "" && len(fnd.Refs) > 0 {
			path = fnd.Refs[0].Path
		}
		values := []any{
			fnd.Severity.String(),
			fnd.RuleId,
			path,
			fnd.LineNumber,
			string(fnd.MatchedSecret),
			fnd.FindingId,
			joinBytesLines(fnd.ContextBefore),
			joinBytesLines(fnd.ContextAfter),
		}
		for col, v := range values {
			cell, _ := xlsxlib.CoordinatesToCellName(col+1, row)
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return fmt.Errorf("sink: set cell %s: %w", cell, err)
			}
		}
		if st, ok := severityStyle[fnd.Severity.String()]; ok {
			start, _ := xlsxlib.CoordinatesToCellName(1, row)
			end, _ := xlsxlib.CoordinatesToCellName(len(values), row)
			_ = f.SetCellStyle(sheet, start, end, st)
		}
	}

	// Reasonable column widths.
	_ = f.SetColWidth(sheet, "A", "A", 12)
	_ = f.SetColWidth(sheet, "B", "B", 26)
	_ = f.SetColWidth(sheet, "C", "C", 48)
	_ = f.SetColWidth(sheet, "D", "D", 8)
	_ = f.SetColWidth(sheet, "E", "E", 60)
	_ = f.SetColWidth(sheet, "F", "F", 38)
	_ = f.SetColWidth(sheet, "G", "H", 60)

	if err := writeXLSXSummary(f, findings, headerStyle); err != nil {
		return fmt.Errorf("sink: write summary: %w", err)
	}

	// excelize requires the file path to end in .xlsx (it detects the
	// format from extension). os.CreateTemp gives random-suffix-after-
	// extension which breaks that, so we mint the name manually with
	// crypto/rand to avoid collisions between concurrent workers
	// writing the same scan_id.
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpName := filepath.Join(x.rootDir, fmt.Sprintf(".%s.%s.xlsx", scanID, hex.EncodeToString(nonce[:])))
	if err := f.SaveAs(tmpName); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: save xlsx: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: rename to %s: %w", path, err)
	}
	return nil
}
