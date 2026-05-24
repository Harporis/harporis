package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestPrepareRepo_LocalPath(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("hello.txt", "hi")
	r.Commit("add hello")

	ctx := context.Background()
	work, cleanup, err := PrepareRepo(ctx, LocalSource{Path: r.Dir}, "")
	require.NoError(t, err)
	defer cleanup()

	require.Equal(t, r.Dir, work, "local path should be used in place")
	require.FileExists(t, filepath.Join(work, "hello.txt"))
}

func TestPrepareRepo_LocalPath_NotARepo(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	_, _, err := PrepareRepo(ctx, LocalSource{Path: dir}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a git repository")
}

func TestPrepareRepo_RemoteFileURL(t *testing.T) {
	// Use a local repo as a fake "remote" via file:// — git supports this natively.
	upstream := testutil.NewGitRepo(t)
	upstream.Write("a.go", "package main\n")
	upstream.Commit("seed")

	workspace := t.TempDir()
	ctx := context.Background()
	url := "file://" + upstream.Dir
	work, cleanup, err := PrepareRepo(ctx, RemoteSource{URL: url}, workspace)
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
