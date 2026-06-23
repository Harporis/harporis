package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harporis/harporis/services/cli/internal/findings"
)

func sampleFindings() []findings.Finding {
	return []findings.Finding{
		{Severity: "HIGH", RuleID: "aws-access-key-id", FilePath: "docs/x.md", LineNumber: 1, MatchedSecret: ""},
		{Severity: "CRITICAL", RuleID: "private-key-pem", FilePath: "a/b.go", LineNumber: 2, MatchedSecret: ""},
		{Severity: "LOW", RuleID: "jwt", FilePath: "c.md", LineNumber: 3, MatchedSecret: ""},
	}
}

func TestFindingsDefaultSortSeverityDesc(t *testing.T) {
	s := findingsState{loaded: sampleFindings()}
	vis := s.visible()
	if vis[0].Severity != "CRITICAL" || vis[len(vis)-1].Severity != "LOW" {
		t.Fatalf("default sort must be severity desc; got %s..%s", vis[0].Severity, vis[len(vis)-1].Severity)
	}
}

func TestFindingsCursorClampAndView(t *testing.T) {
	s := findingsState{loaded: sampleFindings()}
	for i := 0; i < 10; i++ {
		s, _ = s.updateKey(tea.KeyMsg{Type: tea.KeyDown}, 20)
	}
	if s.Cursor() != 2 {
		t.Fatalf("cursor must clamp at last row (2), got %d", s.Cursor())
	}
	if !strings.Contains(s.view(20), "private-key-pem") {
		t.Fatalf("view must render rule names; got:\n%s", s.view(20))
	}
}

func TestFindingsFilterInputApplies(t *testing.T) {
	s := findingsState{loaded: sampleFindings()}
	s, _ = s.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}, 20)
	if !s.filtering {
		t.Fatal("/ must enter filter mode")
	}
	for _, ch := range "severity:critical" {
		s, _ = s.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}, 20)
	}
	s, _ = s.updateKey(tea.KeyMsg{Type: tea.KeyEnter}, 20)
	vis := s.visible()
	if len(vis) != 1 || vis[0].Severity != "CRITICAL" {
		t.Fatalf("filter must leave only CRITICAL, got %d rows", len(vis))
	}
}

func TestFindingsLoadingAndErrorAndEmpty(t *testing.T) {
	if !strings.Contains((findingsState{loading: true}).view(20), "loading") {
		t.Fatal("loading state must render a hint")
	}
	if !strings.Contains((findingsState{err: errSample}).view(20), "unavailable") {
		t.Fatal("error state must render 'unavailable'")
	}
	if !strings.Contains((findingsState{loaded: nil}).view(20), "no findings") {
		t.Fatal("empty loaded must render '(no findings)'")
	}
}

func TestFindingsViewportFollowsCursor(t *testing.T) {
	var fs []findings.Finding
	for i := 0; i < 30; i++ {
		fs = append(fs, findings.Finding{Severity: "HIGH", RuleID: "r", FilePath: fmt.Sprintf("f%02d.go", i), LineNumber: 1})
	}
	s := findingsState{loaded: fs}
	const h = 16 // pageSize(16) = 6 per the impl (height-10)
	for i := 0; i < 20; i++ {
		s, _ = s.updateKey(tea.KeyMsg{Type: tea.KeyDown}, h)
	}
	ps := s.pageSize(h)
	if s.offset == 0 {
		t.Fatalf("offset must advance once cursor passes the first page; cursor=%d offset=%d ps=%d", s.cursor, s.offset, ps)
	}
	if s.cursor < s.offset || s.cursor >= s.offset+ps {
		t.Fatalf("cursor %d must stay within viewport [%d,%d)", s.cursor, s.offset, s.offset+ps)
	}
}

var errSample = errSampleType("boom")

type errSampleType string

func (e errSampleType) Error() string { return string(e) }
