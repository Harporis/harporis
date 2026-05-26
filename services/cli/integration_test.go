//go:build integration

package cli_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
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

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/harporis"
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/harporis")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestScanPublishesRequest(t *testing.T) {
	srv := runJSServer(t)
	bin := buildBinary(t)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.EnsureStreams(js); err != nil {
		t.Fatal(err)
	}
	sub, err := js.PullSubscribe(wire.ScansRequestsSubject, "ittest", nats.BindStream(wire.RequestsStream))
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"--nats", srv.ClientURL(),
		"scan",
		"--local", "/repos/demo",
		"--scan-id", "it-1",
		"--no-wait",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("scan exec: %v\n%s", err, out)
	}

	msgs, err := sub.Fetch(1, nats.MaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var req v1.ScanRequest
	if err := proto.Unmarshal(msgs[0].Data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ScanId != "it-1" || req.GetSource().GetLocalPath() != "/repos/demo" {
		t.Fatalf("unexpected: %+v", &req)
	}
}
