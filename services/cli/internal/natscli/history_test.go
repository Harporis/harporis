package natscli

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

func TestListHistoryLastEventPerScan(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "history-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatal(err)
	}

	pub := func(id string, state v1.ScanState, ts int64) {
		data, _ := proto.Marshal(&v1.StatusEvent{
			ScanId: id, State: state, Timestamp: ts, Message: "msg",
		})
		_, err := cl.JS.Publish(wire.StatusSubject(id), data,
			nats.MsgId(fmt.Sprintf("%s:%s:%d", id, state.String(), ts)))
		if err != nil {
			t.Fatal(err)
		}
	}

	pub("a", v1.ScanState_RUNNING, 100)
	pub("a", v1.ScanState_COMPLETED, 200)
	pub("b", v1.ScanState_FAILED, 150)

	got, err := cl.ListHistory(5, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 scans, got %d: %+v", len(got), got)
	}
	for _, e := range got {
		switch e.ScanId {
		case "a":
			if e.State != v1.ScanState_COMPLETED {
				t.Errorf("a state %s", e.State)
			}
		case "b":
			if e.State != v1.ScanState_FAILED {
				t.Errorf("b state %s", e.State)
			}
		default:
			t.Errorf("unexpected scan %q", e.ScanId)
		}
	}
}

func TestListHistoryTerminalStateSticky(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "history-sticky-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatal(err)
	}

	pub := func(id string, state v1.ScanState, ts int64) {
		data, _ := proto.Marshal(&v1.StatusEvent{
			ScanId: id, State: state, Timestamp: ts,
		})
		_, err := cl.JS.Publish(wire.StatusSubject(id), data,
			nats.MsgId(fmt.Sprintf("%s:%s:%d", id, state.String(), ts)))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Publish COMPLETED first (ts=10), then a later RUNNING tick (ts=20).
	// The late RUNNING must NOT overwrite the terminal COMPLETED.
	pub("x", v1.ScanState_COMPLETED, 10)
	pub("x", v1.ScanState_RUNNING, 20)

	got, err := cl.ListHistory(5, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 scan, got %d: %+v", len(got), got)
	}
	if got[0].State != v1.ScanState_COMPLETED {
		t.Errorf("terminal state must be sticky: got %s, want COMPLETED", got[0].State)
	}
}

func TestShowHistoryReturnsOrdered(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "history-show-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatal(err)
	}

	for _, ts := range []int64{300, 100, 200} {
		data, _ := proto.Marshal(&v1.StatusEvent{
			ScanId: "x", State: v1.ScanState_RUNNING, Timestamp: ts,
		})
		_, err := cl.JS.Publish(wire.StatusSubject("x"), data,
			nats.MsgId(fmt.Sprintf("x:%d", ts)))
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := cl.ShowHistory("x", 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	for i, want := range []int64{100, 200, 300} {
		if got[i].Timestamp != want {
			t.Errorf("idx %d: got ts %d want %d", i, got[i].Timestamp, want)
		}
	}
}
