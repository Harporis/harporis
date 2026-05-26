package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSourceLocal(t *testing.T) {
	s, err := buildSource("/repos/demo", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetLocalPath(); got != "/repos/demo" {
		t.Fatalf("local: %s", got)
	}
}

func TestBuildSourceMutualExclusion(t *testing.T) {
	if _, err := buildSource("/x", "https://y", "", "", ""); err == nil {
		t.Fatal("expected error on local + remote")
	}
}

func TestBuildSourceRemoteToken(t *testing.T) {
	s, err := buildSource("", "https://github.com/x/y.git", "ghp_xxx", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.GetRemote().GetToken() != "ghp_xxx" {
		t.Fatalf("token not set")
	}
}

func TestBuildSourceRemoteSSH(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(key, []byte("PEM-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := buildSource("", "git@github.com:x/y.git", "", key, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.GetRemote().GetSsh().GetPrivateKeyPem(), "PEM-DATA") {
		t.Fatal("ssh key not loaded")
	}
}

func TestScanTypeFromString(t *testing.T) {
	cases := map[string]bool{
		"current_state": true, "full_history": true, "branch_full": true,
		"commit_range": true, "branch_diff": true, "head_diff": true, "staged": true,
		"bogus": false,
	}
	for in, ok := range cases {
		_, got := scanTypeFromString(in)
		if got != ok {
			t.Errorf("%s: want %v got %v", in, ok, got)
		}
	}
}
