package wire

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// Subject and stream constants shared across all Harporis services.
const (
	ScansRequestsSubject = "harporis.scans.requests"
	ScansCancelSubject   = "harporis.scans.cancel"

	RequestsStream = "HARPORIS_REQUESTS"
	ChunksStream   = "HARPORIS_CHUNKS"
	StatusStream   = "HARPORIS_STATUS"
	FindingsStream = "HARPORIS_FINDINGS"

	GetterPoolQueueGroup    = "getter-pool"
	ValidatorPoolQueueGroup = "validator-pool"
	WriterPoolQueueGroup    = "writer-pool"
)

// Per-scan subject builders.
func ChunksSubject(scanID string) string   { return "harporis.chunks." + scanID }
func StatusSubject(scanID string) string   { return "harporis.status." + scanID }
func FindingsSubject(scanID string) string { return "harporis.findings." + scanID }

// DialConfig is a service-agnostic NATS connection config.
type DialConfig struct {
	URL        string
	ClientName string // e.g. "harporis-getter"
}

// Client wraps a NATS connection + JetStream context.
type Client struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

// Dial connects to NATS with reconnect-forever semantics and returns a Client.
func Dial(cfg DialConfig) (*Client, error) {
	nc, err := nats.Connect(cfg.URL,
		nats.Name(cfg.ClientName),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("jetstream context: %w", err)
	}
	return &Client{NC: nc, JS: js}, nil
}

func (c *Client) Close() {
	if c.NC != nil {
		c.NC.Close()
	}
}

// EnsureStreams idempotently creates the 4 Harporis streams. Safe to call from
// any service at startup; concurrent calls from multiple processes are fine
// because AddStream is idempotent on identical config and we tolerate
// "name already in use" errors.
func EnsureStreams(js nats.JetStreamContext) error {
	configs := []*nats.StreamConfig{
		{Name: RequestsStream, Subjects: []string{"harporis.scans.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy},
		{Name: ChunksStream, Subjects: []string{"harporis.chunks.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy},
		{Name: StatusStream, Subjects: []string{"harporis.status.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy},
		{Name: FindingsStream, Subjects: []string{"harporis.findings.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy},
	}
	for _, c := range configs {
		_, err := js.AddStream(c)
		if err == nil {
			continue
		}
		if errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
			continue
		}
		// Some servers return the error as a JS API error (string) rather than
		// the typed sentinel; fall back to checking existence.
		if info, ierr := js.StreamInfo(c.Name); ierr == nil && info != nil {
			continue
		}
		return fmt.Errorf("ensure stream %s: %w", c.Name, err)
	}
	return nil
}
