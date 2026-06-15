package ui

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// sanitizeTerm replaces non-printable runes (ANSI escapes, BEL, etc.) so
// attacker-influenced StatusEvent fields cannot inject terminal control
// sequences when rendered. Tabs/newlines are normalized to spaces.
func sanitizeTerm(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if !unicode.IsPrint(r) {
			return '.'
		}
		return r
	}, s)
}

// PrintStatusLine renders one StatusEvent to the writer using the
// shared theme. Used by both `watch` (line-based path) and `history
// show`, so any format change happens in one place.
func PrintStatusLine(out io.Writer, ev *v1.StatusEvent) {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format(time.RFC3339)
	state := StateStyle(ev.State.String()).Render(ev.State.String())
	m := ev.GetMetrics()
	fmt.Fprintf(out, "[%s] %-9s | src=%s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, state, sanitizeTerm(ev.GetSource()), sanitizeTerm(ev.Message),
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
}
