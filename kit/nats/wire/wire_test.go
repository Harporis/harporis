package wire_test

import (
	"context"
	"testing"
	"time"

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
	require.Contains(t, info.Config.Subjects, "harporis.scans.>")
}

func TestSubjectBuilders(t *testing.T) {
	require.Equal(t, "harporis.chunks.scan-abc", wire.ChunksSubject("scan-abc"))
	require.Equal(t, "harporis.status.scan-xyz", wire.StatusSubject("scan-xyz"))
	require.Equal(t, "harporis.findings.scan-z", wire.FindingsSubject("scan-z"))
	_ = nats.Connect // сохраняем импорт, чтобы линтер не ругался
}