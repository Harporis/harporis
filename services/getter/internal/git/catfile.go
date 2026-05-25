package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// ObjectInfo is the metadata for a git object.
type ObjectInfo struct {
	SHA  string
	Type string // "blob", "tree", "commit", "tag", or "missing"
	Size int64
}

// BatchCheck wraps a long-running `git cat-file --batch-check` process.
// One process per scan worker; thread-safe lookups (calls are serialised
// via an internal mutex because cat-file is line-protocol).
type BatchCheck struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	mu     sync.Mutex
}

func NewBatchCheck(ctx context.Context, repoDir string) (*BatchCheck, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "cat-file",
		"--batch-check=%(objectname) %(objecttype) %(objectsize)")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// Capture stderr so a dying cat-file leaves a trail in Close()'s error.
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &BatchCheck{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr}, nil
}

func (b *BatchCheck) Lookup(sha string) (ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := fmt.Fprintln(b.stdin, sha); err != nil {
		return ObjectInfo{}, err
	}
	line, err := b.stdout.ReadString('\n')
	if err != nil {
		return ObjectInfo{}, err
	}
	line = strings.TrimRight(line, "\n")
	// Format: "<sha> <type> <size>" OR "<sha> missing"
	parts := strings.Fields(line)
	if len(parts) == 2 && parts[1] == "missing" {
		return ObjectInfo{}, fmt.Errorf("object %s missing", sha)
	}
	if len(parts) != 3 {
		return ObjectInfo{}, fmt.Errorf("unexpected batch-check output: %q", line)
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("parse size: %w", err)
	}
	return ObjectInfo{SHA: parts[0], Type: parts[1], Size: size}, nil
}

func (b *BatchCheck) Close() error {
	if err := b.stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	if err := b.cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(b.stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// Batch wraps `git cat-file --batch`. Returns content as an io.ReadCloser
// that must be drained-and-closed before the next Read call.
type Batch struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	mu     sync.Mutex
}

func NewBatch(ctx context.Context, repoDir string) (*Batch, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Batch{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr}, nil
}

// Read sends sha to git cat-file and returns a reader scoped to that blob's
// size. Caller MUST consume it fully and Close it before next Read.
func (b *Batch) Read(sha string) (io.ReadCloser, error) {
	b.mu.Lock()
	if _, err := fmt.Fprintln(b.stdin, sha); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	header, err := b.stdout.ReadString('\n')
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	parts := strings.Fields(strings.TrimRight(header, "\n"))
	if len(parts) == 2 && parts[1] == "missing" {
		b.mu.Unlock()
		return nil, fmt.Errorf("object %s missing", sha)
	}
	if len(parts) != 3 {
		b.mu.Unlock()
		return nil, fmt.Errorf("unexpected cat-file header: %q", header)
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("parse size: %w", err)
	}
	return &boundedReader{r: b.stdout, remaining: size, parent: b}, nil
}

func (b *Batch) Close() error {
	if err := b.stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	if err := b.cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(b.stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// boundedReader exposes exactly N bytes from the parent reader, then consumes
// the trailing newline that cat-file emits between objects and releases the
// parent mutex.
type boundedReader struct {
	r         *bufio.Reader
	remaining int64
	parent    *Batch
	closed    bool
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *boundedReader) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	// Drain any unread bytes + the trailing newline so the next Read sees a clean state.
	if b.remaining > 0 {
		if _, err := io.CopyN(io.Discard, b.r, b.remaining); err != nil {
			b.parent.mu.Unlock()
			return err
		}
	}
	if _, err := b.r.ReadByte(); err != nil {
		b.parent.mu.Unlock()
		return err
	}
	b.parent.mu.Unlock()
	return nil
}
