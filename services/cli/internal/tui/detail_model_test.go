package tui

import (
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestAppendEventDedupsByTimestampAndState(t *testing.T) {
	d := &detailState{}
	e1 := &v1.StatusEvent{Timestamp: 1, State: v1.ScanState_RUNNING}
	d.appendEvent(e1)
	d.appendEvent(e1) // exact dup
	d.appendEvent(&v1.StatusEvent{Timestamp: 1, State: v1.ScanState_RUNNING}) // value dup
	if len(d.history) != 1 {
		t.Fatalf("dup events must not be appended, got %d", len(d.history))
	}
	d.appendEvent(&v1.StatusEvent{Timestamp: 2, State: v1.ScanState_COMPLETED})
	if len(d.history) != 2 {
		t.Fatalf("distinct event must append, got %d", len(d.history))
	}
}

func TestViewDetailRendersMetricsAndHistory(t *testing.T) {
	m := NewFleetModel()
	m.view = viewDetail
	m.detail = detailState{
		scanID: "scan-1",
		latest: &v1.StatusEvent{
			ScanId: "scan-1", State: v1.ScanState_RUNNING, Source: "github.com/x",
			Metrics: &v1.ScanMetrics{ChunksPublished: 7, SecretsFound: 3},
		},
		history: []*v1.StatusEvent{
			{Timestamp: 1, State: v1.ScanState_PENDING, Message: "queued"},
			{Timestamp: 2, State: v1.ScanState_RUNNING, Message: "walking"},
		},
	}
	out := m.View()
	for _, want := range []string{"scan-1", "chunks 7", "secrets 3", "queued", "walking"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail view missing %q; got:\n%s", want, out)
		}
	}
}

func TestViewDetailLoadingAndError(t *testing.T) {
	m := NewFleetModel()
	m.view = viewDetail
	m.detail = detailState{scanID: "s", latest: &v1.StatusEvent{ScanId: "s"}, loading: true}
	if !strings.Contains(m.View(), "loading history") {
		t.Fatal("loading state must render a loading hint")
	}
}
