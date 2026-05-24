package testutil

import (
	"testing"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"
)

// StartEmbeddedNATS launches an in-process NATS server with JetStream enabled
// on an ephemeral port. Returns the client URL and a stop func that must be
// invoked to shut the server down (also registered via t.Cleanup as a safety
// net).
func StartEmbeddedNATS(t *testing.T) (url string, stop func()) {
	t.Helper()
	opts := &natssrv.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
	}
	srv, err := natssrv.NewServer(opts)
	require.NoError(t, err)
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded NATS server not ready")
	}
	shutdown := func() {
		srv.Shutdown()
		srv.WaitForShutdown()
	}
	t.Cleanup(shutdown)
	return srv.ClientURL(), shutdown
}
