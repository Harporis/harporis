package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestStateStyleHasColorWhenProfileSupports(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	got := StateStyle("RUNNING").Render("RUN")
	if !strings.Contains(got, "RUN") {
		t.Fatalf("style erased text: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI escape on truecolor profile, got %q", got)
	}
}

func TestStateStyleNoColorInAsciiProfile(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	got := StateStyle("RUNNING").Render("RUN")
	if got != "RUN" {
		t.Fatalf("expected raw text in ascii profile, got %q", got)
	}
}
