package nats

import (
	"context"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestPublisher_PublishesChunkAndStatus(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()

	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-pub-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	pub := NewPublisher(cl.JS, 5, "getter-test")

	chunkSub, err := cl.JS.PullSubscribe(wire.ChunksSubject("scan-1"), "test-cons-chunks", natsclient.BindStream(wire.ChunksStream))
	require.NoError(t, err)
	statusSub, err := cl.JS.PullSubscribe(wire.StatusSubject("scan-1"), "test-cons-status", natsclient.BindStream(wire.StatusStream))
	require.NoError(t, err)

	chunk := &v1.GitRowChunk{ScanId: "scan-1", Kind: v1.ChunkKind_BLOB, BlobSha: []byte("abc")}
	require.NoError(t, pub.PublishChunk(context.Background(), chunk))

	msgs, err := chunkSub.Fetch(1, natsclient.MaxWait(2*time.Second))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	var got v1.GitRowChunk
	require.NoError(t, proto.Unmarshal(msgs[0].Data, &got))
	require.Equal(t, []byte("abc"), got.BlobSha)
	require.NoError(t, msgs[0].Ack())

	require.NoError(t, pub.PublishStatus(context.Background(),
		&v1.StatusEvent{ScanId: "scan-1", State: v1.ScanState_COMPLETED}))
	smsgs, err := statusSub.Fetch(1, natsclient.MaxWait(2*time.Second))
	require.NoError(t, err)
	require.Len(t, smsgs, 1)
	var st v1.StatusEvent
	require.NoError(t, proto.Unmarshal(smsgs[0].Data, &st))
	require.Equal(t, v1.ScanState_COMPLETED, st.State)
}

func TestPublishStatusStampsSource(t *testing.T) {
	p := &Publisher{source: "getter-testhost"}
	ev := &v1.StatusEvent{ScanId: "s1", State: v1.ScanState_RUNNING}
	p.stampSource(ev)
	if ev.Source != "getter-testhost" {
		t.Fatalf("source = %q, want getter-testhost", ev.Source)
	}
}

// Publishing the same chunk twice with the same ChunkId must result in
// exactly one message in the stream — JetStream's per-message dedup
// guarantees exactly-once on retry storms.
func TestPublisher_DedupsByChunkID(t *testing.T) {
	url, stop := testutil.StartEmbeddedNATS(t)
	defer stop()

	cl, err := wire.Dial(wire.DialConfig{URL: url, ClientName: "getter-dedup-test"})
	require.NoError(t, err)
	defer cl.Close()
	require.NoError(t, wire.EnsureStreams(cl.JS))

	pub := NewPublisher(cl.JS, 5, "getter-test")
	chunk := &v1.GitRowChunk{
		ScanId:  "scan-dedup",
		ChunkId: "fixed-chunk-id-for-dedup",
		Kind:    v1.ChunkKind_BLOB,
		BlobSha: []byte("xyz"),
	}
	require.NoError(t, pub.PublishChunk(context.Background(), chunk))
	require.NoError(t, pub.PublishChunk(context.Background(), chunk)) // retry

	info, err := cl.JS.StreamInfo(wire.ChunksStream)
	require.NoError(t, err)
	require.EqualValues(t, 1, info.State.Msgs, "second publish with same ChunkId must be deduped")
}
