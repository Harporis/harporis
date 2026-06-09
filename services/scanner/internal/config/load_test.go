package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_AppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(p, []byte("nats_url: nats://x:4222\n"), 0o644)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.NATSURL != "nats://x:4222" {
		t.Errorf("NATSURL = %q", cfg.NATSURL)
	}
	if cfg.Workers == 0 {
		t.Errorf("Workers default not applied")
	}
	if cfg.FetchBatch <= 0 || cfg.AckWaitSeconds <= 0 {
		t.Errorf("numeric defaults missing")
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(p, []byte("nats_url: ${NATS_URL:-nats://default:4222}\nlog_level: info\n"), 0o644)
	t.Setenv("NATS_URL", "nats://overridden:4222")
	cfg, _ := Load(p)
	if cfg.NATSURL != "nats://overridden:4222" {
		t.Errorf("env override failed: %q", cfg.NATSURL)
	}
}

func TestLoad_DefaultFile(t *testing.T) {
	// The real config/scanner.yaml file ships with the binary; sanity check it parses.
	cfg, err := Load("../../config/scanner.yaml")
	if err != nil {
		t.Fatalf("Load default file: %v", err)
	}
	if cfg.NATSURL == "" {
		t.Errorf("default scanner.yaml has empty NATSURL")
	}
}
