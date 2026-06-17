package nats

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestRequestSubscriber_DispatchesAndAcks(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-req-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	var seen int32
	handler := func(ctx context.Context, req *v1.ScanRequest) error {
		atomic.AddInt32(&seen, 1)
		require.Equal(t, "scan-z", req.ScanId)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := SubscribeRequests(ctx, cl.JS,
		RequestSubscribeOptions{AckWaitSeconds: 5, MaxInFlightScans: 2}, handler)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	data, err := proto.Marshal(&v1.ScanRequest{ScanId: "scan-z", Type: v1.ScanType_CURRENT_STATE})
	require.NoError(t, err)
	_, err = cl.JS.Publish(wire.ScansRequestsSubject, data)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return atomic.LoadInt32(&seen) == 1 },
		3*time.Second, 50*time.Millisecond)
}

// A handler that runs longer than ack-wait must not be killed by the
// subscriber, and the message must not be redelivered. The subscriber
// sends msg.InProgress() heartbeats while the handler is alive.
func TestRequestSubscriber_LongHandlerKeptAliveByHeartbeat(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-heartbeat-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	const ackWaitS = 2
	const handlerDur = 4 * time.Second // 2x ack-wait — must survive without redelivery

	var dispatches int32
	var ctxDone int32
	done := make(chan struct{})
	handler := func(ctx context.Context, req *v1.ScanRequest) error {
		atomic.AddInt32(&dispatches, 1)
		select {
		case <-time.After(handlerDur):
		case <-ctx.Done():
			atomic.AddInt32(&ctxDone, 1)
		}
		close(done)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := SubscribeRequests(ctx, cl.JS,
		RequestSubscribeOptions{AckWaitSeconds: ackWaitS, MaxInFlightScans: 1}, handler)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	data, _ := proto.Marshal(&v1.ScanRequest{ScanId: "scan-slow", Type: v1.ScanType_CURRENT_STATE})
	_, err = cl.JS.Publish(wire.ScansRequestsSubject, data)
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(handlerDur + 3*time.Second):
		t.Fatal("handler did not finish — subscriber killed it via ack-wait timeout")
	}
	// Wait briefly to surface any in-flight redeliveries.
	time.Sleep(500 * time.Millisecond)

	require.EqualValues(t, 1, atomic.LoadInt32(&dispatches),
		"handler must be invoked exactly once — redelivery means heartbeat failed")
	require.EqualValues(t, 0, atomic.LoadInt32(&ctxDone),
		"handler context must not be cancelled by ack-wait")
}

// A handler that always fails must NOT be redelivered forever. With
// MaxDeliver set, JetStream stops after that many attempts; with a
// NakBackoff the retries are spaced out instead of a tight microsecond
// loop. This is the regression test for the redelivery-storm bug.
func TestRequestSubscriber_StopsAfterMaxDeliver(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-maxdeliver-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	var attempts int32
	handler := func(ctx context.Context, req *v1.ScanRequest) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("always fail")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := SubscribeRequests(ctx, cl.JS, RequestSubscribeOptions{
		AckWaitSeconds:   5,
		MaxInFlightScans: 1,
		MaxDeliver:       3,
		NakBackoff:       50 * time.Millisecond,
	}, handler)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	data, err := proto.Marshal(&v1.ScanRequest{ScanId: "scan-fail", Type: v1.ScanType_CURRENT_STATE})
	require.NoError(t, err)
	_, err = cl.JS.Publish(wire.ScansRequestsSubject, data)
	require.NoError(t, err)

	// Reaches exactly MaxDeliver attempts (3 × 50ms backoff ≈ 150ms).
	require.Eventually(t, func() bool { return atomic.LoadInt32(&attempts) == 3 },
		3*time.Second, 25*time.Millisecond)

	// And does NOT keep growing — no infinite storm.
	time.Sleep(1 * time.Second)
	require.EqualValues(t, 3, atomic.LoadInt32(&attempts),
		"handler must stop being redelivered after MaxDeliver attempts")
}

// A message that cannot be unmarshalled is poison — it will never parse,
// so it must be terminated (dropped) rather than redelivered forever.
func TestRequestSubscriber_PoisonMessageNotRetried(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-poison-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	var dispatches int32
	handler := func(ctx context.Context, req *v1.ScanRequest) error {
		atomic.AddInt32(&dispatches, 1)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := SubscribeRequests(ctx, cl.JS, RequestSubscribeOptions{
		AckWaitSeconds:   2,
		MaxInFlightScans: 1,
		MaxDeliver:       5,
		NakBackoff:       50 * time.Millisecond,
	}, handler)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Not a valid ScanRequest proto.
	_, err = cl.JS.Publish(wire.ScansRequestsSubject, []byte("this is not a protobuf"))
	require.NoError(t, err)

	// The handler must never run (decode fails first), and the poison
	// message must be terminated rather than redelivered in a loop.
	time.Sleep(1 * time.Second)
	require.EqualValues(t, 0, atomic.LoadInt32(&dispatches),
		"poison message must not reach the handler")
}
