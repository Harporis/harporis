package nats

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/wire"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestCancelSubscriber_Broadcast(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()
	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-cancel-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	var seen int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := SubscribeCancel(ctx, cl.NC, func(_ context.Context, req *v1.CancelScanRequest) {
		require.Equal(t, "scan-x", req.ScanId)
		atomic.AddInt32(&seen, 1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	data, err := proto.Marshal(&v1.CancelScanRequest{ScanId: "scan-x", Reason: "test"})
	require.NoError(t, err)
	require.NoError(t, cl.NC.Publish(wire.ScansCancelSubject, data))

	require.Eventually(t, func() bool { return atomic.LoadInt32(&seen) == 1 },
		2*time.Second, 50*time.Millisecond)
}
