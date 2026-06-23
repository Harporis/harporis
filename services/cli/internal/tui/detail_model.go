package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// detailTab selects which sub-panel is active in the drill-in view.
type detailTab int

const (
	tabStatus   detailTab = iota
	tabFindings           // = 1
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
	tab     detailTab
	findings findingsState
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

// viewDetailString routes to the active tab and renders the full detail panel.
func (m FleetModel) viewDetailString() string {
	d := m.detail
	if d.latest == nil {
		return ui.BoxStyle.Render("scan " + d.scanID + "\n  loading…")
	}
	st := d.latest.GetState().String()
	header := fmt.Sprintf("scan %s ── %s ── %s",
		d.scanID, ui.StateStyle(st).Render(st), ui.DimStyle.Render(d.latest.GetSource()))
	tabs := m.detailTabsLine()
	var body string
	if d.tab == tabFindings {
		body = d.findings.view(m.height)
	} else {
		body = m.viewDetailStatusBody()
	}
	footer := ui.DimStyle.Render("tab switch · ↑/↓ scroll · / filter · s/S sort · esc back · q quit")
	return ui.BoxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, footer))
}

// detailTabsLine renders the "[Status] Findings" or "Status [Findings]" header.
func (m FleetModel) detailTabsLine() string {
	var status, find string
	if m.detail.tab == tabStatus {
		status = ui.BrandStyle.Render("[Status]")
		find = ui.DimStyle.Render(" Findings ")
	} else {
		status = ui.DimStyle.Render(" Status ")
		find = ui.BrandStyle.Render("[Findings]")
	}
	return status + find
}

// viewDetailStatusBody renders just the metrics + history body for the Status tab.
func (m FleetModel) viewDetailStatusBody() string {
	d := m.detail

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

	return lipgloss.JoinVertical(lipgloss.Left, metrics, body.String())
}
