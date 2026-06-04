// Package pullconsumer is the shared JetStream pull-consumer loop used
// by scanner and writer. It owns the parts that are easy to get subtly
// wrong — exponential backoff on fetch, exit-clean on Drain, max-deliver
// drop semantics, heartbeat-with-recover wrapping the user handler — so
// the next service inheriting this gets all the bugfixes that landed
// during v0.1 code review for free.
//
// The package is generic over the message type T. Callers provide a
// Lifecycle that knows how to Unmarshal raw bytes into T and pull out
// the identity fields used for logging. Service-specific metric
// counters are injected via the Metrics interface so we don't pull
// prometheus into this package.
//
// Semantic invariants enforced here (and tested in scanner/writer's
// integration tests):
//   - Fetch loop exits cleanly on ctx.Done, ErrBadSubscription,
//     ErrConnectionClosed, ErrConnectionDraining — Drain is not an error.
//   - ErrTimeout from Fetch is benign (the natural idle path); does NOT
//     trigger backoff.
//   - Backoff is exponential: 100ms, 200ms, 400ms, ..., capped at 5s.
//     Resets to 0 on first successful Fetch or first ErrTimeout.
//   - Max-deliver: the Nth allowed attempt RUNS (NumDelivered counts
//     from 1, so we drop only when NumDelivered > MaxDeliver). The
//     pre-fix `>=` form silently lost the final attempt — don't reintroduce.
//   - Heartbeat goroutine is joined in EVERY exit path (success, error,
//     panic) via sync.Once + close(stop) + <-hbDone. Pre-fix the panic
//     path leaked a goroutine that could fire msg.InProgress() after Nak.
//   - On handler panic: log + bump metric + Nak. Worker stays alive.
package pullconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	natsclient "github.com/nats-io/nats.go"
)

// Handler is the user's per-message worker. Returning a non-nil error
// causes the consumer to Nak (immediate redelivery up to MaxDeliver);
// returning nil causes Ack.
type Handler[T any] func(ctx context.Context, msg T) error

// Lifecycle is the service-specific glue between the consumer loop and
// the proto wire format / log identity. Implementations are typically
// trivial: Unmarshal calls proto.Unmarshal; LogFields returns a slice
// of slog kv pairs for the message identity (scan_id, chunk_id, etc.).
type Lifecycle[T any] interface {
	Unmarshal(data []byte) (T, error)
	LogFields(T) []any
}

// Metrics is the service-specific delivery-error counter. Methods are
// invoked exactly once per condition; implementations should bump
// per-service Prometheus counters.
//
// The naming intentionally avoids leaking scanner-vs-writer vocabulary
// so the interface is reusable.
type Metrics interface {
	OnConsumed()
	OnUnmarshalError()
	OnMaxDeliverExceeded()
	OnHandlerPanic()
	OnAckError()
	OnNakError()
}

// Options configures the per-message loop. ServiceName flavors warn
// logs; BatchSize/FetchMaxWait/AckWait/MaxDeliver mirror the underlying
// JetStream consumer config so the heartbeat cadence and drop semantics
// stay consistent with what was declared at PullSubscribe time.
type Options struct {
	ServiceName    string // log prefix, e.g. "scanner" or "writer"
	BatchSize      int
	FetchMaxWait   time.Duration
	AckWaitSeconds int
	MaxDeliver     int
}

// Run blocks until ctx is cancelled. It pulls batches from sub and
// dispatches each message to handler. Returns nil on clean shutdown
// (ctx.Done, Drain, ConnectionClosed, ConnectionDraining).
func Run[T any](
	ctx context.Context,
	sub *natsclient.Subscription,
	opts Options,
	lc Lifecycle[T],
	m Metrics,
	handler Handler[T],
) error {
	// Heartbeat fires at AckWait/3 so a slow handler refreshes the deadline
	// twice before JetStream considers the delivery lost. 200ms floor
	// keeps the cadence sane even when AckWait is set absurdly small
	// (test stacks set 1s; 1s/3 ≈ 333ms which is fine, but a 100ms
	// AckWait would otherwise yield a 33ms ticker).
	heartbeat := time.Duration(opts.AckWaitSeconds) * time.Second / 3
	if heartbeat < 200*time.Millisecond {
		heartbeat = 200 * time.Millisecond
	}
	var backoff time.Duration
	const maxBackoff = 5 * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		msgs, err := sub.Fetch(opts.BatchSize, natsclient.MaxWait(opts.FetchMaxWait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				backoff = 0
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, natsclient.ErrBadSubscription) ||
				errors.Is(err, natsclient.ErrConnectionClosed) ||
				errors.Is(err, natsclient.ErrConnectionDraining) {
				return nil
			}
			slog.Warn(opts.ServiceName+" fetch", "err", err, "backoff_ms", backoff.Milliseconds())
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
			handleOne(ctx, msg, opts, lc, m, handler, heartbeat)
		}
	}
}

func handleOne[T any](
	ctx context.Context,
	msg *natsclient.Msg,
	opts Options,
	lc Lifecycle[T],
	m Metrics,
	handler Handler[T],
	heartbeat time.Duration,
) {
	payload, err := lc.Unmarshal(msg.Data)
	if err != nil {
		slog.Error("unmarshal "+opts.ServiceName+" message", "err", err)
		m.OnUnmarshalError()
		if ackErr := msg.Ack(); ackErr != nil {
			slog.Warn("ack failed for poison message", "err", ackErr)
			m.OnAckError()
		}
		return
	}
	m.OnConsumed()

	if opts.MaxDeliver > 0 {
		if md, mdErr := msg.Metadata(); mdErr == nil && md.NumDelivered > uint64(opts.MaxDeliver) {
			fields := append(lc.LogFields(payload), "delivered", md.NumDelivered, "max_deliver", opts.MaxDeliver)
			slog.Error(opts.ServiceName+" dropped after max deliveries", fields...)
			m.OnMaxDeliverExceeded()
			if ackErr := msg.Ack(); ackErr != nil {
				slog.Warn("ack failed for max-deliver-exceeded", append(lc.LogFields(payload), "err", ackErr)...)
				m.OnAckError()
			}
			return
		}
	}

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
				slog.Error(opts.ServiceName+" handler panic", append(lc.LogFields(payload), "panic", r)...)
				m.OnHandlerPanic()
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

		handlerErr = handler(hctx, payload)
	}()

	if handlerErr != nil {
		slog.Error(opts.ServiceName+" handler", append(lc.LogFields(payload), "err", handlerErr)...)
		if nakErr := msg.Nak(); nakErr != nil {
			slog.Warn("nak failed", append(lc.LogFields(payload), "err", nakErr)...)
			m.OnNakError()
		}
		return
	}
	if ackErr := msg.Ack(); ackErr != nil {
		slog.Warn("ack failed", append(lc.LogFields(payload), "err", ackErr)...)
		m.OnAckError()
	}
}
