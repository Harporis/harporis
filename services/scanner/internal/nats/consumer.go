package nats

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// ChunkHandler is invoked once per delivered GitRowChunk. Returning an
// error causes the consumer to Nak for redelivery (up to MaxDeliver);
// returning nil causes the consumer to Ack.
type ChunkHandler func(ctx context.Context, c *v1.GitRowChunk) error

// ConsumerOptions configures the chunks consumer.
type ConsumerOptions struct {
	BatchSize      int           // JS Fetch batch
	FetchMaxWait   time.Duration // JS Fetch MaxWait
	AckWaitSeconds int           // JS consumer AckWait
	MaxDeliver     int           // JS consumer MaxDeliver — after this many tries, the message is acked + logged
	MaxAckPending  int           // JS consumer MaxAckPending — bounded in-flight per durable
}

// ChunksConsumer subscribes to harporis.chunks.> via a durable pull consumer
// shared across replicas. WorkQueuePolicy on HARPORIS_CHUNKS guarantees
// each message is delivered to exactly one consumer instance.
type ChunksConsumer struct {
	sub  *natsclient.Subscription
	opts ConsumerOptions
}

// NewChunksConsumer creates the durable pull subscription. Must be called
// once per process; concurrent replicas sharing wire.ScannerDurableConsumer
// fan out automatically.
func NewChunksConsumer(js natsclient.JetStreamContext, opts ConsumerOptions) (*ChunksConsumer, error) {
	ackWait := time.Duration(opts.AckWaitSeconds) * time.Second
	sub, err := js.PullSubscribe(
		wire.ChunksWildcardSubject,
		wire.ScannerDurableConsumer,
		natsclient.BindStream(wire.ChunksStream),
		natsclient.ManualAck(),
		natsclient.AckWait(ackWait),
		natsclient.MaxDeliver(opts.MaxDeliver),
		natsclient.MaxAckPending(opts.MaxAckPending),
	)
	if err != nil {
		return nil, fmt.Errorf("pull subscribe: %w", err)
	}
	return &ChunksConsumer{sub: sub, opts: opts}, nil
}

// Drain initiates a graceful shutdown of the subscription.
func (c *ChunksConsumer) Drain() error { return c.sub.Drain() }

// Run blocks until ctx is cancelled. It pulls batches and invokes h for each
// chunk. Slow handlers are kept alive with msg.InProgress() heartbeats so
// they don't trigger redelivery. Handler errors cause Nak (immediate
// redelivery up to MaxDeliver); successful handlers cause Ack.
func (c *ChunksConsumer) Run(ctx context.Context, h ChunkHandler) error {
	heartbeat := time.Duration(c.opts.AckWaitSeconds) * time.Second / 3
	if heartbeat < 200*time.Millisecond {
		heartbeat = 200 * time.Millisecond
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		msgs, err := c.sub.Fetch(c.opts.BatchSize, natsclient.MaxWait(c.opts.FetchMaxWait))
		if err != nil {
			if err == natsclient.ErrTimeout {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("scanner fetch", "err", err)
			continue
		}
		for _, msg := range msgs {
			c.handleOne(ctx, msg, h, heartbeat)
		}
	}
}

func (c *ChunksConsumer) handleOne(ctx context.Context, msg *natsclient.Msg, h ChunkHandler, heartbeat time.Duration) {
	var chunk v1.GitRowChunk
	if err := proto.Unmarshal(msg.Data, &chunk); err != nil {
		slog.Error("unmarshal GitRowChunk", "err", err)
		_ = msg.Ack() // drop poison message; not recoverable
		return
	}

	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(heartbeat)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-hctx.Done():
				return
			case <-t.C:
				_ = msg.InProgress()
			}
		}
	}()

	err := h(hctx, &chunk)
	close(stop)
	<-hbDone

	if err != nil {
		slog.Error("scanner handler", "scan_id", chunk.ScanId, "chunk_id", chunk.ChunkId, "err", err)
		_ = msg.Nak()
		return
	}
	_ = msg.Ack()
}
