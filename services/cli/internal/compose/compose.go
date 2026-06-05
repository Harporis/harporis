// Package compose is the docker-compose-aware façade used by the CLI.
//
// The CLI must be runnable from any working directory — a user typing
// `harporis doctor` from `~/Documents` should not need to first `cd`
// into the repo. Plain `docker compose <cmd>` won't do: compose v2
// resolves its YAML file via CWD and prints "no configuration file
// provided: not found" otherwise.
//
// The trick this package uses:
//   - The running containers carry compose's labels
//     (`com.docker.compose.project` and `…project.config_files`). The
//     first one we find tells us the project name and the path to the
//     original docker-compose.yml.
//   - Inspection commands (exec/ls/cat/wget into a container) don't
//     need the compose file at all — `docker exec <container>` works
//     against a named container regardless of CWD. We resolve the
//     container by `docker ps --filter label=…service=<svc>` and exec
//     directly.
//   - Lifecycle commands (up/down/ps/logs) DO need the compose file.
//     We pass `-f <path>` explicitly so docker-compose finds the file
//     regardless of CWD.
package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Default project name for the Harporis dev stack. Matches the
// directory name compose v2 derives from `docker-compose.yml` when
// nobody passes -p, which is how the installer brings the stack up.
const defaultProject = "harporis"

// Runner executes one docker invocation. Production uses ExecRunner;
// tests stub it.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// Compose is the high-level surface used by cli commands.
type Compose struct {
	r       Runner
	project string // compose project name, default "harporis" or COMPOSE_PROJECT_NAME env
}

// New wraps any Runner. The project name defaults to "harporis"
// (overridden by COMPOSE_PROJECT_NAME env when set non-empty).
func New(r Runner) *Compose {
	proj := defaultProject
	if v := os.Getenv("COMPOSE_PROJECT_NAME"); v != "" {
		proj = v
	}
	return &Compose{r: r, project: proj}
}

// NewDefault picks the right docker compose flavour at runtime and
// returns a wired Compose.
func NewDefault() (*Compose, error) {
	r, err := DetectExecRunner()
	if err != nil {
		return nil, err
	}
	return New(r), nil
}

// Project returns the resolved compose project name.
func (c *Compose) Project() string { return c.project }

// Exec runs a command inside a running service container via
// `docker exec`, NOT `docker compose exec` — that way the call works
// regardless of the operator's current working directory. The target
// container is discovered by compose's standard labels
// (project=<c.project>, service=<service>); the first matching
// running container wins (which matters for --scale N deployments,
// but read-only inspection — wget /metrics, cat /findings — is the
// same on every replica so any replica is fine).
func (c *Compose) Exec(ctx context.Context, service string, cmd ...string) (string, error) {
	container, err := c.resolveContainer(ctx, service)
	if err != nil {
		return "", err
	}
	args := append([]string{"exec", container}, cmd...)
	return c.r.Run(ctx, append([]string{"__direct__"}, args...)...)
}

// resolveContainer returns the name of a running container for the
// (project, service) tuple. Returns an error if none is running —
// the caller can surface that as "the stack isn't up yet".
func (c *Compose) resolveContainer(ctx context.Context, service string) (string, error) {
	out, err := c.r.Run(ctx,
		"__direct__", "ps",
		"--filter", "label=com.docker.compose.project="+c.project,
		"--filter", "label=com.docker.compose.service="+service,
		"--format", "{{.Names}}",
	)
	if err != nil {
		return "", fmt.Errorf("docker ps for %s/%s: %w", c.project, service, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("no running container for service %q in project %q (is the stack up?)", service, c.project)
	}
	// Multiple replicas → take the first. They're functionally
	// interchangeable for our inspection callers.
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out, nil
}

// composeFile reads the original docker-compose.yml path out of a
// running container's labels. Returned empty string means no container
// was found (the stack is probably down); the caller should fall back
// to whatever CWD compose finds.
func (c *Compose) composeFile(ctx context.Context, service string) (string, error) {
	container, err := c.resolveContainer(ctx, service)
	if err != nil {
		return "", err
	}
	out, err := c.r.Run(ctx, "__direct__", "inspect", container, "--format", "{{ index .Config.Labels \"com.docker.compose.project.config_files\" }}")
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", nil
	}
	// Labels store a comma-separated list when multiple files apply
	// (compose.yml + override). The primary one is first.
	if i := strings.IndexByte(out, ','); i >= 0 {
		out = out[:i]
	}
	return out, nil
}

// composeArgs returns the `[-p <project> -f <file>]` prefix to pass
// to any docker-compose subcommand so it finds the right project
// from any CWD. If the stack isn't running yet, the file lookup
// fails silently and we fall back to plain `-p <project>` (compose
// will still try CWD for the YAML, which is the legacy behaviour).
func (c *Compose) composeArgs(ctx context.Context) []string {
	args := []string{"-p", c.project}
	// Try each service in turn — any one that's running gives us the file path.
	for _, svc := range []string{"getter", "scanner", "writer", "nats"} {
		if f, err := c.composeFile(ctx, svc); err == nil && f != "" {
			args = append(args, "-f", f)
			break
		}
	}
	return args
}

// Up is `docker compose up -d`, optionally with --build.
func (c *Compose) Up(ctx context.Context, build bool) (string, error) {
	args := append(c.composeArgs(ctx), "up", "-d")
	if build {
		args = append(args, "--build")
	}
	return c.r.Run(ctx, args...)
}

// Down is `docker compose down`, optionally with -v.
func (c *Compose) Down(ctx context.Context, volumes bool) (string, error) {
	args := append(c.composeArgs(ctx), "down")
	if volumes {
		args = append(args, "-v")
	}
	return c.r.Run(ctx, args...)
}

// PS is `docker compose ps`.
func (c *Compose) PS(ctx context.Context) (string, error) {
	return c.r.Run(ctx, append(c.composeArgs(ctx), "ps")...)
}

// Logs is `docker compose logs <svc?> [-f]`.
func (c *Compose) Logs(ctx context.Context, service string, follow bool) (string, error) {
	args := append(c.composeArgs(ctx), "logs")
	if follow {
		args = append(args, "-f")
	}
	if service != "" {
		args = append(args, service)
	}
	return c.r.Run(ctx, args...)
}

// ExecRunner runs either `docker compose …` (for lifecycle commands)
// or plain `docker …` (for exec/ps/inspect against named containers).
// The choice is encoded in the first arg of args: if it's the literal
// "__direct__", the rest is passed to `docker` directly; otherwise
// the binary tuple ({"docker","compose"} or {"docker-compose"})
// drives the call.
type ExecRunner struct {
	binary []string // e.g. {"docker","compose"} or {"docker-compose"}
}

// DetectExecRunner finds either `docker compose` or `docker-compose`.
func DetectExecRunner() (*ExecRunner, error) {
	if _, err := exec.LookPath("docker"); err == nil {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return &ExecRunner{binary: []string{"docker", "compose"}}, nil
		}
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return &ExecRunner{binary: []string{"docker-compose"}}, nil
	}
	return nil, errors.New("docker compose v2 not found (neither `docker compose` nor `docker-compose`)")
}

// Run executes either a compose or direct-docker command depending on
// the leading "__direct__" sentinel. Returns merged stdout/stderr.
func (e *ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "__direct__" {
		// Plain `docker <args>`. The compose binary tuple's head is
		// always "docker" or "docker-compose" — for direct mode we
		// hard-pin to "docker" because containers + inspect APIs are
		// part of the docker engine, not docker-compose.
		cmd := exec.CommandContext(ctx, "docker", args[1:]...)
		out, err := cmd.CombinedOutput()
		return strings.TrimRight(string(out), "\n"), err
	}
	full := append(append([]string{}, e.binary[1:]...), args...)
	cmd := exec.CommandContext(ctx, e.binary[0], full...)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
