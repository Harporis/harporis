//go:build integration

package writer_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

func runJSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := test.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s := test.RunServer(&opts)
	t.Cleanup(s.Shutdown)
	return s
}

// TestEndToEnd_PublishedFindingMaterializesNDJSON verifies the writer
// goes from "Finding on NATS" to "JSON line in <scan_id>.ndjson" without
// inspecting any internals: build the binary, run it against an embedded
// JetStream server, publish one Finding, assert the file appears with the
// right contents.
func TestEndToEnd_PublishedFindingMaterializesNDJSON(t *testing.T) {
	s := runJSServer(t)

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "writer")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/writer")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build writer: %v\n%s", err, out)
	}

	outputDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath,
		"-config", "config/writer.yaml",
		"-output-dir", outputDir,
	)
	cmd.Env = append(os.Environ(), "NATS_URL="+s.ClientURL())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_, _ = cmd.Process.Wait()
	})

	cl, err := wire.Dial(wire.DialConfig{URL: s.ClientURL(), ClientName: "test"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	// Wait until the writer's durable consumer exists — its presence
	// means the writer is fully booted and ready to receive findings.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := cl.JS.ConsumerInfo(wire.FindingsStream, wire.WriterDurableConsumer); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer never created its durable consumer")
		}
		time.Sleep(250 * time.Millisecond)
	}

	finding := &v1.Finding{
		ScanId:        "scan-e2e",
		FindingId:     "f-1",
		ChunkId:       "c-1",
		RuleId:        "aws-access-key-id",
		Severity:      v1.Severity_HIGH,
		FilePath:      "secrets.txt",
		LineNumber:    2,
		MatchedSecret: []byte("AKIAIOSFODNN7EXAMPLE"),
	}
	body, _ := proto.Marshal(finding)
	if _, err := cl.JS.Publish(wire.FindingsSubject("scan-e2e"), body); err != nil {
		t.Fatalf("publish finding: %v", err)
	}

	// Wait for the writer to materialize <scan_id>.ndjson and a first
	// line. Bounded wait — if it doesn't appear within 15s, fail.
	path := filepath.Join(outputDir, "scan-e2e.ndjson")
	deadline = time.Now().Add(15 * time.Second)
	var line string
	for {
		f, err := os.Open(path)
		if err == nil {
			sc := bufio.NewScanner(f)
			if sc.Scan() {
				line = sc.Text()
				f.Close()
				break
			}
			f.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("finding never materialized at %s", path)
		}
		time.Sleep(200 * time.Millisecond)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("not valid JSON: %v\nline=%q", err, line)
	}
	if got["scan_id"] != "scan-e2e" {
		t.Errorf("scan_id = %v, want scan-e2e", got["scan_id"])
	}
	if got["rule_id"] != "aws-access-key-id" {
		t.Errorf("rule_id = %v, want aws-access-key-id", got["rule_id"])
	}
	if got["severity"] != "HIGH" {
		t.Errorf("severity = %v, want HIGH", got["severity"])
	}
	// Proto bytes (MatchedSecret) are base64-encoded by protojson; the
	// decoded payload should match the AKIA secret we published.
	if ms, _ := got["matched_secret"].(string); !strings.Contains(ms, "QUtJ") {
		t.Errorf("matched_secret = %v (expected base64 of AKIA...)", ms)
	}
}
