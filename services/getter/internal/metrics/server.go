package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
)

// ServeAsync starts an HTTP server exposing /metrics on the given port.
// Returns the http.Server so main can Shutdown it gracefully. Fatal listen
// errors (e.g. port in use) are surfaced as Warn logs so the operator
// finds out without having to inspect strace output.
func ServeAsync(ctx context.Context, port int) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("metrics server stopped", "port", port, "err", err)
		}
	}()
	return srv
}
