// Package version exposes build identity, populated via -ldflags at build time.
// Defaults are safe for unit tests and "go run" without ldflags.
package version

var (
	Version      = "dev"
	Commit       = "unknown"
	ProtoVersion = "v1"
)

// String returns "scanner/<Version>", a stable form embedded in every Finding.
func String() string { return "scanner/" + Version }
