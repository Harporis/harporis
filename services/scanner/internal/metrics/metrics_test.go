package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisters_All(t *testing.T) {
	Init()
	ChunksConsumed.Inc()
	FindingsPublished.WithLabelValues("HIGH").Inc()
	RuleMatches.WithLabelValues("aws-access-key-id", "HIGH").Inc()
	BuildInfo.WithLabelValues("test", "deadbeef", "1").Set(1)

	srv := httptest.NewServer(Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{
		"scanner_chunks_consumed_total",
		"scanner_findings_published_total",
		"scanner_rule_matches_total",
		"scanner_entropy_filter_dropped_total",
		"scanner_chunks_dropped_total",
		"scanner_chunk_processing_seconds",
		"scanner_status_updates_published_total",
		"scanner_nats_publish_errors_total",
		"scanner_active_scans",
		"scanner_build_info",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
