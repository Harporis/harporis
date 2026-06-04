package scan

import (
	"strings"
	"testing"
)

func TestValidateScanID_AcceptsUUIDAndPlainAlphanumeric(t *testing.T) {
	good := []string{
		"d1ba75ba-2536-4088-8d40-84577abd0ce4", // UUIDv4
		"leaky-1",
		"demo_1",
		"abcDEF123",
		"a", // min length
	}
	for _, s := range good {
		if err := ValidateScanID(s); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
}

func TestValidateScanID_RejectsTraversalAndShellMetachars(t *testing.T) {
	bad := []string{
		"",                       // empty
		"../etc/passwd",          // traversal
		"..",                     // pure traversal
		"foo/bar",                // slash
		"foo.bar",                // period
		"foo\\bar",               // backslash
		"foo bar",                // space
		"foo;rm -rf /",           // semicolon
		"foo$bar",                // dollar
		"foo`whoami`",            // backtick
		"foo|baz",                // pipe
		"scan-*",                 // NATS subject wildcard
		"scan->",                 // NATS subject wildcard
		"scan\nid",               // newline
		"сканер",                 // non-ASCII
		strings.Repeat("a", 129), // overlong
	}
	for _, s := range bad {
		if err := ValidateScanID(s); err == nil {
			t.Errorf("%q should be rejected but was accepted", s)
		}
	}
}
