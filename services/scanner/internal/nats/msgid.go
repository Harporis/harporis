// Package nats is the scanner's JetStream wiring: consumer, publisher,
// MsgId formula. Named `nats` (not `natscli`) to mirror getter/internal/nats.
package nats

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// FindingMsgID returns the deterministic MsgId for a single Finding publish.
// Used with nats.MsgId(...) so that JetStream's dedup window
// (HARPORIS_FINDINGS.Duplicates = 5min) drops re-emitted findings from
// chunk redelivery.
//
// MUST be stable across scanner restarts and across replicas. Any change
// here breaks at-least-once → effectively-once semantics for in-flight
// chunks. Treated as part of the kit/wire contract.
func FindingMsgID(scanID, chunkID, ruleID string, lineNumber int32, byteOffset int64) string {
	key := fmt.Sprintf("%s|%s|%s|%d|%d", scanID, chunkID, ruleID, lineNumber, byteOffset)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}
