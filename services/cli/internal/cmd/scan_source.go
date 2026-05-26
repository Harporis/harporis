package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

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
