package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetrics_ExpositionContainsCounters(t *testing.T) {
	Init() // idempotent
	BlobsScanned.WithLabelValues("scan-1").Inc()
	BlobsSkipped.WithLabelValues("scan-1", "binary_extension").Inc()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	require.Contains(t, body, "harporis_getter_blobs_scanned_total")
	require.Contains(t, body, "harporis_getter_blobs_skipped_total")
	require.Contains(t, body, `reason="binary_extension"`)
	_ = strings.Contains // pin import
}
