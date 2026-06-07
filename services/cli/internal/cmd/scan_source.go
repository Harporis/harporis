package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// hostPathStat is the indirection used by translateLocalPath to check
// whether a path exists on the host. Real call goes to os.Stat; tests
// override it.
var hostPathStat = os.Stat

// hostEvalSymlinks is the indirection used by translateLocalPath to
// resolve any symlinks in the user-supplied --local path BEFORE the
// $HOME containment check. Defence-in-depth: getter mounts $HOME
// read-only, so a symlink inside $HOME pointing outside $HOME would
// still resolve through the read-only mount and could expose
// arbitrary files. By following links here and re-checking
// containment against the resolved path, we keep the operator's
// scan confined to what's actually under $HOME.
var hostEvalSymlinks = filepath.EvalSymlinks

// translateLocalPath converts a host-side absolute path to the
// container-side path the getter will see. When mountHost is true and
// the path lies under home (typically $HOME) AND exists on the host,
// it is rewritten to /host/<relative>. Paths that do NOT exist on the
// host are treated as already container-native (the user gave us a
// path that lives inside the getter container, e.g. /repos/leaky via
// docker-compose.override.yml) and pass through unchanged.
// When mountHost is false, the path is always passed through unchanged.
//
// Empty `local` and empty `home` are handled gracefully: this lets the
// scan command call it unconditionally and skips work when there's
// nothing to translate.
func translateLocalPath(local, home string, mountHost bool) (string, error) {
	if local == "" {
		return local, nil
	}
	// User explicitly opted out → no translation. Preserves the legacy
	// override-file workflow for paths inside the container.
	if !mountHost {
		return local, nil
	}
	// If the path doesn't exist on the host, assume the user is
	// referencing a container-internal path mounted via the override
	// file (e.g. /repos/leaky). Pass through unchanged — the getter
	// will validate it server-side and surface a clean error if it's
	// wrong.
	if _, err := hostPathStat(local); err != nil {
		return local, nil
	}
	if home == "" {
		return "", fmt.Errorf(
			"--local %q exists on host but $HOME is empty so the path cannot be auto-translated; pass --no-mount-host and use a container-native path (e.g. /repos/myrepo)",
			local,
		)
	}
	abs, err := filepath.Abs(local)
	if err != nil {
		return "", fmt.Errorf("--local %q: %w", local, err)
	}
	// Resolve symlinks so the containment check below catches
	// "$HOME/link -> /etc" style escapes. Best-effort: if the resolve
	// fails (broken symlink, permission denied), fall back to the
	// original abs path and let the Rel/.. check below reject it if it
	// escapes — the getter's read-only mount limits the blast radius.
	if resolved, rerr := hostEvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	// Also resolve symlinks in $HOME itself — operators sometimes have
	// $HOME on a symlinked partition (e.g. /home -> /var/home). Without
	// this, filepath.Rel would compare resolved abs against unresolved
	// home and report "escapes $HOME" for a path that's clearly inside.
	homeResolved := home
	if resolved, rerr := hostEvalSymlinks(home); rerr == nil {
		homeResolved = resolved
	}
	rel, err := filepath.Rel(homeResolved, abs)
	if err != nil {
		return "", fmt.Errorf(
			"--local %q is not under $HOME (%s); either move it under $HOME, mount it via docker-compose.override.yml, or pass --no-mount-host",
			local, home,
		)
	}
	sep := string(filepath.Separator)
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+sep) {
		return "", fmt.Errorf(
			"--local %q is not under $HOME (%s); either move it under $HOME, mount it via docker-compose.override.yml, or pass --no-mount-host",
			local, home,
		)
	}
	return "/host/" + filepath.ToSlash(rel), nil
}

// applyRangePresets expands the convenience flags (--from-init,
// --init-to, --commit, --range) into the underlying --type/--from/--to
// triple. Presets are mutually exclusive with one another AND with the
// raw flags they would clobber — combining them surfaces as an error
// instead of silently winning, so the operator's intent stays explicit.
func applyRangePresets(cmd *cobra.Command, scanType, commitFrom, commitTo *string,
	fromInit bool, initTo, single, rangeSpec string) error {
	picked := 0
	if fromInit {
		picked++
	}
	if initTo != "" {
		picked++
	}
	if single != "" {
		picked++
	}
	if rangeSpec != "" {
		picked++
	}
	if picked == 0 {
		return nil
	}
	if picked > 1 {
		return errors.New("--from-init, --init-to, --commit, --range are mutually exclusive")
	}
	// Reject conflicts with the raw flags presets would expand into.
	// `--type` is special-cased via Changed() so the default "current_state"
	// doesn't trigger the conflict check.
	if cmd.Flags().Changed("type") {
		return errors.New("range presets conflict with --type; pick one form")
	}
	if *commitFrom != "" || *commitTo != "" {
		return errors.New("range presets conflict with --from/--to; pick one form")
	}
	switch {
	case fromInit:
		*scanType = "full_history"
	case initTo != "":
		*scanType = "commit_range"
		*commitFrom = "" // empty = from init; getter handles via plain rev-list
		*commitTo = initTo
	case single != "":
		*scanType = "commit_range"
		*commitFrom = single + "~1"
		*commitTo = single
	case rangeSpec != "":
		from, to, ok := parseGitRange(rangeSpec)
		if !ok {
			return fmt.Errorf("invalid --range %q (want A..B)", rangeSpec)
		}
		*scanType = "commit_range"
		*commitFrom = from
		*commitTo = to
	}
	return nil
}

// parseGitRange splits "A..B" — the git-native range syntax. Rejects
// the symmetric "..." form (different semantics: merge-base) so the
// operator gets an explicit error rather than a silently wrong scan.
func parseGitRange(s string) (from, to string, ok bool) {
	if strings.Contains(s, "...") {
		return "", "", false
	}
	parts := strings.SplitN(s, "..", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func scanTypeFromString(s string) (v1.ScanType, bool) {
	switch strings.ToLower(s) {
	case "current_state":
		return v1.ScanType_CURRENT_STATE, true
	case "full_history":
		return v1.ScanType_FULL_HISTORY, true
	case "branch_full":
		return v1.ScanType_BRANCH_FULL, true
	case "commit_range":
		return v1.ScanType_COMMIT_RANGE, true
	case "branch_diff":
		return v1.ScanType_BRANCH_DIFF, true
	case "head_diff":
		return v1.ScanType_HEAD_DIFF, true
	case "staged":
		return v1.ScanType_STAGED, true
	}
	return 0, false
}

func buildSource(local, remoteURL, token, sshKey, knownHosts string) (*v1.Source, error) {
	if local != "" {
		if remoteURL != "" {
			return nil, errors.New("--local and --remote-url are mutually exclusive")
		}
		return &v1.Source{Src: &v1.Source_LocalPath{LocalPath: local}}, nil
	}
	if remoteURL == "" {
		return nil, errors.New("either --local or --remote-url is required")
	}
	rr := &v1.RemoteRepo{Url: remoteURL}
	switch {
	case token != "" && sshKey != "":
		return nil, errors.New("--remote-token and --remote-ssh-key are mutually exclusive")
	case token != "":
		rr.Auth = &v1.RemoteRepo_Token{Token: token}
	case sshKey != "":
		key, err := os.ReadFile(sshKey)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", sshKey, err)
		}
		ssh := &v1.SshAuth{PrivateKeyPem: string(key)}
		if knownHosts != "" {
			kh, err := os.ReadFile(knownHosts)
			if err != nil {
				return nil, fmt.Errorf("read known_hosts %s: %w", knownHosts, err)
			}
			ssh.KnownHosts = string(kh)
		}
		rr.Auth = &v1.RemoteRepo_Ssh{Ssh: ssh}
	}
	return &v1.Source{Src: &v1.Source_Remote{Remote: rr}}, nil
}
