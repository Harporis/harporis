// Package rules compiles YAML detection rules and provides supporting
// primitives (entropy filter, severity mapping). The detector consumes
// the compiled []Rule output of LoadEmbedded/LoadFile.
package rules

import (
	"fmt"
	"regexp"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Rule is a compiled detection rule from the YAML pack.
type Rule struct {
	ID          string
	Description string
	Severity    v1.Severity
	Regex       *regexp.Regexp
	EntropyMin  float64 // 0 = no entropy filter
	EntropyGrp  int     // capture-group index for entropy check; 0 = full match
	Tags        []string

	// Examples are populated by the loader and consumed only by Validate.
	// Kept unexported to avoid polluting the public API.
	posExamples []string
	negExamples []string
}

// SeverityFromString maps the YAML severity literal to the proto enum.
// Returns an error on unknown values rather than silently defaulting.
func SeverityFromString(s string) (v1.Severity, error) {
	switch s {
	case "low":
		return v1.Severity_LOW, nil
	case "medium":
		return v1.Severity_MEDIUM, nil
	case "high":
		return v1.Severity_HIGH, nil
	case "critical":
		return v1.Severity_CRITICAL, nil
	}
	return v1.Severity_SEVERITY_UNSPECIFIED, fmt.Errorf("unknown severity %q (want low/medium/high/critical)", s)
}
