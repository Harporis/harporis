// Package metrics holds the scanner's Prometheus collectors. Init() is
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

	ChunksConsumed         prometheus.Counter
	ChunksDropped          *prometheus.CounterVec   // labels: reason
	ChunkProcessingSeconds *prometheus.HistogramVec // labels: kind
	RuleMatches            *prometheus.CounterVec   // labels: rule_id, severity
	FindingsPublished      *prometheus.CounterVec   // labels: severity
	EntropyFilterDropped   *prometheus.CounterVec   // labels: rule_id
	StatusUpdatesPublished prometheus.Counter
	NATSPublishErrors      *prometheus.CounterVec // labels: subject
	ActiveScans            prometheus.Gauge
	BuildInfo              *prometheus.GaugeVec // labels: version, commit, proto_version

	registry *prometheus.Registry
)

// Init creates and registers all collectors. Subsequent calls are no-ops.
func Init() {
	once.Do(func() {
		registry = prometheus.NewRegistry()
		ChunksConsumed = prometheus.NewCounter(prometheus.CounterOpts{Name: "scanner_chunks_consumed_total"})
		ChunksDropped = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scanner_chunks_dropped_total"}, []string{"reason"})
		ChunkProcessingSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "scanner_chunk_processing_seconds", Buckets: prometheus.DefBuckets}, []string{"kind"})
		RuleMatches = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scanner_rule_matches_total"}, []string{"rule_id", "severity"})
		FindingsPublished = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scanner_findings_published_total"}, []string{"severity"})
		EntropyFilterDropped = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scanner_entropy_filter_dropped_total"}, []string{"rule_id"})
		StatusUpdatesPublished = prometheus.NewCounter(prometheus.CounterOpts{Name: "scanner_status_updates_published_total"})
		NATSPublishErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "scanner_nats_publish_errors_total"}, []string{"subject"})
		ActiveScans = prometheus.NewGauge(prometheus.GaugeOpts{Name: "scanner_active_scans"})
		BuildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "scanner_build_info"}, []string{"version", "commit", "proto_version"})
		for _, c := range []prometheus.Collector{
			ChunksConsumed, ChunksDropped, ChunkProcessingSeconds, RuleMatches,
			FindingsPublished, EntropyFilterDropped, StatusUpdatesPublished,
			NATSPublishErrors, ActiveScans, BuildInfo,
		} {
			registry.MustRegister(c)
		}
		// Seed *Vec collectors with empty labels so the metric names appear in
		// /metrics scrape output even before any real observation. BuildInfo is
		// deliberately NOT seeded — main.go populates it with real
		// (version, commit, proto_version) labels at startup; a phantom empty
		// series would break the standard "join on() group_left(version)" pattern.
		ChunksDropped.WithLabelValues("")
		ChunkProcessingSeconds.WithLabelValues("")
		EntropyFilterDropped.WithLabelValues("")
		NATSPublishErrors.WithLabelValues("")
	})
}

// Handler returns the /metrics HTTP handler bound to the package's custom registry.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

// Registry exposes the custom registry for tests that need to Gather() directly.
func Registry() *prometheus.Registry { return registry }
