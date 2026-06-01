package version

import "testing"

func TestString_DefaultDev(t *testing.T) {
	got := String()
	if got != "scanner/dev" {
		t.Errorf("String() = %q, want %q", got, "scanner/dev")
	}
}

func TestString_OverriddenByLDFlags(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "v0.1.0"
	got := String()
	if got != "scanner/v0.1.0" {
		t.Errorf("String() = %q, want %q", got, "scanner/v0.1.0")
	}
}
