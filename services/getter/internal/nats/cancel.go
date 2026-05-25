package nats

import (
	"context"
	"log/slog"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

type CancelHandler func(ctx context.Context, req *v1.CancelScanRequest)

func SubscribeCancel(ctx context.Context, nc *nats.Conn, h CancelHandler) (*nats.Subscription, error) {
	return nc.Subscribe(wire.ScansCancelSubject, func(msg *nats.Msg) {
		var req v1.CancelScanRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			slog.Error("unmarshal cancel", "err", err)
			return
		}
		h(ctx, &req)
	})
}
