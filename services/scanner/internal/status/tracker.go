// Package status holds per-scan finding counters and emits StatusEvent
// updates on a periodic tick. One Tracker per scanner process, shared
// across worker goroutines.
//
// v0.1 caveat: counters are per-replica. With N replicas, each emits its
// own local count for the scans it consumed. Aggregation across replicas
// is the writer service's job (see spec §4.4).
package status

import (
	"context"
	"sync"
	"time"

	"github.com/Harporis/harporis/services/scanner/internal/metrics"
)

// Emitter is what Tracker uses to publish status updates. The concrete
// implementation is *nats.Publisher (or test fake).
type Emitter interface {
	PublishStatusSecretsFound(ctx context.Context, scanID string, count int64) error
}

// Tracker holds per-scan counters and emits StatusEvent updates on a tick.
// Safe for concurrent use.
type Tracker struct {
	emitter Emitter
	tick    time.Duration

	mu          sync.Mutex
	counts      map[string]int64
	lastEmitted map[string]int64
}

func NewTracker(e Emitter, tick time.Duration) *Tracker {
	return &Tracker{
		emitter:     e,
		tick:        tick,
		counts:      make(map[string]int64),
		lastEmitted: make(map[string]int64),
	}
}

// Incr adds delta to the per-scan counter for scanID. Safe for concurrent use.
func (t *Tracker) Incr(scanID string, delta int64) {
	t.mu.Lock()
	t.counts[scanID] += delta
	t.updateGaugeLocked()
	t.mu.Unlock()
}

// updateGaugeLocked publishes the current active-scan count to the metric
// gauge. Must be called with t.mu held.
func (t *Tracker) updateGaugeLocked() {
	metrics.ActiveScans.Set(float64(len(t.counts)))
}

// Run drives the tick loop until ctx is cancelled. Each tick: for each
// active scan whose counter has advanced since last emit, publish a
// StatusEvent and record the new high-water mark.
func (t *Tracker) Run(ctx context.Context) {
	tk := time.NewTicker(t.tick)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			t.emitDeltas(ctx)
		}
	}
}

func (t *Tracker) emitDeltas(ctx context.Context) {
	t.mu.Lock()
	pending := make(map[string]int64)
	for scanID, count := range t.counts {
		if t.lastEmitted[scanID] != count {
			pending[scanID] = count
		}
	}
	t.mu.Unlock()

	for scanID, count := range pending {
		if err := t.emitter.PublishStatusSecretsFound(ctx, scanID, count); err == nil {
			metrics.StatusUpdatesPublished.Inc()
			t.mu.Lock()
			t.lastEmitted[scanID] = count
			t.updateGaugeLocked()
			t.mu.Unlock()
		}
	}
}

// FinalEmit publishes the latest counter for scanID immediately and removes
// the scan from the active map. Called by the worker on chunk with
// is_last_in_scan = true.
func (t *Tracker) FinalEmit(ctx context.Context, scanID string) error {
	t.mu.Lock()
	count := t.counts[scanID]
	t.mu.Unlock()

	if err := t.emitter.PublishStatusSecretsFound(ctx, scanID, count); err != nil {
		return err
	}
	metrics.StatusUpdatesPublished.Inc()
	t.mu.Lock()
	delete(t.counts, scanID)
	delete(t.lastEmitted, scanID)
	t.updateGaugeLocked()
	t.mu.Unlock()
	return nil
}

// ActiveScans returns the current number of scans being tracked. For the
// metrics gauge.
func (t *Tracker) ActiveScans() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.counts)
}
