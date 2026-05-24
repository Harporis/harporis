package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type TreeEntry struct {
	Mode string
	Type string // "blob" | "tree" | "commit"
	SHA  string
	Size int64 // -1 for non-blobs
	Path string
}

// ListTree runs `git ls-tree -r -l <ref>` and parses the output.
func ListTree(ctx context.Context, repoDir, ref string) ([]TreeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-tree", "-r", "-l", ref)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// Format: "<mode> <type> <sha> <size or -> \t<path>"
		tabIdx := strings.IndexByte(line, '\t')
		if tabIdx < 0 {
			return nil, fmt.Errorf("malformed ls-tree line: %q", line)
		}
		meta := strings.Fields(line[:tabIdx])
		path := line[tabIdx+1:]
		if len(meta) != 4 {
			return nil, fmt.Errorf("expected 4 meta fields, got %d: %q", len(meta), line)
		}
		var size int64 = -1
		if meta[3] != "-" {
			s, err := strconv.ParseInt(meta[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse size in %q: %w", line, err)
			}
			size = s
		}
		entries = append(entries, TreeEntry{
			Mode: meta[0],
			Type: meta[1],
			SHA:  meta[2],
			Size: size,
			Path: path,
		})
	}
	return entries, nil
}
