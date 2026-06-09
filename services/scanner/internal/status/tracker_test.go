package status

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeEmitter struct {
	mu     sync.Mutex
	emits  []emit
}
type emit struct {
	scanID string
	count  int64
}

func (f *fakeEmitter) PublishStatusSecretsFound(_ context.Context, scanID string, count int64) error {
	f.mu.Lock()
	f.emits = append(f.emits, emit{scanID, count})
	f.mu.Unlock()
	return nil
}

func (f *fakeEmitter) snapshot() []emit {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]emit, len(f.emits))
	copy(cp, f.emits)
	return cp
}

func TestTracker_TicksEmitOnlyWhenChanged(t *testing.T) {
	fe := &fakeEmitter{}
	tr := NewTracker(fe, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tr.Run(ctx)

	tr.Incr("scan-A", 3)
	time.Sleep(120 * time.Millisecond) // at least 2 ticks
	// No further increments — next tick must not emit again.
	time.Sleep(100 * time.Millisecond)

	es := fe.snapshot()
	if len(es) < 1 || es[0].count != 3 {
		t.Fatalf("first emit wrong: %+v", es)
	}
	// At most one emission for scan-A (no change between ticks).
	count := 0
	for _, e := range es {
		if e.scanID == "scan-A" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("scan-A emitted %d times, want exactly 1", count)
	}
}

func TestTracker_FinalEmitOnIsLast(t *testing.T) {
	fe := &fakeEmitter{}
	tr := NewTracker(fe, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	tr.Incr("scan-B", 7)
	if err := tr.FinalEmit(ctx, "scan-B"); err != nil {
		t.Fatalf("FinalEmit: %v", err)
	}
	es := fe.snapshot()
	if len(es) != 1 || es[0].scanID != "scan-B" || es[0].count != 7 {
		t.Errorf("final emit wrong: %+v", es)
	}
	// Immediately after FinalEmit, scan is in the grace window — still active.
	if got := tr.ActiveScans(); got != 1 {
		t.Errorf("ActiveScans immediately after FinalEmit = %d, want 1 (grace window)", got)
	}
	// After 2*tick + slack, the grace window expires and the entry is dropped.
	time.Sleep(150 * time.Millisecond)
	if got := tr.ActiveScans(); got != 0 {
		t.Errorf("ActiveScans after grace = %d, want 0", got)
	}
}

func TestTracker_FinalEmitGraceWindow(t *testing.T) {
	fe := &fakeEmitter{}
	tr := NewTracker(fe, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)

	tr.Incr("scan-X", 3)
	if err := tr.FinalEmit(ctx, "scan-X"); err != nil {
		t.Fatalf("FinalEmit: %v", err)
	}
	if got := tr.ActiveScans(); got != 1 {
		t.Errorf("ActiveScans after FinalEmit = %d, want 1 (grace window)", got)
	}
	tr.Incr("scan-X", 2)
	time.Sleep(120 * time.Millisecond)

	es := fe.snapshot()
	var lastCount int64
	for _, e := range es {
		if e.scanID == "scan-X" {
			lastCount = e.count
		}
	}
	if lastCount != 5 {
		t.Errorf("last emit count = %d, want 5 (3 from FinalEmit + 2 late Incr)", lastCount)
	}

	time.Sleep(200 * time.Millisecond)
	if got := tr.ActiveScans(); got != 0 {
		t.Errorf("ActiveScans after grace = %d, want 0", got)
	}
}
