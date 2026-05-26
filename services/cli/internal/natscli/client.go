// Package natscli is a thin CLI-side wrapper around kit/nats/wire that
// also owns NATS-specific niceties (default URL, consumer-name
// sanitization) so command files stay focused on UX.
package natscli

import (
	"strings"

	"github.com/Harporis/harporis/kit/nats/wire"
)

// Client wraps wire.Client and exposes helpers for cli use.
type Client struct{ *wire.Client }

// Dial connects to NATS and returns a Client.
func Dial(url, clientName string) (*Client, error) {
	c, err := wire.Dial(wire.DialConfig{URL: url, ClientName: clientName})
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
