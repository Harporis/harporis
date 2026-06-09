//go:build integration

package scanner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
	natsclient "github.com/nats-io/nats.go"
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

func TestEndToEnd_AKIAChunkProducesFinding(t *testing.T) {
	s := runJSServer(t)

	// Build the scanner binary.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "scanner")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/scanner")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scanner: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "-config", "config/scanner.yaml")
	cmd.Env = append(os.Environ(), "NATS_URL="+s.ClientURL())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start scanner: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_, _ = cmd.Process.Wait()
	})

	// Connect a verifier client.
	cl, err := wire.Dial(wire.DialConfig{URL: s.ClientURL(), ClientName: "test"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	// Wait until scanner has created its durable consumer.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := cl.JS.ConsumerInfo(wire.ChunksStream, wire.ScannerDurableConsumer); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scanner never created its durable consumer")
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Publish a chunk containing an AWS access key.
	chunk := &v1.GitRowChunk{
		ScanId: "scan-e2e", ChunkId: "c1", Kind: v1.ChunkKind_DIFF_WINDOW,
		FilePath: "secrets.txt",
		Rows: []*v1.GitRow{
			{LineNumber: 1, Content: []byte("nothing here")},
			{LineNumber: 2, Content: []byte("aws_key=AKIAIOSFODNN7EXAMPLE")},
			{LineNumber: 3, Content: []byte("bye")},
		},
	}
	body, _ := proto.Marshal(chunk)
	if _, err := cl.JS.Publish(wire.ChunksSubject("scan-e2e"), body); err != nil {
		t.Fatalf("publish chunk: %v", err)
	}

	// Wait for a Finding to appear on the findings stream.
	sub, err := cl.JS.PullSubscribe(
		wire.FindingsSubject("scan-e2e"),
		"verify",
		natsclient.BindStream(wire.FindingsStream),
	)
	if err != nil {
		t.Fatalf("verify pull: %v", err)
	}
	msgs, err := sub.Fetch(1, natsclient.MaxWait(15*time.Second))
	if err != nil || len(msgs) == 0 {
		t.Fatalf("did not receive a finding: err=%v msgs=%d", err, len(msgs))
	}
	var f v1.Finding
	if err := proto.Unmarshal(msgs[0].Data, &f); err != nil {
		t.Fatalf("unmarshal finding: %v", err)
	}
	if f.RuleId != "aws-access-key-id" {
		t.Errorf("rule_id = %q, want aws-access-key-id", f.RuleId)
	}
	if f.ScanId != "scan-e2e" {
		t.Errorf("scan_id = %q, want scan-e2e", f.ScanId)
	}
	if f.LineNumber != 2 {
		t.Errorf("line_number = %d, want 2", f.LineNumber)
	}
	_ = msgs[0].Ack()
}
