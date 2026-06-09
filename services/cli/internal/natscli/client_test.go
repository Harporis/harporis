package natscli

import (
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
)

func runJetstream(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := test.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s := test.RunServer(&opts)
	t.Cleanup(s.Shutdown)
	return s
}

func TestDialAndEnsureStreams(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "test-cli")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	if err := cl.EnsureStreams(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cl.NC.IsConnected() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nats not connected within deadline")
}

func TestIsLocalhost(t *testing.T) {
	cases := map[string]bool{
		"nats://localhost:4222":    true,
		"nats://127.0.0.1:4222":    true,
		"nats://[::1]:4222":        true,
		"nats://nats.prod.io:4222": false,
		"nats://192.168.1.10:4222": false,
		"tls://localhost:4222":     true,
		"":                         true, // url.Parse("") returns empty host; default URL fallback
		"://broken":                false,
	}
	for in, want := range cases {
		if got := isLocalhost(in); got != want {
			t.Errorf("isLocalhost(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSanitizeConsumerName(t *testing.T) {
	cases := map[string]string{
		"abc-123":         "abc-123",
		"scan_x":          "scan_x",
		"foo/bar":         "foo_bar",
		"with space":      "with_space",
		"unicode-π-ok":    "unicode-_-ok",
	}
	for in, want := range cases {
		if got := SanitizeConsumerName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
