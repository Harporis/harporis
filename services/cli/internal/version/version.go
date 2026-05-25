// Package version holds build-time identity injected via -ldflags.
package version

var (
	// Version is the semver tag at build time, or "dev-<sha>" for untagged builds.
	Version = "dev"
	// Commit is the short git SHA.
	Commit = "unknown"
	// ProtoVersion is the major version of the harporis proto contract this binary speaks.
	ProtoVersion = "v1"
)
