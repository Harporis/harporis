package compose

import (
	"context"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.out, f.err
}

func TestUpDetached(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	c := New(r)
	if _, err := c.Up(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"up", "-d"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestUpWithBuild(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	c := New(r)
	if _, err := c.Up(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"up", "-d", "--build"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestDownVolumes(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	c := New(r)
	if _, err := c.Down(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"down", "-v"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestPSPassesPSCommand(t *testing.T) {
	r := &fakeRunner{out: "Name State"}
	c := New(r)
	out, _ := c.PS(context.Background())
	if !strings.Contains(out, "Name") {
		t.Fatal("output not surfaced")
	}
	if !equalSlice(r.calls[0], []string{"ps"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestLogsFollowAndService(t *testing.T) {
	r := &fakeRunner{out: "log"}
	c := New(r)
	if _, err := c.Logs(context.Background(), "getter", true); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"logs", "-f", "getter"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestExecPassesServiceAndCommand(t *testing.T) {
	r := &fakeRunner{out: "body"}
	c := New(r)
	out, err := c.Exec(context.Background(), "scanner", "wget", "-qO-", "http://localhost:9101/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if out != "body" {
		t.Fatalf("output not surfaced: %q", out)
	}
	want := []string{"exec", "-T", "scanner", "wget", "-qO-", "http://localhost:9101/metrics"}
	if !equalSlice(r.calls[0], want) {
		t.Fatalf("got %v, want %v", r.calls[0], want)
	}
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
