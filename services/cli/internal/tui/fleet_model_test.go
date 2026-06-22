package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestFleetModelBoundsMemoryAndKeepsActive(t *testing.T) {
	m := NewFleetModel()
	send := func(fm FleetModel, id string, st v1.ScanState, ts int64) FleetModel {
		next, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: st, Timestamp: ts}})
		return next.(FleetModel)
	}
	// One active scan we expect to survive eviction.
	m = send(m, "live", v1.ScanState_RUNNING, 0)
	// Flood with terminal scans well past the cap.
	for i := 0; i < maxFleetScans+300; i++ {
		m = send(m, fmt.Sprintf("done-%05d", i), v1.ScanState_COMPLETED, int64(i+1))
	}
	if got := len(m.Scans()); got > maxFleetScans {
		t.Fatalf("map not bounded: got %d, want <= %d", got, maxFleetScans)
	}
	if _, ok := m.Scans()["live"]; !ok {
		t.Fatalf("active scan was evicted; active scans must be retained over terminal ones")
	}
	// Oldest terminal scans should be the ones evicted, newest retained.
	if _, ok := m.Scans()["done-00000"]; ok {
		t.Fatalf("oldest terminal scan should have been evicted first")
	}
}

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

func TestFleetModelTerminalStateSticky(t *testing.T) {
	m := NewFleetModel()
	// COMPLETED at ts=10, then RUNNING at ts=20 (newer timestamp).
	// The later RUNNING must NOT overwrite terminal COMPLETED.
	m2, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "x", State: v1.ScanState_COMPLETED, Timestamp: 10}})
	fm := m2.(FleetModel)
	m3, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "x", State: v1.ScanState_RUNNING, Timestamp: 20}})
	fm = m3.(FleetModel)

	if fm.Scans()["x"].State != v1.ScanState_COMPLETED {
		t.Fatalf("terminal state must be sticky: got %v, want COMPLETED", fm.Scans()["x"].State)
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

func keyMsg(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestCursorNavigationClamps(t *testing.T) {
	m := NewFleetModel()
	send := func(fm FleetModel, id string, st v1.ScanState, ts int64) FleetModel {
		next, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: st, Timestamp: ts}})
		return next.(FleetModel)
	}
	m = send(m, "a", v1.ScanState_RUNNING, 3)
	m = send(m, "b", v1.ScanState_RUNNING, 2)
	m = send(m, "c", v1.ScanState_RUNNING, 1)

	// Up at top stays at 0.
	up, _ := m.Update(keyMsg("up"))
	if up.(FleetModel).Cursor() != 0 {
		t.Fatalf("cursor must clamp at 0, got %d", up.(FleetModel).Cursor())
	}
	// Down moves, and clamps at the last row (3 rows -> max index 2).
	cur := m
	for i := 0; i < 5; i++ {
		n, _ := cur.Update(keyMsg("down"))
		cur = n.(FleetModel)
	}
	if cur.Cursor() != 2 {
		t.Fatalf("cursor must clamp at last row (2), got %d", cur.Cursor())
	}
}

func TestViewShowsCursorMarker(t *testing.T) {
	m := NewFleetModel()
	n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "only", State: v1.ScanState_RUNNING, Timestamp: 1}})
	m = n.(FleetModel)
	if !strings.Contains(m.View(), ">") {
		t.Fatalf("list view must render a cursor marker; got:\n%s", m.View())
	}
}

func TestFleetModelHeaderCountsAllScans(t *testing.T) {
	m := NewFleetModel()
	send := func(fm FleetModel, id string, st v1.ScanState, ts int64) FleetModel {
		next, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: st, Timestamp: ts}})
		return next.(FleetModel)
	}
	m = send(m, "run-a", v1.ScanState_RUNNING, 1)
	m = send(m, "done-b", v1.ScanState_COMPLETED, 2)
	m.activeOnly = true
	out := m.View()
	if !strings.Contains(out, "1 active / 2 scans") {
		t.Fatalf("header should show total tracked scans under filter; got:\n%s", out)
	}
}

func TestClampCursorEmptyVisibleSet(t *testing.T) {
	m := NewFleetModel()
	n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "done", State: v1.ScanState_COMPLETED, Timestamp: 1}})
	m = n.(FleetModel)
	// Toggle active-only: the only scan is terminal, so visible set becomes empty.
	f, _ := m.Update(keyMsg("f"))
	m = f.(FleetModel)
	if m.Cursor() < 0 {
		t.Fatalf("cursor must never be negative, got %d", m.Cursor())
	}
}

func TestSortKeysCycleAndReverse(t *testing.T) {
	m := NewFleetModel()
	s1, _ := m.Update(keyMsg("s"))
	col, rev, explicit := s1.(FleetModel).SortState()
	if !explicit || col != sortScanID || rev {
		t.Fatalf("first s: want explicit ScanID asc, got col=%d rev=%v explicit=%v", col, rev, explicit)
	}
	s2, _ := s1.(FleetModel).Update(keyMsg("s"))
	col, _, _ = s2.(FleetModel).SortState()
	if col != sortState {
		t.Fatalf("second s: want STATE, got %d", col)
	}
	rev1, _ := s2.(FleetModel).Update(keyMsg("S"))
	_, rev, _ = rev1.(FleetModel).SortState()
	if !rev {
		t.Fatal("S must toggle reverse")
	}
}

func TestFilterInputApplies(t *testing.T) {
	m := NewFleetModel()
	n, _ := m.Update(keyMsg("/"))
	m = n.(FleetModel)
	if !m.Filtering() {
		t.Fatal("/ must enter filter mode")
	}
	for _, ch := range []string{"s", "t", "a", "t", "e", ":", "r", "u", "n"} {
		n, _ = m.Update(keyMsg(ch))
		m = n.(FleetModel)
	}
	n, _ = m.Update(keyMsg("enter"))
	m = n.(FleetModel)
	if m.Filtering() {
		t.Fatal("enter must leave filter mode")
	}
	if m.FilterRaw() != "state:run" {
		t.Fatalf("filter not applied, got %q", m.FilterRaw())
	}
}

func TestFilterInputUnknownKeyShowsError(t *testing.T) {
	m := NewFleetModel()
	n, _ := m.Update(keyMsg("/"))
	m = n.(FleetModel)
	for _, ch := range []string{"x", ":", "y"} {
		n, _ = m.Update(keyMsg(ch))
		m = n.(FleetModel)
	}
	n, _ = m.Update(keyMsg("enter"))
	m = n.(FleetModel)
	if !strings.Contains(m.View(), "filter error") {
		t.Fatalf("invalid filter must surface an error in the view; got:\n%s", m.View())
	}
}

func TestHeaderShowsSortIndicator(t *testing.T) {
	m := NewFleetModel()
	nn, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 1}})
	m = nn.(FleetModel)
	s1, _ := m.Update(keyMsg("s")) // explicit ScanID asc
	if !strings.Contains(s1.(FleetModel).View(), "SCAN_ID↑") {
		t.Fatalf("active sort column must show ↑; got:\n%s", s1.(FleetModel).View())
	}
}
