package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestPrepareRepo_LocalPath(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("hello.txt", "hi")
	r.Commit("add hello")

	ctx := context.Background()
	work, cleanup, err := PrepareRepo(ctx, LocalSource{Path: r.Dir}, "", 0)
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, r.Dir, work, "local path should be used in place")
	require.FileExists(t, filepath.Join(work, "hello.txt"))
}

func TestPrepareRepo_LocalPath_NotARepo(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	_, _, err := PrepareRepo(ctx, LocalSource{Path: dir}, "", 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a git repository")
}

func TestAuthMode_HeaderSelected(t *testing.T) {
	src := RemoteSource{URL: "https://example.com/r.git", Header: struct{ Name, Value string }{Name: "Authorization", Value: "Bearer xyz"}}
	if got := authMode(src); got != authHTTPSHeader {
		t.Fatalf("authMode = %v, want authHTTPSHeader", got)
	}
}

func TestBuildCloneCommand_HeaderUsesGitConfigEnvNotArgv(t *testing.T) {
	src := RemoteSource{URL: "https://example.com/r.git", Header: struct{ Name, Value string }{Name: "PRIVATE-TOKEN", Value: "s3cr3t"}}
	cc, err := buildCloneCommand(src, "/tmp/dest")
	if err != nil {
		t.Fatalf("buildCloneCommand: %v", err)
	}
	defer cc.Cleanup()
	// secret must NOT be in argv
	for _, a := range cc.Args {
		if strings.Contains(a, "s3cr3t") {
			t.Fatalf("secret leaked into argv: %v", cc.Args)
		}
	}
	env := strings.Join(cc.Env, "\n")
	for _, want := range []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=PRIVATE-TOKEN: s3cr3t"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q; got:\n%s", want, env)
		}
	}
	// URL must be unchanged (header is not embedded in URL)
	if cc.Args[len(cc.Args)-2] != "https://example.com/r.git" {
		t.Fatalf("URL altered: %v", cc.Args)
	}
}

func TestRedactSecrets_StripsHeaderValue(t *testing.T) {
	src := RemoteSource{Header: struct{ Name, Value string }{Name: "Authorization", Value: "Bearer leaky"}}
	out := redactSecrets(src, "fatal: auth failed with Bearer leaky")
	if strings.Contains(out, "leaky") {
		t.Fatalf("header value not redacted: %q", out)
	}
}

func TestPrepareRepo_RemoteFileURL(t *testing.T) {
	// Use a local repo as a fake "remote" via file:// — git supports this natively.
	upstream := testutil.NewGitRepo(t)
	upstream.Write("a.go", "package main\n")
	upstream.Commit("seed")

	workspace := t.TempDir()
	ctx := context.Background()
	url := "file://" + upstream.Dir
	work, cleanup, err := PrepareRepo(ctx, RemoteSource{URL: url}, workspace, 0)
	require.NoError(t, err)
	defer cleanup()

	require.NotEqual(t, upstream.Dir, work)
	require.FileExists(t, filepath.Join(work, "a.go"))
	require.DirExists(t, filepath.Join(work, ".git"))
	// Cleanup removes the clone directory.
	cleanup()
	_, err = os.Stat(work)
	require.True(t, os.IsNotExist(err))
}
