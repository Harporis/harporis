// Package doctor defines diagnostic checks and a runner.
package doctor

import (
	"context"
	"net/http"
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

// GetterHealthCheck hits /metrics on localhost:9100.
type GetterHealthCheck struct{}

func (GetterHealthCheck) Name() string { return "getter /metrics" }
func (GetterHealthCheck) Run(ctx context.Context) Result {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:9100/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Name: "getter /metrics", OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Result{Name: "getter /metrics", OK: false, Detail: "non-200 status"}
	}
	return Result{Name: "getter /metrics", OK: true, Detail: "HTTP 200"}
}
