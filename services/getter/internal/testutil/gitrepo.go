package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// GitRepo wraps a temp git working tree for use in tests.
type GitRepo struct {
	Dir string
	t   *testing.T
}

// NewGitRepo initialises a fresh repo in t.TempDir() with an initial
// empty commit on `main`.
func NewGitRepo(t *testing.T) *GitRepo {
	t.Helper()
	dir := t.TempDir()
	r := &GitRepo{Dir: dir, t: t}
	r.Run("init", "-b", "main")
	r.Run("config", "user.email", "test@example.com")
	r.Run("config", "user.name", "Test User")
	r.Run("config", "commit.gpgsign", "false")
	r.Run("commit", "--allow-empty", "-m", "init")
	return r
}

// Write writes content to a relative path inside the repo (creating dirs).
func (r *GitRepo) Write(rel, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, rel)
	require.NoError(r.t, ensureDir(filepath.Dir(full)))
	require.NoError(r.t, writeFile(full, []byte(content)))
}

// Commit stages everything and records a commit, returning the new SHA.
func (r *GitRepo) Commit(message string) string {
	r.t.Helper()
	r.Run("add", "-A")
	r.Run("commit", "-m", message)
	out := r.Run("rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

// CreateBranch creates and checks out a new branch from current HEAD.
func (r *GitRepo) CreateBranch(name string) {
	r.t.Helper()
	r.Run("checkout", "-b", name)
}

// Checkout switches to an existing branch or commit.
func (r *GitRepo) Checkout(ref string) {
	r.t.Helper()
	r.Run("checkout", ref)
}

// Run executes a git command in the repo directory and returns stdout.
// Fails the test on error.
func (r *GitRepo) Run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), string(out))
	return string(out)
}

func ensureDir(p string) error            { return os.MkdirAll(p, 0o755) }
func writeFile(p string, b []byte) error  { return os.WriteFile(p, b, 0o644) }
