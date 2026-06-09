package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type CommitRange struct {
	From string // exclusive
	To   string // inclusive
}

type RevListArgs struct {
	All    bool
	Branch string       // optional: limit to a branch ref
	Range  *CommitRange // optional: from..to
}

// RevList runs `git rev-list` and returns commit SHAs newest-first.
//
// Range semantics:
//   - From + To set:  "<From>..<To>"  — From is exclusive (git default).
//   - From empty, To set: just "<To>" — walks every commit reachable from
//     To, which is what `--init-to <sha>` expects. Equivalent to
//     scanning from the initial commit up to and including To.
//   - Both empty: rejected (the caller must pick a mode).
func RevList(ctx context.Context, repoDir string, args RevListArgs) ([]string, error) {
	cliArgs := []string{"-C", repoDir, "rev-list"}
	switch {
	case args.Range != nil:
		if args.Range.To == "" {
			return nil, fmt.Errorf("git rev-list: range.To is required")
		}
		if args.Range.From == "" {
			cliArgs = append(cliArgs, args.Range.To)
		} else {
			cliArgs = append(cliArgs, fmt.Sprintf("%s..%s", args.Range.From, args.Range.To))
		}
	case args.All:
		cliArgs = append(cliArgs, "--all")
	case args.Branch != "":
		cliArgs = append(cliArgs, args.Branch)
	default:
		cliArgs = append(cliArgs, "HEAD")
	}
	cmd := exec.CommandContext(ctx, "git", cliArgs...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-list: %w", err)
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			shas = append(shas, line)
		}
	}
	return shas, nil
}
