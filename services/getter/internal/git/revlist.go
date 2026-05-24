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
func RevList(ctx context.Context, repoDir string, args RevListArgs) ([]string, error) {
	cliArgs := []string{"-C", repoDir, "rev-list"}
	switch {
	case args.Range != nil:
		cliArgs = append(cliArgs, fmt.Sprintf("%s..%s", args.Range.From, args.Range.To))
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
