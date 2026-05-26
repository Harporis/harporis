package natscli

import (
	"errors"
	"fmt"
	"sort"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// ListHistory walks the status stream and returns the latest status
// event per scan id, newest-first. `maxScans` caps the returned slice
// (0 = no cap). `wait` is a per-fetch deadline; it does not bound the
// total time.
func (c *Client) ListHistory(maxScans int, wait time.Duration) ([]*v1.StatusEvent, error) {
	consumer := fmt.Sprintf("cli-history-%d", time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe("harporis.status.>", consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverAll())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	latest := map[string]*v1.StatusEvent{}
	for {
		msgs, err := sub.Fetch(64, natsclient.MaxWait(wait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				break
			}
			return nil, err
		}
		for _, m := range msgs {
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err != nil {
				continue
			}
			prev, ok := latest[ev.ScanId]
			if !ok || ev.Timestamp >= prev.Timestamp {
				latest[ev.ScanId] = &ev
			}
		}
		if len(msgs) < 64 {
			break
		}
	}
	out := make([]*v1.StatusEvent, 0, len(latest))
	for _, ev := range latest {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	if maxScans > 0 && len(out) > maxScans {
		out = out[:maxScans]
	}
	return out, nil
}

// ShowHistory returns every status event for a single scan, oldest-first.
func (c *Client) ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error) {
	consumer := fmt.Sprintf("cli-history-show-%s-%d", SanitizeConsumerName(scanID), time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverAll())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	var out []*v1.StatusEvent
	for {
		msgs, err := sub.Fetch(64, natsclient.MaxWait(wait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				break
			}
			return nil, err
		}
		for _, m := range msgs {
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err == nil {
				out = append(out, &ev)
			}
		}
		if len(msgs) < 64 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out, nil
}
