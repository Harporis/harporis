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
