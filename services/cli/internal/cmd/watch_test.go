package cmd

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestWriteStatusJSONEmitsProtojson(t *testing.T) {
	var b bytes.Buffer
	writeStatusJSON(&b, &v1.StatusEvent{ScanId: "s1", State: v1.ScanState_RUNNING, Source: "getter-h1"})
	out := b.String()
	if !strings.Contains(out, `"scanId":"s1"`) || !strings.Contains(out, `"source":"getter-h1"`) {
		t.Fatalf("not protojson: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("want newline-terminated json line: %q", out)
	}
}
