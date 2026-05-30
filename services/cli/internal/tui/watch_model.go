// Package tui contains bubble tea models for live commands.
package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// StatusEventMsg is delivered by the NATS pump (see cmd/watch.go RunWatchTUI).
type StatusEventMsg struct{ Ev *v1.StatusEvent }

// SubscribeErrMsg is delivered on subscription/fetch failures so the
// model can render the error and quit cleanly.
type SubscribeErrMsg struct{ Err error }

// WatchModel is the live scan dashboard.
type WatchModel struct {
	scanID    string
	state     v1.ScanState
	message   string
	startedAt time.Time
	lastEvent time.Time
	source    string
	walker    progress.Model
	publish   progress.Model
	spinner   spinner.Model
	events    []*v1.StatusEvent
	done      bool
	exitCode  int
	width     int
}

// NewWatchModel creates a freshly-initialized model.
func NewWatchModel(scanID string) WatchModel {
	walker := progress.New(progress.WithDefaultGradient())
	pub := progress.New(progress.WithDefaultGradient())
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	return WatchModel{
		scanID:    scanID,
		state:     v1.ScanState_PENDING,
		startedAt: time.Now(),
		lastEvent: time.Now(),
		walker:    walker,
		publish:   pub,
		spinner:   sp,
	}
}

// Init starts the spinner.
func (m WatchModel) Init() tea.Cmd { return m.spinner.Tick }

// Update advances the model.
func (m WatchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.walker.Width = maxInt(20, v.Width-30)
		m.publish.Width = maxInt(20, v.Width-30)
		return m, nil
	case tea.KeyMsg:
		if v.String() == "ctrl+c" || v.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case StatusEventMsg:
		m.state = v.Ev.State
		m.message = v.Ev.Message
		m.lastEvent = time.Now()
		var cmds []tea.Cmd
		if metrics := v.Ev.GetMetrics(); metrics != nil {
			cmds = append(cmds,
				m.walker.SetPercent(scaledPct(metrics.GetBlobsScanned())),
				m.publish.SetPercent(scaledPct(metrics.GetChunksPublished())),
			)
		}
		m.events = appendCap(m.events, v.Ev, 10)
		if IsTerminal(v.Ev.State) {
			m.done = true
			if v.Ev.State == v1.ScanState_FAILED || v.Ev.State == v1.ScanState_CANCELLED {
				m.exitCode = 3
			}
			cmds = append(cmds, m.walker.SetPercent(1.0), m.publish.SetPercent(1.0), tea.Quit)
			return m, tea.Batch(cmds...)
		}
		return m, tea.Batch(cmds...)
	case SubscribeErrMsg:
		m.message = "error: " + v.Err.Error()
		m.exitCode = 2
		return m, tea.Quit
	}
	return m, nil
}

// View renders the model.
func (m WatchModel) View() string {
	header := fmt.Sprintf("scan %s ── %s ── %s",
		m.scanID,
		ui.StateStyle(m.state.String()).Render(m.state.String()),
		time.Since(m.startedAt).Truncate(time.Second))
	body := fmt.Sprintf(
		"source   %s\nstate    %s %s\nwalker   %s\npublish  %s\n",
		ui.DimStyle.Render(m.source),
		m.spinner.View(),
		m.message,
		m.walker.View(),
		m.publish.View(),
	)
	log := "log\n"
	for _, e := range m.events {
		log += fmt.Sprintf("  %s  %-9s  %s\n",
			time.Unix(e.Timestamp, 0).UTC().Format("15:04:05"),
			e.State.String(), e.Message)
	}
	footer := ui.DimStyle.Render("ctrl+c stop · q quit")
	box := lipgloss.JoinVertical(lipgloss.Left, header, body, log, footer)
	return ui.BoxStyle.Render(box)
}

// ExitCode returns the suggested process exit code after the program quits.
func (m WatchModel) ExitCode() int { return m.exitCode }

// IsTerminal reports whether the scan state cannot transition further.
// Exported so cmd packages can avoid re-defining the same switch.
func IsTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_FAILED,
		v1.ScanState_CANCELLED, v1.ScanState_PARTIAL:
		return true
	}
	return false
}

func appendCap[T any](xs []T, x T, cap int) []T {
	xs = append(xs, x)
	if len(xs) > cap {
		xs = xs[len(xs)-cap:]
	}
	return xs
}

// scaledPct turns a raw count into a 0..0.95 progress percentage on a
// monotonic asymptotic curve so the bar advances visibly during early
// events but never claims 100% before terminal state (Update force-sets
// 1.0 on terminal). One closed form, no branch, no discontinuity.
func scaledPct(n int64) float64 {
	if n <= 0 {
		return 0
	}
	const (
		cap = 0.95
		k   = 20.0
	)
	x := float64(n)
	return cap * x / (x + k)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
