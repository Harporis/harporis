package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	once sync.Once

	BlobsScanned        *prometheus.CounterVec
	BlobsSkipped        *prometheus.CounterVec
	ChunksPublished     *prometheus.CounterVec
	BytesPublished      *prometheus.CounterVec
	ErrorsTotal         *prometheus.CounterVec
	StatusPublishErrors *prometheus.CounterVec
	ScanDuration        *prometheus.HistogramVec
	ActiveScans         prometheus.Gauge

	registry *prometheus.Registry
)

// Init must be called once at startup. Subsequent calls are no-ops.
func Init() {
	once.Do(func() {
		registry = prometheus.NewRegistry()
		BlobsScanned = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_blobs_scanned_total"}, []string{"scan_id"})
		BlobsSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_blobs_skipped_total"}, []string{"scan_id", "reason"})
		ChunksPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_chunks_published_total"}, []string{"scan_id", "kind"})
		BytesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_bytes_published_total"}, []string{"scan_id"})
		ErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_errors_total"}, []string{"scan_id", "type"})
		StatusPublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "harporis_getter_status_publish_errors_total"}, []string{"scan_id"})
		ScanDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "harporis_getter_scan_duration_seconds", Buckets: prometheus.DefBuckets,
		}, []string{"scan_id", "status"})
		ActiveScans = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "harporis_getter_active_scans"})
		for _, c := range []prometheus.Collector{
			BlobsScanned, BlobsSkipped, ChunksPublished, BytesPublished,
			ErrorsTotal, StatusPublishErrors, ScanDuration, ActiveScans,
		} {
			registry.MustRegister(c)
		}
	})
}

// Handler returns an http.Handler exposing the custom registry's metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// Registry returns the custom registry so tests can Gather() metrics
// directly. Returns nil if Init() has not been called.
func Registry() *prometheus.Registry { return registry }
