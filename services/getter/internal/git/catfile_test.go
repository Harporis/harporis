package git

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestBatchCheck_ReturnsSizeAndType(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "hello world\n")
	r.Commit("a")
	r.Write("b.txt", strings.Repeat("x", 5000))
	r.Commit("b")

	shaA := strings.TrimSpace(r.Run("rev-parse", "HEAD~:a.txt"))
	shaB := strings.TrimSpace(r.Run("rev-parse", "HEAD:b.txt"))

	ctx := context.Background()
	bc, err := NewBatchCheck(ctx, r.Dir)
	require.NoError(t, err)
	defer bc.Close()

	infoA, err := bc.Lookup(shaA)
	require.NoError(t, err)
	require.Equal(t, "blob", infoA.Type)
	require.Equal(t, int64(len("hello world\n")), infoA.Size)

	infoB, err := bc.Lookup(shaB)
	require.NoError(t, err)
	require.Equal(t, int64(5000), infoB.Size)
}

func TestBatchCheck_MissingSHA(t *testing.T) {
	r := testutil.NewGitRepo(t)
	ctx := context.Background()
	bc, err := NewBatchCheck(ctx, r.Dir)
	require.NoError(t, err)
	defer bc.Close()

	_, err = bc.Lookup("0000000000000000000000000000000000000000")
	require.Error(t, err)
}

func TestBatch_FetchesBlobContent(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "hello\nworld\n")
	r.Commit("a")
	sha := strings.TrimSpace(r.Run("rev-parse", "HEAD:a.txt"))

	ctx := context.Background()
	bat, err := NewBatch(ctx, r.Dir)
	require.NoError(t, err)
	defer bat.Close()

	blob, err := bat.Read(sha)
	require.NoError(t, err)
	defer blob.Close()

	buf, err := io.ReadAll(blob)
	require.NoError(t, err)
	require.Equal(t, "hello\nworld\n", string(buf))
}
