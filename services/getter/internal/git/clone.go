package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type LocalSource struct {
	Path string
}

// RemoteSource describes a remote git repository plus all supported auth modes.
// Fields are mutually exclusive by transport (HTTPS vs SSH); see authMode() for
// the selection rules.
//
// Supported auth modes:
//   - HTTPS, no auth — public clone (URL https://… with no Token/BasicUser).
//   - HTTPS + Bearer token — set Token. Sent via -c http.extraHeader, never
//     embedded in the URL or shown in argv.
//   - HTTPS + Basic auth — set BasicUser + BasicPassword. base64(user:pass)
//     is sent via http.extraHeader. Raw credentials never appear in argv.
//   - SSH + private key — set SSHPrivateKeyPEM. The key is written to a 0600
//     tempfile and referenced via GIT_SSH_COMMAND -i. IdentitiesOnly=yes
//     prevents the ssh-agent from being tried alongside the explicit key.
//   - SSH + ssh-agent — leave SSH fields empty. git inherits SSH_AUTH_SOCK.
//   - Optional known_hosts pinning — set SSHKnownHosts on any SSH variant to
//     write a temp known_hosts file with StrictHostKeyChecking=yes.
type RemoteSource struct {
	URL string

	// HTTPS auth
	Token         string
	BasicUser     string
	BasicPassword string

	// SSH auth
	SSHPrivateKeyPEM []byte
	SSHKnownHosts    []byte
}

type Source interface {
	isSource()
}

func (LocalSource) isSource()  {}
func (RemoteSource) isSource() {}

// cloneCommand is the fully-resolved invocation: argv, extra env, and a
// cleanup func that removes any tempfiles (private key, known_hosts) written
// during preparation. Always call Cleanup, even on error.
type cloneCommand struct {
	Args    []string
	Env     []string
	Cleanup func()
}

func noopCleanup() {}

// PrepareRepo returns the working directory for a scan plus a cleanup func.
// For LocalSource, the original path is returned and cleanup is a no-op.
// For RemoteSource, the repo is cloned under workspaceRoot/<uuid>/ and
// cleanup removes the clone. cloneTimeout caps the duration of a remote clone
// (no effect for local sources). Pass 0 to disable the timeout.
func PrepareRepo(ctx context.Context, src Source, workspaceRoot string, cloneTimeout time.Duration) (string, func(), error) {
	switch s := src.(type) {
	case LocalSource:
		if err := verifyGitRepo(ctx, s.Path); err != nil {
			return "", noopCleanup, err
		}
		return s.Path, noopCleanup, nil
	case RemoteSource:
		if workspaceRoot == "" {
			return "", noopCleanup, errors.New("workspaceRoot required for remote source")
		}
		dest := filepath.Join(workspaceRoot, uuid.NewString())
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return "", noopCleanup, fmt.Errorf("create clone dir: %w", err)
		}
		cc, err := buildCloneCommand(s, dest)
		if err != nil {
			os.RemoveAll(dest)
			return "", noopCleanup, err
		}
		cloneCtx := ctx
		if cloneTimeout > 0 {
			var cancel context.CancelFunc
			cloneCtx, cancel = context.WithTimeout(ctx, cloneTimeout)
			defer cancel()
		}
		cmd := exec.CommandContext(cloneCtx, cc.Args[0], cc.Args[1:]...)
		if len(cc.Env) > 0 {
			cmd.Env = append(os.Environ(), cc.Env...)
		}
		out, err := cmd.CombinedOutput()
		// Auth tempfiles are no longer needed once clone returns.
		cc.Cleanup()
		if err != nil {
			os.RemoveAll(dest)
			return "", noopCleanup, fmt.Errorf("git clone: %w: %s", err, redactSecrets(s, string(out)))
		}
		return dest, func() { os.RemoveAll(dest) }, nil
	default:
		return "", noopCleanup, fmt.Errorf("unsupported source type %T", src)
	}
}

// verifyGitRepo asks git itself whether the path is a working tree or bare
// repo. Falls back to file checks if git is missing.
func verifyGitRepo(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--git-dir")
	if err := cmd.Run(); err == nil {
		return nil
	}
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		return nil
	}
	return fmt.Errorf("%s: not a git repository", path)
}

// buildCloneCommand turns a RemoteSource into a ready-to-exec git clone
// invocation. Caller must call Cleanup() in all cases.
func buildCloneCommand(src RemoteSource, dest string) (cloneCommand, error) {
	if src.URL == "" {
		return cloneCommand{Cleanup: noopCleanup}, errors.New("RemoteSource.URL required")
	}
	switch authMode(src) {
	case authHTTPSNone:
		return cloneCommand{
			Args:    []string{"git", "clone", "--quiet", src.URL, dest},
			Cleanup: noopCleanup,
		}, nil

	case authHTTPSBearer:
		// PAT pattern: username "x-access-token" (visible, not a secret),
		// password = token, supplied via GIT_ASKPASS-from-env so the token
		// never enters argv. The URL gets the username embedded so git
		// knows who to authenticate as without prompting.
		urlWithUser, err := injectURLUser(src.URL, "x-access-token")
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		askpassPath, cleanup, err := writeAskpassScript()
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		return cloneCommand{
			Args: []string{"git", "clone", "--quiet", urlWithUser, dest},
			Env: []string{
				"GIT_ASKPASS=" + askpassPath,
				"GIT_TERMINAL_PROMPT=0",
				"HARPORIS_GIT_AUTH=" + src.Token,
			},
			Cleanup: cleanup,
		}, nil

	case authHTTPSBasic:
		urlWithUser, err := injectURLUser(src.URL, src.BasicUser)
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		askpassPath, cleanup, err := writeAskpassScript()
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		return cloneCommand{
			Args: []string{"git", "clone", "--quiet", urlWithUser, dest},
			Env: []string{
				"GIT_ASKPASS=" + askpassPath,
				"GIT_TERMINAL_PROMPT=0",
				"HARPORIS_GIT_AUTH=" + src.BasicPassword,
			},
			Cleanup: cleanup,
		}, nil

	case authSSHKey:
		keyPath, hostsPath, cleanup, err := writeSSHFiles(src)
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		sshCmd := buildSSHCommand(keyPath, hostsPath)
		return cloneCommand{
			Args:    []string{"git", "clone", "--quiet", src.URL, dest},
			Env:     []string{"GIT_SSH_COMMAND=" + sshCmd},
			Cleanup: cleanup,
		}, nil

	case authSSHAgent:
		_, hostsPath, cleanup, err := writeSSHFiles(src)
		if err != nil {
			return cloneCommand{Cleanup: noopCleanup}, err
		}
		sshCmd := buildSSHCommand("", hostsPath)
		args := []string{"git", "clone", "--quiet", src.URL, dest}
		var env []string
		if sshCmd != "" {
			env = []string{"GIT_SSH_COMMAND=" + sshCmd}
		}
		return cloneCommand{Args: args, Env: env, Cleanup: cleanup}, nil
	}
	return cloneCommand{Cleanup: noopCleanup}, fmt.Errorf("unrecognised auth mode for URL %q", src.URL)
}

type authModeKind int

const (
	authHTTPSNone authModeKind = iota
	authHTTPSBearer
	authHTTPSBasic
	authSSHKey
	authSSHAgent
)

func authMode(src RemoteSource) authModeKind {
	httpsLike := strings.HasPrefix(src.URL, "https://") ||
		strings.HasPrefix(src.URL, "http://") ||
		strings.HasPrefix(src.URL, "file://")
	sshLike := strings.HasPrefix(src.URL, "ssh://") ||
		strings.HasPrefix(src.URL, "git@") ||
		// `user@host:path` scp-style — has @ before `:` but no scheme.
		(!httpsLike && atBeforeColon(src.URL))

	if sshLike {
		if len(src.SSHPrivateKeyPEM) > 0 {
			return authSSHKey
		}
		return authSSHAgent
	}
	if src.Token != "" {
		return authHTTPSBearer
	}
	if src.BasicUser != "" {
		return authHTTPSBasic
	}
	return authHTTPSNone
}

func atBeforeColon(s string) bool {
	at := strings.IndexByte(s, '@')
	if at < 0 {
		return false
	}
	colon := strings.IndexByte(s, ':')
	return colon > at
}

// writeSSHFiles writes optional private key + known_hosts to temp files
// with safe permissions and returns paths plus a cleanup. keyPath is empty
// if no key was provided. Cleanup is always non-nil.
func writeSSHFiles(src RemoteSource) (keyPath, hostsPath string, cleanup func(), err error) {
	var paths []string
	cleanup = func() {
		for _, p := range paths {
			_ = os.Remove(p)
		}
	}
	if len(src.SSHPrivateKeyPEM) > 0 {
		f, e := os.CreateTemp("", "harporis-getter-sshkey-*")
		if e != nil {
			cleanup()
			return "", "", noopCleanup, fmt.Errorf("create ssh key tempfile: %w", e)
		}
		paths = append(paths, f.Name())
		if e := os.Chmod(f.Name(), 0o600); e != nil {
			f.Close()
			cleanup()
			return "", "", noopCleanup, fmt.Errorf("chmod ssh key tempfile: %w", e)
		}
		if _, e := f.Write(src.SSHPrivateKeyPEM); e != nil {
			f.Close()
			cleanup()
			return "", "", noopCleanup, fmt.Errorf("write ssh key: %w", e)
		}
		f.Close()
		keyPath = f.Name()
	}
	if len(src.SSHKnownHosts) > 0 {
		f, e := os.CreateTemp("", "harporis-getter-knownhosts-*")
		if e != nil {
			cleanup()
			return "", "", noopCleanup, fmt.Errorf("create known_hosts tempfile: %w", e)
		}
		paths = append(paths, f.Name())
		if _, e := f.Write(src.SSHKnownHosts); e != nil {
			f.Close()
			cleanup()
			return "", "", noopCleanup, fmt.Errorf("write known_hosts: %w", e)
		}
		f.Close()
		hostsPath = f.Name()
	}
	return keyPath, hostsPath, cleanup, nil
}

// buildSSHCommand assembles the GIT_SSH_COMMAND value. Empty string means
// "leave ssh defaults alone" (used in ssh-agent mode with no known_hosts).
func buildSSHCommand(keyPath, hostsPath string) string {
	parts := []string{"ssh"}
	if keyPath != "" {
		parts = append(parts, "-i", keyPath, "-o", "IdentitiesOnly=yes")
	}
	if hostsPath != "" {
		parts = append(parts,
			"-o", "UserKnownHostsFile="+hostsPath,
			"-o", "StrictHostKeyChecking=yes",
		)
	}
	if len(parts) == 1 {
		return "" // no overrides — inherit
	}
	return strings.Join(parts, " ")
}

// redactSecrets removes credentials from clone error output. git may echo
// URLs back; if any token/password slipped into one, this strips it.
func redactSecrets(src RemoteSource, msg string) string {
	if src.Token != "" {
		msg = strings.ReplaceAll(msg, src.Token, "<redacted-token>")
	}
	if src.BasicPassword != "" {
		msg = strings.ReplaceAll(msg, src.BasicPassword, "<redacted-password>")
	}
	if src.BasicUser != "" {
		msg = strings.ReplaceAll(msg, src.BasicUser+":", "<redacted-user>:")
	}
	return msg
}

// injectURLUser rewrites https://host/path → https://<user>@host/path.
// The username is not a secret and acceptably visible in argv; password
// is supplied separately via GIT_ASKPASS. If the URL already has userinfo,
// it is replaced.
func injectURLUser(rawURL, user string) (string, error) {
	if user == "" {
		return "", errors.New("username required")
	}
	for _, scheme := range []string{"https://", "http://"} {
		if strings.HasPrefix(rawURL, scheme) {
			tail := rawURL[len(scheme):]
			// strip any existing userinfo
			if at := strings.IndexByte(tail, '@'); at >= 0 {
				// userinfo can contain ':', but @ marks its end
				slash := strings.IndexByte(tail, '/')
				if slash < 0 || at < slash {
					tail = tail[at+1:]
				}
			}
			return scheme + user + "@" + tail, nil
		}
	}
	return "", fmt.Errorf("cannot inject user into non-http(s) URL %q", rawURL)
}

// writeAskpassScript writes a tiny shell script that prints the value of
// $HARPORIS_GIT_AUTH. Returned path is mode 0700 in a temp dir; cleanup
// removes the script. The secret itself is passed to the script via env,
// not via file contents.
func writeAskpassScript() (string, func(), error) {
	dir, err := os.MkdirTemp("", "harporis-askpass-*")
	if err != nil {
		return "", noopCleanup, fmt.Errorf("askpass tempdir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "askpass.sh")
	script := "#!/bin/sh\nprintf '%s' \"$HARPORIS_GIT_AUTH\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("write askpass: %w", err)
	}
	return path, cleanup, nil
}
