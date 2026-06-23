package tui

import (
	"testing"

	"github.com/Harporis/harporis/services/cli/internal/findings"
)

func fnd(sev, rule, path string) findings.Finding {
	return findings.Finding{Severity: sev, RuleID: rule, FilePath: path, LineNumber: 1}
}

func TestParseFindingFilterUnknownKey(t *testing.T) {
	if _, err := ParseFindingFilter("foo:bar"); err == nil {
		t.Fatal("want error for unknown key")
	}
}

func TestFindingFilterZeroMatchesAll(t *testing.T) {
	var f FindingFilter
	if !f.Match(fnd("HIGH", "aws-access-key-id", "docs/x.md")) {
		t.Fatal("zero filter must match everything")
	}
}

func TestFindingFilterPerKeyAndBareWord(t *testing.T) {
	fs, _ := ParseFindingFilter("severity:critical")
	if fs.Match(fnd("HIGH", "r", "p")) || !fs.Match(fnd("CRITICAL", "r", "p")) {
		t.Fatal("severity: must match only CRITICAL")
	}
	fr, _ := ParseFindingFilter("rule:aws")
	if !fr.Match(fnd("LOW", "aws-access-key-id", "p")) || fr.Match(fnd("LOW", "private-key-pem", "p")) {
		t.Fatal("rule: must substring-match rule_id")
	}
	fp, _ := ParseFindingFilter("path:test")
	if !fp.Match(fnd("LOW", "r", "services/x_test.go")) || fp.Match(fnd("LOW", "r", "services/x.go")) {
		t.Fatal("path: must substring-match the location")
	}
	fb, _ := ParseFindingFilter("aws")
	if !fb.Match(fnd("LOW", "aws-access-key-id", "p")) {
		t.Fatal("bare word must match across fields (rule here)")
	}
}

func TestFindingFilterAndCombination(t *testing.T) {
	f, _ := ParseFindingFilter("severity:high rule:aws")
	if !f.Match(fnd("HIGH", "aws-access-key-id", "p")) {
		t.Fatal("both clauses satisfied must match")
	}
	if f.Match(fnd("LOW", "aws-access-key-id", "p")) {
		t.Fatal("one clause failing must reject")
	}
}

func TestFindingFilterMultipleBareWordsAnd(t *testing.T) {
	f, _ := ParseFindingFilter("aws docs")
	// rule contains "aws", path contains "docs" -> both satisfied
	if !f.Match(fnd("HIGH", "aws-access-key-id", "docs/x.md")) {
		t.Fatal("both bare words satisfied (across fields) must match")
	}
	// only "aws" present, "docs" absent -> reject
	if f.Match(fnd("HIGH", "aws-access-key-id", "src/x.go")) {
		t.Fatal("a bare word matching nothing must reject")
	}
}
