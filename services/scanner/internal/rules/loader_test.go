package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFile_ParsesSingleRule(t *testing.T) {
	dir := t.TempDir()
	yaml := `
- id: test-rule
  description: A test
  severity: high
  regex: 'AKIA[A-Z0-9]{16}'
  tags: [test]
  examples:
    positive: ["AKIAIOSFODNN7EXAMPLE"]
    negative: ["AKIA-not-a-key"]
`
	p := filepath.Join(dir, "r.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rules, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	r := rules[0]
	if r.ID != "test-rule" || r.Description != "A test" {
		t.Errorf("rule fields wrong: %+v", r)
	}
	if r.Regex == nil || !r.Regex.MatchString("AKIAIOSFODNN7EXAMPLE") {
		t.Error("regex did not compile / match")
	}
}

func TestLoadFile_WithEntropy(t *testing.T) {
	dir := t.TempDir()
	yaml := `
- id: gen
  description: x
  severity: low
  regex: '(token)=([A-Za-z0-9]+)'
  entropy:
    min: 3.5
    target_group: 2
  examples:
    positive: ["token=3xq8Z1nQpvP7tk"]
    negative: ["token=aaaaa"]
`
	p := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(p, []byte(yaml), 0o644)
	rules, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if rules[0].EntropyMin != 3.5 || rules[0].EntropyGrp != 2 {
		t.Errorf("entropy fields = %+v / %+v", rules[0].EntropyMin, rules[0].EntropyGrp)
	}
}

func TestValidate_RejectsDuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	yaml := `
- id: dup
  description: x
  severity: low
  regex: 'a'
  examples: {positive: ["a"], negative: ["b"]}
- id: dup
  description: x
  severity: low
  regex: 'a'
  examples: {positive: ["a"], negative: ["b"]}
`
	p := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(p, []byte(yaml), 0o644)
	rules, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	err = Validate(rules)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("Validate: want duplicate-id error, got %v", err)
	}
}

func TestValidate_RejectsPositiveExampleThatDoesNotMatch(t *testing.T) {
	dir := t.TempDir()
	yaml := `
- id: bad
  description: x
  severity: low
  regex: 'AKIA[A-Z0-9]{16}'
  examples:
    positive: ["not-a-key"]
    negative: ["whatever"]
`
	p := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(p, []byte(yaml), 0o644)
	rules, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	err = Validate(rules)
	if err == nil || !strings.Contains(err.Error(), "positive example") {
		t.Errorf("Validate: want positive-example error, got %v", err)
	}
}

func TestValidate_RejectsNegativeExampleThatMatches(t *testing.T) {
	dir := t.TempDir()
	yaml := `
- id: bad
  description: x
  severity: low
  regex: 'AKIA[A-Z0-9]{16}'
  examples:
    positive: ["AKIAIOSFODNN7EXAMPLE"]
    negative: ["AKIAIOSFODNN7EXAMPLE2"]
`
	p := filepath.Join(dir, "r.yaml")
	_ = os.WriteFile(p, []byte(yaml), 0o644)
	rules, err := LoadFile(p)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	err = Validate(rules)
	if err == nil || !strings.Contains(err.Error(), "negative example") {
		t.Errorf("Validate: want negative-example error, got %v", err)
	}
}

func TestLoadEmbedded_AlwaysValid(t *testing.T) {
	rules, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if err := Validate(rules); err != nil {
		t.Errorf("embedded rule pack failed Validate: %v", err)
	}
}
