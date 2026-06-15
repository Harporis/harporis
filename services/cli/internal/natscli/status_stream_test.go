package natscli

import (
	"testing"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"google.golang.org/protobuf/proto"
)

func TestSubscribeStatusAllTailsNewEvents(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "test-cli")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	sub, cleanup, err := cl.SubscribeStatusAll()
	if err != nil {
		t.Fatalf("subscribe all: %v", err)
	}
	defer cleanup()

	ev := &v1.StatusEvent{ScanId: "scan-x", State: v1.ScanState_RUNNING, Source: "getter-h1"}
	body, _ := proto.Marshal(ev)
	if _, err := cl.JS.Publish(wire.StatusSubject("scan-x"), body); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		evs, err := FetchStatusEvents(sub, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		for _, got := range evs {
			if got.ScanId == "scan-x" && got.Source == "getter-h1" {
				return
			}
		}
	}
	t.Fatal("did not observe published event within deadline")
}
