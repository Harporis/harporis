package tui

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func mkScan(id string, st v1.ScanState, ts, chunks, secrets int64, source string) *v1.StatusEvent {
	return &v1.StatusEvent{
		ScanId: id, State: st, Timestamp: ts, Source: source,
		Metrics: &v1.ScanMetrics{ChunksPublished: chunks, SecretsFound: secrets},
	}
}

func TestCompareColumn(t *testing.T) {
	a := mkScan("a", v1.ScanState_RUNNING, 1, 5, 2, "github")
	b := mkScan("b", v1.ScanState_RUNNING, 2, 9, 1, "gitlab")
	if compareColumn(a, b, sortScanID) >= 0 {
		t.Fatal("a<b by id")
	}
	if compareColumn(a, b, sortChunks) >= 0 {
		t.Fatal("5<9 chunks")
	}
	if compareColumn(a, b, sortSecrets) <= 0 {
		t.Fatal("2>1 secrets")
	}
	if compareColumn(a, b, sortUpdated) >= 0 {
		t.Fatal("ts 1<2")
	}
	if compareColumn(a, b, sortSource) >= 0 {
		t.Fatal(`"github" < "gitlab" by source`)
	}
	done := mkScan("d", v1.ScanState_COMPLETED, 3, 0, 0, "src")
	// "COMPLETED" < "RUNNING" lexicographically
	if compareColumn(done, a, sortState) >= 0 {
		t.Fatal(`"COMPLETED" < "RUNNING" by state`)
	}
	if compareColumn(a, done, sortState) <= 0 {
		t.Fatal(`"RUNNING" > "COMPLETED" by state`)
	}
}

func TestSortColumnNextCycles(t *testing.T) {
	if sortUpdated.next() != sortScanID {
		t.Fatal("next() must wrap from Updated back to ScanID")
	}
}

func TestSortedExplicitColumnAscDesc(t *testing.T) {
	m := NewFleetModel()
	put := func(e *v1.StatusEvent) { m.scans[e.ScanId] = e }
	put(mkScan("a", v1.ScanState_RUNNING, 1, 5, 0, "s"))
	put(mkScan("b", v1.ScanState_COMPLETED, 2, 9, 0, "s"))
	put(mkScan("c", v1.ScanState_RUNNING, 3, 1, 0, "s"))

	m.sortExplicit = true
	m.sortCol = sortChunks
	asc := m.sorted()
	if asc[0].ScanId != "c" || asc[2].ScanId != "b" {
		t.Fatalf("chunks asc want c..b, got %s..%s", asc[0].ScanId, asc[2].ScanId)
	}
	m.sortRev = true
	desc := m.sorted()
	if desc[0].ScanId != "b" || desc[2].ScanId != "c" {
		t.Fatalf("chunks desc want b..c, got %s..%s", desc[0].ScanId, desc[2].ScanId)
	}
}

func TestSortedAppliesFilter(t *testing.T) {
	m := NewFleetModel()
	m.scans["a"] = mkScan("a", v1.ScanState_RUNNING, 1, 0, 0, "github.com/x")
	m.scans["b"] = mkScan("b", v1.ScanState_RUNNING, 2, 0, 0, "gitlab.com/y")
	f, _ := ParseFilter("source:github")
	m.filter = f
	got := m.sorted()
	if len(got) != 1 || got[0].ScanId != "a" {
		t.Fatalf("filter should leave only a, got %v", got)
	}
}
