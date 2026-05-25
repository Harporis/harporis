package filter

import (
	"bytes"
	"path/filepath"
	"strings"
)

// MatchAnyGlob returns true if any pattern matches the given path.
// Patterns ending in "/" match any path component that contains
// the prefix as a directory segment.
// Other patterns are matched via filepath.Match against the basename
// AND across the whole path joined with '/'.
func MatchAnyGlob(path string, patterns []string) bool {
	norm := filepath.ToSlash(path)
	for _, p := range patterns {
		if strings.HasSuffix(p, "/") {
			// directory match: pattern must appear as a /-bounded segment
			needle := p
			if strings.Contains("/"+norm+"/", "/"+needle) {
				return true
			}
			continue
		}
		// glob match against basename and full path
		base := filepath.Base(norm)
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
		if ok, _ := filepath.Match(p, norm); ok {
			return true
		}
	}
	return false
}

// BuildExtensionSet returns a lookup set of lowercase extensions.
func BuildExtensionSet(exts []string) map[string]struct{} {
	out := make(map[string]struct{}, len(exts))
	for _, e := range exts {
		out[strings.ToLower(e)] = struct{}{}
	}
	return out
}

// IsBinaryExtension returns true if the path's lowercase extension is in the set.
func IsBinaryExtension(path string, set map[string]struct{}) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	_, ok := set[ext]
	return ok
}

// HasNULByte reports whether sample contains a NUL byte. The typical
// caller passes the first 8 KiB of a blob — git's own heuristic for
// binary detection.
func HasNULByte(sample []byte) bool {
	return bytes.IndexByte(sample, 0) >= 0
}

// SkipReason is a label written to metrics when a file is excluded.
type SkipReason string

const (
	ReasonPathExcluded        SkipReason = "path_excluded"
	ReasonBinaryExtension     SkipReason = "binary_extension"
	ReasonSizeCap             SkipReason = "size_cap"
	ReasonGitAttributesBinary SkipReason = "gitattributes_binary"
	ReasonNULByte             SkipReason = "nul_byte"
)

// Filter encapsulates the 5-layer file filter configuration.
type Filter struct {
	PathExclusions   []string
	BinaryExtensions map[string]struct{}
	MaxFileSize      int64
	GitAttrs         *GitAttributes
}

// ShouldScan applies the 5-layer filter in order:
//  1. Path glob exclusion (zero I/O)
//  2. Extension blacklist
//  3. Size cap
//  4. .gitattributes binary
//  5. NUL-byte sniff
//
// sample should be the first ~8 KiB of the blob; it is only inspected if
// the prior four checks pass. Pass nil to defer the NUL-byte check.
func (f *Filter) ShouldScan(path string, size int64, sample []byte) (bool, SkipReason) {
	if MatchAnyGlob(path, f.PathExclusions) {
		return false, ReasonPathExcluded
	}
	if IsBinaryExtension(path, f.BinaryExtensions) {
		return false, ReasonBinaryExtension
	}
	if size > f.MaxFileSize {
		return false, ReasonSizeCap
	}
	if f.GitAttrs != nil && f.GitAttrs.IsBinary(path) {
		return false, ReasonGitAttributesBinary
	}
	if sample != nil && HasNULByte(sample) {
		return false, ReasonNULByte
	}
	return true, ""
}
