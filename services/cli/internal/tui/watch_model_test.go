package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestWatchModelInitialView(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := NewWatchModel("scan-abc")
	out := m.View()
	if !strings.Contains(out, "scan-abc") {
		t.Fatalf("view missing scan id: %s", out)
	}
	if !strings.Contains(out, "PENDING") {
		t.Fatalf("view missing initial state: %s", out)
	}
}

func TestWatchModelTransitionsToRunning(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := NewWatchModel("scan-abc")
	mi, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{
		ScanId:  "scan-abc",
		State:   v1.ScanState_RUNNING,
		Message: "scan started",
	}})
	if !strings.Contains(mi.View(), "RUNNING") {
		t.Fatalf("did not transition: %s", mi.View())
	}
}

func TestWatchModelTerminalQuits(t *testing.T) {
	m := NewWatchModel("scan-abc")
	_, cmd := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{
		ScanId: "scan-abc",
		State:  v1.ScanState_COMPLETED,
	}})
	if cmd == nil {
		t.Fatal("expected tea.Quit command on terminal state")
	}
}

func TestWatchModelFailedSetsExitCode(t *testing.T) {
	m := NewWatchModel("scan-abc")
	mi, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{
		ScanId: "scan-abc",
		State:  v1.ScanState_FAILED,
	}})
	wm, ok := mi.(WatchModel)
	if !ok {
		t.Fatalf("model type changed: %T", mi)
	}
	if wm.ExitCode() != 3 {
		t.Fatalf("failed state should set exit code 3, got %d", wm.ExitCode())
	}
}
