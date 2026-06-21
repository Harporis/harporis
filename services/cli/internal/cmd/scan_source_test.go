package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildSourceLocal(t *testing.T) {
	s, err := buildSource("/repos/demo", "", remoteAuth{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetLocalPath(); got != "/repos/demo" {
		t.Fatalf("local: %s", got)
	}
}

func TestBuildSourceMutualExclusion(t *testing.T) {
	if _, err := buildSource("/x", "https://y", remoteAuth{}); err == nil {
		t.Fatal("expected error on local + remote")
	}
}

func TestBuildSourceRemoteToken(t *testing.T) {
	s, err := buildSource("", "https://github.com/x/y.git", remoteAuth{Token: "ghp_xxx"})
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
	s, err := buildSource("", "git@github.com:x/y.git", remoteAuth{SSHKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.GetRemote().GetSsh().GetPrivateKeyPem(), "PEM-DATA") {
		t.Fatal("ssh key not loaded")
	}
}

func TestBuildSource_BasicAuth(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{User: "alice", Password: "pw"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	b := src.GetRemote().GetBasic()
	if b == nil || b.Username != "alice" || b.Password != "pw" {
		t.Fatalf("basic not built: %+v", src.GetRemote())
	}
}

func TestBuildSource_BearerBuildsAuthorizationHeader(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{Bearer: "jwt123"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	h := src.GetRemote().GetHeader()
	if h == nil || h.Name != "Authorization" || h.Value != "Bearer jwt123" {
		t.Fatalf("bearer header wrong: %+v", h)
	}
}

func TestBuildSource_HeaderParsesNameColonValue(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{Header: "PRIVATE-TOKEN: abc"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	h := src.GetRemote().GetHeader()
	if h == nil || h.Name != "PRIVATE-TOKEN" || h.Value != "abc" {
		t.Fatalf("header parse wrong: %+v", h)
	}
}

func TestBuildSource_HeaderWithoutColonErrors(t *testing.T) {
	if _, err := buildSource("", "https://x/r.git", remoteAuth{Header: "no-colon"}); err == nil {
		t.Fatal("expected error for malformed --remote-header")
	}
}

func TestBuildSource_MultipleMethodsError(t *testing.T) {
	if _, err := buildSource("", "https://x/r.git", remoteAuth{Token: "t", Bearer: "b"}); err == nil {
		t.Fatal("expected error for >1 auth method")
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

// Symlink inside $HOME that resolves OUTSIDE $HOME must be rejected
// even though the user-supplied path appears to be under $HOME.
func TestTranslateLocalPath_SymlinkOutsideHomeRejected(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/home/u/link": true})
	defer func() { hostPathStat = os.Stat }()
	hostEvalSymlinks = func(p string) (string, error) {
		if p == "/home/u/link" {
			return "/etc/secret", nil
		}
		return p, nil
	}
	defer func() { hostEvalSymlinks = filepath.EvalSymlinks }()

	_, err := translateLocalPath("/home/u/link", "/home/u", true)
	if err == nil {
		t.Fatal("expected rejection: symlink resolves outside $HOME")
	}
	if !strings.Contains(err.Error(), "$HOME") {
		t.Fatalf("error must call out $HOME containment: %v", err)
	}
}

// $HOME on a symlinked partition (e.g. /home -> /var/home) must still
// translate paths that live under it.
func TestTranslateLocalPath_SymlinkedHomeStillTranslates(t *testing.T) {
	hostPathStat = stubStat(map[string]bool{"/home/u/code/repo": true})
	defer func() { hostPathStat = os.Stat }()
	hostEvalSymlinks = func(p string) (string, error) {
		// Both abs and home resolve to the same /var/home prefix.
		switch p {
		case "/home/u/code/repo":
			return "/var/home/u/code/repo", nil
		case "/home/u":
			return "/var/home/u", nil
		}
		return p, nil
	}
	defer func() { hostEvalSymlinks = filepath.EvalSymlinks }()

	got, err := translateLocalPath("/home/u/code/repo", "/home/u", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/host/code/repo" {
		t.Fatalf("expected /host/code/repo after symlink resolution; got %q", got)
	}
}

func TestParseGitRange(t *testing.T) {
	cases := []struct {
		in   string
		from string
		to   string
		ok   bool
	}{
		{"abc..def", "abc", "def", true},
		{"main..feature", "main", "feature", true},
		{"abc...def", "", "", false}, // triple-dot rejected
		{"abc..", "", "", false},
		{"..def", "", "", false},
		{"abc", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		from, to, ok := parseGitRange(c.in)
		if ok != c.ok || from != c.from || to != c.to {
			t.Errorf("parseGitRange(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.in, from, to, ok, c.from, c.to, c.ok)
		}
	}
}

func TestApplyRangePresets(t *testing.T) {
	// Helper: build a cobra.Command with the same --type flag the real
	// scan command exposes so cmd.Flags().Changed("type") returns the
	// right value.
	mk := func() *cobra.Command {
		c := &cobra.Command{Use: "scan"}
		var scanType string
		c.Flags().StringVar(&scanType, "type", "current_state", "")
		return c
	}

	t.Run("no preset → noop", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "", "", ""); err != nil {
			t.Fatalf("err: %v", err)
		}
		if st != "current_state" || from != "" || to != "" {
			t.Errorf("unexpected mutation: %s %s %s", st, from, to)
		}
	})

	t.Run("from-init → full_history", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, true, "", "", ""); err != nil {
			t.Fatalf("err: %v", err)
		}
		if st != "full_history" {
			t.Errorf("scanType = %q, want full_history", st)
		}
	})

	t.Run("init-to → commit_range with empty from", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "deadbeef", "", ""); err != nil {
			t.Fatalf("err: %v", err)
		}
		if st != "commit_range" || from != "" || to != "deadbeef" {
			t.Errorf("got (%s, %s, %s); want (commit_range, '', deadbeef)", st, from, to)
		}
	})

	t.Run("--commit <sha> → sha~1..sha", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "", "abc123", ""); err != nil {
			t.Fatalf("err: %v", err)
		}
		if st != "commit_range" || from != "abc123~1" || to != "abc123" {
			t.Errorf("got (%s, %s, %s); want (commit_range, abc123~1, abc123)", st, from, to)
		}
	})

	t.Run("--range A..B → commit_range", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "", "", "main..feature"); err != nil {
			t.Fatalf("err: %v", err)
		}
		if st != "commit_range" || from != "main" || to != "feature" {
			t.Errorf("got (%s, %s, %s)", st, from, to)
		}
	})

	t.Run("invalid --range rejected", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "", "", "abc...def"); err == nil {
			t.Fatal("want error on triple-dot range")
		}
	})

	t.Run("two presets at once rejected", func(t *testing.T) {
		st, from, to := "current_state", "", ""
		if err := applyRangePresets(mk(), &st, &from, &to, true, "abc", "", ""); err == nil {
			t.Fatal("want error when both --from-init and --init-to set")
		}
	})

	t.Run("preset conflicts with --type", func(t *testing.T) {
		c := mk()
		_ = c.Flags().Set("type", "commit_range")
		st, from, to := "commit_range", "", ""
		if err := applyRangePresets(c, &st, &from, &to, true, "", "", ""); err == nil {
			t.Fatal("want error when preset combined with explicit --type")
		}
	})

	t.Run("preset conflicts with --from", func(t *testing.T) {
		st, from, to := "current_state", "abc", ""
		if err := applyRangePresets(mk(), &st, &from, &to, false, "def", "", ""); err == nil {
			t.Fatal("want error when --init-to combined with --from")
		}
	})
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
