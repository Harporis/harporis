package scan

import (
	"sync/atomic"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Context is the per-scan runtime state.
type Context struct {
	ID    string
	state stateGuard

	BlobsScanned    atomic.Int64
	BlobsSkipped    atomic.Int64
	ChunksPublished atomic.Int64
	BytesPublished  atomic.Int64
	Errors          atomic.Int64

	// CancelReason is set by registry.Cancel(id, reason) before the
	// cancel func fires. Empty if cancellation came from process shutdown.
	cancelReason atomic.Pointer[string]
}

// SetCancelReason stores the reason supplied by a CancelScanRequest so the
// runner can surface it in the final status event. Safe to call concurrently.
func (c *Context) SetCancelReason(reason string) {
	c.cancelReason.Store(&reason)
}

// CancelReason returns the stored reason or "" if none was set.
func (c *Context) CancelReason() string {
	if p := c.cancelReason.Load(); p != nil {
		return *p
	}
	return ""
}

func NewContext(id string) *Context {
	c := &Context{ID: id}
	c.state.state = v1.ScanState_PENDING
	return c
}

func (c *Context) State() v1.ScanState              { return c.state.get() }
func (c *Context) Transition(s v1.ScanState) error  { return c.state.transition(s) }
func (c *Context) IsTerminal() bool                 { return isTerminal(c.State()) }

// Snapshot returns a metrics snapshot for status events.
func (c *Context) Snapshot() *v1.ScanMetrics {
	return &v1.ScanMetrics{
		BlobsScanned:    c.BlobsScanned.Load(),
		BlobsSkipped:    c.BlobsSkipped.Load(),
		ChunksPublished: c.ChunksPublished.Load(),
		BytesPublished:  c.BytesPublished.Load(),
		ErrorsTotal:     c.Errors.Load(),
	}
}

