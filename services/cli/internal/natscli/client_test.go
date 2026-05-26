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
