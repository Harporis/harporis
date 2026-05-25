package nats

import (
	"context"
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
