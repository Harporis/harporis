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

// stubStat lets tests pretend a set of paths exists on the host
// without touching the filesystem.
func stubStat(existing map[string]bool) func(string) (os.FileInfo, error) {
	return func(p string) (os.FileInfo, error) {
		if existing[p] {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}
}

func TestTranslateLocalPath_EmptyPassthrough(t *testing.T) {
	hostPathStat = stubStat(nil)
	defer func() { hostPathStat = os.Stat }()
	got, err := translateLocalPath("", "/home/u", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestTranslateLocalPath_NoMountHostPassthrough(t *testing.T) {
	hostPathStat = stubStat(nil)
	defer func() { hostPathStat = os.Stat }()
	got, err := translateLocalPath("/home/u/code/repo", "/home/u", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/u/code/repo" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

// Paths that don't exist on the host are assumed container-native and
// pass through unchanged — this matches the override-file workflow.
func TestTranslateLocalPath_ContainerNativeNotOnHost(t *testing.T) {
	hostPathStat = stubStat(nil)
	defer func() { hostPathStat = os.Stat }()
	for _, p := range []string{"/repos/leaky", "/repos/anything", "/var/cache/foo"} {
		got, err := translateLocalPath(p, "/home/u", true)
		if err != nil {
			t.Errorf("%s: err=%v", p, err)
		}
		if got != p {
			t.Errorf("%s: translated to %q, want unchanged", p, got)
		}
	}
}

func TestTranslateLocalPath_HomeTranslated(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/home/u/code/repo": true})
	defer func() { hostPathStat = os.Stat }()
	got, err := translateLocalPath("/home/u/code/repo", "/home/u", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/host/code/repo" {
		t.Fatalf("expected /host/code/repo, got %q", got)
	}
}

// A directory whose name starts with two dots (~/..staging) is a valid
// $HOME-relative path and must translate, not be rejected.
func TestTranslateLocalPath_DotDotPrefixedNameAllowed(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/home/u/..staging/repo": true})
	defer func() { hostPathStat = os.Stat }()
	got, err := translateLocalPath("/home/u/..staging/repo", "/home/u", true)
	if err != nil {
		t.Fatalf("..staging should be allowed: %v", err)
	}
	if got != "/host/..staging/repo" {
		t.Fatalf("got %q", got)
	}
}

func TestTranslateLocalPath_OutsideHomeButOnHostRejected(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/srv/elsewhere": true})
	defer func() { hostPathStat = os.Stat }()
	_, err := translateLocalPath("/srv/elsewhere", "/home/u", true)
	if err == nil {
		t.Fatal("expected error for host path outside $HOME")
	}
	if !strings.Contains(err.Error(), "--no-mount-host") {
		t.Fatalf("error message should mention the opt-out flag: %v", err)
	}
}

func TestTranslateLocalPath_EmptyHomeRejected(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/some/path": true})
	defer func() { hostPathStat = os.Stat }()
	_, err := translateLocalPath("/some/path", "", true)
	if err == nil {
		t.Fatal("expected error when $HOME is empty and host path exists")
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
