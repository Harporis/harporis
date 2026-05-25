package nats

import (
	"context"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/wire"
)

type RequestHandler func(ctx context.Context, req *v1.ScanRequest) error

type RequestSubscribeOptions struct {
	AckWaitSeconds   int
	MaxInFlightScans int
}

// heartbeatInterval returns how often we send msg.InProgress() while a
// handler is running. We refresh at ack-wait/3 to absorb scheduling jitter
// (with a 200ms floor for very short ack-waits in tests).
func heartbeatInterval(ackWait time.Duration) time.Duration {
	d := ackWait / 3
	if d < 200*time.Millisecond {
		return 200 * time.Millisecond
	}
	return d
}

func SubscribeRequests(ctx context.Context, js nats.JetStreamContext, opts RequestSubscribeOptions, h RequestHandler) (*nats.Subscription, error) {
	ackWait := time.Duration(opts.AckWaitSeconds) * time.Second
	heartbeat := heartbeatInterval(ackWait)

	return js.QueueSubscribe(wire.ScansRequestsSubject, wire.GetterPoolQueueGroup, func(msg *nats.Msg) {
		var req v1.ScanRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			slog.Error("unmarshal ScanRequest", "err", err)
			_ = msg.Nak()
			return
		}

		// Handler context is *only* cancelled by the subscriber's outer ctx
		// (e.g. process shutdown). Real scans take much longer than NATS
		// ack-wait — we keep the message alive via msg.InProgress() heartbeats
		// instead of bounding the handler with a deadline.
		hctx, cancel := context.WithCancel(ctx)
		defer cancel()

		stopHeartbeat := make(chan struct{})
		hbDone := make(chan struct{})
		go func() {
			defer close(hbDone)
			t := time.NewTicker(heartbeat)
			defer t.Stop()
			for {
				select {
				case <-stopHeartbeat:
					return
				case <-hctx.Done():
					return
				case <-t.C:
					if err := msg.InProgress(); err != nil {
						// Best-effort: log and keep going. If the connection
						// is gone, the handler will fail on its own publishes.
						slog.Warn("ack-wait heartbeat failed", "scan_id", req.ScanId, "err", err)
					}
				}
			}
		}()

		err := h(hctx, &req)
		close(stopHeartbeat)
		<-hbDone

		if err != nil {
			slog.Error("scan request handler", "scan_id", req.ScanId, "err", err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	},
		nats.Durable(wire.GetterPoolQueueGroup),
		nats.ManualAck(),
		nats.AckWait(ackWait),
		nats.MaxAckPending(opts.MaxInFlightScans),
	)
}
