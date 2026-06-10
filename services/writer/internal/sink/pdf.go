// PDF sink. Renders findings as a printable A4 report (one per
// scan_id at <rootDir>/<scan_id>.pdf). Pure Go — gopdf for the PDF
// engine, the official "Go" font family for typography. No system
// fonts, no headless Chromium, no wkhtmltopdf.
//
// Layout per page:
//   - Title row with the scan_id (clipped if very long)
//   - Per-severity counts under the title
//   - One card per finding: coloured left bar + rule + location +
//     truncated secret. Cards flow page-to-page automatically.
package sink

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
)

const PDFDefaultMaxPerScan = 10_000

type PDF struct {
	rootDir    string
	maskSecret bool
	acc        *BatchedAccumulator
}

// SetMaskSecrets toggles secret-masking in rendered cards. Off by
// default; mirrors HTML sink semantics.
func (p *PDF) SetMaskSecrets(on bool) { p.maskSecret = on }

func NewPDF(rootDir string) (*PDF, error) {
	return NewPDFN(rootDir, PDFDefaultMaxPerScan)
}

func NewPDFN(rootDir string, maxPerScan int) (*PDF, error) {
	return NewPDFConfig(rootDir, BatchConfig{MaxPerScan: maxPerScan})
}

func NewPDFConfig(rootDir string, cfg BatchConfig) (*PDF, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if cfg.MaxPerScan <= 0 {
		cfg.MaxPerScan = PDFDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	cfg.SinkLabel = "pdf_file"
	p := &PDF{rootDir: rootDir}
	p.acc = NewBatchedAccumulator(cfg, p.flush)
	return p, nil
}

func (p *PDF) Name() string { return "pdf_file" }

func (p *PDF) Write(ctx context.Context, f *v1.Finding) error {
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}
	return p.acc.Add(ctx, f)
}

func (p *PDF) Close() error { return p.acc.Close() }

func (p *PDF) Flush() error { return p.acc.Flush() }

// Finalize drains the pending buffer for scanID and drops state.
func (p *PDF) Finalize(_ context.Context, scanID string) error {
	return p.acc.Finalize(scanID)
}

// pdfStripControl makes a string safe for the PDF row: Go font covers
// the Basic Multilingual Plane but Helvetica-style fallbacks for
// non-printables look like boxes; collapsing to '.' / ' ' keeps the
// row visually intact.
func pdfStripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			b.WriteRune(' ')
		case r < 32:
			b.WriteByte('.')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderPDFContext draws the harvested context_before + matched_line
// + context_after as a monospaced block under a finding card. Line
// numbers are prefixed in grey; the matched line is rendered in the
// darker body colour to set it apart from surrounding rows. Long
// lines are clipped to keep cards from spilling past the right margin.
func renderPDFContext(pdf *gopdf.GoPdf, x, y float64, f *v1.Finding) {
	_ = pdf.SetFont("goregular", "", 8)
	const lineH = 10.0
	const maxChars = 100
	startLine := f.LineNumber - int32(len(f.ContextBefore))
	if startLine < 1 {
		startLine = 1
	}
	cy := y
	ln := startLine
	for _, b := range f.ContextBefore {
		pdf.SetTextColor(140, 140, 140)
		pdf.SetXY(x, cy)
		_ = pdf.Cell(nil, fmt.Sprintf("%4d  %s", ln, clipPDFLine(string(b), maxChars)))
		cy += lineH
		ln++
	}
	next := f.LineNumberEnd + 1
	if next <= 0 {
		next = f.LineNumber + 1
	}
	for _, a := range f.ContextAfter {
		pdf.SetTextColor(140, 140, 140)
		pdf.SetXY(x, cy)
		_ = pdf.Cell(nil, fmt.Sprintf("%4d  %s", next, clipPDFLine(string(a), maxChars)))
		cy += lineH
		next++
	}
}

func clipPDFLine(s string, max int) string {
	s = pdfStripControl(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func severityRGB(sev string) (uint8, uint8, uint8) {
	switch sev {
	case "CRITICAL":
		return 220, 38, 38
	case "HIGH":
		return 234, 88, 12
	case "MEDIUM":
		return 217, 119, 6
	case "LOW":
		return 14, 165, 233
	default:
		return 107, 114, 128
	}
}

func (p *PDF) flush(scanID string, findings []*v1.Finding) error {
	path := filepath.Join(p.rootDir, scanID+".pdf")
	rootClean := filepath.Clean(p.rootDir)
	if !strings.HasPrefix(filepath.Clean(path), rootClean+string(filepath.Separator)) {
		return fmt.Errorf("sink: path %q escapes rootDir %q", path, p.rootDir)
	}

	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData("goregular", goregular.TTF); err != nil {
		return fmt.Errorf("sink: load goregular: %w", err)
	}
	if err := pdf.AddTTFFontData("gobold", gobold.TTF); err != nil {
		return fmt.Errorf("sink: load gobold: %w", err)
	}

	const (
		marginX   = 40.0
		topY      = 40.0
		bottomCut = 760.0
		pageW     = 595.0
		rowH      = 60.0
	)
	contentW := pageW - 2*marginX

	// Page header is drawn at the top of every page; we know the total
	// page count only at the end, so capture each page's findings-list
	// start position and stamp footers in a second pass.
	addPage := func() {
		pdf.AddPage()
		_ = pdf.SetFont("gobold", "", 9)
		pdf.SetTextColor(80, 80, 80)
		pdf.SetXY(marginX, 16)
		_ = pdf.Cell(nil, "Harporis findings — "+scanID)
	}

	addPage()

	// Cover-page title + per-severity counts (only on first page).
	_ = pdf.SetFont("gobold", "", 16)
	pdf.SetTextColor(20, 20, 20)
	pdf.SetXY(marginX, topY)
	_ = pdf.Cell(nil, "Harporis findings — "+scanID)

	_ = pdf.SetFont("goregular", "", 10)
	pdf.SetTextColor(100, 100, 100)
	pdf.SetXY(marginX, topY+24)
	_ = pdf.Cell(nil, fmt.Sprintf("%d finding(s) in this scan", len(findings)))

	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity.String()]++
	}
	pdf.SetXY(marginX, topY+44)
	parts := []string{}
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"} {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", sev, counts[sev]))
		}
	}
	if len(parts) > 0 {
		_ = pdf.Cell(nil, strings.Join(parts, "    "))
	}

	// Findings list. Card height grows with the number of context lines
	// the scanner harvested (rowH = base; +ctxLineH per context line).
	y := topY + 70
	const ctxLineH = 10.0
	for _, f := range findings {
		ctxCount := len(f.ContextBefore) + len(f.ContextAfter)
		cardH := rowH
		if ctxCount > 0 {
			cardH += float64(ctxCount)*ctxLineH + 8
		}
		if y+cardH > bottomCut {
			addPage()
			y = topY
		}
		r, g, b := severityRGB(f.Severity.String())
		pdf.SetFillColor(r, g, b)
		pdf.RectFromUpperLeftWithStyle(marginX, y, 4, cardH, "F")
		pdf.SetFillColor(248, 248, 248)
		pdf.RectFromUpperLeftWithStyle(marginX+4, y, contentW-4, cardH, "F")

		_ = pdf.SetFont("gobold", "", 10)
		pdf.SetTextColor(r, g, b)
		pdf.SetXY(marginX+12, y+10)
		_ = pdf.Cell(nil, f.Severity.String())
		pdf.SetTextColor(30, 30, 30)
		pdf.SetXY(marginX+90, y+10)
		_ = pdf.Cell(nil, f.RuleId)

		_ = pdf.SetFont("goregular", "", 9)
		pdf.SetTextColor(80, 80, 80)
		fp := f.FilePath
		if fp == "" && len(f.Refs) > 0 {
			fp = f.Refs[0].Path
		}
		loc := fp
		if f.LineNumber > 0 {
			loc = fmt.Sprintf("%s:%d", fp, f.LineNumber)
		}
		pdf.SetXY(marginX+12, y+26)
		_ = pdf.Cell(nil, loc)

		secret := string(f.MatchedSecret)
		if p.maskSecret {
			secret = maskSecret(secret)
		}
		if len(secret) > 90 {
			secret = secret[:90] + "…"
		}
		secret = pdfStripControl(secret)
		pdf.SetTextColor(40, 40, 40)
		pdf.SetXY(marginX+12, y+42)
		_ = pdf.Cell(nil, secret)

		if ctxCount > 0 {
			renderPDFContext(&pdf, marginX+12, y+rowH, f)
		}

		y += cardH + 6
	}

	// Stamp Page N/M footers now that we know the total page count.
	// gopdf has SetPage(); iterate every page and draw the footer line.
	total := pdf.GetNumberOfPages()
	for p := 1; p <= total; p++ {
		pdf.SetPage(p)
		_ = pdf.SetFont("goregular", "", 8)
		pdf.SetTextColor(140, 140, 140)
		pdf.SetXY(marginX, 800)
		_ = pdf.Cell(nil, fmt.Sprintf("Page %d / %d", p, total))
	}

	// Atomic write via random-suffix tempfile + rename.
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	tmpName := filepath.Join(p.rootDir, fmt.Sprintf(".%s.%s.pdf", scanID, hex.EncodeToString(nonce[:])))
	if err := pdf.WritePdf(tmpName); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: write pdf: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("sink: rename to %s: %w", path, err)
	}
	return nil
}
