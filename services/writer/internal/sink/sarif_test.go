package sink

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestSARIF_WritesValidReportPerScan(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSARIF(dir)
	if err != nil {
		t.Fatalf("NewSARIF: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	findings := []*v1.Finding{
		{ScanId: "scan-a", FindingId: "f1", RuleId: "aws-key", Severity: v1.Severity_CRITICAL, FilePath: "src/.env", LineNumber: 12, LineNumberEnd: 12},
		{ScanId: "scan-a", FindingId: "f2", RuleId: "generic", Severity: v1.Severity_LOW, FilePath: "lib/util.go", LineNumber: 88},
		{ScanId: "scan-b", FindingId: "f3", RuleId: "pem", Severity: v1.Severity_HIGH, FilePath: "keys/id_rsa", LineNumber: 1},
	}
	for _, f := range findings {
		if err := s.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}

	// scan-a has 2 findings.
	pathA := filepath.Join(dir, "scan-a.sarif")
	bA, err := os.ReadFile(pathA)
	if err != nil {
		t.Fatalf("read scan-a: %v", err)
	}
	var docA sarifReport
	if err := json.Unmarshal(bA, &docA); err != nil {
		t.Fatalf("unmarshal scan-a: %v", err)
	}
	if docA.Version != "2.1.0" {
		t.Errorf("scan-a version = %q, want 2.1.0", docA.Version)
	}
	if len(docA.Runs) != 1 || docA.Runs[0].Tool.Driver.Name != "harporis" {
		t.Errorf("scan-a runs = %+v, want one run with harporis driver", docA.Runs)
	}
	if got := len(docA.Runs[0].Results); got != 2 {
		t.Errorf("scan-a results = %d, want 2", got)
	}
	if r := docA.Runs[0].Results[0]; r.RuleID != "aws-key" || r.Level != "error" {
		t.Errorf("scan-a result[0] = %+v, want aws-key/error", r)
	}
	if r := docA.Runs[0].Results[1]; r.Level != "note" {
		t.Errorf("scan-a result[1] level = %q, want note (LOW->note)", r.Level)
	}

	// scan-b has 1 finding in its own file.
	pathB := filepath.Join(dir, "scan-b.sarif")
	bB, err := os.ReadFile(pathB)
	if err != nil {
		t.Fatalf("read scan-b: %v", err)
	}
	var docB sarifReport
	if err := json.Unmarshal(bB, &docB); err != nil {
		t.Fatalf("unmarshal scan-b: %v", err)
	}
	if got := len(docB.Runs[0].Results); got != 1 {
		t.Errorf("scan-b results = %d, want 1", got)
	}
}

func TestSARIF_RejectsInvalidScanID(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSARIF(dir)
	if err != nil {
		t.Fatalf("NewSARIF: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	bad := []string{
		"../etc/passwd",
		"foo/bar",
		"foo.bar",
		"",
	}
	for _, id := range bad {
		err := s.Write(context.Background(), &v1.Finding{ScanId: id, FindingId: "f1", RuleId: "x"})
		if err == nil {
			t.Errorf("Write with scan_id %q: want error, got nil", id)
		}
	}
	// And no .sarif files should have materialized.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sarif") {
			t.Errorf("unexpected file materialized: %s", e.Name())
		}
	}
}

func TestSARIF_EnforcesMaxPerScan(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSARIFN(dir, 2)
	if err != nil {
		t.Fatalf("NewSARIFN: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	for i := range 2 {
		if err := s.Write(ctx, &v1.Finding{ScanId: "scan", FindingId: idN(i), RuleId: "r"}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	// 3rd write should error.
	err = s.Write(ctx, &v1.Finding{ScanId: "scan", FindingId: "f3", RuleId: "r"})
	if err == nil {
		t.Errorf("3rd Write: want max-per-scan error, got nil")
	}
}

func TestSARIF_WriteAfterCloseFails(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSARIF(dir)
	if err != nil {
		t.Fatalf("NewSARIF: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err = s.Write(context.Background(), &v1.Finding{ScanId: "x", FindingId: "f1", RuleId: "r"})
	if err != ErrSinkClosed {
		t.Errorf("Write after Close: err = %v, want ErrSinkClosed", err)
	}
}

func idN(i int) string {
	return string(rune('a' + i))
}
