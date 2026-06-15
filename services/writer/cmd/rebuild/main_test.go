package main

import (
	"bytes"
	"context"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
	"google.golang.org/protobuf/encoding/protojson"
)

// captureSink records findings written to it, for asserting the filter.
type captureSink struct{ got []v1.Severity }

func (c *captureSink) Name() string { return "capture_file" }
func (c *captureSink) Write(_ context.Context, f *v1.Finding) error {
	c.got = append(c.got, f.Severity)
	return nil
}
func (c *captureSink) Close() error                               { return nil }
func (c *captureSink) Finalize(_ context.Context, _ string) error { return nil }

func ndjsonLine(t *testing.T, scanID string, sev v1.Severity) []byte {
	t.Helper()
	b, err := protojson.Marshal(&v1.Finding{ScanId: scanID, Severity: sev})
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestReplayFiltersBySeverity(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(ndjsonLine(t, "s1", v1.Severity_LOW))
	buf.Write(ndjsonLine(t, "s1", v1.Severity_CRITICAL))
	buf.Write(ndjsonLine(t, "s1", v1.Severity_HIGH))

	set, _ := severity.ParseCSV("CRITICAL,HIGH")
	sink := &captureSink{}
	n, err := replay(context.Background(), &buf, sink, "s1", set)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 written, got %d", n)
	}
	for _, s := range sink.got {
		if s == v1.Severity_LOW {
			t.Fatalf("LOW should have been filtered")
		}
	}
}
