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
	"sync"

	"github.com/signintech/gopdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kitscan "github.com/Harporis/harporis/kit/scan"
)

const PDFDefaultMaxPerScan = 10_000

type PDF struct {
	rootDir    string
	maxPerScan int

	mu     sync.Mutex
	closed bool
	scans  map[string][]*v1.Finding
}

func NewPDF(rootDir string) (*PDF, error) {
	return NewPDFN(rootDir, PDFDefaultMaxPerScan)
}

func NewPDFN(rootDir string, maxPerScan int) (*PDF, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("sink: rootDir is required")
	}
	if maxPerScan <= 0 {
		maxPerScan = PDFDefaultMaxPerScan
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("sink: mkdir %s: %w", rootDir, err)
	}
	return &PDF{
		rootDir:    rootDir,
		maxPerScan: maxPerScan,
		scans:      make(map[string][]*v1.Finding),
	}, nil
}

func (p *PDF) Name() string { return "pdf_file" }

func (p *PDF) Write(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("sink: nil Finding")
	}
	if err := kitscan.ValidateScanID(f.ScanId); err != nil {
		return fmt.Errorf("sink: %w", err)
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrSinkClosed
	}
	findings := append(p.scans[f.ScanId], f)
	if len(findings) > p.maxPerScan {
		p.mu.Unlock()
		return fmt.Errorf("sink: scan %s exceeded max %d findings", f.ScanId, p.maxPerScan)
	}
	p.scans[f.ScanId] = findings
	snapshot := make([]*v1.Finding, len(findings))
	copy(snapshot, findings)
	p.mu.Unlock()
	return p.flush(f.ScanId, snapshot)
}

func (p *PDF) Close() error {
	p.mu.Lock()
	p.closed = true
	p.scans = nil
	p.mu.Unlock()
	return nil
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

	pdf.AddPage()
	const (
		marginX   = 40.0
		bottomCut = 770.0
		pageW     = 595.0
		rowH      = 60.0
	)
	contentW := pageW - 2*marginX

	// Title.
	_ = pdf.SetFont("gobold", "", 16)
	pdf.SetTextColor(20, 20, 20)
	pdf.SetXY(marginX, 40)
	_ = pdf.Cell(nil, "Harporis findings — "+scanID)

	_ = pdf.SetFont("goregular", "", 10)
	pdf.SetTextColor(100, 100, 100)
	pdf.SetXY(marginX, 64)
	_ = pdf.Cell(nil, fmt.Sprintf("%d finding(s) in this scan", len(findings)))

	// Per-severity counts under the subtitle.
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity.String()]++
	}
	pdf.SetXY(marginX, 84)
	parts := []string{}
	for _, sev := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW"} {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", sev, counts[sev]))
		}
	}
	if len(parts) > 0 {
		_ = pdf.Cell(nil, strings.Join(parts, "    "))
	}

	// Findings list.
	y := 110.0
	for _, f := range findings {
		if y+rowH > bottomCut {
			pdf.AddPage()
			y = 40
		}
		r, g, b := severityRGB(f.Severity.String())
		pdf.SetFillColor(r, g, b)
		pdf.RectFromUpperLeftWithStyle(marginX, y, 4, rowH, "F")
		pdf.SetFillColor(248, 248, 248)
		pdf.RectFromUpperLeftWithStyle(marginX+4, y, contentW-4, rowH, "F")

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
		if len(secret) > 90 {
			secret = secret[:90] + "…"
		}
		secret = pdfStripControl(secret)
		pdf.SetTextColor(40, 40, 40)
		pdf.SetXY(marginX+12, y+42)
		_ = pdf.Cell(nil, secret)
		y += rowH + 6
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
