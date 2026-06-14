package ui

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

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
