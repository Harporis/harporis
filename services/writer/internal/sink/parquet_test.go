package sink

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestParquet_WritesValidFilePerScan(t *testing.T) {
	dir := t.TempDir()
	p, err := NewParquet(dir)
	if err != nil {
		t.Fatalf("NewParquet: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	for _, f := range []*v1.Finding{
		{
			ScanId: "scan-pq", FindingId: "f1", RuleId: "aws-key",
			Severity: v1.Severity_HIGH, FilePath: ".env", LineNumber: 12,
			MatchedSecret: []byte("AKIA..."), MatchedLine: []byte("aws_key = AKIA..."),
			ContextBefore: [][]byte{[]byte("before-1"), []byte("before-2")},
			ContextAfter:  [][]byte{[]byte("after-1")},
		},
		{
			ScanId: "scan-pq", FindingId: "f2", RuleId: "generic",
			Severity: v1.Severity_LOW, FilePath: "util.go", LineNumber: 88,
		},
	} {
		if err := p.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}

	path := filepath.Join(dir, "scan-pq.parquet")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("parquet file is empty")
	}

	// Round-trip: read back and verify the row shapes.
	var got []parquetRow
	got, err = parquet.ReadFile[parquetRow](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].FindingID != "f1" || got[0].Severity != "HIGH" || got[0].RuleID != "aws-key" {
		t.Errorf("row[0] mismatch: %+v", got[0])
	}
	if got[0].ContextBefore != "before-1\nbefore-2" {
		t.Errorf("ContextBefore = %q, want newline-joined pair", got[0].ContextBefore)
	}
	if got[0].ContextAfter != "after-1" {
		t.Errorf("ContextAfter = %q", got[0].ContextAfter)
	}
	if got[1].FindingID != "f2" || got[1].Severity != "LOW" {
		t.Errorf("row[1] mismatch: %+v", got[1])
	}
	if got[1].ContextBefore != "" || got[1].ContextAfter != "" {
		t.Errorf("row[1] context must be empty: %+v", got[1])
	}
}

func TestParquet_RejectsInvalidScanID(t *testing.T) {
	dir := t.TempDir()
	p, _ := NewParquet(dir)
	t.Cleanup(func() { _ = p.Close() })
	err := p.Write(context.Background(), &v1.Finding{ScanId: "../escape", FindingId: "f", RuleId: "x"})
	if err == nil {
		t.Fatal("expected ValidateScanID rejection")
	}
}

func TestParquet_AccumulatorCap(t *testing.T) {
	dir := t.TempDir()
	p, err := NewParquetN(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := p.Write(ctx, &v1.Finding{ScanId: "cap-scan", FindingId: "f", RuleId: "x", Severity: v1.Severity_LOW, FilePath: "a"}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	// Third write should breach the cap and surface as an error.
	if err := p.Write(ctx, &v1.Finding{ScanId: "cap-scan", FindingId: "f3", RuleId: "x", Severity: v1.Severity_LOW, FilePath: "a"}); err == nil {
		t.Fatal("expected accumulator cap error")
	}
}
