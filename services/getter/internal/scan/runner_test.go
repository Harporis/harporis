package scan

import (
	"context"
	"fmt"
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
		Workers:            4,
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

func TestRunner_MultiWorker(t *testing.T) {
	r := testutil.NewGitRepo(t)
	for i := 0; i < 20; i++ {
		r.Write(fmt.Sprintf("file-%02d.go", i), fmt.Sprintf("package p\nconst X%d = %q\n", i, fmt.Sprintf("v%d", i)))
	}
	r.Commit("seed")

	pub := &fakePublisher{}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-mw",
		RepoDir:            r.Dir,
		WalkMode:           "current_state",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 1024,
		OverlapLines:       0,
		Workers:            8,
	})
	require.NoError(t, runner.Run(context.Background()))

	// Each file → at least 1 chunk; with 20 distinct contents, expect ≥ 20 unique blob_shas.
	seenBlobs := map[string]bool{}
	for _, c := range pub.chunks {
		seenBlobs[string(c.BlobSha)] = true
	}
	require.GreaterOrEqual(t, len(seenBlobs), 20)
}

type errStatusPublisher struct {
	fakePublisher
	failStatus bool
}

func (p *errStatusPublisher) PublishStatus(ctx context.Context, ev *v1.StatusEvent) error {
	if p.failStatus {
		return fmt.Errorf("simulated status publish failure")
	}
	return p.fakePublisher.PublishStatus(ctx, ev)
}

func TestRunner_StatusPublishFailureDoesNotKillScan(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.go", "package main\n")
	r.Commit("c1")

	pub := &errStatusPublisher{failStatus: true}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-statuserr",
		RepoDir:            r.Dir,
		WalkMode:           "current_state",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 1024,
		Workers:            1,
	})
	// Scan must complete (publishing status fails but doesn't propagate).
	require.NoError(t, runner.Run(context.Background()))
	require.GreaterOrEqual(t, len(pub.chunks), 1)
}

func TestRunner_StagedDiff(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.go", "package main\n")
	r.Commit("base")
	r.Write("a.go", "package main\n// SECRET=AKIAEXAMPLE\nfunc main(){}\n")
	r.Run("add", "a.go") // stage but don't commit

	pub := &fakePublisher{}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-diff-1",
		RepoDir:            r.Dir,
		WalkMode:           "staged",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 4096,
		DiffContextLines:   30,
		Workers:            1,
	})
	require.NoError(t, runner.Run(context.Background()))

	require.GreaterOrEqual(t, len(pub.chunks), 1)
	hadDiff := false
	for _, c := range pub.chunks {
		if c.Kind == v1.ChunkKind_DIFF_WINDOW && c.FilePath == "a.go" {
			hadDiff = true
		}
	}
	require.True(t, hadDiff, "expected DIFF_WINDOW chunk for staged change in a.go")
}

// ctxAwarePublisher honours ctx in PublishStatus — i.e. behaves like the real
// NATS publisher would when the scan ctx is cancelled.
type ctxAwarePublisher struct {
	fakePublisher
}

func (p *ctxAwarePublisher) PublishStatus(ctx context.Context, ev *v1.StatusEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.fakePublisher.PublishStatus(ctx, ev)
}

// The final status event (CANCELLED/FAILED/COMPLETED) must reach downstream
// even when the scan's own ctx was the thing that cancelled the scan.
// Otherwise consumers never learn the scan stopped.
func TestRunner_FinalStatusPublishedAfterCancel(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.go", "package main\n")
	r.Commit("c1")

	pub := &ctxAwarePublisher{}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-cancelled",
		RepoDir:            r.Dir,
		WalkMode:           "current_state",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 1024,
		Workers:            1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Run — every publish on this ctx would fail
	_ = runner.Run(ctx)

	// The deferred final emitStatus must have used a fresh context, otherwise
	// no status event ever reaches the publisher under cancelled-scan path.
	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.NotEmpty(t, pub.statuses, "final status event must be published even on cancelled ctx")
}

// Prometheus counters defined in metrics/metrics.go must actually be
// incremented during a scan — earlier they were declared but never used,
// so /metrics returned mostly zeros regardless of scan activity.
func TestRunner_PrometheusCountersWired(t *testing.T) {
	r := testutil.NewGitRepo(t)
	r.Write("a.go", "package main\n// hello\n")
	r.Write("img.png", "FAKEPNG")
	r.Commit("c1")

	pub := &fakePublisher{}
	flt := &filter.Filter{
		PathExclusions:   []string{".git/"},
		BinaryExtensions: map[string]struct{}{".png": {}},
		MaxFileSize:      int64(10 * 1024 * 1024),
	}
	runner := NewRunner(RunnerConfig{
		ScanID:             "scan-metrics",
		RepoDir:            r.Dir,
		WalkMode:           "current_state",
		Filter:             flt,
		Publisher:          pub,
		RowSizeTargetBytes: 1024,
		Workers:            1,
	})
	require.NoError(t, runner.Run(context.Background()))

	scanned := readCounterValue(t, "harporis_getter_blobs_scanned_total", "scan-metrics", "")
	skipped := readCounterValue(t, "harporis_getter_blobs_skipped_total", "scan-metrics", "binary_extension")
	chunks := readCounterValue(t, "harporis_getter_chunks_published_total", "scan-metrics", "BLOB")
	bytes := readCounterValue(t, "harporis_getter_bytes_published_total", "scan-metrics", "")

	require.GreaterOrEqual(t, scanned, 1.0, "blobs_scanned must reflect actual scans")
	require.GreaterOrEqual(t, skipped, 1.0, "blobs_skipped by binary_extension must count img.png")
	require.GreaterOrEqual(t, chunks, 1.0, "chunks_published must reflect emitted chunks")
	require.Greater(t, bytes, 0.0, "bytes_published must reflect chunk row bytes")
}
