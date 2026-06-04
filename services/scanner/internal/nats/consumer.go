package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/scanner/internal/metrics"
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
	var backoff time.Duration
	const maxBackoff = 5 * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		msgs, err := c.sub.Fetch(c.opts.BatchSize, natsclient.MaxWait(c.opts.FetchMaxWait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				backoff = 0 // reset on benign idle timeout
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			// Drain or connection-closed during graceful shutdown is
			// expected; exit cleanly without warn spam.
			if errors.Is(err, natsclient.ErrBadSubscription) ||
				errors.Is(err, natsclient.ErrConnectionClosed) ||
				errors.Is(err, natsclient.ErrConnectionDraining) {
				return nil
			}
			slog.Warn("scanner fetch", "err", err, "backoff_ms", backoff.Milliseconds())
			// Exponential backoff: 100ms, 200ms, 400ms, ..., capped at 5s.
			if backoff == 0 {
				backoff = 100 * time.Millisecond
			} else {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0 // reset on successful fetch
		for _, msg := range msgs {
			c.handleOne(ctx, msg, h, heartbeat)
		}
	}
}

func (c *ChunksConsumer) handleOne(ctx context.Context, msg *natsclient.Msg, h ChunkHandler, heartbeat time.Duration) {
	var chunk v1.GitRowChunk
	if err := proto.Unmarshal(msg.Data, &chunk); err != nil {
		slog.Error("unmarshal GitRowChunk", "err", err)
		metrics.ChunksDropped.WithLabelValues("unmarshal_error").Inc()
		if ackErr := msg.Ack(); ackErr != nil { // drop poison message; not recoverable
			slog.Warn("ack failed for poison chunk", "err", ackErr)
			metrics.NATSPublishErrors.WithLabelValues("ack").Inc()
		}
		return
	}
	metrics.ChunksConsumed.Inc()

	// Terminal-failure drop: AFTER MaxDeliver retries (NumDelivered counts
	// from 1, so the Nth attempt has NumDelivered=N). We want to RUN the
	// final allowed attempt and only drop messages JetStream is about to
	// stop redelivering — i.e. NumDelivered > MaxDeliver. Pre-fix this used
	// `>=`, silently dropping the final attempt.
	if c.opts.MaxDeliver > 0 {
		if md, mdErr := msg.Metadata(); mdErr == nil && md.NumDelivered > uint64(c.opts.MaxDeliver) {
			slog.Error("chunk dropped after max deliveries",
				"scan_id", chunk.ScanId,
				"chunk_id", chunk.ChunkId,
				"delivered", md.NumDelivered,
				"max_deliver", c.opts.MaxDeliver,
			)
			metrics.ChunksDropped.WithLabelValues("max_deliver_exceeded").Inc()
			if ackErr := msg.Ack(); ackErr != nil {
				slog.Warn("ack failed for max-deliver-exceeded chunk",
					"scan_id", chunk.ScanId,
					"chunk_id", chunk.ChunkId,
					"err", ackErr,
				)
				metrics.NATSPublishErrors.WithLabelValues("ack").Inc()
			}
			return
		}
	}

	// Recovery shim: a panic in the handler must not kill the worker
	// goroutine. JetStream would otherwise redeliver to another replica
	// and crash that one too. Heartbeat goroutine is joined in ALL exit
	// paths (success, error, panic) — pre-fix the panic path skipped
	// `close(stop); <-hbDone`, leaking a heartbeat goroutine that could
	// fire `msg.InProgress()` after `msg.Nak()`.
	var handlerErr error
	func() {
		hctx, cancel := context.WithCancel(ctx)
		stop := make(chan struct{})
		hbDone := make(chan struct{})
		var stopOnce sync.Once
		joinHeartbeat := func() {
			stopOnce.Do(func() { close(stop) })
			<-hbDone
		}
		defer func() {
			cancel()
			joinHeartbeat()
			if r := recover(); r != nil {
				slog.Error("scanner handler panic",
					"scan_id", chunk.ScanId,
					"chunk_id", chunk.ChunkId,
					"panic", r,
				)
				metrics.ChunksDropped.WithLabelValues("handler_panic").Inc()
				handlerErr = fmt.Errorf("handler panic: %v", r)
			}
		}()

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

		start := time.Now()
		handlerErr = h(hctx, &chunk)
		metrics.ChunkProcessingSeconds.WithLabelValues(chunk.Kind.String()).Observe(time.Since(start).Seconds())
	}()

	if handlerErr != nil {
		slog.Error("scanner handler", "scan_id", chunk.ScanId, "chunk_id", chunk.ChunkId, "err", handlerErr)
		if nakErr := msg.Nak(); nakErr != nil {
			slog.Warn("nak failed",
				"scan_id", chunk.ScanId,
				"chunk_id", chunk.ChunkId,
				"err", nakErr,
			)
			metrics.NATSPublishErrors.WithLabelValues("nak").Inc()
		}
		return
	}
	if ackErr := msg.Ack(); ackErr != nil {
		slog.Warn("ack failed",
			"scan_id", chunk.ScanId,
			"chunk_id", chunk.ChunkId,
			"err", ackErr,
		)
		metrics.NATSPublishErrors.WithLabelValues("ack").Inc()
	}
}
