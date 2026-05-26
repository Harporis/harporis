package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// StepDoneMsg is sent by the orchestrator when a step resolves.
type StepDoneMsg struct {
	Index int
	OK    bool
	Took  string // "1.2s"
	Err   string // populated when OK=false
}

// StepNotifier is the callback shape used by the up orchestrator to push
// step results into either the tea program or stdout fallback.
type StepNotifier interface {
	Done(index int, ok bool, took, err string)
}

type stepStatus int

const (
	stepPending stepStatus = iota
	stepRunning
	stepDone
	stepFailed
)

// UpModel is the live startup checklist.
type UpModel struct {
	steps    []string
	status   []stepStatus
	took     []string
	errs     []string
	spinner  spinner.Model
	current  int
	finished bool
}

// NewUpModel creates the checklist with all steps pending and step 0
// running.
func NewUpModel(steps []string) UpModel {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	status := make([]stepStatus, len(steps))
	if len(steps) > 0 {
		status[0] = stepRunning
	}
	return UpModel{
		steps:   steps,
		status:  status,
		took:    make([]string, len(steps)),
		errs:    make([]string, len(steps)),
		spinner: sp,
	}
}

// Init starts the spinner.
func (m UpModel) Init() tea.Cmd { return m.spinner.Tick }

// Update handles step transitions.
func (m UpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		if v.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case StepDoneMsg:
		if v.Index < 0 || v.Index >= len(m.steps) {
			return m, nil
		}
		m.took[v.Index] = v.Took
		if v.OK {
			m.status[v.Index] = stepDone
		} else {
			m.status[v.Index] = stepFailed
			m.errs[v.Index] = v.Err
			m.finished = true
			return m, tea.Quit
		}
		if v.Index+1 < len(m.steps) {
			m.status[v.Index+1] = stepRunning
			m.current = v.Index + 1
		} else {
			m.finished = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders the checklist.
func (m UpModel) View() string {
	icons := ui.NewIcons(false)
	var b strings.Builder
	b.WriteString("starting stack…\n")
	for i, name := range m.steps {
		marker := "○"
		extra := ""
		switch m.status[i] {
		case stepDone:
			marker = ui.OKStyle.Render(icons.OK)
			extra = ui.DimStyle.Render(" (" + m.took[i] + ")")
		case stepFailed:
			marker = ui.ErrStyle.Render(icons.Fail)
			extra = ui.ErrStyle.Render(" — " + m.errs[i])
		case stepRunning:
			marker = ui.WarnStyle.Render(m.spinner.View())
		}
		b.WriteString(fmt.Sprintf(" %s %s%s\n", marker, name, extra))
	}
	if !m.finished {
		b.WriteString(ui.DimStyle.Render("\n ctrl+c abort\n"))
	}
	return b.String()
}
