package git

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Across all auth modes, the secret must NEVER appear in the command-line
// args — /proc/<pid>/cmdline is world-readable, and `ps` would expose
// any token embedded in the URL.
func TestBuildCloneCommand_TokenNeverInArgs(t *testing.T) {
	const token = "ghp_super_secret_abc123"
	cc, err := buildCloneCommand(RemoteSource{
		URL:   "https://github.com/example/repo.git",
		Token: token,
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()
	for _, a := range cc.Args {
		require.NotContains(t, a, token, "token must not appear in any argv element")
	}
}

func TestBuildCloneCommand_HTTPS_NoAuth(t *testing.T) {
	cc, err := buildCloneCommand(RemoteSource{
		URL: "https://github.com/example/repo.git",
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()
	require.Contains(t, cc.Args, "clone")
	require.Contains(t, cc.Args, "https://github.com/example/repo.git")
	// No -c http.extraHeader for unauth public clones.
	require.False(t, hasArgPrefix(cc.Args, "http.extraHeader="),
		"no auth header expected for public clone")
}

func TestBuildCloneCommand_HTTPS_Bearer_UsesAskpassViaEnv(t *testing.T) {
	cc, err := buildCloneCommand(RemoteSource{
		URL:   "https://github.com/example/repo.git",
		Token: "ghp_xxx",
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()

	// Token is supplied to git via GIT_ASKPASS-from-env, so it lives in env
	// (per-UID readable), not in argv (world-readable via /proc/.../cmdline).
	require.Equal(t, "ghp_xxx", envValue(cc.Env, "HARPORIS_GIT_AUTH"))
	require.NotEmpty(t, envValue(cc.Env, "GIT_ASKPASS"))
	require.Equal(t, "0", envValue(cc.Env, "GIT_TERMINAL_PROMPT"),
		"terminal prompts must be disabled in unattended scans")
	// Username is in the URL — acceptable, it isn't a secret. PAT pattern
	// uses the literal "x-access-token".
	require.Contains(t, urlOfArgs(cc.Args), "x-access-token@github.com")
}

func TestBuildCloneCommand_HTTPS_Basic_UsesAskpassViaEnv(t *testing.T) {
	cc, err := buildCloneCommand(RemoteSource{
		URL:           "https://gitlab.example.com/repo.git",
		BasicUser:     "alice",
		BasicPassword: "p@ssw0rd",
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()

	require.Equal(t, "p@ssw0rd", envValue(cc.Env, "HARPORIS_GIT_AUTH"))
	require.NotEmpty(t, envValue(cc.Env, "GIT_ASKPASS"))
	require.Contains(t, urlOfArgs(cc.Args), "alice@gitlab.example.com")
	for _, a := range cc.Args {
		require.NotContains(t, a, "p@ssw0rd",
			"password must never appear in argv (only in env)")
	}
}

func TestBuildCloneCommand_SSH_PrivateKey_WritesTempAndPointsGITSSHCOMMAND(t *testing.T) {
	const fakeKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nfakebody\n-----END OPENSSH PRIVATE KEY-----\n"
	cc, err := buildCloneCommand(RemoteSource{
		URL:              "git@github.com:example/repo.git",
		SSHPrivateKeyPEM: []byte(fakeKey),
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()

	sshCmd := envValue(cc.Env, "GIT_SSH_COMMAND")
	require.NotEmpty(t, sshCmd, "GIT_SSH_COMMAND must be set when a key is provided")
	require.Contains(t, sshCmd, " -i ", "ssh command must reference the key file")
	require.Contains(t, sshCmd, "IdentitiesOnly=yes",
		"must restrict ssh to the supplied key (avoid agent fallback leaking)")

	// Extract the key path and verify perms + content.
	keyPath := extractKeyPath(sshCmd)
	require.NotEmpty(t, keyPath, "key path missing in GIT_SSH_COMMAND")
	st, err := os.Stat(keyPath)
	require.NoError(t, err)
	require.EqualValues(t, 0o600, st.Mode().Perm(), "private key must be mode 0600")
	body, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.Equal(t, fakeKey, string(body))

	cc.Cleanup()
	_, err = os.Stat(keyPath)
	require.True(t, os.IsNotExist(err), "key file must be removed by cleanup")
}

func TestBuildCloneCommand_SSH_Agent_NoKeyTempFile(t *testing.T) {
	cc, err := buildCloneCommand(RemoteSource{
		URL: "git@github.com:example/repo.git",
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()
	sshCmd := envValue(cc.Env, "GIT_SSH_COMMAND")
	// Either empty (inherit default ssh) or set without -i flag.
	if sshCmd != "" {
		require.NotContains(t, sshCmd, " -i ",
			"ssh-agent mode must not write a key file or set -i")
	}
}

func TestBuildCloneCommand_SSH_KnownHosts_PinsHostCheck(t *testing.T) {
	cc, err := buildCloneCommand(RemoteSource{
		URL:           "git@github.com:example/repo.git",
		SSHKnownHosts: []byte("github.com ssh-rsa AAAA...fake\n"),
	}, "/tmp/dest")
	require.NoError(t, err)
	defer cc.Cleanup()
	sshCmd := envValue(cc.Env, "GIT_SSH_COMMAND")
	require.Contains(t, sshCmd, "UserKnownHostsFile=",
		"known_hosts must pin host verification")
	require.Contains(t, sshCmd, "StrictHostKeyChecking=yes",
		"with explicit known_hosts, host key checks must be strict")
}

// ---------------- helpers ----------------

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.Contains(a, prefix) {
			return true
		}
	}
	return false
}

func urlOfArgs(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "https://") || strings.HasPrefix(a, "git@") || strings.HasPrefix(a, "ssh://") {
			return a
		}
	}
	return ""
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return ""
}

func extractKeyPath(sshCmd string) string {
	// Look for " -i <path>" — path until next space.
	idx := strings.Index(sshCmd, " -i ")
	if idx < 0 {
		return ""
	}
	rest := sshCmd[idx+4:]
	end := strings.Index(rest, " ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
