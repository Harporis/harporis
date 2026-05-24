package getter_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/wire"

	"github.com/Harporis/harporis/services/getter/internal/filter"
	getnats "github.com/Harporis/harporis/services/getter/internal/nats"
	"github.com/Harporis/harporis/services/getter/internal/scan"
	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestEndToEnd_CurrentState(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()

	repo := testutil.NewGitRepo(t)
	repo.Write("a.go", "package main\nconst X = \"secret\"\n")
	repo.Write("README.md", "# hi\n")
	repo.Commit("seed")

	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-e2e"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))
	pub := getnats.NewPublisher(cl.JS, 5)

	registry := scan.NewRegistry()
	dispatcher := func(ctx context.Context, req *v1.ScanRequest) error {
		sc := scan.NewContext(req.ScanId)
		runCtx, cancel := context.WithCancel(ctx)
		require.NoError(t, registry.Register(req.ScanId, sc, cancel))
		defer registry.Unregister(req.ScanId)
		runner := scan.NewRunner(scan.RunnerConfig{
			ScanID:   req.ScanId,
			RepoDir:  repo.Dir,
			WalkMode: "current_state",
			Filter: &filter.Filter{
				PathExclusions: []string{".git/"},
				MaxFileSize:    10 * 1024 * 1024,
			},
			Publisher:          pub,
			RowSizeTargetBytes: 4096,
			OverlapLines:       0,
			Workers:            1,
		})
		return runner.Run(runCtx)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := getnats.SubscribeRequests(ctx, cl.JS,
		getnats.RequestSubscribeOptions{AckWaitSeconds: 30, MaxInFlightScans: 2}, dispatcher)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	cSub, err := cl.JS.PullSubscribe(wire.ChunksSubject("scan-e2e"), "e2e-validator",
		natsclient.BindStream(wire.ChunksStream))
	require.NoError(t, err)

	data, _ := proto.Marshal(&v1.ScanRequest{
		ScanId: "scan-e2e",
		Type:   v1.ScanType_CURRENT_STATE,
	})
	_, err = cl.JS.Publish(wire.ScansRequestsSubject, data)
	require.NoError(t, err)

	var seen int32
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&seen) < 2 {
		msgs, err := cSub.Fetch(2, natsclient.MaxWait(500*time.Millisecond))
		if err == nil {
			for _, m := range msgs {
				var c v1.GitRowChunk
				require.NoError(t, proto.Unmarshal(m.Data, &c))
				atomic.AddInt32(&seen, 1)
				_ = m.Ack()
			}
		}
	}
	require.GreaterOrEqual(t, atomic.LoadInt32(&seen), int32(2))
}
