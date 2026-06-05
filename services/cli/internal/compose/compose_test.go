package compose

import (
	"context"
	"strings"
	"testing"
)

// stubRunner returns a canned output for every Run call and remembers
// what was asked. For tests that need different responses per call,
// use scriptedRunner below.
type stubRunner struct {
	calls [][]string
	out   string
	err   error
}

func (s *stubRunner) Run(_ context.Context, args ...string) (string, error) {
	s.calls = append(s.calls, args)
	return s.out, s.err
}

// scriptedRunner returns response[i] for call i; falls back to "" once
// the script is exhausted.
type scriptedRunner struct {
	calls    [][]string
	response []string
	errs     []error
}

func (s *scriptedRunner) Run(_ context.Context, args ...string) (string, error) {
	idx := len(s.calls)
	s.calls = append(s.calls, args)
	if idx < len(s.response) {
		var err error
		if idx < len(s.errs) {
			err = s.errs[idx]
		}
		return s.response[idx], err
	}
	return "", nil
}

func TestExec_ResolvesContainerByLabelThenExecs(t *testing.T) {
	// First call: ps --filter (project,service) -> name. Second call: exec.
	r := &scriptedRunner{response: []string{"harporis-scanner-1", "metrics body"}}
	c := New(r)
	out, err := c.Exec(context.Background(), "scanner", "wget", "-qO-", "http://localhost:9101/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if out != "metrics body" {
		t.Fatalf("Exec returned %q, want %q", out, "metrics body")
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 docker calls (ps + exec), got %d: %v", len(r.calls), r.calls)
	}
	if r.calls[0][0] != "__direct__" || r.calls[0][1] != "ps" {
		t.Errorf("first call should be __direct__ ps, got %v", r.calls[0])
	}
	hasProj, hasSvc := false, false
	for _, a := range r.calls[0] {
		if a == "label=com.docker.compose.project=harporis" {
			hasProj = true
		}
		if a == "label=com.docker.compose.service=scanner" {
			hasSvc = true
		}
	}
	if !hasProj || !hasSvc {
		t.Errorf("ps filters missing project/service: %v", r.calls[0])
	}
	want := []string{"__direct__", "exec", "harporis-scanner-1", "wget", "-qO-", "http://localhost:9101/metrics"}
	if !equalSlice(r.calls[1], want) {
		t.Errorf("exec call = %v, want %v", r.calls[1], want)
	}
}

func TestExec_ErrorsWhenStackNotRunning(t *testing.T) {
	// ps returns empty -> no container.
	r := &stubRunner{out: ""}
	c := New(r)
	_, err := c.Exec(context.Background(), "scanner", "wget", "x")
	if err == nil || !strings.Contains(err.Error(), "no running container") {
		t.Errorf("expected 'no running container' error, got %v", err)
	}
}

func TestExec_PicksFirstReplicaWhenScaledOut(t *testing.T) {
	r := &scriptedRunner{response: []string{"harporis-scanner-1\nharporis-scanner-2\nharporis-scanner-3", "ok"}}
	c := New(r)
	if _, err := c.Exec(context.Background(), "scanner", "wget", "x"); err != nil {
		t.Fatal(err)
	}
	if r.calls[1][2] != "harporis-scanner-1" {
		t.Errorf("expected first replica, got %v", r.calls[1])
	}
}

func TestExec_HonorsCOMPOSE_PROJECT_NAME(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "custom-proj")
	r := &scriptedRunner{response: []string{"custom-proj-scanner-1", "ok"}}
	c := New(r)
	if _, err := c.Exec(context.Background(), "scanner", "x"); err != nil {
		t.Fatal(err)
	}
	if !sliceContains(r.calls[0], "label=com.docker.compose.project=custom-proj") {
		t.Errorf("project name not used in label filter: %v", r.calls[0])
	}
}

func TestLifecycle_PrependsProjectAndDiscoveredFile(t *testing.T) {
	// Up calls composeArgs which iterates 4 services trying composeFile.
	// First (getter) succeeds: ps returns container name, inspect returns
	// the file path. Then `up -d` runs with -p + -f prefixed.
	r := &scriptedRunner{response: []string{
		"harporis-getter-1",                  // ps for getter
		"/home/me/harporis/docker-compose.yml", // inspect for getter
		"Created",                            // the up call itself
	}}
	c := New(r)
	if _, err := c.Up(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 {
		t.Fatalf("expected ps + inspect + up, got %d calls: %v", len(r.calls), r.calls)
	}
	upArgs := r.calls[2]
	want := []string{"-p", "harporis", "-f", "/home/me/harporis/docker-compose.yml", "up", "-d"}
	if !equalSlice(upArgs, want) {
		t.Errorf("up call = %v, want %v", upArgs, want)
	}
}

func TestLifecycle_NoFileFlagWhenStackDown(t *testing.T) {
	// All 4 service lookups fail because ps returns empty.
	r := &stubRunner{out: ""}
	c := New(r)
	if _, err := c.PS(context.Background()); err != nil {
		t.Fatal(err)
	}
	// PS = 4 failed lookups + the ps call itself = 5 calls; last is the ps.
	last := r.calls[len(r.calls)-1]
	want := []string{"-p", "harporis", "ps"}
	if !equalSlice(last, want) {
		t.Errorf("last call = %v, want %v (no -f when stack is down)", last, want)
	}
}

func sliceContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
