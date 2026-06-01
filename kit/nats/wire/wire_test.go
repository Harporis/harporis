package wire_test

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	nstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Harporis/harporis/kit/nats/wire"
)

func startNATSContainer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "nats:2.10-alpine",
		ExposedPorts: []string{"4222/tcp"},
		Cmd:          []string{"-js"},
		WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, container.Terminate(ctx))
	})

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "4222/tcp")
	require.NoError(t, err)

	return "nats://" + host + ":" + port.Port()
}

func TestDial_EnsureStreams_Idempotent(t *testing.T) {
	url := startNATSContainer(t)

	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "wire-test"})
	require.NoError(t, err)
	defer cl.Close()

	require.NoError(t, wire.EnsureStreams(cl.JS))
	require.NoError(t, wire.EnsureStreams(cl.JS)) // проверка идемпотентности

	info, err := cl.JS.StreamInfo(wire.RequestsStream)
	require.NoError(t, err)
	require.Contains(t, info.Config.Subjects, wire.ScansRequestsSubject)
}

func TestSubjectBuilders(t *testing.T) {
	require.Equal(t, "harporis.chunks.scan-abc", wire.ChunksSubject("scan-abc"))
	require.Equal(t, "harporis.status.scan-xyz", wire.StatusSubject("scan-xyz"))
	require.Equal(t, "harporis.findings.scan-z", wire.FindingsSubject("scan-z"))
	_ = nats.Connect // сохраняем импорт, чтобы линтер не ругался
}

// --- Embedded-JetStream tests (no docker, fast) ---------------------------

func runJSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := nstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s := nstest.RunServer(&opts)
	t.Cleanup(s.Shutdown)
	return s
}

func dial(t *testing.T, s *natsserver.Server) *wire.Client {
	t.Helper()
	cl, err := wire.Dial(wire.DialConfig{URL: s.ClientURL(), ClientName: "test"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(cl.Close)
	return cl
}

func TestEnsureStreams_FindingsHasDuplicatesWindow(t *testing.T) {
	s := runJSServer(t)
	cl := dial(t, s)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	info, err := cl.JS.StreamInfo(wire.FindingsStream)
	if err != nil {
		t.Fatalf("StreamInfo: %v", err)
	}
	if info.Config.Duplicates != 5*time.Minute {
		t.Errorf("FindingsStream.Duplicates = %v, want 5m", info.Config.Duplicates)
	}
}

func TestEnsureStreams_UpdatesExistingStreamConfig(t *testing.T) {
	s := runJSServer(t)
	cl := dial(t, s)

	// Pre-create FindingsStream with NO duplicates window (simulates pre-v0.2.0 deploy).
	_, err := cl.JS.AddStream(&nats.StreamConfig{
		Name:      wire.FindingsStream,
		Subjects:  []string{"harporis.findings.>"},
		Storage:   nats.FileStorage,
		Retention: nats.WorkQueuePolicy,
	})
	if err != nil {
		t.Fatalf("pre-create: %v", err)
	}

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams (update path): %v", err)
	}
	info, _ := cl.JS.StreamInfo(wire.FindingsStream)
	if info.Config.Duplicates != 5*time.Minute {
		t.Errorf("after EnsureStreams, Duplicates = %v, want 5m", info.Config.Duplicates)
	}
}

func TestScannerDurableConsumerConst(t *testing.T) {
	if wire.ScannerDurableConsumer != "scanner-pool" {
		t.Errorf("ScannerDurableConsumer = %q, want %q", wire.ScannerDurableConsumer, "scanner-pool")
	}
}