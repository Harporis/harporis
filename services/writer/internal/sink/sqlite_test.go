package sink

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

	_ "modernc.org/sqlite"
)

func TestSQLite_WritesAndQueryableCrossScan(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSQLite(dir)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	findings := []*v1.Finding{
		{ScanId: "scan-a", FindingId: "f1", RuleId: "aws-key", Severity: v1.Severity_CRITICAL, FilePath: "src/.env", LineNumber: 12, MatchedSecret: []byte("AKIA")},
		{ScanId: "scan-a", FindingId: "f2", RuleId: "generic", Severity: v1.Severity_LOW, FilePath: "lib/u.go", LineNumber: 88},
		{ScanId: "scan-b", FindingId: "f3", RuleId: "pem", Severity: v1.Severity_HIGH, FilePath: "k/id_rsa", LineNumber: 1},
	}
	for _, f := range findings {
		if err := s.Write(ctx, f); err != nil {
			t.Fatalf("Write %s: %v", f.FindingId, err)
		}
	}

	// Re-open the DB and run a cross-scan query.
	db, err := sql.Open("sqlite", filepath.Join(dir, "findings.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM findings").Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}

	var critA int
	if err := db.QueryRow("SELECT COUNT(*) FROM findings WHERE scan_id='scan-a' AND severity='CRITICAL'").Scan(&critA); err != nil {
		t.Fatalf("count scan-a critical: %v", err)
	}
	if critA != 1 {
		t.Errorf("scan-a CRITICAL = %d, want 1", critA)
	}

	rows, err := db.Query("SELECT DISTINCT scan_id FROM findings ORDER BY scan_id")
	if err != nil {
		t.Fatalf("scan_id query: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	if len(got) != 2 || got[0] != "scan-a" || got[1] != "scan-b" {
		t.Errorf("distinct scan_ids = %v, want [scan-a scan-b]", got)
	}
}

func TestSQLite_RedeliveryIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSQLite(dir)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	f := &v1.Finding{ScanId: "scan-x", FindingId: "dup", RuleId: "r", Severity: v1.Severity_LOW}
	for range 5 {
		if err := s.Write(ctx, f); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	db, _ := sql.Open("sqlite", filepath.Join(dir, "findings.db"))
	t.Cleanup(func() { _ = db.Close() })
	var n int
	_ = db.QueryRow("SELECT COUNT(*) FROM findings WHERE scan_id='scan-x'").Scan(&n)
	if n != 1 {
		t.Errorf("redelivered scan: rows = %d, want 1 (OR IGNORE dedup)", n)
	}
}

func TestSQLite_RejectsBadScanID(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSQLite(dir)
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Write(context.Background(), &v1.Finding{ScanId: "../etc/passwd", FindingId: "f1", RuleId: "x"}); err == nil {
		t.Error("traversal scan_id should error")
	}
}

func TestSQLite_WriteAfterCloseFails(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSQLite(dir)
	_ = s.Close()
	err := s.Write(context.Background(), &v1.Finding{ScanId: "x", FindingId: "f1", RuleId: "r"})
	if err != ErrSinkClosed {
		t.Errorf("Write after Close: err = %v, want ErrSinkClosed", err)
	}
}

func TestSQLite_ReplicaIDChangesFilename(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSQLite(dir)
	t.Cleanup(func() { _ = s.Close() })
	s.SetReplicaID("repA")
	if got, want := s.DBPath(), filepath.Join(dir, "findings.repA.db"); got != want {
		t.Errorf("DBPath = %s, want %s", got, want)
	}
}
