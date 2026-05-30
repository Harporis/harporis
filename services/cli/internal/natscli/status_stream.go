package natscli

import (
	"context"
	"errors"
	"fmt"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// statusFetchBatch is the per-fetch message batch size for the status
// stream. 8 is small enough that watch panels feel reactive and large
// enough to avoid round-trip-per-event chattiness.
const statusFetchBatch = 8

// SubscribeStatus returns a pull subscription to one scan's status
// subject plus a cleanup func that unsubscribes and deletes the
// ephemeral consumer. The consumer is bounded by InactiveThreshold so
// it disappears server-side even if the CLI is killed mid-watch.
func (c *Client) SubscribeStatus(scanID string) (*natsclient.Subscription, func(), error) {
	consumer := "cli-watch-" + SanitizeConsumerName(scanID)
	sub, err := c.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.InactiveThreshold(30*time.Second))
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe status: %w", err)
	}
	cleanup := func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}
	return sub, cleanup, nil
}

// FetchStatusEvents pulls up to statusFetchBatch StatusEvents from the
// subscription with the given per-fetch deadline. Returns a benign
// empty slice on per-fetch timeout (the caller decides whether to keep
// looping); a real error otherwise.
func FetchStatusEvents(sub *natsclient.Subscription, wait time.Duration) ([]*v1.StatusEvent, error) {
	msgs, err := sub.Fetch(statusFetchBatch, natsclient.MaxWait(wait))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]*v1.StatusEvent, 0, len(msgs))
	for _, m := range msgs {
		var ev v1.StatusEvent
		if err := proto.Unmarshal(m.Data, &ev); err != nil {
			_ = m.Ack()
			continue
		}
		out = append(out, &ev)
		_ = m.Ack()
	}
	return out, nil
}
