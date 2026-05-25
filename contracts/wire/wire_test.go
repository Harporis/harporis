package wire_test

import (
	"testing"
	"time"

	natssrv "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/contracts/wire"
)

func startEmbedded(t *testing.T) string {
	t.Helper()
	opts := &natssrv.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()}
	srv, err := natssrv.NewServer(opts)
	require.NoError(t, err)
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	return srv.ClientURL()
}

func TestDial_EnsureStreams_Idempotent(t *testing.T) {
	url := startEmbedded(t)
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "wire-test"})
	require.NoError(t, err)
	defer cl.Close()

	require.NoError(t, wire.EnsureStreams(cl.JS))
	require.NoError(t, wire.EnsureStreams(cl.JS)) // idempotent

	info, err := cl.JS.StreamInfo(wire.RequestsStream)
	require.NoError(t, err)
	// RequestsStream must capture only the requests subject. The cancel
	// subject is a broadcast on core NATS — if we also stored cancels here
	// (WorkQueuePolicy + no consumer matching that filter), they'd pile up.
	require.Equal(t, []string{wire.ScansRequestsSubject}, info.Config.Subjects)
	require.NotContains(t, info.Config.Subjects, wire.ScansCancelSubject)
}

func TestSubjectBuilders(t *testing.T) {
	require.Equal(t, "harporis.chunks.scan-abc", wire.ChunksSubject("scan-abc"))
	require.Equal(t, "harporis.status.scan-xyz", wire.StatusSubject("scan-xyz"))
	require.Equal(t, "harporis.findings.scan-z", wire.FindingsSubject("scan-z"))
	_ = nats.Connect // keep import in case lint complains
}
