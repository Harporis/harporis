// Package severity provides the single, shared mapping between severity
// level names and the v1.Severity enum, plus a Set type used by the
// writer (config default) and the CLI (read-time --severity filter) to
// decide which findings reach reports. Mirrors contracts/scanstate:
// one place owns the string<->enum knowledge.
package severity

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Set is a set of selectable severity levels. An EMPTY set means
// "no filter" — Contains returns true for every level. This lets call
// sites write `if !set.Contains(f.Severity) { drop }` with empty = pass-all.
type Set map[v1.Severity]bool

// Contains reports whether sev passes the filter. An empty set passes all.
func (s Set) Contains(sev v1.Severity) bool {
	if len(s) == 0 {
		return true
	}
	return s[sev]
}

// validLevels is the set of operator-selectable levels (UNSPECIFIED is
// excluded — it is the zero value, not a real severity).
var validLevels = map[string]v1.Severity{
	"LOW":      v1.Severity_LOW,
	"MEDIUM":   v1.Severity_MEDIUM,
	"HIGH":     v1.Severity_HIGH,
	"CRITICAL": v1.Severity_CRITICAL,
}

// ParseSet parses level names (case-insensitive, whitespace-trimmed) into
// a Set. Empty input yields an empty Set ("no filter"). An unknown or
// unspecified name returns an error listing the valid levels.
func ParseSet(names []string) (Set, error) {
	set := make(Set)
	for _, raw := range names {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		sev, ok := validLevels[name]
		if !ok {
			return nil, fmt.Errorf("unknown severity %q (want one of: %s)", raw, validNamesList())
		}
		set[sev] = true
	}
	return set, nil
}

// ParseCSV splits a comma-separated string ("CRITICAL,HIGH") and parses it.
func ParseCSV(s string) (Set, error) {
	if strings.TrimSpace(s) == "" {
		return Set{}, nil
	}
	return ParseSet(strings.Split(s, ","))
}

func validNamesList() string {
	names := make([]string, 0, len(validLevels))
	for n := range validLevels {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
