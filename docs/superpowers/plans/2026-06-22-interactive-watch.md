# Interactive Watch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the read-only `harporis watch` fleet table into an interactive master-detail dashboard with cursor navigation, drill-in to per-scan status + event history, structured `key:value` filtering, and multi-column sorting — all in one bubbletea program and one NATS subscription.

**Architecture:** `FleetModel` (in `services/cli/internal/tui/`) graduates from "table + fold" to a master-detail controller with a `viewMode` (list | detail). New focused files carry filtering (`filter.go`), sorting (`sort.go`), and the detail panel (`detail_model.go`). Drill-in seeds history via a one-shot `ShowHistory` `tea.Cmd`; the existing wildcard status tail keeps the metrics and history live. The single-scan `WatchModel` is untouched.

**Tech Stack:** Go, charmbracelet/bubbletea + lipgloss, the project's `internal/ui` primitives, `internal/natscli` client, generated `contracts/gen/go/harporis/v1` protos.

## Global Constraints

- Module: run all `go` commands from `services/cli/` (multi-module monorepo, no top-level `go.work`).
- Trust `go build ./...` + `go test ./...` from the module dir, NOT IDE/gopls squiggles (known to lie for this module).
- Commits: NO `Co-Authored-By` trailer.
- All generated proto getters are nil-safe (`ev.GetMetrics().GetChunksPublished()` returns 0 on a nil metrics) — rely on this; do not add nil guards.
- `IsTerminal(v1.ScanState) bool` already exists in this package (`watch_model.go`) — reuse it, do not redefine.
- Keep the existing `WatchModel` and the single-scan `harporis watch <id>` path unchanged.
- The spec for this work: `docs/superpowers/specs/2026-06-22-interactive-watch-design.md`.

---

### Task 1: Structured filter (`filter.go`)

Pure parser + matcher for `key:value` fleet queries. No `FleetModel` change yet.

**Files:**
- Create: `services/cli/internal/tui/filter.go`
- Test: `services/cli/internal/tui/filter_test.go`

**Interfaces:**
- Consumes: `v1.StatusEvent`, package-local `IsTerminal`.
- Produces:
  - `type Filter struct` (zero value matches everything; field `raw string` exported-for-package use via method, holds the trimmed source query)
  - `func ParseFilter(s string) (Filter, error)`
  - `func (f Filter) Match(ev *v1.StatusEvent) bool`
  - `func (f Filter) Raw() string`

- [ ] **Step 1: Write the failing test**

```go
package tui

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func ev(id, source string, st v1.ScanState) *v1.StatusEvent {
	return &v1.StatusEvent{ScanId: id, Source: source, State: st}
}

func TestParseFilterUnknownKey(t *testing.T) {
	if _, err := ParseFilter("foo:bar"); err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
}

func TestFilterZeroValueMatchesEverything(t *testing.T) {
	var f Filter
	if !f.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("zero-value filter must match everything")
	}
}

func TestFilterStateActiveAndTerminal(t *testing.T) {
	fa, _ := ParseFilter("state:active")
	if !fa.Match(ev("a", "s", v1.ScanState_RUNNING)) {
		t.Fatal("state:active must match a non-terminal scan")
	}
	if fa.Match(ev("a", "s", v1.ScanState_COMPLETED)) {
		t.Fatal("state:active must not match a terminal scan")
	}
	ft, _ := ParseFilter("state:terminal")
	if !ft.Match(ev("a", "s", v1.ScanState_FAILED)) {
		t.Fatal("state:terminal must match a terminal scan")
	}
}

func TestFilterStateSubstringSourceIDAndBareWord(t *testing.T) {
	fs, _ := ParseFilter("state:run")
	if !fs.Match(ev("a", "s", v1.ScanState_RUNNING)) {
		t.Fatal("state:run must substring-match RUNNING")
	}
	fsrc, _ := ParseFilter("source:github")
	if !fsrc.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) ||
		fsrc.Match(ev("a", "gitlab.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("source: must substring-match Source only")
	}
	fid, _ := ParseFilter("id:abc")
	if !fid.Match(ev("xx-abc-yy", "s", v1.ScanState_RUNNING)) {
		t.Fatal("id: must substring-match ScanId")
	}
	fb, _ := ParseFilter("github")
	if !fb.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("bare word must match across fields (source here)")
	}
}

func TestFilterCombinesClausesAsAnd(t *testing.T) {
	f, _ := ParseFilter("state:active source:github")
	if !f.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("both clauses satisfied must match")
	}
	if f.Match(ev("a", "github.com/x", v1.ScanState_COMPLETED)) {
		t.Fatal("one clause failing must reject")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run TestFilter -v`
Expected: FAIL — `undefined: ParseFilter` / `undefined: Filter`.

- [ ] **Step 3: Write minimal implementation**

```go
package tui

import (
	"fmt"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Filter is a parsed structured query over the fleet table. The zero value
// matches every event. Build one with ParseFilter.
type Filter struct {
	state  string // matches State: substring, or the words "active"/"terminal"
	source string // substring over Source
	id     string // substring over ScanId
	text   string // bare word: substring across id, source, and state
	raw    string // the trimmed source query, for redisplay in the input line
}

var filterKeys = map[string]bool{"state": true, "source": true, "id": true}

// ParseFilter parses a space-separated query like `state:failed source:gh`.
// A token without a colon is a bare word matched across all fields. An
// unknown key returns an error and the zero Filter.
func ParseFilter(s string) (Filter, error) {
	f := Filter{raw: strings.TrimSpace(s)}
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, ":")
		if !ok {
			f.text = tok // last bare word wins — simple by design
			continue
		}
		k = strings.ToLower(k)
		if !filterKeys[k] {
			return Filter{}, fmt.Errorf("unknown key %q", k)
		}
		switch k {
		case "state":
			f.state = strings.ToLower(v)
		case "source":
			f.source = strings.ToLower(v)
		case "id":
			f.id = strings.ToLower(v)
		}
	}
	return f, nil
}

// Raw returns the trimmed source query, so the filter input line can be
// pre-populated when the operator reopens it.
func (f Filter) Raw() string { return f.raw }

// Match reports whether ev satisfies every clause (clauses are AND-ed).
func (f Filter) Match(ev *v1.StatusEvent) bool {
	if f.state != "" && !matchState(f.state, ev) {
		return false
	}
	src := strings.ToLower(ev.GetSource())
	id := strings.ToLower(ev.GetScanId())
	if f.source != "" && !strings.Contains(src, f.source) {
		return false
	}
	if f.id != "" && !strings.Contains(id, f.id) {
		return false
	}
	if f.text != "" {
		t := strings.ToLower(f.text)
		st := strings.ToLower(ev.GetState().String())
		if !strings.Contains(id, t) && !strings.Contains(src, t) && !strings.Contains(st, t) {
			return false
		}
	}
	return true
}

func matchState(want string, ev *v1.StatusEvent) bool {
	switch want {
	case "active":
		return !IsTerminal(ev.GetState())
	case "terminal", "done":
		return IsTerminal(ev.GetState())
	}
	return strings.Contains(strings.ToLower(ev.GetState().String()), want)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -run TestFilter -v`
Expected: PASS (all `TestFilter*` / `TestParseFilter*`).

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/filter.go services/cli/internal/tui/filter_test.go
git commit -m "feat(tui): structured key:value fleet filter"
```

---

### Task 2: Multi-column sort (`sort.go`) + parameterized `sorted()`

Add the sort column enum and comparators, then rewire `FleetModel.sorted()` to apply the filter and the chosen column/direction. Default (non-explicit) order is unchanged.

**Files:**
- Create: `services/cli/internal/tui/sort.go`
- Modify: `services/cli/internal/tui/fleet_model.go` (add sort+filter fields to `FleetModel`; rewrite `sorted()`)
- Test: `services/cli/internal/tui/sort_test.go`

**Interfaces:**
- Consumes: `Filter` from Task 1; `v1.StatusEvent`; `IsTerminal`.
- Produces:
  - `type sortColumn int` with consts `sortScanID, sortState, sortSource, sortChunks, sortSecrets, sortUpdated`
  - `var sortColumns []sortColumn`
  - `func (c sortColumn) label() string`
  - `func (c sortColumn) next() sortColumn`
  - `func compareColumn(a, b *v1.StatusEvent, col sortColumn) int`
  - New `FleetModel` fields: `sortCol sortColumn`, `sortRev bool`, `sortExplicit bool`, `filter Filter`
  - `func (m FleetModel) sorted() []*v1.StatusEvent` (now filter+sort aware)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run 'TestCompareColumn|TestSort|TestSorted' -v`
Expected: FAIL — `undefined: compareColumn` / `undefined: sortChunks` / unknown field `sortExplicit`.

- [ ] **Step 3a: Create `sort.go`**

```go
package tui

import (
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// sortColumn identifies a fleet-table column the operator can sort by.
type sortColumn int

const (
	sortScanID sortColumn = iota
	sortState
	sortSource
	sortChunks
	sortSecrets
	sortUpdated
)

var sortColumns = []sortColumn{
	sortScanID, sortState, sortSource, sortChunks, sortSecrets, sortUpdated,
}

func (c sortColumn) label() string {
	switch c {
	case sortScanID:
		return "SCAN_ID"
	case sortState:
		return "STATE"
	case sortSource:
		return "SOURCE"
	case sortChunks:
		return "CHUNKS"
	case sortSecrets:
		return "SECRETS"
	case sortUpdated:
		return "UPDATED"
	}
	return ""
}

// next returns the following column, wrapping Updated back to ScanID.
func (c sortColumn) next() sortColumn { return sortColumns[(int(c)+1)%len(sortColumns)] }

// compareColumn orders a before b on col, ascending: negative if a<b, zero
// if equal, positive if a>b. The caller applies reverse and tiebreak.
func compareColumn(a, b *v1.StatusEvent, col sortColumn) int {
	switch col {
	case sortScanID:
		return strings.Compare(a.GetScanId(), b.GetScanId())
	case sortState:
		return strings.Compare(a.GetState().String(), b.GetState().String())
	case sortSource:
		return strings.Compare(a.GetSource(), b.GetSource())
	case sortChunks:
		return cmpInt64(a.GetMetrics().GetChunksPublished(), b.GetMetrics().GetChunksPublished())
	case sortSecrets:
		return cmpInt64(a.GetMetrics().GetSecretsFound(), b.GetMetrics().GetSecretsFound())
	case sortUpdated:
		return cmpInt64(a.GetTimestamp(), b.GetTimestamp())
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
```

- [ ] **Step 3b: Add fields to `FleetModel` and rewrite `sorted()` in `fleet_model.go`**

Add these fields to the `FleetModel` struct (after the existing `err error`):

```go
	cursor       int
	sortCol      sortColumn
	sortRev      bool
	sortExplicit bool
	filter       Filter
	filtering    bool
	filterInput  string
	filterErr    string
	view         viewMode
	detail       detailState
	height       int
	cl           historyLoader
```

Replace the existing `sorted()` method body with:

```go
// sorted returns the filtered scans in display order. Without an explicit
// sort column it keeps the default active-first / newest-first order; once
// the operator picks a column it sorts purely by that column, ascending or
// reversed, with a ScanId tiebreak.
func (m FleetModel) sorted() []*v1.StatusEvent {
	out := make([]*v1.StatusEvent, 0, len(m.scans))
	for _, ev := range m.scans {
		if m.activeOnly && IsTerminal(ev.State) {
			continue
		}
		if !m.filter.Match(ev) {
			continue
		}
		out = append(out, ev)
	}
	if !m.sortExplicit {
		sort.Slice(out, func(i, j int) bool {
			ai, aj := IsTerminal(out[i].State), IsTerminal(out[j].State)
			if ai != aj {
				return !ai // active (non-terminal) first
			}
			if out[i].Timestamp != out[j].Timestamp {
				return out[i].Timestamp > out[j].Timestamp
			}
			return out[i].ScanId < out[j].ScanId
		})
		return out
	}
	sort.Slice(out, func(i, j int) bool {
		c := compareColumn(out[i], out[j], m.sortCol)
		if c == 0 {
			return out[i].ScanId < out[j].ScanId
		}
		if m.sortRev {
			return c > 0
		}
		return c < 0
	})
	return out
}
```

Note: `viewMode`, `detailState`, and `historyLoader` are defined in Tasks 5–6. To keep this task compiling on its own, add temporary stubs at the bottom of `sort.go`:

```go
// Defined fully in later tasks; declared here so FleetModel compiles.
type viewMode int

type detailState struct{}

type historyLoader interface {
	ShowHistory(scanID string, wait timeDuration) ([]*v1.StatusEvent, error)
}
```

That stub references `timeDuration` which does not exist — instead use the real `time.Duration`. Add `"time"` to `sort.go` imports and write the interface as:

```go
type historyLoader interface {
	ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error)
}
```

(Task 6 moves `viewMode`/`detailState`/`historyLoader` to their proper files and deletes these stubs.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -v`
Expected: PASS — new sort tests pass AND the pre-existing `TestFleetModelSortAndActiveFilter` still passes (default order unchanged).

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/sort.go services/cli/internal/tui/sort_test.go services/cli/internal/tui/fleet_model.go
git commit -m "feat(tui): multi-column fleet sort + filter-aware sorted()"
```

---

### Task 3: Cursor navigation + selection marker (list mode)

Add up/down cursor movement with clamping and render a cursor gutter column. No drill-in yet (Enter is wired in Task 6).

**Files:**
- Modify: `services/cli/internal/tui/fleet_model.go` (`Update` key handling, `View`, helpers)
- Test: `services/cli/internal/tui/fleet_model_test.go` (add cases)

**Interfaces:**
- Consumes: `sorted()` (Task 2), new fields `cursor`, `filtering`, `view`.
- Produces:
  - `func (m *FleetModel) clampCursor()`
  - `func (m FleetModel) updateListKey(v tea.KeyMsg) (tea.Model, tea.Cmd)`
  - `func (m FleetModel) viewListString() string` (the current `View` body, plus a `>` cursor marker)
  - `func (m FleetModel) Cursor() int` (test accessor)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run 'TestCursor|TestViewShowsCursorMarker' -v`
Expected: FAIL — `m.Cursor` undefined / no `>` in view.

- [ ] **Step 3: Implement**

In `fleet_model.go`, replace the `case tea.KeyMsg:` block inside `Update` with a router (this is the dispatch other tasks extend):

```go
	case tea.WindowSizeMsg:
		m.height = v.Height
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilterInput(v) // defined in Task 4
		}
		if m.view == viewDetail {
			return m.updateDetailKey(v) // defined in Task 6
		}
		return m.updateListKey(v)
```

Add the list-key handler and helpers:

```go
func (m FleetModel) updateListKey(v tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.sorted()
	switch v.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(rows)-1 {
			m.cursor++
		}
	case "f":
		m.activeOnly = !m.activeOnly
		m.clampCursor()
	}
	return m, nil
}

func (m *FleetModel) clampCursor() {
	n := len(m.sorted())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// Cursor exposes the selected row index for tests.
func (m FleetModel) Cursor() int { return m.cursor }
```

Rename the existing `View()` body to `viewListString()` and add a router `View()`:

```go
func (m FleetModel) View() string {
	if m.err != nil {
		return ui.BoxStyle.Render("error: " + m.err.Error())
	}
	if m.view == viewDetail {
		return m.viewDetailString() // defined in Task 5
	}
	return m.viewListString()
}
```

In `viewListString()` (the moved body), change the table to include a leading cursor gutter and a marker per row:

```go
	t := ui.NewTable("", "SCAN_ID", "STATE", "SOURCE", "CHUNKS", "SECRETS", "UPDATED")
	for i, e := range rows {
		marker := " "
		if i == m.cursor {
			marker = ui.BrandStyle.Render(">")
		}
		mtr := e.GetMetrics()
		t.Row(
			marker,
			e.ScanId,
			ui.StateStyle(e.State.String()).Render(e.State.String()),
			e.GetSource(),
			fmt.Sprintf("%d", mtr.GetChunksPublished()),
			fmt.Sprintf("%d", mtr.GetSecretsFound()),
			agoString(e.Timestamp),
		)
	}
```

Note: `viewListString` references `viewDetailString` (Task 5), `updateFilterInput` (Task 4), `updateDetailKey`/`viewDetail` (Task 6). Those identifiers do not exist yet. To keep THIS task compiling, add temporary stubs at the bottom of `fleet_model.go`:

```go
// Temporary stubs — replaced by Tasks 4–6.
const viewDetail viewMode = 1

func (m FleetModel) viewDetailString() string             { return "" }
func (m FleetModel) updateDetailKey(tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
func (m FleetModel) updateFilterInput(tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
```

(Tasks 4 and 6 delete each stub as they provide the real implementation.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -v`
Expected: PASS — new cursor tests pass and all prior tui tests still pass.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/fleet_model.go services/cli/internal/tui/fleet_model_test.go
git commit -m "feat(tui): fleet cursor navigation + selection marker"
```

---

### Task 4: Sort & filter keys + header indicator + filter input line

Wire `s`/`S` (sort), `/` (filter input), render the sort `↑/↓` indicator on the active column header and the `filter> ` line with error feedback.

**Files:**
- Modify: `services/cli/internal/tui/fleet_model.go`
- Test: `services/cli/internal/tui/fleet_model_test.go`

**Interfaces:**
- Consumes: `sortColumn.next`/`label` (Task 2), `ParseFilter` (Task 1), `updateListKey` (Task 3).
- Produces:
  - extends `updateListKey` with `s`, `S`, `/`
  - `func (m FleetModel) updateFilterInput(v tea.KeyMsg) (tea.Model, tea.Cmd)` (real version; deletes the Task 3 stub)
  - `func (m FleetModel) columnHeader(c sortColumn) string`
  - `func (m FleetModel) SortState() (sortColumn, bool, bool)` (test accessor: col, rev, explicit)
  - `func (m FleetModel) Filtering() bool` and `func (m FleetModel) FilterRaw() string` (test accessors)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run 'TestSortKeys|TestFilterInput|TestHeaderShowsSortIndicator' -v`
Expected: FAIL — `SortState`/`Filtering`/`FilterRaw` undefined; no indicator; filter not applied.

- [ ] **Step 3: Implement**

Delete the Task 3 `updateFilterInput` stub. Extend `updateListKey`'s switch with these cases (before the closing brace):

```go
	case "s":
		if !m.sortExplicit {
			m.sortExplicit = true
			m.sortCol = sortScanID
		} else {
			m.sortCol = m.sortCol.next()
		}
		m.clampCursor()
	case "S":
		if !m.sortExplicit {
			m.sortExplicit = true
			m.sortCol = sortScanID
		}
		m.sortRev = !m.sortRev
	case "/":
		m.filtering = true
		m.filterInput = m.filter.Raw()
		m.filterErr = ""
```

Add the filter-input handler and accessors:

```go
func (m FleetModel) updateFilterInput(v tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch v.String() {
	case "esc":
		m.filtering = false
		m.filterErr = ""
		return m, nil
	case "enter":
		f, err := ParseFilter(m.filterInput)
		if err != nil {
			m.filterErr = "filter error: " + err.Error()
			return m, nil
		}
		m.filter = f
		m.filtering = false
		m.filterErr = ""
		m.clampCursor()
		return m, nil
	case "backspace":
		if n := len(m.filterInput); n > 0 {
			m.filterInput = m.filterInput[:n-1]
		}
		return m, nil
	default:
		if len(v.Runes) > 0 {
			m.filterInput += string(v.Runes)
		}
		return m, nil
	}
}

func (m FleetModel) columnHeader(c sortColumn) string {
	h := c.label()
	if m.sortExplicit && m.sortCol == c {
		if m.sortRev {
			return h + "↓"
		}
		return h + "↑"
	}
	return h
}

// Test accessors.
func (m FleetModel) SortState() (sortColumn, bool, bool) { return m.sortCol, m.sortRev, m.sortExplicit }
func (m FleetModel) Filtering() bool                     { return m.filtering }
func (m FleetModel) FilterRaw() string                  { return m.filter.Raw() }
```

In `viewListString()`, build the table headers via `columnHeader` and append the filter/footer lines. Replace the `ui.NewTable(...)` call with:

```go
	t := ui.NewTable(
		"",
		m.columnHeader(sortScanID),
		m.columnHeader(sortState),
		m.columnHeader(sortSource),
		m.columnHeader(sortChunks),
		m.columnHeader(sortSecrets),
		m.columnHeader(sortUpdated),
	)
```

Replace the footer assembly at the end of `viewListString()` with:

```go
	var sb strings.Builder
	sb.WriteString(header + "\n")
	_, _ = t.WriteTo(&sb)
	if m.filtering {
		sb.WriteString(ui.InfoStyle.Render("filter> "+m.filterInput) + "\n")
	} else if m.filter.Raw() != "" {
		sb.WriteString(ui.DimStyle.Render("filter: "+m.filter.Raw()) + "\n")
	}
	if m.filterErr != "" {
		sb.WriteString(ui.ErrStyle.Render(m.filterErr) + "\n")
	}
	sb.WriteString(ui.DimStyle.Render(fleetFooter))
	return ui.BoxStyle.Render(sb.String())
```

Update the footer constant:

```go
const fleetFooter = "↑↓ move · enter open · s/S sort · / filter · f active · q quit"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -v`
Expected: PASS — all new + existing tui tests (the prior `TestFleetModelHeaderCountsAllScans` still finds `1 active / 2 scans` in the header).

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/fleet_model.go services/cli/internal/tui/fleet_model_test.go
git commit -m "feat(tui): fleet sort keys, filter input, header indicators"
```

---

### Task 5: Detail panel (`detail_model.go`)

The detail state struct, dedup-append, and render. Pure — unit tested by constructing a `FleetModel` in detail view. (Key handling + drill-in wiring is Task 6.)

**Files:**
- Create: `services/cli/internal/tui/detail_model.go`
- Modify: `services/cli/internal/tui/sort.go` (remove the `detailState struct{}` stub — moved here)
- Test: `services/cli/internal/tui/detail_model_test.go`

**Interfaces:**
- Consumes: `v1.StatusEvent`, `ui` styles, `m.height`, `m.detail`.
- Produces:
  - `type detailState struct { scanID string; latest *v1.StatusEvent; history []*v1.StatusEvent; err error; loading bool; offset int }`
  - `func (d *detailState) appendEvent(ev *v1.StatusEvent)`
  - `func (m FleetModel) pageSize() int`
  - `func (m FleetModel) viewDetailString() string` (real version; deletes the Task 3 stub)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run 'TestAppendEvent|TestViewDetail' -v`
Expected: FAIL — `detailState` has no fields (still the stub) / `appendEvent` undefined.

- [ ] **Step 3: Implement**

Remove the `type detailState struct{}` stub line from `sort.go`. Create `detail_model.go`:

```go
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// detailState backs the drill-in panel for a single selected scan. latest
// holds the freshest StatusEvent (seeded from the fleet map, kept live by
// the tail); history is the time-ordered event list seeded by ShowHistory
// and extended by live events.
type detailState struct {
	scanID  string
	latest  *v1.StatusEvent
	history []*v1.StatusEvent
	err     error
	loading bool
	offset  int
}

// appendEvent adds ev unless an event with the same timestamp AND state is
// already present — dedups the ShowHistory seed against live re-deliveries.
func (d *detailState) appendEvent(ev *v1.StatusEvent) {
	for _, e := range d.history {
		if e.Timestamp == ev.Timestamp && e.State == ev.State {
			return
		}
	}
	d.history = append(d.history, ev)
}

// pageSize is how many history rows the detail viewport shows at once.
func (m FleetModel) pageSize() int {
	if m.height > 10 {
		return m.height - 8
	}
	return 12
}

func (m FleetModel) viewDetailString() string {
	d := m.detail
	st := d.latest.GetState().String()
	header := fmt.Sprintf("scan %s ── %s ── %s",
		d.scanID,
		ui.StateStyle(st).Render(st),
		ui.DimStyle.Render(d.latest.GetSource()))

	mtr := d.latest.GetMetrics()
	metrics := fmt.Sprintf(
		"metrics  blobs %d (skipped %d) · chunks %d · bytes %d · errors %d · secrets %d · dur %dms",
		mtr.GetBlobsScanned(), mtr.GetBlobsSkipped(), mtr.GetChunksPublished(),
		mtr.GetBytesPublished(), mtr.GetErrorsTotal(), mtr.GetSecretsFound(), mtr.GetDurationMs())

	var body strings.Builder
	body.WriteString("─ history ──────────────────────────\n")
	switch {
	case d.loading:
		body.WriteString(ui.DimStyle.Render("  loading history…"))
	case d.err != nil:
		body.WriteString(ui.ErrStyle.Render("  history error: " + d.err.Error()))
	case len(d.history) == 0:
		body.WriteString(ui.DimStyle.Render("  (no history)"))
	default:
		rows := d.history
		start := d.offset
		if start > len(rows) {
			start = len(rows)
		}
		end := start + m.pageSize()
		if end > len(rows) {
			end = len(rows)
		}
		for _, e := range rows[start:end] {
			body.WriteString(fmt.Sprintf("  %s  %-9s  %s\n",
				time.Unix(e.Timestamp, 0).UTC().Format("15:04:05"),
				e.State.String(), e.Message))
		}
	}

	footer := ui.DimStyle.Render("↑/↓ scroll · esc back · q quit")
	return ui.BoxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, metrics, body.String(), footer))
}
```

Delete the Task 3 `viewDetailString` stub from `fleet_model.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -v`
Expected: PASS — detail render + dedup tests pass; all prior tests still green.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/detail_model.go services/cli/internal/tui/detail_model_test.go services/cli/internal/tui/sort.go services/cli/internal/tui/fleet_model.go
git commit -m "feat(tui): scan detail panel — metrics + scrollable history"
```

---

### Task 6: Drill-in wiring + live updates + RunFleetTUI client

Connect Enter→detail, the `ShowHistory` Cmd, `HistoryLoadedMsg`, live append while in detail, detail key navigation, and pass the client from `cmd/watch.go`. Finalizes `viewMode`/`historyLoader` in their proper files.

**Files:**
- Modify: `services/cli/internal/tui/fleet_model.go` (Enter, StatusEventMsg detail update, HistoryLoadedMsg, `updateDetailKey`, `WithClient`, finalize `viewMode`/`historyLoader`)
- Modify: `services/cli/internal/tui/sort.go` (remove the `viewMode`/`historyLoader` stubs moved out)
- Modify: `services/cli/internal/cmd/watch.go` (`RunFleetTUI`: `.WithClient(cl)`)
- Test: `services/cli/internal/tui/fleet_model_test.go`

**Interfaces:**
- Consumes: `detailState` (Task 5), `loadHistoryCmd`, `*natscli.Client.ShowHistory`.
- Produces:
  - `type viewMode int` with `viewList`, `viewDetail`
  - `type historyLoader interface { ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error) }`
  - `type HistoryLoadedMsg struct { ScanID string; Events []*v1.StatusEvent; Err error }`
  - `func (m FleetModel) WithClient(cl historyLoader) FleetModel`
  - `func (m FleetModel) loadHistoryCmd(scanID string) tea.Cmd`
  - `func (m FleetModel) updateDetailKey(v tea.KeyMsg) (tea.Model, tea.Cmd)`
  - `func (m FleetModel) View_() viewMode` → expose via `func (m FleetModel) ViewMode() viewMode` (test accessor)

- [ ] **Step 1: Write the failing test**

```go
type fakeLoader struct {
	events []*v1.StatusEvent
	err    error
	called string
}

func (f *fakeLoader) ShowHistory(scanID string, _ time.Duration) ([]*v1.StatusEvent, error) {
	f.called = scanID
	return f.events, f.err
}

func TestEnterOpensDetailAndLoadsHistory(t *testing.T) {
	loader := &fakeLoader{events: []*v1.StatusEvent{{ScanId: "a", Timestamp: 1, State: v1.ScanState_PENDING, Message: "queued"}}}
	m := NewFleetModel().WithClient(loader)
	n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 5}})
	m = n.(FleetModel)

	n2, cmd := m.Update(keyMsg("enter"))
	m = n2.(FleetModel)
	if m.ViewMode() != viewDetail {
		t.Fatal("enter must switch to detail view")
	}
	if cmd == nil {
		t.Fatal("enter must return a history-load Cmd")
	}
	// Execute the Cmd → it must produce a HistoryLoadedMsg for scan a.
	msg := cmd()
	loaded, ok := msg.(HistoryLoadedMsg)
	if !ok || loaded.ScanID != "a" {
		t.Fatalf("cmd must yield HistoryLoadedMsg for a, got %#v", msg)
	}
	n3, _ := m.Update(loaded)
	m = n3.(FleetModel)
	if !strings.Contains(m.View(), "queued") {
		t.Fatalf("history must seed the detail view; got:\n%s", m.View())
	}
}

func TestLiveEventAppendsToDetailWithDedup(t *testing.T) {
	loader := &fakeLoader{events: []*v1.StatusEvent{{ScanId: "a", Timestamp: 1, State: v1.ScanState_PENDING}}}
	m := NewFleetModel().WithClient(loader)
	n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 1}})
	m = n.(FleetModel)
	n2, cmd := m.Update(keyMsg("enter"))
	m = n2.(FleetModel)
	m2, _ := m.Update(cmd().(HistoryLoadedMsg))
	m = m2.(FleetModel)
	// A new live event with a fresh timestamp appends.
	n3, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 2, Message: "walking"}})
	m = n3.(FleetModel)
	if !strings.Contains(m.View(), "walking") {
		t.Fatalf("live event must append to detail history; got:\n%s", m.View())
	}
}

func TestEscReturnsToListPreservingCursor(t *testing.T) {
	loader := &fakeLoader{}
	m := NewFleetModel().WithClient(loader)
	for _, id := range []string{"a", "b", "c"} {
		n, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: id, State: v1.ScanState_RUNNING, Timestamp: 1}})
		m = n.(FleetModel)
	}
	d, _ := m.Update(keyMsg("down")) // cursor -> 1
	m = d.(FleetModel)
	e, _ := m.Update(keyMsg("enter"))
	m = e.(FleetModel)
	b, _ := m.Update(keyMsg("esc"))
	m = b.(FleetModel)
	if m.ViewMode() != viewList {
		t.Fatal("esc must return to list")
	}
	if m.Cursor() != 1 {
		t.Fatalf("cursor must be preserved at 1, got %d", m.Cursor())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/tui/ -run 'TestEnterOpens|TestLiveEvent|TestEscReturns' -v`
Expected: FAIL — `WithClient`/`HistoryLoadedMsg`/`ViewMode` undefined; Enter doesn't open detail.

- [ ] **Step 3: Implement**

In `sort.go`, delete the three stubs (`type viewMode int`, `type detailState struct{}` already removed in Task 5, and the `historyLoader` interface) — they move to `fleet_model.go`. In `fleet_model.go`, delete the Task 3 stubs (`const viewDetail viewMode = 1`, `updateDetailKey` stub).

Add the real types and messages near the top of `fleet_model.go`:

```go
type viewMode int

const (
	viewList viewMode = iota
	viewDetail
)

// historyLoader is the slice of *natscli.Client the fleet model needs for
// drill-in. An interface keeps the tui package free of a natscli import
// (and lets tests inject a fake).
type historyLoader interface {
	ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error)
}

// HistoryLoadedMsg delivers the result of a drill-in ShowHistory fetch.
type HistoryLoadedMsg struct {
	ScanID string
	Events []*v1.StatusEvent
	Err    error
}

// WithClient injects the history loader used on drill-in.
func (m FleetModel) WithClient(cl historyLoader) FleetModel { m.cl = cl; return m }

// ViewMode exposes the current view for tests.
func (m FleetModel) ViewMode() viewMode { return m.view }

func (m FleetModel) loadHistoryCmd(scanID string) tea.Cmd {
	cl := m.cl
	if cl == nil {
		return nil
	}
	return func() tea.Msg {
		evs, err := cl.ShowHistory(scanID, time.Second)
		return HistoryLoadedMsg{ScanID: scanID, Events: evs, Err: err}
	}
}
```

Add the `enter` case to `updateListKey`'s switch:

```go
	case "enter":
		rows := m.sorted()
		if len(rows) == 0 {
			return m, nil
		}
		if m.cursor >= len(rows) {
			m.cursor = len(rows) - 1
		}
		sel := rows[m.cursor]
		m.view = viewDetail
		m.detail = detailState{scanID: sel.ScanId, latest: sel, loading: m.cl != nil}
		return m, m.loadHistoryCmd(sel.ScanId)
```

Add the real `updateDetailKey`:

```go
func (m FleetModel) updateDetailKey(v tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch v.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.view = viewList
		m.detail = detailState{}
		return m, nil
	case "up", "k":
		if m.detail.offset > 0 {
			m.detail.offset--
		}
	case "down", "j":
		if m.detail.offset < len(m.detail.history)-1 {
			m.detail.offset++
		}
	}
	return m, nil
}
```

Add `HistoryLoadedMsg` handling to `Update` (new case):

```go
	case HistoryLoadedMsg:
		if m.view == viewDetail && v.ScanID == m.detail.scanID {
			m.detail.loading = false
			if v.Err != nil {
				m.detail.err = v.Err
			} else {
				m.detail.history = v.Events
			}
		}
		return m, nil
```

Extend the existing `StatusEventMsg` case: after the fold logic and BEFORE its `return m, nil`, insert:

```go
		if m.view == viewDetail && ev.ScanId == m.detail.scanID {
			m.detail.latest = ev
			m.detail.appendEvent(ev)
		}
```

In `cmd/watch.go` `RunFleetTUI`, change the program construction:

```go
	p := tea.NewProgram(tui.NewFleetModel().WithNATSURL(natsURL).WithClient(cl), tea.WithAltScreen())
```

(`cl` is the `*natscli.Client` already in scope; it satisfies `historyLoader`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/tui/ -v && go build ./...`
Expected: PASS — all tui tests green; full module builds (confirms `*natscli.Client` satisfies `historyLoader`).

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/fleet_model.go services/cli/internal/tui/sort.go services/cli/internal/tui/fleet_model_test.go services/cli/internal/cmd/watch.go
git commit -m "feat(tui): fleet drill-in — history seed, live append, detail nav"
```

---

### Task 7: Full module verification

Confirm the whole CLI builds and all tests pass, with no leftover stubs.

**Files:**
- None (verification only).

- [ ] **Step 1: Grep for leftover stubs**

Run: `cd services/cli && grep -rn 'Temporary stub\|stub —\|struct{} *$' internal/tui/`
Expected: no matches referencing the temporary stubs from Tasks 2–3.

- [ ] **Step 2: Vet + build + test the whole module**

Run: `cd services/cli && go vet ./... && go build ./... && go test ./...`
Expected: clean vet, successful build, all packages PASS.

- [ ] **Step 3: Commit (only if vet/build surfaced a fix)**

```bash
git add -A
git commit -m "chore(tui): interactive watch verification fixes"
```

(If steps 1–2 are already clean with nothing to change, skip this commit.)

---

## Self-Review

**Spec coverage:**
- Master-detail with view mode → Tasks 3 (router) + 6 (drill-in). ✓
- Cursor nav (↑/↓·j/k, clamp) → Task 3. ✓
- Drill-in Enter + ShowHistory seed + live append dedup → Tasks 5–6. ✓
- Detail render (metrics + scrollable history, Esc back, cursor preserved) → Tasks 5–6. ✓
- Structured `key:value` filter (`state`/`source`/`id` + bare word; unknown key error) → Tasks 1, 4. ✓
- `state:active` semantics + `f` toggle retained → Tasks 1 (matchState), 3 (`f`). ✓
- Multi-column sort `s`/`S` + header indicator; active-first only in default, pure column when explicit; ScanId tiebreak → Tasks 2, 4. ✓
- Single program / single subscription; `WatchModel` untouched → architecture honored (no new subscription; only a one-shot `ShowHistory` Cmd). ✓
- Error handling: empty filter result hint, invalid filter line, history error in detail → `(no scans yet…)` existing + Task 4 filter error + Task 5 detail error. ✓
- Findings drill-in explicitly out of scope → not planned. ✓
- Tests across filter/sort/fleet/detail → Tasks 1–6 each ship tests. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code; the only intentional throwaways are the clearly-labeled compile stubs in Tasks 2–3, each deleted by name in Tasks 4–6, and Task 7 greps to confirm none remain.

**Type consistency:** `Filter`/`ParseFilter`/`Match`/`Raw` (Task 1) used unchanged in Tasks 2–4. `sortColumn`/`compareColumn`/`next`/`label` (Task 2) used in Task 4 `columnHeader`. `detailState` fields (Task 5) match their use in Tasks 5–6. `historyLoader.ShowHistory(string, time.Duration)` matches `*natscli.Client.ShowHistory` (verified signature). `HistoryLoadedMsg{ScanID, Events, Err}` produced by `loadHistoryCmd` and consumed in `Update` identically.
