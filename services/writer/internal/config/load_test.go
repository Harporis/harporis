package config

import (
	"os"
	"path/filepath"
	"testing"
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
