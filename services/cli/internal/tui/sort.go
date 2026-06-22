package tui

import (
	"strings"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// sortColumn identifies a fleet-table column the operator can sort by.
type sortColumn int

const (
	sortScanID sortColumn = iota
	sortState
	sortSource
	sortChunks
	sortSecrets
	sortUpdated
)

var sortColumns = []sortColumn{
	sortScanID, sortState, sortSource, sortChunks, sortSecrets, sortUpdated,
}

func (c sortColumn) label() string {
	switch c {
	case sortScanID:
		return "SCAN_ID"
	case sortState:
		return "STATE"
	case sortSource:
		return "SOURCE"
	case sortChunks:
		return "CHUNKS"
	case sortSecrets:
		return "SECRETS"
	case sortUpdated:
		return "UPDATED"
	}
	return ""
}

// next returns the following column, wrapping Updated back to ScanID.
func (c sortColumn) next() sortColumn { return sortColumns[(int(c)+1)%len(sortColumns)] }

// compareColumn orders a before b on col, ascending: negative if a<b, zero
// if equal, positive if a>b. The caller applies reverse and tiebreak.
func compareColumn(a, b *v1.StatusEvent, col sortColumn) int {
	switch col {
	case sortScanID:
		return strings.Compare(a.GetScanId(), b.GetScanId())
	case sortState:
		return strings.Compare(a.GetState().String(), b.GetState().String())
	case sortSource:
		return strings.Compare(a.GetSource(), b.GetSource())
	case sortChunks:
		return cmpInt64(a.GetMetrics().GetChunksPublished(), b.GetMetrics().GetChunksPublished())
	case sortSecrets:
		return cmpInt64(a.GetMetrics().GetSecretsFound(), b.GetMetrics().GetSecretsFound())
	case sortUpdated:
		return cmpInt64(a.GetTimestamp(), b.GetTimestamp())
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Defined fully in later tasks; declared here so FleetModel compiles.
type viewMode int

type historyLoader interface {
	ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error)
}
