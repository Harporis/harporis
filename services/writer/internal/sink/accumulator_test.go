package sink

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// TestBatchedAccumulator_FlushBatchOne_SyncBehavior ensures FlushBatch=1
// keeps the legacy "render-on-every-write" semantics so existing tests
// + operators that haven't enabled batching see no change.
func TestBatchedAccumulator_FlushBatchOne_SyncBehavior(t *testing.T) {
	var flushed atomic.Int32
	a := NewBatchedAccumulator(BatchConfig{
		FlushBatch: 1, SinkLabel: "test_file",
	}, func(scanID string, findings []*v1.Finding) error {
		flushed.Add(1)
		return nil
	})
	t.Cleanup(func() { _ = a.Close() })

	for i := 0; i < 10; i++ {
		if err := a.Add(context.Background(), &v1.Finding{ScanId: "s1", FindingId: "f"}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	if got := flushed.Load(); got != 10 {
		t.Errorf("FlushBatch=1: want 10 flushes, got %d", got)
	}
}

// TestBatchedAccumulator_BatchedFlush_Coalesces verifies the headline
// optimisation: 100 writes with FlushBatch=10 -> 10 flushes, not 100.
func TestBatchedAccumulator_BatchedFlush_Coalesces(t *testing.T) {
	var flushed atomic.Int32
	var totalSize atomic.Int32
	a := NewBatchedAccumulator(BatchConfig{
		FlushBatch: 10, SinkLabel: "test_file",
	}, func(scanID string, findings []*v1.Finding) error {
		flushed.Add(1)
		totalSize.Add(int32(len(findings)))
		return nil
	})
	t.Cleanup(func() { _ = a.Close() })

	for i := 0; i < 100; i++ {
		if err := a.Add(context.Background(), &v1.Finding{ScanId: "s1", FindingId: "f"}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	if got := flushed.Load(); got != 10 {
		t.Errorf("FlushBatch=10 / 100 adds: want 10 flushes, got %d", got)
	}
	// Each flush gets a SNAPSHOT of all findings to date — so the last
	// flush at 100 sees 100 findings; total across all flushes
	// is 10+20+30+...+100 = 550.
	if got := totalSize.Load(); got != 550 {
		t.Errorf("total findings across flushes = %d, want 550", got)
	}
}

// TestBatchedAccumulator_Close_FlushesPending makes sure findings that
// sat below the batch threshold still hit disk on Close.
func TestBatchedAccumulator_Close_FlushesPending(t *testing.T) {
	var lastTrigger string
	a := NewBatchedAccumulator(BatchConfig{
		FlushBatch: 100, SinkLabel: "test_file",
	}, func(scanID string, findings []*v1.Finding) error {
		// The doFlush wrapper stamps trigger via metrics; we cheat by
		// observing that the only flush we'll see is the close one.
		lastTrigger = "close-or-batch"
		return nil
	})

	for i := 0; i < 5; i++ {
		if err := a.Add(context.Background(), &v1.Finding{ScanId: "sc", FindingId: "f"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	if lastTrigger != "" {
		t.Fatalf("flush fired below batch threshold: %q", lastTrigger)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if lastTrigger != "close-or-batch" {
		t.Fatalf("Close did not flush pending; lastTrigger=%q", lastTrigger)
	}
}

// TestBatchedAccumulator_Ticker_FlushesIdle simulates a writer running
// under the batch threshold; the ticker should drain the buffer.
func TestBatchedAccumulator_Ticker_FlushesIdle(t *testing.T) {
	var flushed atomic.Int32
	a := NewBatchedAccumulator(BatchConfig{
		FlushBatch:    100,
		FlushInterval: 50 * time.Millisecond,
		SinkLabel:     "test_file",
	}, func(scanID string, findings []*v1.Finding) error {
		flushed.Add(1)
		return nil
	})
	t.Cleanup(func() { _ = a.Close() })

	for i := 0; i < 5; i++ {
		if err := a.Add(context.Background(), &v1.Finding{ScanId: "sc", FindingId: "f"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	// Ticker fires every 50ms; allow a few cycles. Need 2x the interval
	// because the FIRST tick after Add sees lastFlush==addTime so the
	// `now.Sub(lastFlush) >= interval` check fails; the SECOND tick
	// catches it.
	deadline := time.Now().Add(500 * time.Millisecond)
	for flushed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if flushed.Load() == 0 {
		t.Fatalf("ticker never flushed idle buffer")
	}
}
