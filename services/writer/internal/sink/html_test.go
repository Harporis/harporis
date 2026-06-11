package sink

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestHTML_SelfContainedReportPerScan(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHTML(dir)
	if err != nil {
		t.Fatalf("NewHTML: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	ctx := context.Background()
	findings := []*v1.Finding{
		{ScanId: "s1", FindingId: "f1", RuleId: "aws-key", Severity: v1.Severity_CRITICAL, FilePath: "src/.env", LineNumber: 12, MatchedSecret: []byte("AKIAIOSFODNN7EXAMPLE")},
		{ScanId: "s1", FindingId: "f2", RuleId: "generic", Severity: v1.Severity_LOW, Refs: []*v1.CommitFileRef{{Path: "lib/util.go"}}, LineNumber: 1, MatchedSecret: []byte("ghp_xxx")},
	}
	for _, f := range findings {
		if err := h.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}
	// Streaming sink: file is only valid after Finalize.
	if err := h.Finalize(ctx, "s1"); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "s1.html"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	s := string(body)
	for _, want := range []string{
		"<!DOCTYPE html>",
		"Harporis findings",
		"s1",
		"aws-key",
		"src/.env",
		"AKIAIOSFODNN7EXAMPLE",
		"generic",
		"lib/util.go",                  // Refs fallback for empty FilePath
		`data-sev="CRITICAL"`,          // severity is now stamped per row;
		`data-sev="LOW"`,               // counts get rendered by JS at load.
		"<script>",                     // inline JS for sort/filter
		"localeCompare",                // sort code
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestHTML_RejectsBadScanID(t *testing.T) {
	dir := t.TempDir()
	h, err := NewHTML(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if err := h.Write(context.Background(), &v1.Finding{ScanId: "../etc/passwd", RuleId: "x"}); err == nil {
		t.Error("Write with traversal scan_id should error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".html") {
			t.Errorf("unexpected file: %s", e.Name())
		}
	}
}
