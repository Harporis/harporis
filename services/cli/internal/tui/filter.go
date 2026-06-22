package tui

import (
	"fmt"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Filter is a parsed structured query over the fleet table. The zero value
// matches every event. Build one with ParseFilter.
type Filter struct {
	state  string // matches State: substring, or the words "active"/"terminal"
	source string // substring over Source
	id     string // substring over ScanId
	text   string // bare word: substring across id, source, and state
	raw    string // the trimmed source query, for redisplay in the input line
}

var filterKeys = map[string]bool{"state": true, "source": true, "id": true}

// ParseFilter parses a space-separated query like `state:failed source:gh`.
// A token without a colon is a bare word matched across all fields. An
// unknown key returns an error and the zero Filter.
func ParseFilter(s string) (Filter, error) {
	f := Filter{raw: strings.TrimSpace(s)}
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, ":")
		if !ok {
			f.text = tok // last bare word wins — simple by design
			continue
		}
		k = strings.ToLower(k)
		if !filterKeys[k] {
			return Filter{}, fmt.Errorf("unknown key %q", k)
		}
		switch k {
		case "state":
			f.state = strings.ToLower(v)
		case "source":
			f.source = strings.ToLower(v)
		case "id":
			f.id = strings.ToLower(v)
		}
	}
	return f, nil
}

// Raw returns the trimmed source query, so the filter input line can be
// pre-populated when the operator reopens it.
func (f Filter) Raw() string { return f.raw }

// Match reports whether ev satisfies every clause (clauses are AND-ed).
func (f Filter) Match(ev *v1.StatusEvent) bool {
	if f.state != "" && !matchState(f.state, ev) {
		return false
	}
	src := strings.ToLower(ev.GetSource())
	id := strings.ToLower(ev.GetScanId())
	if f.source != "" && !strings.Contains(src, f.source) {
		return false
	}
	if f.id != "" && !strings.Contains(id, f.id) {
		return false
	}
	if f.text != "" {
		t := strings.ToLower(f.text)
		st := strings.ToLower(ev.GetState().String())
		if !strings.Contains(id, t) && !strings.Contains(src, t) && !strings.Contains(st, t) {
			return false
		}
	}
	return true
}

func matchState(want string, ev *v1.StatusEvent) bool {
	switch want {
	case "active":
		return !IsTerminal(ev.GetState())
	case "terminal", "done":
		return IsTerminal(ev.GetState())
	}
	return strings.Contains(strings.ToLower(ev.GetState().String()), want)
}
