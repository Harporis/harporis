package tui

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestFleetModelFoldsLatestPerScan(t *testing.T) {
	m := NewFleetModel()
	m2, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 1}})
	fm := m2.(FleetModel)
	m3, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_COMPLETED, Timestamp: 2}})
	fm = m3.(FleetModel)

	scans := fm.Scans()
	if len(scans) != 1 {
		t.Fatalf("want 1 scan, got %d", len(scans))
	}
	if scans["a"].State != v1.ScanState_COMPLETED {
		t.Fatalf("want latest COMPLETED, got %v", scans["a"].State)
	}
}

func TestFleetModelOlderTimestampIgnored(t *testing.T) {
	m := NewFleetModel()
	m2, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_COMPLETED, Timestamp: 5}})
	fm := m2.(FleetModel)
	m3, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 2}})
	fm = m3.(FleetModel)

	if fm.Scans()["a"].State != v1.ScanState_COMPLETED {
		t.Fatalf("older event must not overwrite newer; got %v", fm.Scans()["a"].State)
	}
}
