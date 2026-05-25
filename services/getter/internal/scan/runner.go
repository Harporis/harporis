package scan

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

	"github.com/Harporis/harporis/services/getter/internal/chunk"
	"github.com/Harporis/harporis/services/getter/internal/filter"
	"github.com/Harporis/harporis/services/getter/internal/git"
	"github.com/Harporis/harporis/services/getter/internal/metrics"
)

// Publisher is implemented by the NATS publisher (and by test fakes).
type Publisher interface {
	PublishChunk(ctx context.Context, c *v1.GitRowChunk) error
	PublishStatus(ctx context.Context, ev *v1.StatusEvent) error
}

type RunnerConfig struct {
	ScanID             string
	RepoDir            string
	WalkMode           string // "current_state" | "full_history" | "branch_full" | "commit_range" | "branch_diff" | "head_diff" | "staged"
	Branch             string
	BaseBranch         string
	Range              *git.CommitRange
	Filter             *filter.Filter
	Publisher          Publisher
	RowSizeTargetBytes int
	OverlapLines       int
	DiffContextLines   int
	Workers            int
	Output             *v1.OutputConfig
}

type Runner struct {
	cfg     RunnerConfig
	scanCtx *Context
}

func NewRunner(cfg RunnerConfig) *Runner {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	// Idempotent: ensures metric counters are registered even when the runner
	// is exercised outside of main (e.g. in tests).
	metrics.Init()
	return &Runner{cfg: cfg, scanCtx: NewContext(cfg.ScanID)}
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.scanCtx.Transition(v1.ScanState_RUNNING); err != nil {
		return err
	}
	r.emitStatus(ctx, v1.ScanState_RUNNING, "scan started")
	startedAt := time.Now()

	defer func() {
		// Final status must reach downstream even if the scan was killed by
		// ctx cancellation — use a fresh context so the publish is not
		// short-circuited by the already-cancelled scan ctx.
		finalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		final := r.scanCtx.State()
		metrics.ScanDuration.WithLabelValues(r.cfg.ScanID, final.String()).Observe(time.Since(startedAt).Seconds())
		msg := "scan finished"
		if final == v1.ScanState_CANCELLED {
			if reason := r.scanCtx.CancelReason(); reason != "" {
				msg = "scan cancelled: " + reason
			} else {
				msg = "scan cancelled"
			}
		}
		r.emitStatus(finalCtx, final, msg)
	}()

	switch r.cfg.WalkMode {
	case "current_state":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkCurrentState}); err != nil {
			return r.finishWith(terminalFor(ctx, err), err)
		}
	case "full_history":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkFullHistory}); err != nil {
			return r.finishWith(terminalFor(ctx, err), err)
		}
	case "branch_full":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkBranchFull, Branch: r.cfg.Branch}); err != nil {
			return r.finishWith(terminalFor(ctx, err), err)
		}
	case "commit_range":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkCommitRange, Range: r.cfg.Range}); err != nil {
			return r.finishWith(terminalFor(ctx, err), err)
		}
	case "branch_diff", "head_diff", "staged":
		if err := r.runDiff(ctx); err != nil {
			return r.finishWith(terminalFor(ctx, err), err)
		}
	default:
		return r.finishWith(v1.ScanState_FAILED, fmt.Errorf("unknown walk mode %q", r.cfg.WalkMode))
	}
	return r.finishWith(v1.ScanState_COMPLETED, nil)
}

func (r *Runner) runBlobWalk(ctx context.Context, args git.WalkArgs) error {
	workers := r.cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	walkCtx, walkCancel := context.WithCancel(ctx)
	defer walkCancel()

	jobs := make(chan git.BlobJob, 2*workers)

	walkErr := make(chan error, 1)
	go func() {
		walkErr <- git.WalkBlobs(walkCtx, r.cfg.RepoDir, args, jobs)
		close(jobs)
	}()

	// Worker pool: each owns its own cat-file subprocess.
	var wg sync.WaitGroup
	workerErrs := make(chan error, workers)
	// Each worker writes exactly once to spawnSig: true on cat-file spawn
	// success, false on failure. A coordinator goroutine waits on this and
	// cancels the walker iff *all* workers fail to spawn — replacing a
	// previous hard-coded 100ms watchdog that could fire prematurely on
	// cold-start containers.
	spawnSig := make(chan bool, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch, err := git.NewBatch(ctx, r.cfg.RepoDir)
			if err != nil {
				spawnSig <- false
				workerErrs <- fmt.Errorf("worker: spawn cat-file: %w", err)
				return
			}
			spawnSig <- true
			defer batch.Close()
			for job := range jobs {
				ok, reason := r.cfg.Filter.ShouldScan(job.Path, job.Size, nil)
				if !ok {
					r.scanCtx.BlobsSkipped.Add(1)
					metrics.BlobsSkipped.WithLabelValues(r.cfg.ScanID, string(reason)).Inc()
					continue
				}
				if err := r.processBlob(ctx, batch, job); err != nil {
					r.scanCtx.Errors.Add(1)
					metrics.ErrorsTotal.WithLabelValues(r.cfg.ScanID, "process_blob").Inc()
				} else {
					r.scanCtx.BlobsScanned.Add(1)
					metrics.BlobsScanned.WithLabelValues(r.cfg.ScanID).Inc()
				}
			}
		}()
	}

	go func() {
		if !waitFirstSpawn(spawnSig, workers) {
			walkCancel()
		}
	}()

	wg.Wait()
	close(workerErrs)

	for werr := range workerErrs {
		if werr != nil {
			return werr
		}
	}
	return <-walkErr
}

func (r *Runner) processBlob(ctx context.Context, batch *git.Batch, job git.BlobJob) error {
	rc, err := batch.Read(job.SHA)
	if err != nil {
		return err
	}
	defer rc.Close()

	// NUL sniff on first 8 KiB.
	prefix := make([]byte, 8192)
	n, _ := io.ReadFull(rc, prefix)
	prefix = prefix[:n]
	if ok, reason := r.cfg.Filter.ShouldScan(job.Path, job.Size, prefix); !ok {
		r.scanCtx.BlobsSkipped.Add(1)
		metrics.BlobsSkipped.WithLabelValues(r.cfg.ScanID, string(reason)).Inc()
		return nil
	}

	// Build a multi-reader: prefix + remaining stream.
	combined := io.MultiReader(bytes.NewReader(prefix), rc)
	scanner := chunk.NewLineScanner(combined, 1<<24)
	builder := chunk.NewBuilder(chunk.BuilderConfig{
		ScanID:             r.cfg.ScanID,
		RowSizeTargetBytes: r.cfg.RowSizeTargetBytes,
		OverlapLines:       r.cfg.OverlapLines,
	})
	shaBytes, err := hex.DecodeString(job.SHA)
	if err != nil {
		return fmt.Errorf("decode blob sha %q: %w", job.SHA, err)
	}
	builder.Begin(chunk.BlobSource(shaBytes, job.Refs))

	var lineNo int32
	var offset int64
	for scanner.Scan() {
		lineNo++
		b := scanner.Bytes()
		if err := builder.AddLine(lineNo, offset, b); err != nil {
			return err
		}
		offset += int64(len(b)) + 1 // include the \n
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	chunks, err := builder.Finish()
	if err != nil {
		return err
	}
	for _, c := range chunks {
		c.SequenceNumber = r.scanCtx.ChunksPublished.Add(1) - 1
		if err := r.cfg.Publisher.PublishChunk(ctx, c); err != nil {
			metrics.ErrorsTotal.WithLabelValues(r.cfg.ScanID, "publish_chunk").Inc()
			return err
		}
		metrics.ChunksPublished.WithLabelValues(r.cfg.ScanID, c.Kind.String()).Inc()
		var chunkBytes int64
		for _, row := range c.Rows {
			chunkBytes += int64(len(row.Content))
		}
		r.scanCtx.BytesPublished.Add(chunkBytes)
		metrics.BytesPublished.WithLabelValues(r.cfg.ScanID).Add(float64(chunkBytes))
	}
	return nil
}

// runDiff handles BRANCH_DIFF / HEAD_DIFF / STAGED.
func (r *Runner) runDiff(ctx context.Context) error {
	var extra []string
	switch r.cfg.WalkMode {
	case "branch_diff":
		if r.cfg.BaseBranch == "" || r.cfg.Branch == "" {
			return fmt.Errorf("branch_diff requires Branch and BaseBranch")
		}
		extra = []string{fmt.Sprintf("%s..%s", r.cfg.BaseBranch, r.cfg.Branch)}
	case "head_diff":
		// unstaged: no extra args
	case "staged":
		extra = []string{"--cached"}
	default:
		return fmt.Errorf("unknown diff mode %q", r.cfg.WalkMode)
	}
	commitSHA, err := git.ResolveHead(ctx, r.cfg.RepoDir)
	if err != nil {
		return fmt.Errorf("resolve HEAD for diff scan: %w", err)
	}
	raw, err := git.RunDiff(ctx, r.cfg.RepoDir, r.cfg.DiffContextLines, extra...)
	if err != nil {
		return fmt.Errorf("git diff: %w", err)
	}
	patches, err := git.ParseUnifiedDiff(raw)
	if err != nil {
		return err
	}
	for _, p := range patches {
		if p.Deleted {
			continue
		}
		ok, reason := r.cfg.Filter.ShouldScan(p.Path, 0, nil)
		if !ok {
			r.scanCtx.BlobsSkipped.Add(1)
			metrics.BlobsSkipped.WithLabelValues(r.cfg.ScanID, string(reason)).Inc()
			continue
		}
		if err := r.publishDiffPatch(ctx, commitSHA, p); err != nil {
			r.scanCtx.Errors.Add(1)
			metrics.ErrorsTotal.WithLabelValues(r.cfg.ScanID, "publish_diff").Inc()
		} else {
			r.scanCtx.BlobsScanned.Add(1)
			metrics.BlobsScanned.WithLabelValues(r.cfg.ScanID).Inc()
		}
	}
	return nil
}

func (r *Runner) publishDiffPatch(ctx context.Context, commitSHA []byte, p git.Patch) error {
	for _, h := range p.Hunks {
		builder := chunk.NewBuilder(chunk.BuilderConfig{
			ScanID:             r.cfg.ScanID,
			RowSizeTargetBytes: r.cfg.RowSizeTargetBytes,
			OverlapLines:       r.cfg.OverlapLines,
		})
		builder.Begin(chunk.DiffWindowSource(commitSHA, p.Path,
			int32(r.cfg.DiffContextLines), int32(r.cfg.DiffContextLines)))
		for _, line := range h.Lines {
			if line.Op == '-' {
				continue // dropped lines aren't in the new file
			}
			if err := builder.AddLine(line.NewLine, 0, []byte(line.Text)); err != nil {
				return err
			}
		}
		chunks, err := builder.Finish()
		if err != nil {
			return err
		}
		for _, c := range chunks {
			c.SequenceNumber = r.scanCtx.ChunksPublished.Add(1) - 1
			if err := r.cfg.Publisher.PublishChunk(ctx, c); err != nil {
				metrics.ErrorsTotal.WithLabelValues(r.cfg.ScanID, "publish_chunk").Inc()
				return err
			}
			metrics.ChunksPublished.WithLabelValues(r.cfg.ScanID, c.Kind.String()).Inc()
			var chunkBytes int64
			for _, row := range c.Rows {
				chunkBytes += int64(len(row.Content))
			}
			r.scanCtx.BytesPublished.Add(chunkBytes)
			metrics.BytesPublished.WithLabelValues(r.cfg.ScanID).Add(float64(chunkBytes))
		}
	}
	return nil
}

func (r *Runner) finishWith(state v1.ScanState, runErr error) error {
	_ = r.scanCtx.Transition(state)
	return runErr
}

// terminalFor maps an error from a scan stage to its terminal scan state.
// A cancelled / deadline-exceeded ctx means the scan was aborted from
// outside (operator CancelScanRequest, or process shutdown) — that's
// CANCELLED. Anything else is a real failure.
func terminalFor(ctx context.Context, err error) v1.ScanState {
	if err == nil {
		return v1.ScanState_COMPLETED
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return v1.ScanState_CANCELLED
	}
	if ctx.Err() != nil {
		// Stage returned a non-ctx error but the ctx is dead — most likely
		// the stage observed the cancellation through a different code path
		// (e.g. closed channel). Still a cancellation.
		return v1.ScanState_CANCELLED
	}
	return v1.ScanState_FAILED
}

func (r *Runner) emitStatus(ctx context.Context, state v1.ScanState, msg string) {
	ev := &v1.StatusEvent{
		ScanId:       r.cfg.ScanID,
		State:        state,
		Timestamp:    time.Now().Unix(),
		Message:      msg,
		Metrics:      r.scanCtx.Snapshot(),
		OutputConfig: r.cfg.Output,
	}
	if err := r.cfg.Publisher.PublishStatus(ctx, ev); err != nil {
		slog.Warn("status publish failed", "scan_id", r.cfg.ScanID, "state", state.String(), "err", err)
		metrics.StatusPublishErrors.WithLabelValues(r.cfg.ScanID).Inc()
	}
}

