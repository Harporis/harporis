package scan

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

	"github.com/Harporis/harporis/services/getter/internal/filter"
	"github.com/Harporis/harporis/services/getter/internal/testutil"
)

// fakePublisher records every chunk and status event in memory.
type fakePublisher struct {
	mu       sync.Mutex
	chunks   []*v1.GitRowChunk
	statuses []*v1.StatusEvent
}

func (p *fakePublisher) PublishChunk(_ context.Context, ch *v1.GitRowChunk) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chunks = append(p.chunks, ch)
	return nil
}
func (p *fakePublisher) PublishStatus(_ context.Context, ev *v1.StatusEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses = append(p.statuses, ev)
	return nil
}

func TestRunner_CurrentState_EndToEnd(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.go", "package main\nfunc main() {}\n")
	r.Write("README.md", "# hello\n")
	r.Commit("seed")

	pub := &fakePublisher{}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-int-1",
		RepoDir:            r.Dir,
		WalkMode:           "current_state",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 1024,
		OverlapLines:       0,
		Workers:            1,
	})

	ctx := context.Background()
	require.NoError(t, runner.Run(ctx))

	// Each file → at least 1 chunk; chunk_count consistent within each blob.
	require.GreaterOrEqual(t, len(pub.chunks), 2)
	paths := map[string]bool{}
	for _, c := range pub.chunks {
		require.Equal(t, v1.ChunkKind_BLOB, c.Kind)
		require.NotEmpty(t, c.BlobSha)
		for _, ref := range c.Refs {
			paths[ref.Path] = true
		}
	}
	require.True(t, paths["a.go"])
	require.True(t, paths["README.md"])

	// First status = RUNNING, last = COMPLETED.
	require.GreaterOrEqual(t, len(pub.statuses), 2)
	require.Equal(t, v1.ScanState_RUNNING, pub.statuses[0].State)
	require.Equal(t, v1.ScanState_COMPLETED, pub.statuses[len(pub.statuses)-1].State)
}
