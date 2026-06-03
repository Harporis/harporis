package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunAllCollectsResults(t *testing.T) {
	checks := []Check{
		StaticCheck("always-ok", true, "no detail"),
		StaticCheck("always-bad", false, "broken"),
	}
	results := RunAll(checks)
	if len(results) != 2 {
		t.Fatalf("got %d", len(results))
	}
	if !results[0].OK || results[1].OK {
		t.Fatalf("unexpected: %+v", results)
	}
	if results[0].Detail != "no detail" || results[1].Detail != "broken" {
		t.Fatalf("detail not propagated: %+v", results)
	}
}

type fakeExecer struct {
	gotService string
	gotCmd     []string
	out        string
	err        error
}

func (f *fakeExecer) Exec(_ context.Context, service string, cmd ...string) (string, error) {
	f.gotService = service
	f.gotCmd = cmd
	return f.out, f.err
}

func TestContainerMetricsCheck_OK(t *testing.T) {
	exec := &fakeExecer{
		out: "# HELP harporis_getter_chunks_total\n# TYPE harporis_getter_chunks_total counter\nharporis_getter_chunks_total 0\n",
	}
	c := ContainerMetricsCheck{Service: "getter", Port: 9100, Exec: exec}
	r := c.Run(context.Background())
	if !r.OK {
		t.Fatalf("expected OK, got %+v", r)
	}
	if r.Name != "getter /metrics" {
		t.Fatalf("name: %q", r.Name)
	}
	if exec.gotService != "getter" {
		t.Fatalf("service: %q", exec.gotService)
	}
	want := []string{"wget", "-qO-", "http://localhost:9100/metrics"}
	if len(exec.gotCmd) != len(want) {
		t.Fatalf("cmd len: %v", exec.gotCmd)
	}
	for i := range want {
		if exec.gotCmd[i] != want[i] {
			t.Fatalf("cmd[%d]: %q != %q", i, exec.gotCmd[i], want[i])
		}
	}
}

func TestContainerMetricsCheck_ExecFailure(t *testing.T) {
	exec := &fakeExecer{err: errors.New("service not running")}
	c := ContainerMetricsCheck{Service: "scanner", Port: 9101, Exec: exec}
	r := c.Run(context.Background())
	if r.OK {
		t.Fatalf("expected fail, got %+v", r)
	}
	if !strings.Contains(r.Detail, "service not running") {
		t.Fatalf("detail did not propagate error: %q", r.Detail)
	}
}

func TestContainerMetricsCheck_BadResponseBody(t *testing.T) {
	exec := &fakeExecer{out: "<html>404 not found</html>"}
	c := ContainerMetricsCheck{Service: "scanner", Port: 9101, Exec: exec}
	r := c.Run(context.Background())
	if r.OK {
		t.Fatalf("expected fail on non-prom output, got %+v", r)
	}
	if !strings.Contains(r.Detail, "Prometheus") {
		t.Fatalf("detail did not flag format: %q", r.Detail)
	}
}
