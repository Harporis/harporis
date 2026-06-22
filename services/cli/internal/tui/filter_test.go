package tui

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func ev(id, source string, st v1.ScanState) *v1.StatusEvent {
	return &v1.StatusEvent{ScanId: id, Source: source, State: st}
}

func TestParseFilterUnknownKey(t *testing.T) {
	if _, err := ParseFilter("foo:bar"); err == nil {
		t.Fatal("want error for unknown key, got nil")
	}
}

func TestFilterZeroValueMatchesEverything(t *testing.T) {
	var f Filter
	if !f.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("zero-value filter must match everything")
	}
}

func TestFilterStateActiveAndTerminal(t *testing.T) {
	fa, _ := ParseFilter("state:active")
	if !fa.Match(ev("a", "s", v1.ScanState_RUNNING)) {
		t.Fatal("state:active must match a non-terminal scan")
	}
	if fa.Match(ev("a", "s", v1.ScanState_COMPLETED)) {
		t.Fatal("state:active must not match a terminal scan")
	}
	ft, _ := ParseFilter("state:terminal")
	if !ft.Match(ev("a", "s", v1.ScanState_FAILED)) {
		t.Fatal("state:terminal must match a terminal scan")
	}
}

func TestFilterStateSubstringSourceIDAndBareWord(t *testing.T) {
	fs, _ := ParseFilter("state:run")
	if !fs.Match(ev("a", "s", v1.ScanState_RUNNING)) {
		t.Fatal("state:run must substring-match RUNNING")
	}
	fsrc, _ := ParseFilter("source:github")
	if !fsrc.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) ||
		fsrc.Match(ev("a", "gitlab.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("source: must substring-match Source only")
	}
	fid, _ := ParseFilter("id:abc")
	if !fid.Match(ev("xx-abc-yy", "s", v1.ScanState_RUNNING)) {
		t.Fatal("id: must substring-match ScanId")
	}
	fb, _ := ParseFilter("github")
	if !fb.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("bare word must match across fields (source here)")
	}
}

func TestFilterCombinesClausesAsAnd(t *testing.T) {
	f, _ := ParseFilter("state:active source:github")
	if !f.Match(ev("a", "github.com/x", v1.ScanState_RUNNING)) {
		t.Fatal("both clauses satisfied must match")
	}
	if f.Match(ev("a", "github.com/x", v1.ScanState_COMPLETED)) {
		t.Fatal("one clause failing must reject")
	}
}
