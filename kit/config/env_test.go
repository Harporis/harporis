package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandEnv_UsesSetValue(t *testing.T) {
	t.Setenv("HARP_TEST_FOO", "bar")
	if got := ExpandEnv("v=${HARP_TEST_FOO:-default}"); got != "v=bar" {
		t.Fatalf("ExpandEnv = %q, want v=bar", got)
	}
}

func TestExpandEnv_FallsBackWhenUnset(t *testing.T) {
	_ = os.Unsetenv("HARP_TEST_UNSET")
	if got := ExpandEnv("v=${HARP_TEST_UNSET:-default}"); got != "v=default" {
		t.Fatalf("ExpandEnv = %q, want v=default", got)
	}
}

func TestExpandEnv_FallsBackWhenEmpty(t *testing.T) {
	t.Setenv("HARP_TEST_EMPTY", "")
	if got := ExpandEnv("v=${HARP_TEST_EMPTY:-fallback}"); got != "v=fallback" {
		t.Fatalf("ExpandEnv = %q, want v=fallback", got)
	}
}

func TestExpandEnv_BareNameNoDefault(t *testing.T) {
	t.Setenv("HARP_TEST_BARE", "set")
	if got := ExpandEnv("v=${HARP_TEST_BARE}"); got != "v=set" {
		t.Fatalf("ExpandEnv = %q, want v=set", got)
	}
}

func TestLoadYAML_RoundTrip(t *testing.T) {
	type cfg struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	t.Setenv("HARP_TEST_NAME", "scanner")
	body := "name: \"${HARP_TEST_NAME:-fallback}\"\nurl: \"${HARP_TEST_URL:-nats://x:4222}\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var c cfg
	if err := LoadYAML(p, &c); err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if c.Name != "scanner" || c.URL != "nats://x:4222" {
		t.Fatalf("got %+v, want scanner / nats://x:4222", c)
	}
}
