package rulewatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// minimalPack is one valid rule that matches "FOO=" — sufficient to
// build a detector. The negative example asserts the regex really is
// what we say it is so Validate() passes.
const minimalPack = `- id: foo
  description: matches FOO assignments
  severity: low
  regex: 'FOO=([A-Z]+)'
  secret_group: 1
  examples:
    positive: ["FOO=ABC"]
    negative: ["BAR=ABC"]
`

const minimalPackTwoRules = minimalPack + `- id: bar
  description: matches BAR assignments
  severity: low
  regex: 'BAR=([A-Z]+)'
  secret_group: 1
  examples:
    positive: ["BAR=XYZ"]
    negative: ["FOO=XYZ"]
`

func TestWatcher_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(p, []byte(minimalPack), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(p, "test")
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	if w.Current() == nil {
		t.Fatal("Current() returned nil after successful construction")
	}
	if got := w.RulesCount(); got != 1 {
		t.Errorf("RulesCount = %d, want 1", got)
	}
	if got := w.Reloads(); got != 0 {
		t.Errorf("Reloads = %d, want 0 (initial load doesn't count)", got)
	}
}

func TestWatcher_DetectsMtimeChangeAndSwapsAtomically(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(p, []byte(minimalPack), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(p, "test")
	if err != nil {
		t.Fatal(err)
	}
	firstDet := w.Current()

	// Pause to make sure mtime changes on disk — some filesystems
	// quantize mtime to 1s. 1.1s margin is conservative.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(p, []byte(minimalPackTwoRules), 0o644); err != nil {
		t.Fatal(err)
	}

	// Drive a single tick manually rather than waiting for Run() — that
	// makes the test deterministic.
	if err := w.checkAndReload(); err != nil {
		t.Fatalf("checkAndReload: %v", err)
	}
	if w.Current() == firstDet {
		t.Error("detector pointer did not swap after mtime change")
	}
	if got := w.RulesCount(); got != 2 {
		t.Errorf("RulesCount after reload = %d, want 2", got)
	}
	if got := w.Reloads(); got != 1 {
		t.Errorf("Reloads after one mtime change = %d, want 1", got)
	}
}

func TestWatcher_InvalidPackPreservesPreviousDetector(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(p, []byte(minimalPack), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(p, "test")
	if err != nil {
		t.Fatal(err)
	}
	prev := w.Current()
	prevCount := w.RulesCount()

	// Overwrite with broken YAML.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(p, []byte("- this is: not [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.checkAndReload(); err == nil {
		t.Fatal("checkAndReload should have errored on broken YAML")
	}
	if w.Current() != prev {
		t.Error("detector swapped to nil/garbage on parse failure (should keep previous)")
	}
	if w.RulesCount() != prevCount {
		t.Error("RulesCount changed on failed reload")
	}
}

func TestWatcher_RunStopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pack.yaml")
	if err := os.WriteFile(p, []byte(minimalPack), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(p, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx, 50*time.Millisecond)
		close(done)
	}()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit within 2s of cancel")
	}
}
