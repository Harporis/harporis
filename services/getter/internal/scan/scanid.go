package scan

import (
	"errors"
	"fmt"
)

// ValidateScanID rejects scan IDs that would break NATS subject builders
// (ChunksSubject / StatusSubject / FindingsSubject) by injecting wildcards
// or token separators. We accept the practical alphabet used by UUIDs and
// human-readable client-side IDs: [A-Za-z0-9_-].
//
// Length bounds keep subjects under typical NATS limits (256 chars) and
// prevent zero-length subjects (`harporis.chunks.`).
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
