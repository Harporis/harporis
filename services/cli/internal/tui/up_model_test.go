package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestUpModelStepProgression(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	steps := []string{"compose up", "nats container", "nats healthz", "getter container", "getter health"}
	m := NewUpModel(steps)
	out := m.View()
	for _, s := range steps {
		if !strings.Contains(out, s) {
			t.Errorf("initial view missing step %q", s)
		}
	}
	mi, _ := m.Update(StepDoneMsg{Index: 0, OK: true, Took: "1.2s"})
	if !strings.Contains(mi.View(), "1.2s") {
		t.Fatal("step done not reflected")
	}
}

func TestUpModelFailedStepQuits(t *testing.T) {
	m := NewUpModel([]string{"a", "b"})
	_, cmd := m.Update(StepDoneMsg{Index: 0, OK: false, Took: "0s", Err: "boom"})
	if cmd == nil {
		t.Fatal("expected tea.Quit on failed step")
	}
}

func TestUpModelLastStepQuits(t *testing.T) {
	m := NewUpModel([]string{"a"})
	_, cmd := m.Update(StepDoneMsg{Index: 0, OK: true, Took: "1s"})
	if cmd == nil {
		t.Fatal("expected tea.Quit when last step done")
	}
}
