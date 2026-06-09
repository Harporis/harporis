// Package scan exposes scan-related primitives shared across services.
// ValidateScanID is the single source of truth for what shapes the
// pipeline (getter, scanner, writer, CLI) accepts as a scan_id. Putting
// it in kit/ prevents validation drift between request submission and
// downstream consumers — see the path-traversal class of bugs that
// arises when one service validates and another trusts the payload.
package scan

import (
	"errors"
	"fmt"
)

// ValidateScanID rejects scan IDs that would break NATS subject builders
// (ChunksSubject / StatusSubject / FindingsSubject) by injecting
// wildcards or token separators, AND scan IDs that would escape a
// rootDir under filepath.Join (any "." / "/" / "\" / "..").
//
// Allowed alphabet: [A-Za-z0-9_-], length in [1, 128].
// This matches the alphabet UUIDs produce plus the conservative set of
// human-readable client-side IDs.
func ValidateScanID(id string) error {
	if id == "" {
		return errors.New("scan_id: must not be empty")
	}
	if len(id) > 128 {
		return fmt.Errorf("scan_id: too long (%d > 128)", len(id))
	}
	for i, r := range id {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("scan_id: invalid character %q at position %d (allowed: [A-Za-z0-9_-])", r, i)
		}
	}
	return nil
}
