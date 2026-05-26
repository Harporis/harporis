package ui

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var updateGolden = flag.Bool("update", false, "regenerate testdata/banner.golden")

func TestBannerASCIIGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	got := Banner("v1.0.0", "v1", "nats://localhost:4222")
	if *updateGolden {
		if err := os.WriteFile("testdata/banner.golden", []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile("testdata/banner.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("banner mismatch.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestBannerContainsDynamicFields(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	got := Banner("v9.9.9", "v2", "nats://example:4222")
	// Brand wordmark is rendered as Unicode block-drawing chars, not
	// literal "HARPORIS" text — assert presence of the box-drawing
	// glyphs and dynamic fields.
	for _, want := range []string{"v9.9.9", "v2", "nats://example:4222", "██", "git-aware secret hunter"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q", want)
		}
	}
}
