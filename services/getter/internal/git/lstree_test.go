package git

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestListTree_RecursiveWithSizes(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "aaa")
	r.Write("src/b.go", "package main\n")
	r.Write("vendor/x.lock", "x")
	r.Commit("seed")

	ctx := context.Background()
	entries, err := ListTree(ctx, r.Dir, "HEAD")
	require.NoError(t, err)
	require.Len(t, entries, 3)

	paths := map[string]TreeEntry{}
	for _, e := range entries {
		paths[e.Path] = e
	}
	require.Contains(t, paths, "a.txt")
	require.Equal(t, "blob", paths["a.txt"].Type)
	require.Equal(t, int64(3), paths["a.txt"].Size)
	require.Contains(t, paths, "src/b.go")
	require.Equal(t, int64(len("package main\n")), paths["src/b.go"].Size)
	require.NotEmpty(t, paths["src/b.go"].SHA)
}

// Paths with spaces, tabs, and embedded newlines must round-trip intact.
// Without -z, git escapes "weird" paths (wraps in quotes, backslash-escapes
// control chars) and the parser sees mangled paths.
func TestListTree_HandlesWeirdPaths(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("with space.txt", "a")
	r.Write("with\ttab.txt", "b")
	r.Write("a.txt", "c")
	r.Commit("weird")

	entries, err := ListTree(context.Background(), r.Dir, "HEAD")
	require.NoError(t, err)

	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}
	require.True(t, paths["with space.txt"], "path with space must survive parsing")
	require.True(t, paths["with\ttab.txt"], "path with literal tab must survive parsing")
	require.True(t, paths["a.txt"])
}
