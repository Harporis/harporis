package git

import (
	"bufio"
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
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &BatchCheck{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}, nil
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
	return b.cmd.Wait()
}
