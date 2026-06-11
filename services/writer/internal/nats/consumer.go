// Package nats holds the writer's JetStream consumer for HARPORIS_FINDINGS.
// The shared fetch/heartbeat/recover loop lives in kit/nats/pullconsumer;
// this file owns the writer-specific PullSubscribe config + metric
// mapping so behaviour stays in sync with scanner without copy/paste.
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
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

// FindingHandler is invoked once per delivered Finding. Returning an
// error causes the consumer to Nak for redelivery (up to MaxDeliver);
// returning nil causes the consumer to Ack.
type FindingHandler func(ctx context.Context, f *v1.Finding) error

// ConsumerOptions configures the findings consumer.
type ConsumerOptions struct {
	BatchSize      int
	FetchMaxWait   time.Duration
	AckWaitSeconds int
	MaxDeliver     int
	MaxAckPending  int

	// ShardIndex / ShardCount control per-scan writer affinity. When
	// ShardCount<=1, the consumer binds the legacy `writer-pool` durable
	// to `harporis.findings.>` — every replica sees every Finding. When
	// ShardCount>1, the consumer binds `writer-pool-s<idx>` to
	// `harporis.findings.s<idx>.>` so each Finding for a given scan
	// lands on exactly one replica. The publisher hashes scan_id the
	// same way (see wire.FindingsSubject).
	ShardIndex int
	ShardCount int
}

// FindingsConsumer subscribes to harporis.findings.> via a durable pull
// consumer shared across writer replicas.
type FindingsConsumer struct {
	sub  *natsclient.Subscription
	opts ConsumerOptions
}

// DefaultInactiveThreshold is how long a writer durable consumer lingers
// without an active subscriber before JetStream tears it down. If the
// writer is scaled to 0 longer than this, the consumer is reclaimed so
// pending findings don't block the WorkQueuePolicy stream forever.
const DefaultInactiveThreshold = 24 * time.Hour

// NewFindingsConsumer creates the durable pull subscription. ShardIndex
// / ShardCount in opts select per-scan affinity; zero/one disables it.
func NewFindingsConsumer(js natsclient.JetStreamContext, opts ConsumerOptions) (*FindingsConsumer, error) {
	ackWait := time.Duration(opts.AckWaitSeconds) * time.Second
	filterSubject := wire.FindingsShardFilterSubject(opts.ShardIndex, opts.ShardCount)
	durable := wire.WriterDurableForShard(opts.ShardIndex, opts.ShardCount)
	sub, err := js.PullSubscribe(
		filterSubject,
		durable,
		natsclient.BindStream(wire.FindingsStream),
		natsclient.ManualAck(),
		natsclient.AckWait(ackWait),
		natsclient.MaxDeliver(opts.MaxDeliver),
		natsclient.MaxAckPending(opts.MaxAckPending),
		natsclient.InactiveThreshold(DefaultInactiveThreshold),
	)
	if err != nil {
		return nil, fmt.Errorf("pull subscribe: %w", err)
	}
	return &FindingsConsumer{sub: sub, opts: opts}, nil
}

// Drain initiates a graceful shutdown of the subscription.
func (c *FindingsConsumer) Drain() error { return c.sub.Drain() }

// Run blocks until ctx is cancelled, delegating the fetch/heartbeat/
// recover loop to kit/nats/pullconsumer.
func (c *FindingsConsumer) Run(ctx context.Context, h FindingHandler) error {
	return pullconsumer.Run[*v1.Finding](
		ctx,
		c.sub,
		pullconsumer.Options{
			ServiceName:    "writer",
			BatchSize:      c.opts.BatchSize,
			FetchMaxWait:   c.opts.FetchMaxWait,
			AckWaitSeconds: c.opts.AckWaitSeconds,
			MaxDeliver:     c.opts.MaxDeliver,
		},
		findingLifecycle{},
		findingMetrics{},
		func(ctx context.Context, f *v1.Finding) error { return h(ctx, f) },
	)
}

// findingLifecycle plugs *v1.Finding into pullconsumer.Lifecycle.
type findingLifecycle struct{}

func (findingLifecycle) Unmarshal(data []byte) (*v1.Finding, error) {
	var f v1.Finding
	if err := proto.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (findingLifecycle) LogFields(f *v1.Finding) []any {
	return []any{"scan_id", f.ScanId, "finding_id", f.FindingId}
}

// findingMetrics maps pullconsumer.Metrics onto writer Prometheus counters.
type findingMetrics struct{}

func (findingMetrics) OnConsumed()           { metrics.FindingsConsumed.Inc() }
func (findingMetrics) OnUnmarshalError()     { metrics.NATSDeliveryErrors.WithLabelValues("unmarshal").Inc() }
func (findingMetrics) OnMaxDeliverExceeded() { metrics.NATSDeliveryErrors.WithLabelValues("max_deliver_exceeded").Inc() }
func (findingMetrics) OnHandlerPanic()       { metrics.NATSDeliveryErrors.WithLabelValues("handler_panic").Inc() }
func (findingMetrics) OnAckError()           { metrics.NATSDeliveryErrors.WithLabelValues("ack").Inc() }
func (findingMetrics) OnNakError()           { metrics.NATSDeliveryErrors.WithLabelValues("nak").Inc() }
