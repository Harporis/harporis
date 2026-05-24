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

func SubscribeRequests(ctx context.Context, js nats.JetStreamContext, opts RequestSubscribeOptions, h RequestHandler) (*nats.Subscription, error) {
	ackWait := time.Duration(opts.AckWaitSeconds) * time.Second

	return js.QueueSubscribe(wire.ScansRequestsSubject, wire.GetterPoolQueueGroup, func(msg *nats.Msg) {
		var req v1.ScanRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			slog.Error("unmarshal ScanRequest", "err", err)
			_ = msg.Nak()
			return
		}
		hctx, cancel := context.WithTimeout(ctx, ackWait-time.Second)
		defer cancel()
		if err := h(hctx, &req); err != nil {
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
