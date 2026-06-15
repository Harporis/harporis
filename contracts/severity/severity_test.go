package severity

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestParseCSV_ValidLevels(t *testing.T) {
	set, err := ParseCSV("CRITICAL,high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_CRITICAL) || !set.Contains(v1.Severity_HIGH) {
		t.Fatalf("expected CRITICAL and HIGH in set")
	}
	if set.Contains(v1.Severity_LOW) {
		t.Fatalf("LOW should not be in set")
	}
}

func TestParseCSV_Empty_IsNoFilter(t *testing.T) {
	set, err := ParseCSV("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range []v1.Severity{v1.Severity_LOW, v1.Severity_MEDIUM, v1.Severity_HIGH, v1.Severity_CRITICAL} {
		if !set.Contains(s) {
			t.Fatalf("empty set should contain %v", s)
		}
	}
	if len(set) != 0 {
		t.Fatalf("empty CSV should yield empty set, got %d", len(set))
	}
}

func TestParseCSV_WhitespaceAndCase(t *testing.T) {
	set, err := ParseCSV("  Low , MEDIUM ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_LOW) || !set.Contains(v1.Severity_MEDIUM) {
		t.Fatalf("expected LOW and MEDIUM")
	}
}

func TestParseSet_UnknownLevel(t *testing.T) {
	_, err := ParseSet([]string{"HIGH", "BOGUS"})
	if err == nil {
		t.Fatalf("expected error for unknown level")
	}
}

func TestParseSet_RejectsUnspecified(t *testing.T) {
	_, err := ParseSet([]string{"SEVERITY_UNSPECIFIED"})
	if err == nil {
		t.Fatalf("SEVERITY_UNSPECIFIED is not a selectable level")
	}
}

func TestParseSet_NilAndEmpty_IsNoFilter(t *testing.T) {
	for _, input := range [][]string{nil, {}} {
		set, err := ParseSet(input)
		if err != nil {
			t.Fatalf("unexpected error for input %v: %v", input, err)
		}
		if len(set) != 0 {
			t.Fatalf("expected empty no-filter set for input %v, got %d", input, len(set))
		}
		if !set.Contains(v1.Severity_LOW) {
			t.Fatalf("empty set should pass all levels for input %v", input)
		}
	}
}
