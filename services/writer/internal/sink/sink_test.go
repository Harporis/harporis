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

func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
