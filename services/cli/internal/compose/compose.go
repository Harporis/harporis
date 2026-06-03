// Package compose is a thin wrapper around `docker compose` (or, as a
// fallback, the legacy `docker-compose` binary). Commands are dispatched
// through a Runner interface so tests can stub them.
package compose

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Runner executes one compose invocation. Production uses ExecRunner;
// tests use a stub.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// Compose is the high-level surface used by cli commands.
type Compose struct{ r Runner }

// New wraps any Runner.
func New(r Runner) *Compose { return &Compose{r: r} }

// NewDefault picks the right docker compose flavour at runtime and
// returns a wired Compose.
func NewDefault() (*Compose, error) {
	r, err := DetectExecRunner()
	if err != nil {
		return nil, err
	}
	return New(r), nil
}

// Up is `docker compose up -d`, optionally with --build.
func (c *Compose) Up(ctx context.Context, build bool) (string, error) {
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	return c.r.Run(ctx, args...)
}

// Down is `docker compose down`, optionally with -v.
func (c *Compose) Down(ctx context.Context, volumes bool) (string, error) {
	args := []string{"down"}
	if volumes {
		args = append(args, "-v")
	}
	return c.r.Run(ctx, args...)
}

// PS is `docker compose ps`.
func (c *Compose) PS(ctx context.Context) (string, error) {
	return c.r.Run(ctx, "ps")
}

// Logs is `docker compose logs <svc?> [-f]`.
func (c *Compose) Logs(ctx context.Context, service string, follow bool) (string, error) {
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	if service != "" {
		args = append(args, service)
	}
	return c.r.Run(ctx, args...)
}

// Exec runs `docker compose exec -T <service> <cmd...>`. The `-T` disables
// TTY allocation so the call works under non-interactive contexts (CI,
// piped IO, callers that capture stdout). Returns the command's combined
// stdout/stderr.
func (c *Compose) Exec(ctx context.Context, service string, cmd ...string) (string, error) {
	args := append([]string{"exec", "-T", service}, cmd...)
	return c.r.Run(ctx, args...)
}

// ExecRunner runs `docker compose …` via os/exec.
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

// Run executes the compose command and returns merged stdout/stderr.
func (e *ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	full := append(append([]string{}, e.binary[1:]...), args...)
	cmd := exec.CommandContext(ctx, e.binary[0], full...)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
