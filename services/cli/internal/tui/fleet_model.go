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

// Init starts the refresh tick.
func (m FleetModel) Init() tea.Cmd { return fleetTick() }

// Update folds events and handles keys.
func (m FleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "f":
			m.activeOnly = !m.activeOnly
			return m, nil
		}
		return m, nil
	case fleetTickMsg:
		return m, fleetTick()
	case StatusEventMsg:
		ev := v.Ev
		prev, ok := m.scans[ev.ScanId]
		if !ok {
			m.scans[ev.ScanId] = ev
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

// sorted returns scans ordered active-first, then by most-recent update.
func (m FleetModel) sorted() []*v1.StatusEvent {
	out := make([]*v1.StatusEvent, 0, len(m.scans))
	for _, ev := range m.scans {
		if m.activeOnly && IsTerminal(ev.State) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := IsTerminal(out[i].State), IsTerminal(out[j].State)
		if ai != aj {
			return !ai // active (non-terminal) first
		}
		if out[i].Timestamp != out[j].Timestamp {
			return out[i].Timestamp > out[j].Timestamp
		}
		return out[i].ScanId < out[j].ScanId // deterministic tiebreak — no flicker
	})
	return out
}

// View renders the dashboard.
func (m FleetModel) View() string {
	if m.err != nil {
		return ui.BoxStyle.Render("error: " + m.err.Error())
	}
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
	t := ui.NewTable("SCAN_ID", "STATE", "SOURCE", "CHUNKS", "SECRETS", "UPDATED")
	for _, e := range rows {
		mtr := e.GetMetrics()
		t.Row(
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
