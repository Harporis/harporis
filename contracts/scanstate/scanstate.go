// Package scanstate provides the single, shared classification of
// v1.ScanState values so every service (getter, scanner, CLI) agrees on
// which states are terminal. Adding a new terminal state requires editing
// only this function.
package scanstate

import v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

// IsTerminal reports whether a scan state cannot transition further.
func IsTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED,
		v1.ScanState_FAILED,
		v1.ScanState_PARTIAL,
		v1.ScanState_CANCELLED:
		return true
	}
	return false
}
