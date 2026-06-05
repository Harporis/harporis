// Package worker glues the detector to the NATS publisher and the status
// tracker. One Handler is shared across N worker goroutines; the
// ChunksConsumer (Task 10) calls Handler.Handle from each Fetch loop.
package worker

import (
	"context"
	"fmt"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/scanner/internal/detect"
	"github.com/Harporis/harporis/services/scanner/internal/metrics"
)

// Publisher is the subset of nats.Publisher used here.
type Publisher interface {
	PublishFinding(ctx context.Context, f *v1.Finding) error
}

// Tracker is the subset of status.Tracker used here.
type Tracker interface {
	Incr(scanID string, delta int64)
	FinalEmit(ctx context.Context, scanID string) error
}

// DetectorProvider returns the currently active detector. Indirection
// here is what lets rules hot-reload work: in production this is the
// reload watcher's Current() method; in tests it's a static closure.
type DetectorProvider interface {
	Current() *detect.Detector
}

// staticDetector wraps a single *Detector for callers that don't need
// hot-reload (tests, one-shot scans).
type staticDetector struct{ d *detect.Detector }

func (s staticDetector) Current() *detect.Detector { return s.d }

// Static returns a DetectorProvider over a single immutable detector.
func Static(d *detect.Detector) DetectorProvider { return staticDetector{d: d} }

// Handler is the per-chunk processing logic. One Handler is shared across
// all worker goroutines (it carries only immutable dependencies).
type Handler struct {
	dp  DetectorProvider
	pub Publisher
	tr  Tracker
}

// NewHandler binds the handler to a DetectorProvider. Use worker.Static
// for tests / non-reloading callers, or rules.Watcher in production.
func NewHandler(dp DetectorProvider, pub Publisher, tr Tracker) *Handler {
	return &Handler{dp: dp, pub: pub, tr: tr}
}

// Handle is what the consumer's ChunkHandler delegates to. Errors are
// surfaced so the consumer Naks for redelivery.
//
// Counter increments fire per successful publish (not once at the end). If a
// publish mid-batch fails and we bail out, earlier successes have already
// bumped the counter, so JetStream MsgId dedup on redelivery can safely
// absorb the duplicates without losing the count.
func (h *Handler) Handle(ctx context.Context, c *v1.GitRowChunk) error {
	findings := h.dp.Current().ScanChunk(c)
	for _, f := range findings {
		if err := h.pub.PublishFinding(ctx, f); err != nil {
			return fmt.Errorf("publish finding %s: %w", f.FindingId, err)
		}
		h.tr.Incr(c.ScanId, 1)
		metrics.FindingsPublished.WithLabelValues(f.Severity.String()).Inc()
	}
	if c.IsLastInScan {
		if err := h.tr.FinalEmit(ctx, c.ScanId); err != nil {
			return fmt.Errorf("final emit %s: %w", c.ScanId, err)
		}
	}
	return nil
}
