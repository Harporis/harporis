// Package wire is the cross-service NATS contract for Harporis. All
// services dial NATS through wire.Dial, allowing the operator to plug
// in TLS, credentials, or tokens uniformly.
//
// Production deployments MUST set at least one of:
//   - DialConfig.TLSConfig (or RootCAs) for transport security
//   - DialConfig.CredsFile or Token for authentication
//
// The local dev stack (docker-compose.yml) leaves them zero — that is
// the only context where an unauthenticated, plaintext NATS connection
// is acceptable.
package wire

import (
	"crypto/tls"
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

// WriterDurableConsumer is the durable consumer name shared by all writer
// replicas — same pattern as ScannerDurableConsumer but bound to the
// HARPORIS_FINDINGS stream. Round-robin across replicas falls out of
// WorkQueuePolicy + shared durable name. The name matches ScannerDurableConsumer's
// "scanner-pool" shape ("<service>-pool", no -pull suffix) — both are
// pull consumers; the suffix carried no information and made dashboards
// inconsistent.
const WriterDurableConsumer = "writer-pool"

// Wildcard subjects for cross-scan subscribers (history, audit, etc.).
const (
	ChunksWildcardSubject   = "harporis.chunks.>"
	StatusWildcardSubject   = "harporis.status.>"
	FindingsWildcardSubject = "harporis.findings.>"
)

// ServiceGetter, ServiceScanner, ServiceWriter are the canonical names
// used as keys into MetricsPorts and elsewhere where a service is
// referenced by string (compose-exec target, k8s label, log field).
const (
	ServiceGetter  = "getter"
	ServiceScanner = "scanner"
	ServiceWriter  = "writer"
)

// MetricsPorts is the single source of truth for which TCP port each
// Harporis service exposes its Prometheus /metrics + /healthz + /readyz
// endpoints on. Kept in sync with each service's Dockerfile EXPOSE
// directive and the k8s deployment/service manifests (those are static
// YAML so the duplication is necessary; this map covers every code
// path that depends on the value).
var MetricsPorts = map[string]int{
	ServiceGetter:  9100,
	ServiceScanner: 9101,
	ServiceWriter:  9102,
}

// Services returns the canonical service names in a deterministic
// order. Use this when iterating MetricsPorts so the order is stable
// across runs (map iteration is not).
func Services() []string {
	return []string{ServiceGetter, ServiceScanner, ServiceWriter}
}

// Per-scan subject builders.
func ChunksSubject(scanID string) string   { return "harporis.chunks." + scanID }
func StatusSubject(scanID string) string   { return "harporis.status." + scanID }
func FindingsSubject(scanID string) string { return "harporis.findings." + scanID }

// DialConfig is a service-agnostic NATS connection config. All TLS/auth
// fields are optional; zero values preserve the dev-stack default
// (unauthenticated, no TLS) but production deployments MUST set them.
type DialConfig struct {
	URL        string
	ClientName string // e.g. "harporis-getter"

	// Optional TLS/auth knobs. Empty/nil = not applied.
	TLSConfig *tls.Config // sets nats.Secure(tlsCfg) when non-nil
	RootCAs   string      // path to PEM bundle; sets nats.RootCAs(path) when non-empty
	CredsFile string      // path to JWT/nkey credentials; sets nats.UserCredentials(path) when non-empty
	Token     string      // sets nats.Token(token) when non-empty
}

// Client wraps a NATS connection + JetStream context.
type Client struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

// Dial connects to NATS with reconnect-forever semantics and returns a Client.
// Optional TLS/auth fields on cfg are translated to nats.go connect options;
// zero values mean "not applied" so existing dev-stack callers keep working.
func Dial(cfg DialConfig) (*Client, error) {
	opts := []nats.Option{
		nats.Name(cfg.ClientName),
		nats.MaxReconnects(-1),
		nats.RetryOnFailedConnect(true),
	}
	if cfg.TLSConfig != nil {
		opts = append(opts, nats.Secure(cfg.TLSConfig))
	}
	if cfg.RootCAs != "" {
		opts = append(opts, nats.RootCAs(cfg.RootCAs))
	}
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}
	if cfg.Token != "" {
		opts = append(opts, nats.Token(cfg.Token))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
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
//
// Bounded growth notes:
//   - REQUESTS / CHUNKS / FINDINGS are WorkQueuePolicy → messages
//     delete on first successful Ack. A backstop MaxBytes is set on
//     each so a stuck consumer + producer pressure can't fill the
//     disk; DiscardOld means oldest unacked is dropped at the cap
//     (preferable to publish failure on the producer side).
//   - STATUS is LimitsPolicy (every replica wants to read it; no Ack
//     deletes it). Set BOTH MaxAge (rolling window) and MaxBytes
//     (hard cap) so an operator who runs scans nonstop for months
//     doesn't silently fill the NATS volume.
//
// streamConfigDrifted compares these fields, so existing deployments
// pick the new limits up on the next service start.
const (
	// StatusMaxAge keeps ~a week of historical status events for
	// post-mortem on recent scans. Tune via wire.go if you need more.
	StatusMaxAge = 7 * 24 * time.Hour
	// StatusMaxBytes is a hard cap on disk used by the status stream.
	// 512 MiB at ~200 B/event ≈ 2.6M events ≈ tens of thousands of
	// scans depending on chunk count.
	StatusMaxBytes = 512 * 1024 * 1024
	// WorkQueueMaxBytes bounds REQUESTS / CHUNKS / FINDINGS at 2 GiB
	// each so a wedged consumer can't fill the NATS volume during a
	// large scan or a burst of submissions.
	WorkQueueMaxBytes = 2 * 1024 * 1024 * 1024
)

func EnsureStreams(js nats.JetStreamContext) error {
	configs := []*nats.StreamConfig{
		// RequestsStream captures ONLY the requests subject. CancelSubject is
		// intentionally not in the filter: cancel is a fire-and-forget broadcast
		// over core NATS, and a WorkQueuePolicy stream with no matching
		// subscriber would let cancels accumulate without bound.
		{Name: RequestsStream, Subjects: []string{ScansRequestsSubject}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, MaxBytes: WorkQueueMaxBytes, Discard: nats.DiscardOld},
		{Name: ChunksStream, Subjects: []string{"harporis.chunks.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, MaxBytes: WorkQueueMaxBytes, Discard: nats.DiscardOld},
		{Name: StatusStream, Subjects: []string{"harporis.status.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, MaxAge: StatusMaxAge, MaxBytes: StatusMaxBytes, Discard: nats.DiscardOld},
		{Name: FindingsStream, Subjects: []string{"harporis.findings.>"}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, Duplicates: 5 * time.Minute, MaxBytes: WorkQueueMaxBytes, Discard: nats.DiscardOld},
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
	if have.MaxAge != want.MaxAge {
		return true
	}
	if have.MaxBytes != want.MaxBytes {
		return true
	}
	if have.Discard != want.Discard {
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
