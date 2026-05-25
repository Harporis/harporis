package git

import (
	"bytes"
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

// ListTree runs `git ls-tree -r -l -z <ref>` and parses the NUL-terminated
// output. The -z flag preserves paths verbatim — without it, git escapes
// paths containing spaces, tabs, or newlines (wraps in quotes,
// backslash-escapes control bytes), which a naive parser sees as mangled.
func ListTree(ctx context.Context, repoDir, ref string) ([]TreeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "ls-tree", "-r", "-l", "-z", ref)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w: %s", err, stderr.String())
	}
	var entries []TreeEntry
	// Records are NUL-separated. The output ends with a trailing NUL.
	for _, record := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if record == "" {
			continue
		}
		// Record: "<mode> <type> <sha> <size_or_->\t<path>"
		// The tab separates meta from path; path bytes are preserved verbatim.
		tabIdx := strings.IndexByte(record, '\t')
		if tabIdx < 0 {
			return nil, fmt.Errorf("malformed ls-tree record: %q", record)
		}
		meta := strings.Fields(record[:tabIdx])
		path := record[tabIdx+1:]
		if len(meta) != 4 {
			return nil, fmt.Errorf("expected 4 meta fields, got %d: %q", len(meta), record)
		}
		var size int64 = -1
		if meta[3] != "-" {
			s, err := strconv.ParseInt(meta[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse size in %q: %w", record, err)
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
