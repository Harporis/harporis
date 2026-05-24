package git

import (
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

type WalkMode int

const (
	WalkCurrentState WalkMode = iota + 1
	WalkFullHistory
	WalkBranchFull
	WalkCommitRange
)

type WalkArgs struct {
	Mode   WalkMode
	Branch string       // for WalkBranchFull
	Range  *CommitRange // for WalkCommitRange
}

// BlobJob is one unit of work shipped to a chunk worker. The SHA is
// guaranteed unique within a single WalkBlobs invocation (dedup by sha).
// SHA is hex; the chunk worker decodes to bytes for the wire.
type BlobJob struct {
	SHA  string
	Size int64
	Path string              // first-seen path (MVP: not full refs list)
	Refs []*v1.CommitFileRef // MVP: 1 entry for first-seen (commit, path)
}

// resolveHead returns the hex commit SHA that HEAD currently points to.
func resolveHead(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// decodeCommitSHA converts a hex commit SHA into its raw byte representation
// for the proto CommitFileRef.commit_sha field (which is bytes).
func decodeCommitSHA(hexSHA string) ([]byte, error) {
	b, err := hex.DecodeString(hexSHA)
	if err != nil {
		return nil, fmt.Errorf("decode commit sha %q: %w", hexSHA, err)
	}
	return b, nil
}

// WalkBlobs emits unique blob jobs to the jobs channel based on Mode.
// Closes nothing — caller owns the channel.
func WalkBlobs(ctx context.Context, repoDir string, args WalkArgs, jobs chan<- BlobJob) error {
	switch args.Mode {
	case WalkCurrentState:
		entries, err := ListTree(ctx, repoDir, "HEAD")
		if err != nil {
			return err
		}
		headHex, err := resolveHead(ctx, repoDir)
		if err != nil {
			return err
		}
		headBytes, err := decodeCommitSHA(headHex)
		if err != nil {
			return err
		}
		seen := map[string]struct{}{}
		for _, e := range entries {
			if e.Type != "blob" {
				continue
			}
			if _, dup := seen[e.SHA]; dup {
				continue
			}
			seen[e.SHA] = struct{}{}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case jobs <- BlobJob{SHA: e.SHA, Size: e.Size, Path: e.Path,
				Refs: []*v1.CommitFileRef{{CommitSha: headBytes, Path: e.Path}}}:
			}
		}
		return nil
	case WalkFullHistory, WalkBranchFull, WalkCommitRange:
		ra := RevListArgs{}
		switch args.Mode {
		case WalkFullHistory:
			ra.All = true
		case WalkBranchFull:
			if args.Branch == "" {
				return fmt.Errorf("WalkBranchFull: branch required")
			}
			ra.Branch = args.Branch
		case WalkCommitRange:
			if args.Range == nil {
				return fmt.Errorf("WalkCommitRange: range required")
			}
			ra.Range = args.Range
		}
		shas, err := RevList(ctx, repoDir, ra)
		if err != nil {
			return err
		}
		seen := map[string]struct{}{}
		for _, commit := range shas {
			entries, err := ListTree(ctx, repoDir, commit)
			if err != nil {
				return fmt.Errorf("ls-tree %s: %w", commit, err)
			}
			commitBytes, err := decodeCommitSHA(commit)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Type != "blob" {
					continue
				}
				if _, dup := seen[e.SHA]; dup {
					continue
				}
				seen[e.SHA] = struct{}{}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case jobs <- BlobJob{SHA: e.SHA, Size: e.Size, Path: e.Path,
					Refs: []*v1.CommitFileRef{{CommitSha: commitBytes, Path: e.Path}}}:
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown walk mode %d", args.Mode)
	}
}
