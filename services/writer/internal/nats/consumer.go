// Package nats holds the writer's JetStream consumer for HARPORIS_FINDINGS.
// Mirrors the scanner's chunks consumer in shape (durable pull + heartbeat
// + backoff + recover) so behaviour is uniform across services.
package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
)

// FindingHandler is invoked once per delivered Finding. Returning an error
// causes the consumer to Nak for redelivery (up to MaxDeliver); returning
// nil causes the consumer to Ack.
type FindingHandler func(ctx context.Context, f *v1.Finding) error

// ConsumerOptions configures the findings consumer.
type ConsumerOptions struct {
	BatchSize      int
	FetchMaxWait   time.Duration
	AckWaitSeconds int
	MaxDeliver     int
	MaxAckPending  int
}

// FindingsConsumer subscribes to harporis.findings.> via a durable pull
// consumer shared across writer replicas. WorkQueuePolicy on
// HARPORIS_FINDINGS gives exactly-one-replica delivery per message.
type FindingsConsumer struct {
	sub  *natsclient.Subscription
	opts ConsumerOptions
}

// NewFindingsConsumer creates the durable pull subscription. Must be called
// once per process; concurrent replicas sharing wire.WriterDurableConsumer
// fan out automatically.
func NewFindingsConsumer(js natsclient.JetStreamContext, opts ConsumerOptions) (*FindingsConsumer, error) {
	ackWait := time.Duration(opts.AckWaitSeconds) * time.Second
	sub, err := js.PullSubscribe(
		wire.FindingsWildcardSubject,
		wire.WriterDurableConsumer,
		natsclient.BindStream(wire.FindingsStream),
		natsclient.ManualAck(),
		natsclient.AckWait(ackWait),
		natsclient.MaxDeliver(opts.MaxDeliver),
		natsclient.MaxAckPending(opts.MaxAckPending),
	)
	if err != nil {
		return nil, fmt.Errorf("pull subscribe: %w", err)
	}
	return &FindingsConsumer{sub: sub, opts: opts}, nil
}

// Drain initiates a graceful shutdown of the subscription.
func (c *FindingsConsumer) Drain() error { return c.sub.Drain() }

// Run blocks until ctx is cancelled. It pulls batches and invokes h for
// each finding. Slow handlers stay alive via msg.InProgress() heartbeats.
// Handler errors cause Nak; success causes Ack.
func (c *FindingsConsumer) Run(ctx context.Context, h FindingHandler) error {
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
				backoff = 0
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("writer fetch", "err", err, "backoff_ms", backoff.Milliseconds())
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
		backoff = 0
		for _, msg := range msgs {
			c.handleOne(ctx, msg, h, heartbeat)
		}
	}
}

func (c *FindingsConsumer) handleOne(ctx context.Context, msg *natsclient.Msg, h FindingHandler, heartbeat time.Duration) {
	var finding v1.Finding
	if err := proto.Unmarshal(msg.Data, &finding); err != nil {
		slog.Error("unmarshal Finding", "err", err)
		metrics.NATSDeliveryErrors.WithLabelValues("unmarshal").Inc()
		if ackErr := msg.Ack(); ackErr != nil {
			slog.Warn("ack failed for poison finding", "err", ackErr)
			metrics.NATSDeliveryErrors.WithLabelValues("ack").Inc()
		}
		return
	}
	metrics.FindingsConsumed.Inc()

	// Terminal-failure drop: after MaxDeliver retries, Ack + log + count
	// so the stream unblocks.
	if c.opts.MaxDeliver > 0 {
		if md, mdErr := msg.Metadata(); mdErr == nil && md.NumDelivered >= uint64(c.opts.MaxDeliver) {
			slog.Error("finding dropped after max deliveries",
				"scan_id", finding.ScanId,
				"finding_id", finding.FindingId,
				"delivered", md.NumDelivered,
				"max_deliver", c.opts.MaxDeliver,
			)
			metrics.NATSDeliveryErrors.WithLabelValues("max_deliver_exceeded").Inc()
			if ackErr := msg.Ack(); ackErr != nil {
				slog.Warn("ack failed for max-deliver-exceeded finding",
					"scan_id", finding.ScanId,
					"finding_id", finding.FindingId,
					"err", ackErr,
				)
				metrics.NATSDeliveryErrors.WithLabelValues("ack").Inc()
			}
			return
		}
	}

	// Recovery shim: a sink panic mustn't kill the worker. Log identity
	// (not bytes), bump metric, fall through to Nak.
	var handlerErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("writer handler panic",
					"scan_id", finding.ScanId,
					"finding_id", finding.FindingId,
					"panic", r,
				)
				metrics.NATSDeliveryErrors.WithLabelValues("handler_panic").Inc()
				handlerErr = fmt.Errorf("handler panic: %v", r)
			}
		}()

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

		handlerErr = h(hctx, &finding)
		close(stop)
		<-hbDone
	}()

	if handlerErr != nil {
		slog.Error("writer handler", "scan_id", finding.ScanId, "finding_id", finding.FindingId, "err", handlerErr)
		if nakErr := msg.Nak(); nakErr != nil {
			slog.Warn("nak failed",
				"scan_id", finding.ScanId,
				"finding_id", finding.FindingId,
				"err", nakErr,
			)
			metrics.NATSDeliveryErrors.WithLabelValues("nak").Inc()
		}
		return
	}
	if ackErr := msg.Ack(); ackErr != nil {
		slog.Warn("ack failed",
			"scan_id", finding.ScanId,
			"finding_id", finding.FindingId,
			"err", ackErr,
		)
		metrics.NATSDeliveryErrors.WithLabelValues("ack").Inc()
	}
}
