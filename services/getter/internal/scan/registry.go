package scan

import (
	"errors"
	"sync"
)

var ErrAlreadyExists = errors.New("scan already active")

type entry struct {
	ctx    *Context
	cancel func()
}

type Registry struct {
	mu    sync.RWMutex
	scans map[string]entry
}

func NewRegistry() *Registry {
	return &Registry{scans: map[string]entry{}}
}

func (r *Registry) Register(id string, sc *Context, cancel func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.scans[id]; exists {
		return ErrAlreadyExists
	}
	r.scans[id] = entry{ctx: sc, cancel: cancel}
	return nil
}

func (r *Registry) Get(id string) (*Context, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.scans[id]
	if !ok {
		return nil, false
	}
	return e.ctx, true
}

// Cancel invokes the cancel func for the given scan_id, if active, and
// records the supplied reason on the scan context so the runner can
// surface it in the final status event. Returns false if no such scan
// is in the registry.
func (r *Registry) Cancel(id, reason string) bool {
	r.mu.RLock()
	e, ok := r.scans[id]
	r.mu.RUnlock()
	if !ok {
		return false
	}
	if reason != "" {
		e.ctx.SetCancelReason(reason)
	}
	e.cancel()
	return true
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.scans, id)
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.scans)
}
