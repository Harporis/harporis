package git

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

func TestResolveHead(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "hi")
	want := r.Commit("c1")

	sha, err := ResolveHead(context.Background(), r.Dir)
	require.NoError(t, err)
	require.Len(t, sha, 20, "SHA-1 is 20 bytes")
	require.Equal(t, want, hex.EncodeToString(sha))
}

func TestWalkBlobs_CurrentState_DedupsAndEmitsAll(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "alpha")
	r.Write("dir/b.txt", "beta")
	r.Commit("seed")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan BlobJob, 8)
	errCh := make(chan error, 1)
	go func() {
		errCh <- WalkBlobs(ctx, r.Dir, WalkArgs{Mode: WalkCurrentState}, jobs)
		close(jobs)
	}()

	got := map[string]BlobJob{}
	for job := range jobs {
		got[job.Path] = job
	}
	require.NoError(t, <-errCh)
	require.Len(t, got, 2)
	require.NotEmpty(t, got["a.txt"].SHA)
	require.NotEmpty(t, got["dir/b.txt"].SHA)
}

func TestWalkBlobs_FullHistory_DedupsAcrossCommits(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.txt", "v1")
	r.Commit("c1")
	r.Write("a.txt", "v2")
	r.Commit("c2") // 2nd distinct blob
	r.Write("b.txt", "static")
	r.Commit("c3")
	// "amend" by re-staging same content shouldn't add a new blob.
	// (Use an empty commit because re-writing identical content has no diff.)
	r.Write("b.txt", "static")
	r.Run("add", "-A")
	r.Run("commit", "--allow-empty", "-m", "c4-noop-content")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan BlobJob, 16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- WalkBlobs(ctx, r.Dir, WalkArgs{Mode: WalkFullHistory}, jobs)
		close(jobs)
	}()

	uniqSHAs := map[string]struct{}{}
	for job := range jobs {
		uniqSHAs[job.SHA] = struct{}{}
	}
	require.NoError(t, <-errCh)
	// a.txt has 2 distinct contents (v1, v2), b.txt has 1 → 3 unique blobs
	require.Len(t, uniqSHAs, 3)
}
