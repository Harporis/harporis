package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Harporis/harporis/services/scanner/internal/health"
)

// ServeAsync starts an HTTP server exposing /metrics, /healthz, /readyz on addr.
// Returns the http.Server so main can Shutdown it gracefully. Fatal listen
// errors are logged at Warn level.
func ServeAsync(ctx context.Context, addr string, h *health.Health) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	mux.Handle("/healthz", h.HealthzHandler())
	mux.Handle("/readyz", h.ReadyzHandler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("metrics server stopped", "addr", addr, "err", err)
		}
	}()
	return srv
}
