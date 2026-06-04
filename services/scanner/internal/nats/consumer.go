// Package nats holds the scanner's JetStream publisher and durable
// pull-consumer wiring. The consumer fetch/heartbeat/recover loop lives
// in kit/nats/pullconsumer so scanner and writer share the same battle-
// tested implementation; this file just owns the scanner-specific bits
// (PullSubscribe config, metric mapping, ChunkProcessingSeconds wrap).
package nats

import (
	"context"
	"fmt"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/pullconsumer"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/scanner/internal/metrics"
)

// ChunkHandler is invoked once per delivered GitRowChunk. Returning an
// error causes the consumer to Nak for redelivery (up to MaxDeliver);
// returning nil causes the consumer to Ack.
type ChunkHandler func(ctx context.Context, c *v1.GitRowChunk) error

// ConsumerOptions configures the chunks consumer.
type ConsumerOptions struct {
	BatchSize      int
	FetchMaxWait   time.Duration
	AckWaitSeconds int
	MaxDeliver     int
	MaxAckPending  int
}

// ChunksConsumer subscribes to harporis.chunks.> via a durable pull
// consumer shared across replicas. WorkQueuePolicy on HARPORIS_CHUNKS
// guarantees each message is delivered to exactly one consumer instance.
type ChunksConsumer struct {
	sub  *natsclient.Subscription
	opts ConsumerOptions
}

// NewChunksConsumer creates the durable pull subscription.
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

// Run blocks until ctx is cancelled. It delegates to kit/nats/pullconsumer.Run
// for the shared fetch/heartbeat/recover loop and wraps the user handler
// with the scanner-specific ChunkProcessingSeconds histogram observation.
func (c *ChunksConsumer) Run(ctx context.Context, h ChunkHandler) error {
	return pullconsumer.Run[*v1.GitRowChunk](
		ctx,
		c.sub,
		pullconsumer.Options{
			ServiceName:    "scanner",
			BatchSize:      c.opts.BatchSize,
			FetchMaxWait:   c.opts.FetchMaxWait,
			AckWaitSeconds: c.opts.AckWaitSeconds,
			MaxDeliver:     c.opts.MaxDeliver,
		},
		chunkLifecycle{},
		chunkMetrics{},
		func(ctx context.Context, chunk *v1.GitRowChunk) error {
			start := time.Now()
			err := h(ctx, chunk)
			metrics.ChunkProcessingSeconds.WithLabelValues(chunk.Kind.String()).Observe(time.Since(start).Seconds())
			return err
		},
	)
}

// chunkLifecycle plugs *v1.GitRowChunk into pullconsumer.Lifecycle.
type chunkLifecycle struct{}

func (chunkLifecycle) Unmarshal(data []byte) (*v1.GitRowChunk, error) {
	var c v1.GitRowChunk
	if err := proto.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (chunkLifecycle) LogFields(c *v1.GitRowChunk) []any {
	return []any{"scan_id", c.ScanId, "chunk_id", c.ChunkId}
}

// chunkMetrics maps pullconsumer.Metrics onto scanner Prometheus counters.
// Names mirror the pre-extraction labels so dashboards keep working.
type chunkMetrics struct{}

func (chunkMetrics) OnConsumed()           { metrics.ChunksConsumed.Inc() }
func (chunkMetrics) OnUnmarshalError()     { metrics.ChunksDropped.WithLabelValues("unmarshal_error").Inc() }
func (chunkMetrics) OnMaxDeliverExceeded() { metrics.ChunksDropped.WithLabelValues("max_deliver_exceeded").Inc() }
func (chunkMetrics) OnHandlerPanic()       { metrics.ChunksDropped.WithLabelValues("handler_panic").Inc() }
func (chunkMetrics) OnAckError()           { metrics.NATSPublishErrors.WithLabelValues("ack").Inc() }
func (chunkMetrics) OnNakError()           { metrics.NATSPublishErrors.WithLabelValues("nak").Inc() }
