package sink

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestPDF_GeneratesValidPDFPerScan(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPDF(dir)
	if err != nil {
		t.Fatalf("NewPDF: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	findings := []*v1.Finding{
		{ScanId: "p1", FindingId: "f1", RuleId: "aws-key", Severity: v1.Severity_CRITICAL, FilePath: "src/.env", LineNumber: 12, MatchedSecret: []byte("AKIAIOSFODNN7EXAMPLE")},
		{ScanId: "p1", FindingId: "f2", RuleId: "generic", Severity: v1.Severity_LOW, Refs: []*v1.CommitFileRef{{Path: "lib/util.go"}}, LineNumber: 1, MatchedSecret: []byte("ghp_xxx")},
	}
	for _, f := range findings {
		if err := p.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}
	path := filepath.Join(dir, "p1.pdf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pdf: %v", err)
	}
	// PDF magic header.
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Errorf("file does not start with %%PDF- magic, got %q", body[:8])
	}
	// EOF trailer.
	if !bytes.Contains(body[len(body)-1024:], []byte("%%EOF")) {
		t.Errorf("missing %%EOF trailer")
	}
	if len(body) < 1000 {
		t.Errorf("PDF suspiciously short: %d bytes", len(body))
	}
}

func TestPDF_RejectsBadScanID(t *testing.T) {
	dir := t.TempDir()
	p, err := NewPDF(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if err := p.Write(context.Background(), &v1.Finding{ScanId: "../etc/passwd", RuleId: "x"}); err == nil {
		t.Error("traversal scan_id should error")
	}
}

func TestPDFStripControl_CollapsesAndAllows(t *testing.T) {
	cases := map[string]string{
		"hello\nworld":  "hello world",
		"a\x00b":        "a.b",
		"plain ASCII":   "plain ASCII",
		"\tfoo\rbar":    " foo bar",
	}
	for in, want := range cases {
		if got := pdfStripControl(in); got != want {
			t.Errorf("pdfStripControl(%q) = %q, want %q", in, got, want)
		}
	}
}
