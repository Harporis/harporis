package scan

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"

	"github.com/Harporis/harporis/services/getter/internal/chunk"
	"github.com/Harporis/harporis/services/getter/internal/filter"
	"github.com/Harporis/harporis/services/getter/internal/git"
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
	return &Runner{cfg: cfg, scanCtx: NewContext(cfg.ScanID)}
}

func (r *Runner) Run(ctx context.Context) error {
	if err := r.scanCtx.Transition(v1.ScanState_RUNNING); err != nil {
		return err
	}
	r.emitStatus(ctx, v1.ScanState_RUNNING, "scan started")

	defer func() {
		// emit a final status reflecting whatever terminal state we reached
		r.emitStatus(ctx, r.scanCtx.State(), "scan finished")
	}()

	switch r.cfg.WalkMode {
	case "current_state":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkCurrentState}); err != nil {
			return r.finishWith(v1.ScanState_FAILED, err)
		}
	case "full_history":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkFullHistory}); err != nil {
			return r.finishWith(v1.ScanState_FAILED, err)
		}
	case "branch_full":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkBranchFull, Branch: r.cfg.Branch}); err != nil {
			return r.finishWith(v1.ScanState_FAILED, err)
		}
	case "commit_range":
		if err := r.runBlobWalk(ctx, git.WalkArgs{Mode: git.WalkCommitRange, Range: r.cfg.Range}); err != nil {
			return r.finishWith(v1.ScanState_FAILED, err)
		}
	case "branch_diff", "head_diff", "staged":
		if err := r.runDiff(ctx); err != nil {
			return r.finishWith(v1.ScanState_FAILED, err)
		}
	default:
		return r.finishWith(v1.ScanState_FAILED, fmt.Errorf("unknown walk mode %q", r.cfg.WalkMode))
	}
	return r.finishWith(v1.ScanState_COMPLETED, nil)
}

func (r *Runner) runBlobWalk(ctx context.Context, args git.WalkArgs) error {
	jobs := make(chan git.BlobJob, 2*r.cfg.Workers)
	walkErr := make(chan error, 1)
	go func() {
		walkErr <- git.WalkBlobs(ctx, r.cfg.RepoDir, args, jobs)
		close(jobs)
	}()

	// MVP single-worker loop; multi-worker comes after NATS wiring.
	batch, err := git.NewBatch(ctx, r.cfg.RepoDir)
	if err != nil {
		return err
	}
	defer batch.Close()

	for job := range jobs {
		ok, reason := r.cfg.Filter.ShouldScan(job.Path, job.Size, nil)
		if !ok {
			r.scanCtx.BlobsSkipped.Add(1)
			_ = reason // metrics layer wires this up in Task 40
			continue
		}
		if err := r.processBlob(ctx, batch, job); err != nil {
			r.scanCtx.Errors.Add(1)
		} else {
			r.scanCtx.BlobsScanned.Add(1)
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
	if ok, _ := r.cfg.Filter.ShouldScan(job.Path, job.Size, prefix); !ok {
		r.scanCtx.BlobsSkipped.Add(1)
		return nil
	}

	// Build a multi-reader: prefix + remaining stream.
	combined := io.MultiReader(bytesReader(prefix), rc)
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
			return err
		}
		for _, row := range c.Rows {
			r.scanCtx.BytesPublished.Add(int64(len(row.Content)))
		}
	}
	return nil
}

// runDiff handles BRANCH_DIFF / HEAD_DIFF / STAGED — wired in Task 29.
func (r *Runner) runDiff(ctx context.Context) error {
	return fmt.Errorf("diff mode %q not yet implemented", r.cfg.WalkMode)
}

func (r *Runner) finishWith(state v1.ScanState, runErr error) error {
	_ = r.scanCtx.Transition(state)
	return runErr
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
	_ = r.cfg.Publisher.PublishStatus(ctx, ev)
}

// bytesReader is io.Reader over a []byte without depending on bytes.NewReader's
// extra surface (kept inline to make the code self-contained).
func bytesReader(b []byte) io.Reader { return &byteSliceReader{b: b} }

type byteSliceReader struct {
	b   []byte
	off int
}

func (r *byteSliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}
