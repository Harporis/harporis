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
	return p.publishWithRetry(ctx, wire.ChunksSubject(c.ScanId), data)
}

func (p *Publisher) PublishStatus(ctx context.Context, ev *v1.StatusEvent) error {
	data, err := proto.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	return p.publishWithRetry(ctx, wire.StatusSubject(ev.ScanId), data)
}

func (p *Publisher) publishWithRetry(ctx context.Context, subject string, data []byte) error {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, err := p.js.Publish(subject, data, nats.AckWait(p.ackWait))
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
