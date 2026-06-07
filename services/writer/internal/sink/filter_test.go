package sink

import (
	"context"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// fakeSink lets us probe WantedByFinding without standing up a real sink.
type fakeSink struct{ n string }

func (f *fakeSink) Name() string                            { return f.n }
func (f *fakeSink) Write(context.Context, *v1.Finding) error { return nil }
func (f *fakeSink) Close() error                            { return nil }

func TestWantedByFinding_EmptyMatchesAll(t *testing.T) {
	for _, name := range []string{"ndjson_file", "sarif_file", "html_file", "pdf_file"} {
		if !WantedByFinding(&fakeSink{n: name}, nil) {
			t.Errorf("empty formats should pass %s", name)
		}
	}
}

func TestWantedByFinding_ShortNameMatch(t *testing.T) {
	s := &fakeSink{n: "ndjson_file"}
	if !WantedByFinding(s, []string{"ndjson"}) {
		t.Error("ndjson should match ndjson_file")
	}
	if WantedByFinding(s, []string{"pdf", "sarif"}) {
		t.Error("ndjson_file should NOT match {pdf, sarif}")
	}
}

func TestWantedByFinding_CaseAndWhitespace(t *testing.T) {
	s := &fakeSink{n: "html_file"}
	cases := []string{"HTML", "  html  ", "Html"}
	for _, c := range cases {
		if !WantedByFinding(s, []string{c}) {
			t.Errorf("format %q should match html_file", c)
		}
	}
}

func TestWantedByFinding_MultiFormat(t *testing.T) {
	for _, name := range []string{"ndjson_file", "html_file", "pdf_file"} {
		if !WantedByFinding(&fakeSink{n: name}, []string{"ndjson", "html", "pdf"}) {
			t.Errorf("multi-format request should pass %s", name)
		}
	}
	if WantedByFinding(&fakeSink{n: "sarif_file"}, []string{"ndjson", "html", "pdf"}) {
		t.Error("sarif_file should be excluded when not requested")
	}
}
