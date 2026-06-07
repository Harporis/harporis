package sink

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLooksLikeOrphanTempfile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Real outputs — MUST NOT be matched.
		{"scan-1.ndjson", false},
		{"scan-1.sarif", false},
		{"scan-1.html", false},
		{"scan-1.xlsx", false},
		{"scan-1.pdf", false},
		// Hidden files that are not our tempfiles — MUST NOT be matched.
		{".hidden.xlsx", false}, // only 2 dots, fails the >=3 check
		{".gitignore", false},
		// Tempfile shapes — MUST be matched.
		{"scan-1.sarif.tmp-123abc", true},
		{"scan-1.html.tmp-deadbeef0", true},
		{".scan-1.abc123.xlsx", true},
		{".scan-1.abc123.pdf", true},
		// Edge: scan id containing dots is fine — still matches.
		{".scan.with.dots.abc123.pdf", true},
	}
	for _, c := range cases {
		if got := looksLikeOrphanTempfile(c.name); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSweepOrphanTempfiles_RemovesOnlyTempfiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]bool{
		"scan-a.ndjson":             false, // keep
		"scan-a.sarif":              false, // keep
		"scan-a.html":               false, // keep
		"scan-a.xlsx":               false, // keep
		"scan-a.pdf":                false, // keep
		"scan-a.sarif.tmp-abc":      true,  // sweep
		"scan-b.html.tmp-0xff":      true,  // sweep
		".scan-c.abc123.xlsx":       true,  // sweep
		".scan-d.deadbeef.pdf":      true,  // sweep
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantRemoved := 0
	for _, v := range files {
		if v {
			wantRemoved++
		}
	}

	got, err := SweepOrphanTempfiles(dir, nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got != wantRemoved {
		t.Errorf("removed %d, want %d", got, wantRemoved)
	}
	for name, shouldBeGone := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		exists := err == nil
		if shouldBeGone && exists {
			t.Errorf("%s: should have been swept", name)
		}
		if !shouldBeGone && !exists {
			t.Errorf("%s: must NOT have been swept", name)
		}
	}
}

func TestSweepOrphanTempfiles_MissingDirIsNoop(t *testing.T) {
	n, err := SweepOrphanTempfiles("/nonexistent/path-no-writer-yet", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}
