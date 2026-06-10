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
	SinkFlushSeconds       *prometheus.HistogramVec // labels: sink
	SinkFlushTotal         *prometheus.CounterVec   // labels: sink, trigger
	SinkFlushBatchSize     *prometheus.HistogramVec // labels: sink
	SinkPendingFindings    *prometheus.GaugeVec     // labels: sink
	SinkPostFinalizeDropped *prometheus.CounterVec  // labels: sink
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
		SinkFlushSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "writer_sink_flush_seconds",
			Help:    "Time the accumulator sinks (SARIF/HTML/XLSX/PDF/Parquet) spend rendering + atomically renaming a per-scan file. NDJSON streams and does NOT contribute.",
			Buckets: []float64{.0005, .001, .002, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"sink"})
		SinkFlushTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "writer_sink_flush_total",
			Help: "Sink finalizations by trigger. Accumulator sinks (SARIF/HTML/XLSX/PDF): `batch` (count threshold) | `interval` (periodic ticker) | `close` (shutdown) | `terminal` (HARPORIS_STATUS terminal event). Streaming Parquet: `terminal` | `idle` (no Write for idle_timeout) | `close`.",
		}, []string{"sink", "trigger"})
		SinkFlushBatchSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "writer_sink_flush_batch_size",
			Help:    "Number of NEW findings coalesced into a single accumulator-sink flush.",
			Buckets: []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		}, []string{"sink"})
		SinkPendingFindings = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "writer_sink_pending_findings",
			Help: "Findings accumulated in memory but not yet flushed to disk (sum across all live scans, per sink).",
		}, []string{"sink"})
		SinkPostFinalizeDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "writer_sink_post_finalize_dropped_total",
			Help: "Findings dropped because they arrived after the per-scan sink was finalized (would overwrite the on-disk file on rename). Indicates scanner-vs-writer timing drift — tune finalize_grace_ms or flush_interval_ms.",
		}, []string{"sink"})
		NATSDeliveryErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "writer_nats_delivery_errors_total"}, []string{"kind"})
		ActiveScans = prometheus.NewGauge(prometheus.GaugeOpts{Name: "writer_active_scans"})
		BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "writer_build_info"}, []string{"version", "commit", "proto_version"})
		OrphanTempfilesSwept = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "writer_orphan_tempfiles_swept_total",
			Help: "Stale .<scan_id>.<hex>.{xlsx,pdf,sarif,html} files cleaned up on writer startup (left behind by a crash mid-flush).",
		})
		for _, c := range []prometheus.Collector{
			FindingsConsumed, FindingsWriteSeconds, SinkWrites, SinkErrors, SinkFormatIgnored,
			SinkFlushSeconds, SinkFlushTotal, SinkFlushBatchSize, SinkPendingFindings,
			SinkPostFinalizeDropped,
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
		SinkFlushSeconds.WithLabelValues("")
		SinkFlushTotal.WithLabelValues("", "")
		SinkFlushBatchSize.WithLabelValues("")
		SinkPendingFindings.WithLabelValues("")
		SinkPostFinalizeDropped.WithLabelValues("")
		NATSDeliveryErrors.WithLabelValues("")
	})
}

// Handler returns the /metrics HTTP handler bound to the package's custom registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// ObserveFlush is a one-shot helper for accumulator sinks that want to
// emit all four flush metrics from a single call site. trigger is one
// of "batch" / "interval" / "close". Calls Init() so sinks can emit
// metrics from unit tests without an explicit metrics setup step.
func ObserveFlush(sinkName string, batchSize int, trigger string, dur float64) {
	Init()
	SinkFlushSeconds.WithLabelValues(sinkName).Observe(dur)
	SinkFlushTotal.WithLabelValues(sinkName, trigger).Inc()
	SinkFlushBatchSize.WithLabelValues(sinkName).Observe(float64(batchSize))
}

// Registry exposes the custom registry for tests that need to Gather() directly.
func Registry() *prometheus.Registry { return registry }
