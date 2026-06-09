// Package metrics holds the writer's Prometheus collectors. Init() is
// called once at startup; Handler() returns the /metrics HTTP handler.
package metrics

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	once sync.Once

	FindingsConsumed       prometheus.Counter
	FindingsWriteSeconds   *prometheus.HistogramVec // labels: sink
	SinkWrites             *prometheus.CounterVec   // labels: sink, severity
	SinkErrors             *prometheus.CounterVec   // labels: sink, reason
	SinkFormatIgnored      *prometheus.CounterVec   // labels: requested_format
	NATSDeliveryErrors     *prometheus.CounterVec   // labels: kind
	ActiveScans            prometheus.Gauge
	BuildInfo              *prometheus.GaugeVec // labels: version, commit, proto_version
	OrphanTempfilesSwept   prometheus.Counter

	registry *prometheus.Registry
)

// Init creates and registers all collectors. Subsequent calls are no-ops.
func Init() {
	once.Do(func() {
		registry = prometheus.NewRegistry()
		FindingsConsumed = prometheus.NewCounter(prometheus.CounterOpts{Name: "writer_findings_consumed_total"})
		FindingsWriteSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "writer_findings_write_seconds", Buckets: prometheus.DefBuckets}, []string{"sink"})
		SinkWrites = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "writer_sink_writes_total"}, []string{"sink", "severity"})
		SinkErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "writer_sink_errors_total"}, []string{"sink", "reason"})
		SinkFormatIgnored = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "writer_sink_format_ignored_total",
			Help: "Findings whose per-scan -f named a format that has no enabled sink (e.g. `-f pdf` while pdf_enabled=false).",
		}, []string{"requested_format"})
		NATSDeliveryErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "writer_nats_delivery_errors_total"}, []string{"kind"})
		ActiveScans = prometheus.NewGauge(prometheus.GaugeOpts{Name: "writer_active_scans"})
		BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "writer_build_info"}, []string{"version", "commit", "proto_version"})
		OrphanTempfilesSwept = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "writer_orphan_tempfiles_swept_total",
			Help: "Stale .<scan_id>.<hex>.{xlsx,pdf,sarif,html} files cleaned up on writer startup (left behind by a crash mid-flush).",
		})
		for _, c := range []prometheus.Collector{
			FindingsConsumed, FindingsWriteSeconds, SinkWrites, SinkErrors, SinkFormatIgnored,
			NATSDeliveryErrors, ActiveScans, BuildInfo, OrphanTempfilesSwept,
		} {
			registry.MustRegister(c)
		}
		// Seed *Vec collectors with empty labels so the metric names appear in
		// /metrics scrape output even before any real observation. BuildInfo is
		// deliberately NOT seeded — main.go populates it with real
		// (version, commit, proto_version) labels at startup.
		FindingsWriteSeconds.WithLabelValues("")
		SinkWrites.WithLabelValues("", "")
		SinkErrors.WithLabelValues("", "")
		SinkFormatIgnored.WithLabelValues("")
		NATSDeliveryErrors.WithLabelValues("")
	})
}

// Handler returns the /metrics HTTP handler bound to the package's custom registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// Registry exposes the custom registry for tests that need to Gather() directly.
func Registry() *prometheus.Registry { return registry }
