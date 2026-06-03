// Package version exposes ldflags-injected build metadata. Mutable
// package-level vars so the test harness can stub them deterministically.
package version

var (
	Version      = "dev"
	Commit       = "unknown"
	ProtoVersion = "v1"
)
