package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

const fakeMetrics = `# HELP foo
foo_total 42
harporis_blobs_scanned 100
harporis_chunks_published 7
unrelated_metric 1
`

func TestFetchAndFilterPrintsMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fakeMetrics))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := fetchAndPrintMetrics(srv.URL, regexp.MustCompile("^harporis_"), &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"harporis_blobs_scanned 100", "harporis_chunks_published 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unrelated_metric") {
		t.Errorf("filter leaked unrelated: %s", got)
	}
}
