// StatusConsumer subscribes the writer to harporis.status.> and
// invokes the supplied callback for every terminal ScanState event
// (COMPLETED / PARTIAL / FAILED / CANCELLED). The writer uses this
// to drive Sink.Finalize so streaming sinks (Parquet) can write
// their footer + atomically rename onto the final path.
//
// Consumer shape: ephemeral, per-replica, DeliverNew. Every writer
// replica sees every terminal event regardless of WorkQueue
// distribution on the FINDINGS stream — finalization is a per-replica
// operation (each replica only knows about scans it Wrote to).
// Non-durable so a crashed replica doesn't leave consumers behind.

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

// TerminalHandler is invoked for every terminal-state StatusEvent the
// writer observes. Implementations are expected to be quick — heavy
// work blocks the next fetch.
type TerminalHandler func(ctx context.Context, scanID string, state v1.ScanState) error

// StatusConsumer wraps the ephemeral subscription.
type StatusConsumer struct {
	sub *natsclient.Subscription
}

// NewStatusConsumer creates a NEW (ephemeral) pull subscription on the
// HARPORIS_STATUS stream. clientID disambiguates replicas; pass
// something stable per-replica (e.g. hostname).
func NewStatusConsumer(js natsclient.JetStreamContext, clientID string) (*StatusConsumer, error) {
	sub, err := js.PullSubscribe(
		wire.StatusWildcardSubject,
		"", // empty durable = ephemeral
		natsclient.BindStream(wire.StatusStream),
		natsclient.ManualAck(),
		natsclient.AckWait(30*time.Second),
		natsclient.DeliverNew(),
		natsclient.AckExplicit(),
	)
	if err != nil {
		return nil, fmt.Errorf("status pull subscribe: %w", err)
	}
	return &StatusConsumer{sub: sub}, nil
}

// Drain initiates a graceful shutdown.
func (c *StatusConsumer) Drain() error { return c.sub.Drain() }

// Run blocks until ctx is cancelled, invoking onTerminal for every
// terminal-state event observed. Non-terminal states (PENDING /
// RUNNING) are silently Ack'd without touching the handler.
func (c *StatusConsumer) Run(ctx context.Context, onTerminal TerminalHandler) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		msgs, err := c.sub.Fetch(16, natsclient.MaxWait(5*time.Second))
		if err != nil {
			if err == natsclient.ErrTimeout {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("status fetch error", "err", err)
			continue
		}
		for _, msg := range msgs {
			var evt v1.StatusEvent
			if perr := proto.Unmarshal(msg.Data, &evt); perr != nil {
				slog.Warn("status unmarshal", "err", perr)
				_ = msg.Ack()
				continue
			}
			if !isTerminal(evt.State) {
				_ = msg.Ack()
				continue
			}
			if err := onTerminal(ctx, evt.ScanId, evt.State); err != nil {
				slog.Warn("terminal handler error", "scan_id", evt.ScanId, "state", evt.State.String(), "err", err)
				// Ack anyway — re-running Finalize on the same scan_id
				// is idempotent; the failure is logged and will surface
				// in writer_sink_errors_total if the underlying sink
				// reported the problem itself.
			}
			_ = msg.Ack()
		}
	}
}

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED,
		v1.ScanState_PARTIAL,
		v1.ScanState_FAILED,
		v1.ScanState_CANCELLED:
		return true
	}
	return false
}
