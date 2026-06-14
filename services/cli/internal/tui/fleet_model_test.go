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

func TestFleetModelSortAndActiveFilter(t *testing.T) {
	m := NewFleetModel()
	send := func(fm FleetModel, id string, st v1.ScanState, ts int64) FleetModel {
		next, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: st, Timestamp: ts}})
		return next.(FleetModel)
	}
	m = send(m, "done-old", v1.ScanState_COMPLETED, 10)
	m = send(m, "run-a", v1.ScanState_RUNNING, 20)
	m = send(m, "run-b", v1.ScanState_RUNNING, 30)

	// Default: active (non-terminal) first, then newest timestamp first.
	got := m.sorted()
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	if got[0].ScanId != "run-b" || got[1].ScanId != "run-a" || got[2].ScanId != "done-old" {
		t.Fatalf("unexpected order: %s, %s, %s", got[0].ScanId, got[1].ScanId, got[2].ScanId)
	}

	// activeOnly hides terminal scans.
	m.activeOnly = true
	act := m.sorted()
	if len(act) != 2 {
		t.Fatalf("activeOnly want 2 rows, got %d", len(act))
	}
	for _, e := range act {
		if e.ScanId == "done-old" {
			t.Fatalf("terminal scan leaked into activeOnly view")
		}
	}
}
