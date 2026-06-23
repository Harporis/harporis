package tui

import (
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/findings"
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

func TestViewDetailNilLatestNoPanic(t *testing.T) {
	m := NewFleetModel()
	m.view = viewDetail
	m.detail = detailState{scanID: "s", latest: nil, loading: true}
	_ = m.View() // must not panic
}

type fakeFindingsLoader struct {
	out    []findings.Finding
	err    error
	called string
}

func (f *fakeFindingsLoader) Load(scanID string) ([]findings.Finding, error) {
	f.called = scanID
	return f.out, f.err
}

func TestDetailTabSwitchLoadsFindings(t *testing.T) {
	loader := &fakeFindingsLoader{out: []findings.Finding{{Severity: "HIGH", RuleID: "aws-access-key-id", FilePath: "x", LineNumber: 1}}}
	m := NewFleetModel().WithClient(&fakeLoader{}).WithFindingsLoader(loader)
	n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_COMPLETED, Timestamp: 1}})
	m = n.(FleetModel)
	e, _ := m.Update(keyMsg("enter")) // drill in -> tabStatus
	m = e.(FleetModel)
	if m.detail.tab != tabStatus {
		t.Fatal("drill-in must start on Status tab")
	}
	// Switch to Findings -> must emit a load Cmd.
	tb, cmd := m.Update(keyMsg("tab"))
	m = tb.(FleetModel)
	if m.detail.tab != tabFindings {
		t.Fatal("tab must switch to Findings")
	}
	if cmd == nil {
		t.Fatal("first Findings entry must emit a load Cmd")
	}
	msg := cmd()
	loaded, ok := msg.(FindingsLoadedMsg)
	if !ok || loaded.ScanID != "a" {
		t.Fatalf("cmd must yield FindingsLoadedMsg for a, got %#v", msg)
	}
	n2, _ := m.Update(loaded)
	m = n2.(FleetModel)
	if !strings.Contains(m.View(), "aws-access-key-id") {
		t.Fatalf("findings must render after load; got:\n%s", m.View())
	}
}

func TestDetailChangingScanResetsTab(t *testing.T) {
	loader := &fakeFindingsLoader{}
	m := NewFleetModel().WithClient(&fakeLoader{}).WithFindingsLoader(loader)
	for _, id := range []string{"a", "b"} {
		n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: v1.ScanState_RUNNING, Timestamp: 1}})
		m = n.(FleetModel)
	}
	e, _ := m.Update(keyMsg("enter"))
	m = e.(FleetModel)
	tb, _ := m.Update(keyMsg("tab")) // go to Findings on first scan
	m = tb.(FleetModel)
	esc, _ := m.Update(keyMsg("esc")) // back to fleet
	m = esc.(FleetModel)
	d, _ := m.Update(keyMsg("down")) // move cursor to other scan
	m = d.(FleetModel)
	e2, _ := m.Update(keyMsg("enter")) // drill into the other scan
	m = e2.(FleetModel)
	if m.detail.tab != tabStatus {
		t.Fatal("a fresh drill-in must reset to Status tab")
	}
}
