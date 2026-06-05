package sink

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	xlsxlib "github.com/xuri/excelize/v2"
)

func TestXLSX_ReportPerScanRoundtrips(t *testing.T) {
	dir := t.TempDir()
	x, err := NewXLSX(dir)
	if err != nil {
		t.Fatalf("NewXLSX: %v", err)
	}
	t.Cleanup(func() { _ = x.Close() })

	ctx := context.Background()
	findings := []*v1.Finding{
		{ScanId: "x1", FindingId: "f1", RuleId: "aws-key", Severity: v1.Severity_CRITICAL, FilePath: "src/.env", LineNumber: 12, MatchedSecret: []byte("AKIAIOSFODNN7EXAMPLE")},
		{ScanId: "x1", FindingId: "f2", RuleId: "generic", Severity: v1.Severity_LOW, Refs: []*v1.CommitFileRef{{Path: "lib/util.go"}}, LineNumber: 1, MatchedSecret: []byte("ghp_xxx")},
	}
	for _, f := range findings {
		if err := x.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}
	path := filepath.Join(dir, "x1.xlsx")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("xlsx not created: %v", err)
	}
	// Reopen and assert the header + the two data rows.
	wb, err := xlsxlib.OpenFile(path)
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	t.Cleanup(func() { _ = wb.Close() })
	rows, err := wb.GetRows("Findings")
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3 (header + 2 findings)", len(rows))
	}
	if rows[0][0] != "severity" || rows[0][2] != "file_path" {
		t.Errorf("header row malformed: %v", rows[0])
	}
	if rows[1][0] != "CRITICAL" || rows[1][1] != "aws-key" || rows[1][2] != "src/.env" {
		t.Errorf("first data row malformed: %v", rows[1])
	}
	if rows[2][2] != "lib/util.go" {
		t.Errorf("Refs fallback not applied to file_path column: %v", rows[2])
	}
}

func TestXLSX_RejectsBadScanID(t *testing.T) {
	dir := t.TempDir()
	x, err := NewXLSX(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = x.Close() })
	if err := x.Write(context.Background(), &v1.Finding{ScanId: "../../etc/passwd", RuleId: "x"}); err == nil {
		t.Error("traversal scan_id should error")
	}
}
