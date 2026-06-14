package sink

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name string, size int, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

func TestSweepRetention_AgePass(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := writeFile(t, dir, "old-scan.ndjson", 100, now.Add(-72*time.Hour))
	fresh := writeFile(t, dir, "fresh-scan.ndjson", 100, now.Add(-1*time.Hour))

	stats, err := SweepRetention(dir, RetentionPolicy{TTL: 48 * time.Hour}, now, nil)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if stats.RemovedByAge != 1 || stats.BytesRemoved != 100 {
		t.Fatalf("stats = %+v, want RemovedByAge=1 BytesRemoved=100", stats)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old file should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file should survive, stat err=%v", err)
	}
}

func TestSweepRetention_SizePass(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Three files, 100 bytes each, ascending mtime. Cap at 150 → drops two oldest.
	writeFile(t, dir, "a.ndjson", 100, now.Add(-3*time.Hour))
	writeFile(t, dir, "b.ndjson", 100, now.Add(-2*time.Hour))
	writeFile(t, dir, "c.ndjson", 100, now.Add(-1*time.Hour))

	stats, err := SweepRetention(dir, RetentionPolicy{MaxBytes: 150}, now, nil)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if stats.RemovedBySize != 2 || stats.BytesRemoved != 200 {
		t.Fatalf("stats = %+v, want RemovedBySize=2 BytesRemoved=200", stats)
	}
	if stats.RemainingFiles != 1 || stats.RemainingBytes != 100 {
		t.Errorf("remaining = %d files / %d bytes, want 1 / 100", stats.RemainingFiles, stats.RemainingBytes)
	}
}

func TestSweepRetention_IgnoresNonSinkFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, dir, "operator-archive.tar.gz", 100, now.Add(-72*time.Hour))
	writeFile(t, dir, "README.md", 100, now.Add(-72*time.Hour))
	writeFile(t, dir, ".hidden.ndjson", 100, now.Add(-72*time.Hour))
	writeFile(t, dir, "scan.sarif.tmp-abc", 100, now.Add(-72*time.Hour))
	target := writeFile(t, dir, "scan.ndjson", 100, now.Add(-72*time.Hour))

	stats, err := SweepRetention(dir, RetentionPolicy{TTL: 24 * time.Hour}, now, nil)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if stats.RemovedByAge != 1 {
		t.Fatalf("RemovedByAge = %d, want 1 (only the legit sink file)", stats.RemovedByAge)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target sink file should be removed: %v", err)
	}
	for _, n := range []string{"operator-archive.tar.gz", "README.md", ".hidden.ndjson", "scan.sarif.tmp-abc"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("non-sink file %s should survive: %v", n, err)
		}
	}
}

func TestSweepRetention_MissingDirIsNoError(t *testing.T) {
	stats, err := SweepRetention(filepath.Join(t.TempDir(), "does-not-exist"), RetentionPolicy{TTL: time.Hour}, time.Now(), nil)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if stats.RemovedByAge != 0 || stats.RemovedBySize != 0 {
		t.Errorf("stats = %+v, want zero", stats)
	}
}

func TestSweepRetention_DisabledPolicyIsNoop(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, dir, "scan.ndjson", 100, now.Add(-100*time.Hour))

	stats, err := SweepRetention(dir, RetentionPolicy{}, now, nil)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if stats.RemovedByAge != 0 || stats.RemovedBySize != 0 {
		t.Errorf("disabled policy should be no-op, got %+v", stats)
	}
}

func TestSweepRetention_MatchesReplicaSuffix(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	target := writeFile(t, dir, "scan-id.replica1.parquet", 100, now.Add(-100*time.Hour))

	stats, err := SweepRetention(dir, RetentionPolicy{TTL: time.Hour}, now, nil)
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if stats.RemovedByAge != 1 {
		t.Fatalf("RemovedByAge=%d, want 1 for replica-suffixed file", stats.RemovedByAge)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("replica-suffixed file should be removed: %v", err)
	}
}
