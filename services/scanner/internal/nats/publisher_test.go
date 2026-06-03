package nats

import (
	"context"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

func TestPublisher_FindingDedupedByMsgID(t *testing.T) {
	s := runJSServer(t)
	cl := dialAndEnsure(t, s)
	p := NewPublisher(cl.JS, 5*time.Second)

	f := &v1.Finding{
		ScanId: "scan-X", FindingId: "fid-1", ChunkId: "c-1",
		RuleId: "test", LineNumber: 5, ByteOffset: 0,
	}
	if err := p.PublishFinding(context.Background(), f); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	// Second publish with the same dedup key (same scan|chunk|rule|line|offset)
	// should be dropped by JetStream's Duplicates window.
	f2 := &v1.Finding{
		ScanId: "scan-X", FindingId: "fid-2-different", ChunkId: "c-1",
		RuleId: "test", LineNumber: 5, ByteOffset: 0,
	}
	if err := p.PublishFinding(context.Background(), f2); err != nil {
		t.Fatalf("publish 2: %v", err)
	}

	// Drain stream and count delivered messages.
	sub, err := cl.JS.PullSubscribe(
		wire.FindingsSubject("scan-X"),
		"verify",
		natsclient.BindStream(wire.FindingsStream),
	)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	msgs, err := sub.Fetch(5, natsclient.MaxWait(500*time.Millisecond))
	if err != nil && err != natsclient.ErrTimeout {
		t.Fatalf("fetch: %v", err)
	}
	for _, m := range msgs {
		_ = m.Ack()
	}
	if len(msgs) != 1 {
		t.Errorf("got %d messages, want 1 (second publish should be deduped)", len(msgs))
	}
}

func TestPublisher_StatusEventCarriesSecretsFound(t *testing.T) {
	s := runJSServer(t)
	cl := dialAndEnsure(t, s)
	p := NewPublisher(cl.JS, 5*time.Second)

	err := p.PublishStatusSecretsFound(context.Background(), "scan-Y", 42)
	if err != nil {
		t.Fatalf("publish status: %v", err)
	}

	sub, _ := cl.JS.PullSubscribe(
		wire.StatusSubject("scan-Y"),
		"verify",
		natsclient.BindStream(wire.StatusStream),
	)
	msgs, _ := sub.Fetch(1, natsclient.MaxWait(500*time.Millisecond))
	if len(msgs) != 1 {
		t.Fatalf("got %d status msgs, want 1", len(msgs))
	}
	var ev v1.StatusEvent
	if err := proto.Unmarshal(msgs[0].Data, &ev); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if ev.ScanId != "scan-Y" || ev.Metrics == nil || ev.Metrics.SecretsFound != 42 {
		t.Errorf("status event wrong: %+v", &ev)
	}
	if ev.State != v1.ScanState_RUNNING {
		t.Errorf("state = %v, want RUNNING", ev.State)
	}
	_ = msgs[0].Ack()
}
