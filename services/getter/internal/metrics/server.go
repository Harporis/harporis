package metrics

import (
	"context"
	"fmt"
	"net/http"
)

// ServeAsync starts an HTTP server exposing /metrics on the given port.
// Returns the http.Server so main can Shutdown it gracefully.
func ServeAsync(ctx context.Context, port int) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
	go func() {
		_ = srv.ListenAndServe()
	}()
	return srv
}
