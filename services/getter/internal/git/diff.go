package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Patch struct {
	Path    string
	OldPath string
	NewFile bool
	Deleted bool
	Hunks   []Hunk
}

type Hunk struct {
	OldStart int32
	OldLines int32
	NewStart int32
	NewLines int32
	Lines    []DiffLine
}

type DiffLine struct {
	OldLine int32 // 0 if added
	NewLine int32 // 0 if removed
	Op      byte  // ' ', '+', '-'
	Text    string
}

var (
	fileHeaderRE = regexp.MustCompile(`^diff --git a/(.+?) b/(.+?)$`)
	hunkRE       = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
)

// ParseUnifiedDiff parses output of `git diff -U<N>` into per-file patches.
//
// MVP scope: removed lines (lines prefixed with `-`) are intentionally
// discarded. Secret scanning only inspects added and context lines because
// removed text cannot introduce new secrets. Consumers that need the full
// patch (e.g. compliance audits of deleted content) should not use this
// parser — extend it to populate Hunk.Lines for `-` ops and track OldLine.
func ParseUnifiedDiff(input []byte) ([]Patch, error) {
	var patches []Patch
	var cur *Patch
	var curHunk *Hunk
	var oldLine, newLine int32

	sc := bufio.NewScanner(bytes.NewReader(input))
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if m := fileHeaderRE.FindStringSubmatch(line); m != nil {
			if cur != nil {
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
					curHunk = nil
				}
				patches = append(patches, *cur)
			}
			cur = &Patch{Path: m[2], OldPath: m[1]}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "new file mode") {
			cur.NewFile = true
			continue
		}
		if strings.HasPrefix(line, "deleted file mode") {
			cur.Deleted = true
			continue
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "index ") {
			continue
		}
		if m := hunkRE.FindStringSubmatch(line); m != nil {
			if curHunk != nil {
				cur.Hunks = append(cur.Hunks, *curHunk)
			}
			oldStart := parseInt32(m[1])
			oldLines := int32(1)
			if m[2] != "" {
				oldLines = parseInt32(m[2])
			}
			newStart := parseInt32(m[3])
			newLines := int32(1)
			if m[4] != "" {
				newLines = parseInt32(m[4])
			}
			curHunk = &Hunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines}
			oldLine = oldStart
			newLine = newStart
			continue
		}
		if curHunk == nil || len(line) == 0 {
			continue
		}
		op := line[0]
		text := line[1:]
		switch op {
		case ' ':
			curHunk.Lines = append(curHunk.Lines, DiffLine{NewLine: newLine, Op: ' ', Text: text})
			oldLine++
			newLine++
		case '+':
			curHunk.Lines = append(curHunk.Lines, DiffLine{NewLine: newLine, Op: '+', Text: text})
			newLine++
		case '-':
			// Removed lines are not present in the new file; skip them so the
			// hunk reflects exactly the new-file view (matches NewLines count).
			oldLine++
		default:
			// "\ No newline at end of file", binary blobs, etc.
			continue
		}
	}
	if curHunk != nil {
		cur.Hunks = append(cur.Hunks, *curHunk)
	}
	if cur != nil {
		patches = append(patches, *cur)
	}
	return patches, sc.Err()
}

func parseInt32(s string) int32 {
	n, _ := strconv.ParseInt(s, 10, 32)
	return int32(n)
}

// RunDiff invokes git diff with -U<context> and the given args (e.g. "--cached" or "base..head")
// and returns its raw output for parsing.
func RunDiff(ctx context.Context, repoDir string, contextLines int, extraArgs ...string) ([]byte, error) {
	cliArgs := []string{"-C", repoDir, "diff", fmt.Sprintf("-U%d", contextLines)}
	cliArgs = append(cliArgs, extraArgs...)
	cmd := exec.CommandContext(ctx, "git", cliArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %v: %w: %s", cliArgs, err, stderr.String())
	}
	return out, nil
}
