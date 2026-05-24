package git

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestRevList_All(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a", "1")
	c1 := r.Commit("c1")
	r.Write("a", "2")
	c2 := r.Commit("c2")

	ctx := context.Background()
	shas, err := RevList(ctx, r.Dir, RevListArgs{All: true})
	require.NoError(t, err)
	require.Contains(t, shas, c1)
	require.Contains(t, shas, c2)
}

func TestRevList_Range(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a", "1")
	base := r.Commit("base")
	r.Write("a", "2")
	mid := r.Commit("mid")
	r.Write("a", "3")
	head := r.Commit("head")

	ctx := context.Background()
	shas, err := RevList(ctx, r.Dir, RevListArgs{
		Range: &CommitRange{From: base, To: head},
	})
	require.NoError(t, err)
	require.NotContains(t, shas, base) // exclusive
	require.Contains(t, shas, mid)
	require.Contains(t, shas, head)
}

func TestRevList_Branch(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a", "1")
	r.Commit("main-1")
	r.CreateBranch("feature")
	r.Write("a", "f")
	featSha := r.Commit("feature-1")
	r.Checkout("main")
	r.Write("a", "m")
	mainSha := r.Commit("main-2")

	ctx := context.Background()
	shas, err := RevList(ctx, r.Dir, RevListArgs{Branch: "feature"})
	require.NoError(t, err)
	require.Contains(t, shas, featSha)
	require.NotContains(t, shas, mainSha)
	_ = strings.Contains // satisfy import if unused
}
