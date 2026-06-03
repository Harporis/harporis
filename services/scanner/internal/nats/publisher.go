package nats

import (
	"context"
	"fmt"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/scanner/internal/metrics"
)

// Publisher emits Findings (deduped via JetStream MsgId) and minimal
// StatusEvent updates (carrying only the secrets_found metric for the
// scanner's per-scan counter).
type Publisher struct {
	js          natsclient.JetStreamContext
	publishWait time.Duration
}

func NewPublisher(js natsclient.JetStreamContext, publishWait time.Duration) *Publisher {
	return &Publisher{js: js, publishWait: publishWait}
}

// PublishFinding emits one Finding to harporis.findings.<scan_id> with a
// deterministic MsgId. JetStream's Duplicates window on the stream drops
// repeats of the same (scan|chunk|rule|line|offset) tuple within the window.
func (p *Publisher) PublishFinding(ctx context.Context, f *v1.Finding) error {
	body, err := proto.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal finding: %w", err)
	}
	msgID := FindingMsgID(f.ScanId, f.ChunkId, f.RuleId, f.LineNumber, f.ByteOffset)
	pubCtx, cancel := context.WithTimeout(ctx, p.publishWait)
	defer cancel()
	_, err = p.js.PublishMsg(&natsclient.Msg{
		Subject: wire.FindingsSubject(f.ScanId),
		Data:    body,
		Header:  natsclient.Header{natsclient.MsgIdHdr: []string{msgID}},
	}, natsclient.Context(pubCtx))
	if err != nil {
		metrics.NATSPublishErrors.WithLabelValues("harporis.findings").Inc()
		return fmt.Errorf("publish finding: %w", err)
	}
	return nil
}

// PublishStatusSecretsFound emits a StatusEvent with only metrics.secrets_found
// populated. The status emitter calls this on a tick.
func (p *Publisher) PublishStatusSecretsFound(ctx context.Context, scanID string, count int64) error {
	ev := &v1.StatusEvent{
		ScanId:    scanID,
		State:     v1.ScanState_RUNNING,
		Timestamp: time.Now().Unix(),
		Metrics:   &v1.ScanMetrics{SecretsFound: count},
	}
	body, err := proto.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	pubCtx, cancel := context.WithTimeout(ctx, p.publishWait)
	defer cancel()
	if _, err := p.js.Publish(wire.StatusSubject(scanID), body, natsclient.Context(pubCtx)); err != nil {
		metrics.NATSPublishErrors.WithLabelValues("harporis.status").Inc()
		return fmt.Errorf("publish status: %w", err)
	}
	return nil
}
