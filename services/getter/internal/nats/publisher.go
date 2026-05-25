package nats

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

type Publisher struct {
	js      nats.JetStreamContext
	ackWait time.Duration
}

func NewPublisher(js nats.JetStreamContext, ackWaitSeconds int) *Publisher {
	return &Publisher{js: js, ackWait: time.Duration(ackWaitSeconds) * time.Second}
}

func (p *Publisher) PublishChunk(ctx context.Context, c *v1.GitRowChunk) error {
	data, err := proto.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal chunk: %w", err)
	}
	// ChunkId is the JetStream dedup key: a retry of the same chunk (after
	// network blip or backoff) lands as the same message, not a duplicate.
	return p.publishWithRetry(ctx, wire.ChunksSubject(c.ScanId), data, c.ChunkId)
}

func (p *Publisher) PublishStatus(ctx context.Context, ev *v1.StatusEvent) error {
	data, err := proto.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	// Status events are dedup'd by (scan_id, state, timestamp) — a retry of
	// the same transition doesn't double-report.
	msgID := fmt.Sprintf("%s:%s:%d", ev.ScanId, ev.State.String(), ev.Timestamp)
	return p.publishWithRetry(ctx, wire.StatusSubject(ev.ScanId), data, msgID)
}

func (p *Publisher) publishWithRetry(ctx context.Context, subject string, data []byte, msgID string) error {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		opts := []nats.PubOpt{nats.AckWait(p.ackWait)}
		if msgID != "" {
			opts = append(opts, nats.MsgId(msgID))
		}
		_, err := p.js.Publish(subject, data, opts...)
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff[i]):
		}
	}
	return fmt.Errorf("publish %s after retries: %w", subject, lastErr)
}
