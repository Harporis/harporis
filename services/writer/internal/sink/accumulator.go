// Batched accumulator helper used by the five accumulator sinks
// (SARIF, HTML, XLSX, PDF, Parquet). Coalesces per-finding writes into
// chunked flushes so heavy scans don't pay the O(N²) full-rewrite cost
// on every Finding. NDJSON does not use this — it streams.
//
// Durability contract:
//   - NDJSON is authoritative: every Finding is persisted (O_APPEND)
//     before the JetStream Ack returns.
//   - Accumulator sinks are eventually-consistent views over the same
//     stream. On a writer crash, up to one batch (≤ FlushBatch findings
//     or ≤ FlushInterval ms, whichever fired first) can be lost from
//     these files. The NDJSON record survives, and an operator can
//     reconstruct the sink by re-publishing findings from it.
//
// Triggers (one of these flushes the buffer):
//   - "batch"    — pending count reached cfg.FlushBatch (synchronous,
//                  happens inline on the Add() call that crosses the
//                  threshold). Caller blocks on flush completion.
//   - "interval" — periodic ticker every cfg.FlushInterval; any scan
//                  whose buffer is non-empty AND idle for >= the
//                  interval gets flushed.
//   - "close"    — Close() drains every pending buffer (synchronous).

package sink

import (
	"context"
	"fmt"
	"sync"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

// BatchConfig tunes the batching behaviour.
type BatchConfig struct {
	// MaxPerScan caps in-memory findings per scan_id to bound writer
	// memory under a runaway producer. Beyond this, Add returns an
	// error and the worker will Nak the JetStream message.
	MaxPerScan int
	// FlushBatch is the number of NEW findings since the last flush
	// that triggers a synchronous flush. <= 1 means "flush on every
	// write" (the legacy O(N²) behaviour, useful for sync tests).
	FlushBatch int
	// FlushInterval is the periodic ticker that catches idle buffers.
	// 0 disables the ticker entirely (sync-only mode, useful for
	// tests and operators who want strict freshness).
	FlushInterval time.Duration
	// SinkLabel is the value attached to the {sink} label on the
	// Prometheus collectors (e.g. "sarif_file"). Mirrors Sink.Name().
	SinkLabel string
}

// FlushFn renders the full per-scan snapshot to disk atomically.
// Implementations are passed an immutable slice — they must not retain
// or mutate it after the call returns.
type FlushFn func(scanID string, findings []*v1.Finding) error

type accState struct {
	findings     []*v1.Finding
	pendingCount int
	lastFlush    time.Time
}

// BatchedAccumulator owns the per-scan in-memory buffers plus the
// optional ticker. Embed it (via composition) in each sink struct;
// the sink supplies the FlushFn and forwards Write -> Add, Close -> Close.
type BatchedAccumulator struct {
	cfg     BatchConfig
	flushFn FlushFn

	mu     sync.Mutex
	closed bool
	scans  map[string]*accState

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewBatchedAccumulator constructs an accumulator with the supplied
// config. If cfg.FlushInterval > 0 a background ticker is started;
// it stops on Close.
func NewBatchedAccumulator(cfg BatchConfig, fn FlushFn) *BatchedAccumulator {
	// Ensure the Prometheus collectors exist before Add() reaches for
	// SinkPendingFindings (otherwise unit tests that skip the writer
	// main panic with a nil-deref inside the locked region).
	metrics.Init()
	if cfg.FlushBatch <= 0 {
		cfg.FlushBatch = 1
	}
	a := &BatchedAccumulator{
		cfg:     cfg,
		flushFn: fn,
		scans:   make(map[string]*accState),
		stopCh:  make(chan struct{}),
	}
	if cfg.FlushInterval > 0 {
		a.wg.Add(1)
		go a.runFlusher()
	}
	return a
}

// Add records f in the per-scan buffer and runs a synchronous flush
// when the batch threshold is reached. Caller has already validated f
// (nil + scan_id).
func (a *BatchedAccumulator) Add(ctx context.Context, f *v1.Finding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrSinkClosed
	}
	s, ok := a.scans[f.ScanId]
	if !ok {
		s = &accState{lastFlush: time.Now()}
		a.scans[f.ScanId] = s
	}
	s.findings = append(s.findings, f)
	if a.cfg.MaxPerScan > 0 && len(s.findings) > a.cfg.MaxPerScan {
		a.mu.Unlock()
		return fmt.Errorf("sink %s: scan %s exceeded max %d findings",
			a.cfg.SinkLabel, f.ScanId, a.cfg.MaxPerScan)
	}
	s.pendingCount++
	metrics.SinkPendingFindings.WithLabelValues(a.cfg.SinkLabel).Inc()

	if s.pendingCount < a.cfg.FlushBatch {
		a.mu.Unlock()
		return nil
	}
	snapshot := append([]*v1.Finding(nil), s.findings...)
	pending := s.pendingCount
	s.pendingCount = 0
	s.lastFlush = time.Now()
	a.mu.Unlock()

	return a.doFlush(f.ScanId, snapshot, pending, "batch")
}

// Close drains every dirty buffer in a single synchronous pass and
// shuts the ticker down. Idempotent: subsequent calls return nil.
func (a *BatchedAccumulator) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	type pending struct {
		scanID   string
		snapshot []*v1.Finding
		count    int
	}
	var todo []pending
	for id, s := range a.scans {
		if s.pendingCount > 0 {
			todo = append(todo, pending{id,
				append([]*v1.Finding(nil), s.findings...), s.pendingCount})
		}
	}
	a.mu.Unlock()
	close(a.stopCh)
	a.wg.Wait()

	var firstErr error
	for _, p := range todo {
		if err := a.doFlush(p.scanID, p.snapshot, p.count, "close"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Flush forces a flush of every dirty buffer. Useful for tests that
// don't want to wait on the ticker.
func (a *BatchedAccumulator) Flush() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrSinkClosed
	}
	type pending struct {
		scanID   string
		snapshot []*v1.Finding
		count    int
	}
	var todo []pending
	now := time.Now()
	for id, s := range a.scans {
		if s.pendingCount > 0 {
			todo = append(todo, pending{id,
				append([]*v1.Finding(nil), s.findings...), s.pendingCount})
			s.pendingCount = 0
			s.lastFlush = now
		}
	}
	a.mu.Unlock()

	var firstErr error
	for _, p := range todo {
		if err := a.doFlush(p.scanID, p.snapshot, p.count, "interval"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *BatchedAccumulator) doFlush(scanID string, snapshot []*v1.Finding, pending int, trigger string) error {
	start := time.Now()
	err := a.flushFn(scanID, snapshot)
	metrics.ObserveFlush(a.cfg.SinkLabel, pending, trigger, time.Since(start).Seconds())
	metrics.SinkPendingFindings.WithLabelValues(a.cfg.SinkLabel).Sub(float64(pending))
	return err
}

// runFlusher fires every cfg.FlushInterval and flushes any scan that
// has been idle for at least one interval — keeps partial-scan files
// reasonably fresh without waking on every Add.
func (a *BatchedAccumulator) runFlusher() {
	defer a.wg.Done()
	t := time.NewTicker(a.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-t.C:
			a.tickFlush()
		}
	}
}

func (a *BatchedAccumulator) tickFlush() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	type pending struct {
		scanID   string
		snapshot []*v1.Finding
		count    int
	}
	var todo []pending
	now := time.Now()
	for id, s := range a.scans {
		if s.pendingCount == 0 {
			continue
		}
		if now.Sub(s.lastFlush) < a.cfg.FlushInterval {
			continue
		}
		todo = append(todo, pending{id,
			append([]*v1.Finding(nil), s.findings...), s.pendingCount})
		s.pendingCount = 0
		s.lastFlush = now
	}
	a.mu.Unlock()

	for _, p := range todo {
		// Best-effort: an interval flush failure is logged via the
		// metric (it'll surface as a writer_sink_errors_total bump
		// in the caller's flushFn) but doesn't block the next tick.
		_ = a.doFlush(p.scanID, p.snapshot, p.count, "interval")
	}
}
