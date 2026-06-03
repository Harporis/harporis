// Package doctor defines diagnostic checks and a runner.
package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

// Check is a single diagnostic. Name is shown to the user; Run returns
// a Result with ok + detail (detail is shown on both success and fail).
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// Result is the outcome of one Check.
type Result struct {
	Name   string
	OK     bool
	Detail string
}

// RunAll invokes each check sequentially and returns their results.
func RunAll(checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range checks {
		out = append(out, c.Run(ctx))
	}
	return out
}

// StaticCheck returns a Check that always reports the given values.
// Used in tests.
func StaticCheck(name string, ok bool, detail string) Check {
	return staticCheck{name: name, ok: ok, detail: detail}
}

type staticCheck struct {
	name   string
	ok     bool
	detail string
}

func (s staticCheck) Name() string                  { return s.name }
func (s staticCheck) Run(_ context.Context) Result  { return Result{Name: s.name, OK: s.ok, Detail: s.detail} }

// ----- concrete checks used by `harporis doctor` -----

// DockerCheck verifies docker is installed and responsive.
type DockerCheck struct{}

func (DockerCheck) Name() string { return "docker" }
func (DockerCheck) Run(ctx context.Context) Result {
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return Result{Name: "docker", OK: false, Detail: "docker not running or not installed"}
	}
	return Result{Name: "docker", OK: true, Detail: "server " + strings.TrimSpace(string(out))}
}

// ComposeCheck verifies `docker compose` v2 is available.
type ComposeCheck struct{}

func (ComposeCheck) Name() string { return "docker compose v2" }
func (ComposeCheck) Run(ctx context.Context) Result {
	out, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").Output()
	if err != nil {
		return Result{Name: "docker compose v2", OK: false, Detail: "missing or pre-v2"}
	}
	return Result{Name: "docker compose v2", OK: true, Detail: "v" + strings.TrimSpace(string(out))}
}

// NATSCheck pings the configured NATS URL.
type NATSCheck struct{ URL string }

func (n NATSCheck) Name() string { return "nats reachable" }
func (n NATSCheck) Run(_ context.Context) Result {
	cl, err := natscli.Dial(n.URL, "harporis-cli-doctor")
	if err != nil {
		return Result{Name: n.Name(), OK: false, Detail: err.Error()}
	}
	cl.Close()
	return Result{Name: n.Name(), OK: true, Detail: n.URL}
}

// Execer runs a command inside a compose service container. Satisfied by
// *compose.Compose; local interface so the doctor package can be tested
// without importing the real compose runner.
type Execer interface {
	Exec(ctx context.Context, service string, cmd ...string) (string, error)
}

// ContainerMetricsCheck probes a service's /metrics endpoint from inside
// the container via `docker compose exec <svc> wget -qO- localhost:<port>/metrics`.
// This works under `docker compose up --scale N` (where ports aren't
// published to the host) and replaces the prior host-side localhost probe.
type ContainerMetricsCheck struct {
	Service string
	Port    int
	Exec    Execer
}

func (c ContainerMetricsCheck) Name() string { return c.Service + " /metrics" }

func (c ContainerMetricsCheck) Run(ctx context.Context) Result {
	url := fmt.Sprintf("http://localhost:%d/metrics", c.Port)
	out, err := c.Exec.Exec(ctx, c.Service, "wget", "-qO-", url)
	if err != nil {
		return Result{Name: c.Name(), OK: false, Detail: fmt.Sprintf("compose exec failed: %v", err)}
	}
	if !strings.Contains(out, "# HELP") && !strings.Contains(out, "# TYPE") {
		return Result{Name: c.Name(), OK: false, Detail: "response not in Prometheus exposition format"}
	}
	return Result{Name: c.Name(), OK: true, Detail: "via docker compose exec " + c.Service}
}
