package tui

import (
	"strings"

	"github.com/Harporis/harporis/services/cli/internal/findings"
)

type findingColumn int

const (
	fcolSeverity findingColumn = iota
	fcolRule
	fcolPath
	fcolSecret
)

var findingColumns = []findingColumn{fcolSeverity, fcolRule, fcolPath, fcolSecret}

func (c findingColumn) label() string {
	switch c {
	case fcolSeverity:
		return "SEVERITY"
	case fcolRule:
		return "RULE"
	case fcolPath:
		return "PATH:LINE"
	case fcolSecret:
		return "SECRET"
	}
	return ""
}

func (c findingColumn) next() findingColumn {
	return findingColumns[(int(c)+1)%len(findingColumns)]
}

// compareFinding orders a before b on col, ascending: negative if a<b. The
// caller applies reverse and tiebreak. Severity orders by rank (CRITICAL high).
func compareFinding(a, b findings.Finding, col findingColumn) int {
	switch col {
	case fcolSeverity:
		return findings.SeverityRank(a.Severity) - findings.SeverityRank(b.Severity)
	case fcolRule:
		return strings.Compare(a.RuleID, b.RuleID)
	case fcolPath:
		return strings.Compare(a.Location(), b.Location())
	case fcolSecret:
		return strings.Compare(a.SecretPreview(48), b.SecretPreview(48))
	}
	return 0
}
