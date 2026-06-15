package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestScanRejectsInvalidScanID verifies the CLI validates --scan-id with the
// shared kit/scan rule BEFORE any NATS dial, so the user gets a clear error
// instead of a confusing server-side reject. A '.' is invalid.
func TestScanRejectsInvalidScanID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--quiet", "scan", "--scan-id", "bad.id"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for scan_id containing '.', got nil")
	}
	if !strings.Contains(err.Error(), "scan_id") {
		t.Fatalf("error should mention scan_id, got: %v", err)
	}
}
