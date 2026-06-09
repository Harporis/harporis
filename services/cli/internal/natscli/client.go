// Package natscli is a thin CLI-side wrapper around kit/nats/wire that
// also owns NATS-specific niceties (default URL, consumer-name
// sanitization) so command files stay focused on UX.
package natscli

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Harporis/harporis/kit/nats/wire"
)

// Client wraps wire.Client and exposes helpers for cli use.
type Client struct{ *wire.Client }

// Dial connects to NATS and returns a Client.
//
// Token resolution (first non-empty wins):
//  1. NATS_TOKEN env var — operator override
//  2. Discovered from the running harporis-nats container's env via
//     docker inspect, but ONLY when natsURL points at a localhost host.
//     Production NATS URLs (TLS, remote hosts) intentionally skip
//     discovery — the operator must configure the token explicitly.
//
// The discovery path makes the CLI work out-of-the-box on a fresh
// shell against the dev stack: token lives in the docker-compose env,
// CLI reads it from there without the operator needing to source any
// rc-file or set NATS_TOKEN themselves.
func Dial(natsURL, clientName string) (*Client, error) {
	token := os.Getenv("NATS_TOKEN")
	if token == "" && isLocalhost(natsURL) {
		token = discoverNATSToken()
	}
	c, err := wire.Dial(wire.DialConfig{
		URL:        natsURL,
		ClientName: clientName,
		Token:      token,
	})
	if err != nil {
		return nil, err
	}
	return &Client{Client: c}, nil
}

// EnsureStreams creates the four canonical streams if they don't exist.
func (c *Client) EnsureStreams() error { return wire.EnsureStreams(c.JS) }

// SanitizeConsumerName makes any scan-id safe to use as a JetStream
// consumer name (alphanumeric, dash, underscore only).
func SanitizeConsumerName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z',
			r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// isLocalhost returns true if the NATS URL's host portion resolves to
// 127.0.0.1, ::1, or the literal hostname "localhost". Anything else
// (DNS name, remote IP) is treated as production and skips discovery.
func isLocalhost(natsURL string) bool {
	u, err := url.Parse(natsURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	switch host {
	case "localhost", "127.0.0.1", "::1", "":
		return true
	default:
		return false
	}
}

// discoverNATSToken returns the NATS_TOKEN env var from the running
// harporis-nats container, or empty if the container isn't found
// (stack down, project renamed, docker unavailable). Best-effort and
// silent — Dial falls back to no-token when this returns empty.
//
// 1.5s timeout because this runs on every NATS-touching command and
// must not block the UX when docker is slow/absent.
func discoverNATSToken() string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	// Find the nats container by compose service label.
	psCmd := exec.CommandContext(ctx, "docker", "ps",
		"--filter", "label=com.docker.compose.service=nats",
		"--filter", "label=com.docker.compose.project=harporis",
		"--format", "{{.Names}}",
	)
	out, err := psCmd.Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return ""
	}
	if i := strings.IndexByte(name, '\n'); i >= 0 {
		name = name[:i]
	}

	// Inspect its env for NATS_TOKEN=<value>.
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect", name,
		"--format", "{{range .Config.Env}}{{println .}}{{end}}",
	)
	out, err = inspectCmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "NATS_TOKEN="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
