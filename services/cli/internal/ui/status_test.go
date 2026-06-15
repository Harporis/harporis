package ui

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestPrintStatusLineStripsControlChars(t *testing.T) {
	var b bytes.Buffer
	PrintStatusLine(&b, &v1.StatusEvent{
		ScanId: "s1", State: v1.ScanState_CANCELLED,
		Message: "scan cancelled: \x1b[2J\x1b]0;evil\x07boom",
		Source:  "getter-\x1b[31mx",
		Metrics: &v1.ScanMetrics{},
	})
	out := b.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("ESC byte leaked into terminal output: %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Fatalf("BEL byte leaked into terminal output: %q", out)
	}
	// Printable content must survive.
	if !strings.Contains(out, "boom") {
		t.Fatalf("printable message content was lost: %q", out)
	}
}

func TestPrintStatusLineIncludesSource(t *testing.T) {
	var b bytes.Buffer
	PrintStatusLine(&b, &v1.StatusEvent{
		ScanId: "s1", State: v1.ScanState_RUNNING, Source: "scanner-7f2a",
		Metrics: &v1.ScanMetrics{ChunksPublished: 12},
	})
	if !strings.Contains(b.String(), "src=scanner-7f2a") {
		t.Fatalf("output missing source: %q", b.String())
	}
}
