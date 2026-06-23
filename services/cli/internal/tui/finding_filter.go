package tui

import (
	"fmt"
	"strings"

	"github.com/Harporis/harporis/services/cli/internal/findings"
)

// FindingFilter is a parsed structured query over a findings list. The zero
// value matches every finding. Build one with ParseFindingFilter.
type FindingFilter struct {
	severity string   // substring over severity (case-insensitive)
	rule     string   // substring over rule_id
	path     string   // substring over Location()
	text     []string // bare words: each must substring-match at least one field
	raw      string
}

var findingFilterKeys = map[string]bool{"severity": true, "rule": true, "path": true}

// ParseFindingFilter parses `severity:critical rule:aws path:test`. A token
// without a colon is a bare word matched across all fields. An unknown key
// returns an error and the zero filter.
func ParseFindingFilter(s string) (FindingFilter, error) {
	f := FindingFilter{raw: strings.TrimSpace(s)}
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, ":")
		if !ok {
			f.text = append(f.text, tok)
			continue
		}
		k = strings.ToLower(k)
		if !findingFilterKeys[k] {
			return FindingFilter{}, fmt.Errorf("unknown key %q", k)
		}
		switch k {
		case "severity":
			f.severity = strings.ToLower(v)
		case "rule":
			f.rule = strings.ToLower(v)
		case "path":
			f.path = strings.ToLower(v)
		}
	}
	return f, nil
}

// Raw returns the trimmed source query.
func (f FindingFilter) Raw() string { return f.raw }

// Match reports whether fd satisfies every clause (AND).
func (f FindingFilter) Match(fd findings.Finding) bool {
	sev := strings.ToLower(fd.Severity)
	rule := strings.ToLower(fd.RuleID)
	loc := strings.ToLower(fd.Location())
	if f.severity != "" && !strings.Contains(sev, f.severity) {
		return false
	}
	if f.rule != "" && !strings.Contains(rule, f.rule) {
		return false
	}
	if f.path != "" && !strings.Contains(loc, f.path) {
		return false
	}
	for _, w := range f.text {
		t := strings.ToLower(w)
		if !strings.Contains(sev, t) && !strings.Contains(rule, t) && !strings.Contains(loc, t) {
			return false
		}
	}
	return true
}
