package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

const fleetFooter = "q quit · f filter active"

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
			return m.updateFilterInput(v) // defined in Task 4
		}
		if m.view == viewDetail {
			return m.updateDetailKey(v) // defined in Task 6
		}
		return m.updateListKey(v)
	case fleetTickMsg:
		return m, fleetTick()
	case StatusEventMsg:
		ev := v.Ev
		prev, ok := m.scans[ev.ScanId]
		if !ok {
			m.scans[ev.ScanId] = ev
			m.evictIfOver() // only a new scan_id can grow the map
		} else if !(IsTerminal(prev.State) && !IsTerminal(ev.State)) && ev.Timestamp >= prev.Timestamp {
			m.scans[ev.ScanId] = ev
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
		return m.viewDetailString() // defined in Task 5
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
		body := ui.DimStyle.Render("(no scans yet, waiting…)")
		footer := ui.DimStyle.Render(fleetFooter)
		return ui.BoxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
	}
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
	var sb strings.Builder
	sb.WriteString(header + "\n")
	_, _ = t.WriteTo(&sb)
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

// Temporary stubs — replaced by Tasks 4–6.
const viewDetail viewMode = 1

func (m FleetModel) viewDetailString() string                          { return "" }
func (m FleetModel) updateDetailKey(tea.KeyMsg) (tea.Model, tea.Cmd)  { return m, nil }
func (m FleetModel) updateFilterInput(tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
