package wire

import (
	"errors"
	"fmt"
	"time"

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

// ScannerDurableConsumer is the durable consumer name shared by all
// scanner replicas. JetStream's WorkQueuePolicy on HARPORIS_CHUNKS plus
// a shared durable name gives us round-robin distribution across replicas
// without explicit queue-group plumbing.
const ScannerDurableConsumer = "scanner-pool"

// Wildcard subjects for cross-scan subscribers (history, audit, etc.).
const (
	ChunksWildcardSubject   = "harporis.chunks.>"
	StatusWildcardSubject   = "harporis.status.>"
	FindingsWildcardSubject = "harporis.findings.>"
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
		// RequestsStream captures ONLY the requests subject. CancelSubject is
		// intentionally not in the filter: cancel is a fire-and-forget broadcast
		// over core NATS, and a WorkQueuePolicy stream with no matching
		// subscriber would let cancels accumulate without bound.
		{Name: RequestsStream, Subjects: []string{ScansRequestsSubject}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy},
		{Name: ChunksStream, Subjects: []string{"harporis.chunks.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy},
		{Name: StatusStream, Subjects: []string{"harporis.status.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy},
		{Name: FindingsStream, Subjects: []string{"harporis.findings.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, Duplicates: 5 * time.Minute},
	}
	for _, c := range configs {
		if _, err := js.AddStream(c); err == nil {
			continue
		} else if !errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
			// Some servers return the error as a JS API error rather than the
			// typed sentinel. Fall back to checking existence — if the stream
			// does not exist at all, surface the original AddStream error.
			if _, ierr := js.StreamInfo(c.Name); ierr != nil {
				return fmt.Errorf("ensure stream %s: %w", c.Name, err)
			}
		}
		// Stream already exists. Check whether config drifted and update if so.
		info, err := js.StreamInfo(c.Name)
		if err != nil {
			return fmt.Errorf("stream info %s: %w", c.Name, err)
		}
		if streamConfigDrifted(info.Config, *c) {
			if _, err := js.UpdateStream(c); err != nil {
				return fmt.Errorf("update stream %s: %w", c.Name, err)
			}
		}
	}
	return nil
}

// streamConfigDrifted returns true if any field this package manages in
// EnsureStreams differs between have and want. We intentionally do NOT
// compare fields outside our control (e.g. storage backend changes an
// operator may have made deliberately) — only what wire.go declares.
func streamConfigDrifted(have, want nats.StreamConfig) bool {
	if have.Retention != want.Retention {
		return true
	}
	if have.Duplicates != want.Duplicates {
		return true
	}
	if !subjectsEqual(have.Subjects, want.Subjects) {
		return true
	}
	return false
}

func subjectsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			return false
		}
	}
	return true
}
