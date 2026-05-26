package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Harporis/harporis/services/cli/internal/version"
)

func TestVersionCommand(t *testing.T) {
	version.Version = "v9.9.9-test"
	version.Commit = "abcd123"
	version.ProtoVersion = "v1"
	t.Cleanup(func() {
		version.Version, version.Commit, version.ProtoVersion = "dev", "unknown", "v1"
	})

	var stdout, stderr bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"--quiet", "version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := stdout.String()
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr output: %s", stderr.String())
	}
	for _, want := range []string{"v9.9.9-test", "abcd123", "v1"} {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q: %s", want, got)
		}
	}
}
