// Package health centralises the scanner's liveness/readiness probes.
// Mutated from main during startup; read by HTTP handlers.
package health

import (
	"net/http"
	"sync/atomic"
)

// Health holds the three boolean conditions that drive /healthz and /readyz.
// Goroutine-safe via atomic.Bool.
type Health struct {
	natsConnected   atomic.Bool
	consumerCreated atomic.Bool
	workerStarted   atomic.Bool
}

func New() *Health { return &Health{} }

func (h *Health) SetNATSConnected(v bool)   { h.natsConnected.Store(v) }
func (h *Health) SetConsumerCreated(v bool) { h.consumerCreated.Store(v) }
func (h *Health) SetWorkerStarted(v bool)   { h.workerStarted.Store(v) }

// HealthzHandler returns 200 iff NATS is connected.
func (h *Health) HealthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if h.natsConnected.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("nats not connected"))
	})
}

// ReadyzHandler returns 200 iff NATS is connected AND the durable consumer
// has been created AND at least one worker goroutine has entered its loop.
func (h *Health) ReadyzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if h.natsConnected.Load() && h.consumerCreated.Load() && h.workerStarted.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready"))
	})
}
