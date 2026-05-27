package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL == "" || c.Color == "" || c.DefaultScanType == "" {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestLoadParsesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("nats_url: nats://example:4222\ncolor: never\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL != "nats://example:4222" || c.Color != "never" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestLoadFillsBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("nats_url: \"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL == "" {
		t.Fatal("blank nats_url should be replaced with default")
	}
}
