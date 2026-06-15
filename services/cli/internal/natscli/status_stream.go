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

// watchConsumerInactiveThreshold bounds how long an ephemeral watch
// consumer lingers server-side after the CLI stops fetching, so a killed
// CLI doesn't leak consumers. Comfortably above the per-fetch interval.
const watchConsumerInactiveThreshold = 30 * time.Second

// SubscribeStatus returns a pull subscription to one scan's status
// subject plus a cleanup func that unsubscribes and deletes the
// ephemeral consumer. The consumer is bounded by InactiveThreshold so
// it disappears server-side even if the CLI is killed mid-watch.
func (c *Client) SubscribeStatus(scanID string) (*natsclient.Subscription, func(), error) {
	consumer := "cli-watch-" + SanitizeConsumerName(scanID)
	sub, err := c.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.InactiveThreshold(watchConsumerInactiveThreshold))
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe status: %w", err)
	}
	cleanup := func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}
	return sub, cleanup, nil
}

// SubscribeStatusAll returns a pull subscription over EVERY scan's status
// subject (wildcard) plus a cleanup func. DeliverNew so it tails only
// events arriving after subscription; callers seed historical state
// separately via ListHistory. InactiveThreshold reaps the ephemeral
// consumer server-side if the CLI is killed.
func (c *Client) SubscribeStatusAll() (*natsclient.Subscription, func(), error) {
	consumer := fmt.Sprintf("cli-watch-all-%d", time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe(wire.StatusWildcardSubject, consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverNew(),
		natsclient.InactiveThreshold(watchConsumerInactiveThreshold))
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe status all: %w", err)
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
