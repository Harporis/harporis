package nats

import (
	"testing"

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

func dialAndEnsure(t *testing.T, s *natsserver.Server) *wire.Client {
	t.Helper()
	cl, err := wire.Dial(wire.DialConfig{URL: s.ClientURL(), ClientName: "test"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(cl.Close)
	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("ensure streams: %v", err)
	}
	return cl
}

func publishChunk(t *testing.T, cl *wire.Client, scanID string, chunk *v1.GitRowChunk) {
	t.Helper()
	b, err := proto.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := cl.JS.Publish(wire.ChunksSubject(scanID), b); err != nil {
		t.Fatalf("publish: %v", err)
	}
}
