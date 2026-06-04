package sink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func mkFinding(scanID, findingID, ruleID string, sev v1.Severity) *v1.Finding {
	return &v1.Finding{
		ScanId:        scanID,
		FindingId:     findingID,
		RuleId:        ruleID,
		Severity:      sev,
		FilePath:      "main.go",
		LineNumber:    42,
		MatchedSecret: []byte("AKIAIOSFODNN7EXAMPLE"),
	}
}

func TestNDJSONFile_WriteOnePerLine(t *testing.T) {
	dir := t.TempDir()
	s, err := NewNDJSONFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f := mkFinding("scan-A", "f-"+strconv.Itoa(i), "aws-access-key-id", v1.Severity_HIGH)
		if err := s.Write(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	// File should be exactly 3 lines, each parseable JSON.
	body, err := os.ReadFile(filepath.Join(dir, "scan-A.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(open(t, filepath.Join(dir, "scan-A.ndjson")))
	count := 0
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("line %d not valid JSON: %v", count, err)
		}
		if m["scan_id"] != "scan-A" {
			t.Errorf("line %d scan_id: %v", count, m["scan_id"])
		}
		if m["severity"] != "HIGH" {
			t.Errorf("line %d severity: %v (want HIGH)", count, m["severity"])
		}
		count++
	}
	if count != 3 {
		t.Errorf("got %d lines, want 3; raw:\n%s", count, body)
	}
}

func TestNDJSONFile_SeparatesScans(t *testing.T) {
	dir := t.TempDir()
	s, err := NewNDJSONFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Write(ctx, mkFinding("scan-A", "f-1", "r", v1.Severity_LOW)); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, mkFinding("scan-B", "f-1", "r", v1.Severity_LOW)); err != nil {
		t.Fatal(err)
	}
	for _, scanID := range []string{"scan-A", "scan-B"} {
		body, err := os.ReadFile(filepath.Join(dir, scanID+".ndjson"))
		if err != nil {
			t.Fatalf("read %s: %v", scanID, err)
		}
		if len(body) == 0 {
			t.Errorf("%s empty", scanID)
		}
	}
}

func TestNDJSONFile_ConcurrentSameScan(t *testing.T) {
	dir := t.TempDir()
	s, err := NewNDJSONFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	const N = 200
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := s.Write(ctx, mkFinding("scan-X", "f-"+strconv.Itoa(i), "r", v1.Severity_MEDIUM)); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Verify: exactly N lines, no truncated lines, all valid JSON.
	f := open(t, filepath.Join(dir, "scan-X.ndjson"))
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	count := 0
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("torn line %d: %s", count, scanner.Text())
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != N {
		t.Errorf("got %d lines, want %d", count, N)
	}
}

func TestNDJSONFile_RejectsEmptyScanID(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewNDJSONFile(dir)
	defer s.Close()
	f := &v1.Finding{FindingId: "x"}
	if err := s.Write(context.Background(), f); err == nil {
		t.Fatal("expected error for empty scan_id")
	}
}

func TestNDJSONFile_HonoursContextCancel(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewNDJSONFile(dir)
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := s.Write(ctx, mkFinding("scan-A", "f-1", "r", v1.Severity_LOW))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestNDJSONFile_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewNDJSONFile(dir)
	_ = s.Write(context.Background(), mkFinding("scan-A", "f-1", "r", v1.Severity_LOW))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestNDJSONFile_WriteAfterCloseReturnsErrSinkClosed(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewNDJSONFile(dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	err := s.Write(context.Background(), mkFinding("scan-A", "f-1", "r", v1.Severity_LOW))
	if !errors.Is(err, ErrSinkClosed) {
		t.Fatalf("expected ErrSinkClosed, got %v", err)
	}
}

// Stress: many concurrent Writers + a Close racing in the middle.
// Pre-fix this either tore lines or wrote to a closed fd; post-fix the
// successful subset must be valid JSON and the rest must come back as
// ErrSinkClosed cleanly.
func TestNDJSONFile_ConcurrentWritesAndClose(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewNDJSONFile(dir)
	ctx := context.Background()
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Write(ctx, mkFinding("scan-R", "f-"+strconv.Itoa(i), "r", v1.Severity_MEDIUM))
			if err != nil && !errors.Is(err, ErrSinkClosed) {
				t.Errorf("unexpected err %d: %v", i, err)
			}
		}(i)
		// Trigger Close roughly midway through the goroutine fan-out.
		if i == N/2 {
			go func() {
				if err := s.Close(); err != nil {
					t.Errorf("close: %v", err)
				}
			}()
		}
	}
	wg.Wait()
	// Whatever survived must be valid JSON, one line each, no torn lines.
	f, err := os.Open(filepath.Join(dir, "scan-R.ndjson"))
	if err != nil {
		// Allowed: Close ran before any Write opened the file.
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("torn line: %s", sc.Text())
		}
	}
}

func TestNDJSONFile_LRUEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	s, err := NewNDJSONFileN(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Open 3 scans against cap=2: third one must evict scan-A.
	for _, id := range []string{"scan-A", "scan-B", "scan-C"} {
		if err := s.Write(ctx, mkFinding(id, "f-1", "r", v1.Severity_LOW)); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	// Internal: only 2 files should be in the map; scan-A evicted.
	s.mu.Lock()
	got := len(s.files)
	_, aOpen := s.files["scan-A"]
	s.mu.Unlock()
	if got != 2 {
		t.Errorf("expected 2 open files, got %d", got)
	}
	if aOpen {
		t.Errorf("scan-A should have been evicted")
	}
	// Re-write scan-A: O_APPEND preserves data, file reopens.
	if err := s.Write(ctx, mkFinding("scan-A", "f-2", "r", v1.Severity_LOW)); err != nil {
		t.Fatalf("re-open scan-A: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// scan-A must have BOTH findings (eviction didn't truncate).
	body, _ := os.ReadFile(filepath.Join(dir, "scan-A.ndjson"))
	lines := 0
	for _, b := range body {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("scan-A.ndjson has %d lines after evict+reopen, want 2", lines)
	}
}

func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
