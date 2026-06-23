package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/findings"
	"github.com/Harporis/harporis/services/cli/internal/scanmetrics"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

const fleetFooter = "↑↓ move · enter open · s/S sort · / filter · f active · q quit"

// maxFleetScans bounds the in-memory scan map so a long-lived `harporis
// watch` session on a busy fleet cannot grow without limit. Community
// edition value; the DB-backed Pro/Enterprise dashboards page from a store
// instead of holding everything in memory.
const maxFleetScans = 1000

// fleetTickMsg drives the "x ago" column refresh.
type fleetTickMsg struct{}

func fleetTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return fleetTickMsg{} })
}

// FleetModel is the live multi-scan dashboard. It folds StatusEventMsg
// into latest-per-scan and renders a sorted table. No idle timeout — it
// runs until the user quits.
type FleetModel struct {
	scans      map[string]*v1.StatusEvent
	activeOnly bool
	natsURL    string
	err        error

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
	fl           findingsLoader
}

// NewFleetModel returns an empty dashboard.
func NewFleetModel() FleetModel {
	return FleetModel{scans: map[string]*v1.StatusEvent{}}
}

// WithNATSURL sets the header URL label.
func (m FleetModel) WithNATSURL(u string) FleetModel { m.natsURL = u; return m }

// Scans exposes a read-only view of folded state for tests. Callers must
// not mutate the returned map.
func (m FleetModel) Scans() map[string]*v1.StatusEvent { return m.scans }

// Cursor exposes the selected row index for tests.
func (m FleetModel) Cursor() int { return m.cursor }

// Init starts the refresh tick.
func (m FleetModel) Init() tea.Cmd { return fleetTick() }

// Update folds events and handles keys.
func (m FleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = v.Height
		return m, nil
	case tea.KeyMsg:
		if m.filtering {
			return m.updateFilterInput(v)
		}
		if m.view == viewDetail {
			return m.updateDetailKey(v)
		}
		return m.updateListKey(v)
	case fleetTickMsg:
		return m, fleetTick()
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
	case FindingsLoadedMsg:
		if m.view == viewDetail && v.ScanID == m.detail.scanID {
			m.detail.findings.loading = false
			if v.Err != nil {
				m.detail.findings.err = v.Err
			} else {
				m.detail.findings.loaded = v.Findings
				m.detail.findings.loadedOnce = true
			}
		}
		return m, nil
	case StatusEventMsg:
		ev := v.Ev
		prev, ok := m.scans[ev.ScanId]
		// Display precedence is unchanged: a newer non-terminal-overriding
		// event wins state/message/timestamp. Metrics, however, arrive split
		// across producers (getter throughput on COMPLETED, scanner secrets on
		// RUNNING), so they are ALWAYS merged field-wise (max) into the
		// retained snapshot — even when the event loses the display race.
		display := ev
		accepted := true
		if ok && !(!(IsTerminal(prev.State) && !IsTerminal(ev.State)) && ev.Timestamp >= prev.Timestamp) {
			display = prev
			accepted = false
		}
		var prevMetrics *v1.ScanMetrics
		if ok {
			prevMetrics = prev.GetMetrics()
		}
		snap := &v1.StatusEvent{
			ScanId:       display.ScanId,
			State:        display.State,
			Timestamp:    display.Timestamp,
			Message:      display.Message,
			Source:       display.Source,
			OutputConfig: display.GetOutputConfig(),
			Metrics:      scanmetrics.Merge(prevMetrics, ev.GetMetrics()),
		}
		m.scans[ev.ScanId] = snap
		if !ok {
			m.evictIfOver() // only a new scan_id can grow the map
		}
		if m.view == viewDetail && ev.ScanId == m.detail.scanID {
			m.detail.latest = snap // merged metrics + current display, always
			if accepted {
				m.detail.appendEvent(ev)
			}
		}
		return m, nil
	case SubscribeErrMsg:
		m.err = v.Err
		return m, tea.Quit
	}
	return m, nil
}

// updateListKey handles key events in list mode.
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
	}
	return m, nil
}

// clampCursor keeps cursor within valid bounds after the row count changes.
func (m *FleetModel) clampCursor() {
	n := len(m.sorted())
	if n > 0 && m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// evictIfOver keeps the scan map bounded at maxFleetScans. It removes the
// single most-evictable entry: the oldest finished (terminal) scan, falling
// back to the oldest active one only if every tracked scan is still active.
// Active scans are retained preferentially since they are what the operator
// is watching. Called after a new scan_id is inserted, so at most one entry
// is over the cap.
func (m FleetModel) evictIfOver() {
	if len(m.scans) <= maxFleetScans {
		return
	}
	var victim string
	var victimTS int64
	var victimTerminal bool
	first := true
	for id, ev := range m.scans {
		term := IsTerminal(ev.State)
		replace := first
		if !first {
			if term != victimTerminal {
				replace = term && !victimTerminal // prefer evicting terminal
			} else {
				replace = ev.Timestamp < victimTS // same kind: evict oldest
			}
		}
		if replace {
			victim, victimTS, victimTerminal, first = id, ev.Timestamp, term, false
		}
	}
	delete(m.scans, victim)
}

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

// View renders the dashboard.
func (m FleetModel) View() string {
	if m.err != nil {
		return ui.BoxStyle.Render("error: " + m.err.Error())
	}
	if m.view == viewDetail {
		return m.viewDetailString()
	}
	return m.viewListString()
}

// viewListString renders the list view with a cursor gutter column.
func (m FleetModel) viewListString() string {
	rows := m.sorted()
	countLabel := fmt.Sprintf("%d scans", len(m.scans))
	if m.activeOnly {
		countLabel = fmt.Sprintf("%d active / %d scans", len(rows), len(m.scans))
	}
	header := fmt.Sprintf("harporis watch — %s   %s   %s",
		countLabel, ui.DimStyle.Render(m.natsURL),
		time.Now().UTC().Format("15:04:05"))
	if len(rows) == 0 {
		var sb strings.Builder
		sb.WriteString(header + "\n")
		sb.WriteString(ui.DimStyle.Render("(no scans yet, waiting…)") + "\n")
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
	}
	t := ui.NewTable(
		"",
		m.columnHeader(sortScanID),
		m.columnHeader(sortState),
		m.columnHeader(sortSource),
		m.columnHeader(sortChunks),
		m.columnHeader(sortSecrets),
		m.columnHeader(sortUpdated),
	)
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
}

// agoString renders a compact relative age like "2s ago".
func agoString(unix int64) string {
	d := time.Since(time.Unix(unix, 0)).Truncate(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String() + " ago"
}

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
		if r := []rune(m.filterInput); len(r) > 0 {
			m.filterInput = string(r[:len(r)-1])
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
func (m FleetModel) FilterRaw() string                   { return m.filter.Raw() }

// viewMode distinguishes the list and drill-in views.
type viewMode int

const (
	viewList   viewMode = iota
	viewDetail          // = 1
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

// findingsLoader is the interface the fleet model needs to load findings for
// the Findings tab. An interface keeps the tui package free of a findings
// import path and lets tests inject a fake.
type findingsLoader interface {
	Load(scanID string) ([]findings.Finding, error)
}

// FindingsLoadedMsg delivers the result of a Findings-tab load.
type FindingsLoadedMsg struct {
	ScanID   string
	Findings []findings.Finding
	Err      error
}

// WithFindingsLoader injects the findings loader used by the Findings tab.
func (m FleetModel) WithFindingsLoader(l findingsLoader) FleetModel { m.fl = l; return m }

func (m FleetModel) loadFindingsCmd(scanID string) tea.Cmd {
	fl := m.fl
	if fl == nil {
		return nil
	}
	return func() tea.Msg {
		fs, err := fl.Load(scanID)
		return FindingsLoadedMsg{ScanID: scanID, Findings: fs, Err: err}
	}
}

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

func (m FleetModel) updateDetailKey(v tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While typing a findings filter, route everything to the findings state.
	if m.detail.tab == tabFindings && m.detail.findings.filtering {
		m.detail.findings, _ = m.detail.findings.updateKey(v, m.height)
		return m, nil
	}
	switch v.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "left", "right":
		if m.detail.tab == tabStatus {
			m.detail.tab = tabFindings
			if !m.detail.findings.loading && !m.detail.findings.loadedOnce && m.detail.findings.err == nil {
				m.detail.findings.loading = true
				return m, m.loadFindingsCmd(m.detail.scanID)
			}
		} else {
			m.detail.tab = tabStatus
		}
		return m, nil
	}
	if m.detail.tab == tabFindings {
		if v.String() == "r" && (m.detail.findings.err != nil) {
			m.detail.findings = findingsState{loading: true}
			return m, m.loadFindingsCmd(m.detail.scanID)
		}
		var back bool
		m.detail.findings, back = m.detail.findings.updateKey(v, m.height)
		if back {
			m.view = viewList
			m.detail = detailState{}
		}
		return m, nil
	}
	// Status tab (unchanged behavior).
	switch v.String() {
	case "esc":
		m.view = viewList
		m.detail = detailState{}
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
