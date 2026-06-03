package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// containerNativePrefixes lists path prefixes that mean "already a path
// inside the getter container" — so they pass through translation
// unchanged. /repos preserves the legacy override-file workflow
// (where users mount their repo at /repos/<name>); /host is the new
// auto-mount we added in docker-compose.yml.
var containerNativePrefixes = []string{"/host/", "/repos/", "/var/", "/etc/", "/tmp/"}

// translateLocalPath converts a host-side absolute path to the
// container-side path the getter will see. When mountHost is true and
// the path lies under home (typically $HOME), it is rewritten to
// /host/<relative>. When mountHost is false, or the path is already a
// container-native path (one of containerNativePrefixes), it is
// returned unchanged. A host path outside home with mountHost=true is
// rejected with a helpful error pointing at the opt-out flag.
//
// Empty `local` and empty `home` are handled gracefully: this lets the
// scan command call it unconditionally, and skips work when there's
// nothing to translate.
func translateLocalPath(local, home string, mountHost bool) (string, error) {
	if local == "" {
		return local, nil
	}
	// User explicitly opted out → no translation, pass the value
	// straight through to the proto. This preserves the legacy
	// override-file workflow for anyone who needs it.
	if !mountHost {
		return local, nil
	}
	for _, p := range containerNativePrefixes {
		if strings.HasPrefix(local, p) {
			return local, nil
		}
	}
	if home == "" {
		return "", fmt.Errorf(
			"--local %q: $HOME is empty so the path cannot be auto-translated; pass --no-mount-host and use a container-native path (e.g. /repos/myrepo)",
			local,
		)
	}
	abs, err := filepath.Abs(local)
	if err != nil {
		return "", fmt.Errorf("--local %q: %w", local, err)
	}
	rel, err := filepath.Rel(home, abs)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." || rel == "" {
		return "", fmt.Errorf(
			"--local %q is outside $HOME (%s); either move it under $HOME, mount it via docker-compose.override.yml, or pass --no-mount-host with the container-side path",
			local, home,
		)
	}
	return "/host/" + filepath.ToSlash(rel), nil
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
