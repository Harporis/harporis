package ui

import (
	"fmt"
	"io"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// PrintStatusLine renders one StatusEvent to the writer using the
// shared theme. Used by both `watch` (line-based path) and `history
// show`, so any format change happens in one place.
func PrintStatusLine(out io.Writer, ev *v1.StatusEvent) {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format(time.RFC3339)
	state := StateStyle(ev.State.String()).Render(ev.State.String())
	m := ev.GetMetrics()
	fmt.Fprintf(out, "[%s] %-9s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, state, ev.Message,
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
}
