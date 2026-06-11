package wire_test

import (
	"context"
	"crypto/tls"
	"strconv"
	"strings"
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

func TestEnsureStreams_StatusHasMaxAgeAndMaxBytes(t *testing.T) {
	s := runJSServer(t)
	cl := dial(t, s)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	info, err := cl.JS.StreamInfo(wire.StatusStream)
	if err != nil {
		t.Fatalf("StreamInfo: %v", err)
	}
	if info.Config.MaxAge != wire.StatusMaxAge {
		t.Errorf("StatusStream.MaxAge = %v, want %v", info.Config.MaxAge, wire.StatusMaxAge)
	}
	if info.Config.MaxBytes != wire.StatusMaxBytes {
		t.Errorf("StatusStream.MaxBytes = %d, want %d", info.Config.MaxBytes, wire.StatusMaxBytes)
	}
	if info.Config.Discard != nats.DiscardOld {
		t.Errorf("StatusStream.Discard = %v, want DiscardOld", info.Config.Discard)
	}
}

func TestEnsureStreams_WorkQueueStreamsHaveMaxBytes(t *testing.T) {
	s := runJSServer(t)
	cl := dial(t, s)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	for _, name := range []string{wire.RequestsStream, wire.ChunksStream, wire.FindingsStream} {
		info, err := cl.JS.StreamInfo(name)
		if err != nil {
			t.Fatalf("StreamInfo %s: %v", name, err)
		}
		if info.Config.MaxBytes != wire.WorkQueueMaxBytes {
			t.Errorf("%s.MaxBytes = %d, want %d", name, info.Config.MaxBytes, wire.WorkQueueMaxBytes)
		}
		if info.Config.Discard != nats.DiscardOld {
			t.Errorf("%s.Discard = %v, want DiscardOld", name, info.Config.Discard)
		}
	}
}

// Migration: an existing stream with no MaxAge / MaxBytes (pre-v0.4.1
// shape) must be updated when EnsureStreams runs against it.
func TestEnsureStreams_MigratesStatusStreamLimits(t *testing.T) {
	s := runJSServer(t)
	cl := dial(t, s)

	_, err := cl.JS.AddStream(&nats.StreamConfig{
		Name:      wire.StatusStream,
		Subjects:  []string{"harporis.status.>"},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
		// No MaxAge / MaxBytes / Discard — simulates the unbounded
		// shape that v0.1 → v0.4 stacks created.
	})
	if err != nil {
		t.Fatalf("pre-create StatusStream: %v", err)
	}

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams (migrate): %v", err)
	}
	info, _ := cl.JS.StreamInfo(wire.StatusStream)
	if info.Config.MaxAge != wire.StatusMaxAge {
		t.Errorf("after migrate, MaxAge = %v, want %v", info.Config.MaxAge, wire.StatusMaxAge)
	}
	if info.Config.MaxBytes != wire.StatusMaxBytes {
		t.Errorf("after migrate, MaxBytes = %d, want %d", info.Config.MaxBytes, wire.StatusMaxBytes)
	}
}

func TestFindingsSubject_DefaultIsLegacy(t *testing.T) {
	// Unset the env var explicitly; the wire pkg default must be 1 shard.
	t.Setenv(wire.EnvFindingsShards, "")
	if got := wire.FindingsSubject("scan-abc"); got != "harporis.findings.scan-abc" {
		t.Errorf("FindingsSubject = %q, want legacy 'harporis.findings.scan-abc'", got)
	}
	if got := wire.FindingsShardCount(); got != 1 {
		t.Errorf("FindingsShardCount = %d, want 1", got)
	}
	if got := wire.FindingsShardFilterSubject(0, 1); got != "harporis.findings.>" {
		t.Errorf("filter subject = %q, want legacy wildcard", got)
	}
	if got := wire.WriterDurableForShard(0, 1); got != wire.WriterDurableConsumer {
		t.Errorf("durable = %q, want legacy %q", got, wire.WriterDurableConsumer)
	}
}

func TestFindingsSubject_ShardedSubjects(t *testing.T) {
	t.Setenv(wire.EnvFindingsShards, "4")
	if got := wire.FindingsShardCount(); got != 4 {
		t.Fatalf("FindingsShardCount = %d, want 4", got)
	}
	got := wire.FindingsSubject("scan-abc")
	want := "harporis.findings.s" + strconv.Itoa(wire.ShardForScanID("scan-abc", 4)) + ".scan-abc"
	if got != want {
		t.Errorf("FindingsSubject = %q, want %q", got, want)
	}
	// Filter subjects per shard.
	for i := 0; i < 4; i++ {
		if got := wire.FindingsShardFilterSubject(i, 4); got != "harporis.findings.s"+strconv.Itoa(i)+".>" {
			t.Errorf("filter subject for shard %d = %q", i, got)
		}
		if got := wire.WriterDurableForShard(i, 4); got != "writer-pool-s"+strconv.Itoa(i) {
			t.Errorf("durable for shard %d = %q", i, got)
		}
	}
}

func TestShardForScanID_StableAcrossCalls(t *testing.T) {
	for _, scanID := range []string{"a", "scan-1", "00000000-0000-0000-0000-000000000000", "long-scan-id-with-dashes"} {
		first := wire.ShardForScanID(scanID, 8)
		for i := 0; i < 100; i++ {
			if wire.ShardForScanID(scanID, 8) != first {
				t.Fatalf("ShardForScanID not stable for %q", scanID)
			}
		}
		if first < 0 || first >= 8 {
			t.Errorf("shard %d out of range for %q", first, scanID)
		}
	}
}

func TestShardForScanID_RoughlyEvenDistribution(t *testing.T) {
	const N = 4
	const samples = 10000
	counts := make([]int, N)
	for i := 0; i < samples; i++ {
		counts[wire.ShardForScanID("scan-"+strconv.Itoa(i), N)]++
	}
	for i, c := range counts {
		// 25% expected ± 25% slack — FNV-1a on monotonic IDs isn't
		// perfectly uniform but should be well within these bounds.
		if c < samples/N*3/4 || c > samples/N*5/4 {
			t.Errorf("shard %d got %d of %d (%.1f%%), expected ~25%%", i, c, samples, float64(c)/samples*100)
		}
	}
}

func TestScannerDurableConsumerConst(t *testing.T) {
	if wire.ScannerDurableConsumer != "scanner-pool" {
		t.Errorf("ScannerDurableConsumer = %q, want %q", wire.ScannerDurableConsumer, "scanner-pool")
	}
}

func TestEnsureStreams_EnvOverridesRetention(t *testing.T) {
	t.Setenv(wire.EnvStatusRetentionAge, "1h")
	t.Setenv(wire.EnvStatusRetentionMaxBytes, "1048576")
	t.Setenv(wire.EnvWorkQueueMaxBytes, "2097152")

	s := runJSServer(t)
	cl := dial(t, s)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	st, _ := cl.JS.StreamInfo(wire.StatusStream)
	if st.Config.MaxAge != time.Hour {
		t.Errorf("StatusStream.MaxAge = %v, want 1h", st.Config.MaxAge)
	}
	if st.Config.MaxBytes != 1<<20 {
		t.Errorf("StatusStream.MaxBytes = %d, want 1MiB", st.Config.MaxBytes)
	}
	wq, _ := cl.JS.StreamInfo(wire.RequestsStream)
	if wq.Config.MaxBytes != 1<<21 {
		t.Errorf("RequestsStream.MaxBytes = %d, want 2MiB", wq.Config.MaxBytes)
	}
}

func TestEnsureStreams_InvalidEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv(wire.EnvStatusRetentionAge, "not-a-duration")
	t.Setenv(wire.EnvStatusRetentionMaxBytes, "negative-please")

	s := runJSServer(t)
	cl := dial(t, s)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	st, _ := cl.JS.StreamInfo(wire.StatusStream)
	if st.Config.MaxAge != wire.StatusMaxAge {
		t.Errorf("MaxAge = %v, want default %v", st.Config.MaxAge, wire.StatusMaxAge)
	}
}

func TestDial_CredsFileNotFound(t *testing.T) {
	s := runJSServer(t)
	_, err := wire.Dial(wire.DialConfig{
		URL:        s.ClientURL(),
		ClientName: "test",
		CredsFile:  "/nonexistent/creds.txt",
	})
	if err == nil {
		t.Fatal("Dial: want error for missing creds file, got nil")
	}
	if !strings.Contains(err.Error(), "creds") && !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "open") {
		t.Errorf("Dial err = %v, want creds/file-related", err)
	}
}

func TestDial_TLSConfigPropagates(t *testing.T) {
	// A tls.Config against a non-TLS test server proves the Secure option
	// was applied: the underlying nats.Conn either fails to reach CONNECTED
	// or reports a TLS-related last error. We rely on connection state
	// because nats.RetryOnFailedConnect(true) hides the initial error from
	// the Connect call itself.
	s := runJSServer(t)
	cl, err := wire.Dial(wire.DialConfig{
		URL:        s.ClientURL(),
		ClientName: "test",
		TLSConfig:  &tls.Config{},
	})
	if err != nil {
		// Acceptable outcome: Dial surfaced the TLS mismatch synchronously.
		return
	}
	defer cl.Close()
	if cl.NC.Status() == nats.CONNECTED {
		t.Fatalf("Dial: TLSConfig was not applied — connection reached CONNECTED against a non-TLS server (status=%v, lastErr=%v)", cl.NC.Status(), cl.NC.LastError())
	}
}