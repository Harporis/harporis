package tui

import (
	"testing"

	"github.com/Harporis/harporis/services/cli/internal/findings"
)

func TestCompareFinding(t *testing.T) {
	hi := findings.Finding{Severity: "HIGH", RuleID: "aws", FilePath: "a.go", LineNumber: 5}
	crit := findings.Finding{Severity: "CRITICAL", RuleID: "pem", FilePath: "b.go", LineNumber: 1}
	if compareFinding(hi, crit, fcolSeverity) >= 0 {
		t.Fatal("HIGH < CRITICAL by severity rank")
	}
	if compareFinding(hi, crit, fcolRule) >= 0 {
		t.Fatal("aws < pem by rule")
	}
	if compareFinding(hi, crit, fcolPath) >= 0 {
		t.Fatal("a.go < b.go by path")
	}
}

func TestFindingColumnNextCycles(t *testing.T) {
	if fcolSecret.next() != fcolSeverity {
		t.Fatal("next must wrap Secret -> Severity")
	}
}
