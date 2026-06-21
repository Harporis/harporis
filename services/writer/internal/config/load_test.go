package config

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestLoadAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "writer.yaml")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL != "nats://nats:4222" {
		t.Errorf("default nats url: %q", c.NATSURL)
	}
	if c.OutputDir != "/var/lib/harporis/findings" {
		t.Errorf("default output dir: %q", c.OutputDir)
	}
	if c.MetricsAddr != ":9102" {
		t.Errorf("default metrics addr: %q", c.MetricsAddr)
	}
	if c.MaxAckPending != 64 {
		t.Errorf("default max ack pending: %d", c.MaxAckPending)
	}
}

func TestLoadEnvSubstitution(t *testing.T) {
	t.Setenv("HARPORIS_NATS", "nats://override:4222")
	dir := t.TempDir()
	p := filepath.Join(dir, "writer.yaml")
	yaml := `
nats_url: "${HARPORIS_NATS:-nats://fallback:4222}"
output_dir: "${HARPORIS_OUT:-/tmp/findings}"
`
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL != "nats://override:4222" {
		t.Errorf("env override not applied: %q", c.NATSURL)
	}
	if c.OutputDir != "/tmp/findings" {
		t.Errorf("env default not applied: %q", c.OutputDir)
	}
}

func TestSeveritySet_Valid(t *testing.T) {
	c := &Config{Severities: []string{"CRITICAL", "HIGH"}}
	set, err := c.SeveritySet()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_CRITICAL) || !set.Contains(v1.Severity_HIGH) {
		t.Fatalf("expected CRITICAL+HIGH in set")
	}
	if set.Contains(v1.Severity_LOW) {
		t.Fatalf("LOW should be filtered out")
	}
}

func TestSeveritySet_EmptyMeansAll(t *testing.T) {
	c := &Config{Severities: nil}
	set, err := c.SeveritySet()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_LOW) {
		t.Fatalf("empty severities should pass all levels")
	}
}

func TestSeveritySet_UnknownLevelErrors(t *testing.T) {
	c := &Config{Severities: []string{"BOGUS"}}
	if _, err := c.SeveritySet(); err == nil {
		t.Fatalf("expected error for unknown level")
	}
}

func TestLoad_InvalidSeveritiesRejectsEarly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "writer.yaml")
	if err := os.WriteFile(p, []byte("severities: [BOGUS]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatalf("Load should reject an invalid severity level")
	}
}

func TestLoad_ValidSeveritiesParse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "writer.yaml")
	if err := os.WriteFile(p, []byte("severities: [CRITICAL, HIGH]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	set, err := c.SeveritySet()
	if err != nil {
		t.Fatal(err)
	}
	if !set.Contains(v1.Severity_CRITICAL) || set.Contains(v1.Severity_LOW) {
		t.Fatalf("expected CRITICAL kept, LOW filtered")
	}
}
