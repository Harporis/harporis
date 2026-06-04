// Package httpserver is the shared metrics + health HTTP server used by
// every Harporis service. It binds /metrics (caller-provided handler),
// /healthz, /readyz on a single mux and returns the *http.Server so the
// caller can Shutdown it gracefully.
//
// Why a tiny shared package: every service was duplicating ~20 lines of
// mux+ListenAndServe+slog-on-error boilerplate. Moving it here means
// the next service inherits the "log ListenAndServe failures at Error
// so a port-in-use never silently dark /metrics" property for free.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// ServeAsync starts an HTTP server on addr exposing /metrics, /healthz,
// /readyz. The caller passes the metrics + health handlers — this
// package intentionally doesn't depend on Prometheus or on kit/health so
// services can wire whatever Handler shape they want.
//
// Returns the *http.Server so the caller can Shutdown it. Listener
// errors other than http.ErrServerClosed are logged at Error.
//
// ctx is reserved for future cancellation hooks; it is currently unused
// because http.Server.Shutdown is the cancellation path callers actually
// want and they have the *http.Server return value to do it with.
func ServeAsync(ctx context.Context, addr string, metricsHandler, healthzHandler, readyzHandler http.Handler) *http.Server {
	_ = ctx
	mux := http.NewServeMux()
	mux.Handle("/metrics", metricsHandler)
	mux.Handle("/healthz", healthzHandler)
	mux.Handle("/readyz", readyzHandler)
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server stopped", "addr", addr, "err", err)
		}
	}()
	return srv
}
