package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/uuid"
)

type LocalSource struct {
	Path string
}

type RemoteSource struct {
	URL   string
	Token string // optional PAT for HTTPS; injected via X-Token header is non-portable, so we use URL-rewrite
}

type Source interface {
	isSource()
}

func (LocalSource) isSource()  {}
func (RemoteSource) isSource() {}

// PrepareRepo returns the working directory for a scan plus a cleanup func.
// For LocalSource, the original path is returned and cleanup is a no-op.
// For RemoteSource, the repo is cloned under workspaceRoot/<uuid>/ and
// cleanup removes the clone.
func PrepareRepo(ctx context.Context, src Source, workspaceRoot string) (string, func(), error) {
	switch s := src.(type) {
	case LocalSource:
		if err := verifyGitRepo(s.Path); err != nil {
			return "", func() {}, err
		}
		return s.Path, func() {}, nil
	case RemoteSource:
		if workspaceRoot == "" {
			return "", func() {}, errors.New("workspaceRoot required for remote source")
		}
		dest := filepath.Join(workspaceRoot, uuid.NewString())
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return "", func() {}, fmt.Errorf("create clone dir: %w", err)
		}
		url := s.URL
		if s.Token != "" {
			url = injectToken(s.URL, s.Token)
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", url, dest)
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(dest)
			return "", func() {}, fmt.Errorf("git clone: %w: %s", err, string(out))
		}
		cleanup := func() { os.RemoveAll(dest) }
		return dest, cleanup, nil
	default:
		return "", func() {}, fmt.Errorf("unsupported source type %T", src)
	}
}

func verifyGitRepo(path string) error {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}
	// Maybe a bare repo
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err == nil {
		return nil
	}
	return fmt.Errorf("%s: not a git repository", path)
}

// injectToken rewrites https://host/x → https://<token>@host/x.
func injectToken(url, token string) string {
	const prefix = "https://"
	if len(url) >= len(prefix) && url[:len(prefix)] == prefix {
		return prefix + token + "@" + url[len(prefix):]
	}
	return url
}
