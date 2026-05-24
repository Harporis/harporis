package filter

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"
)

// GitAttributes holds parsed entries from one or more .gitattributes files.
// Only the binary classification ("binary" macro or "-text" attribute) is tracked;
// other attributes are ignored.
type GitAttributes struct {
	rules []binaryRule
}

type binaryRule struct {
	pattern string
	binary  bool // true => match means binary
}

func ParseGitAttributes(r io.Reader) (*GitAttributes, error) {
	out := &GitAttributes{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pattern := fields[0]
		var binary bool
		var matched bool
		for _, attr := range fields[1:] {
			switch attr {
			case "binary", "-text":
				binary = true
				matched = true
			case "text":
				binary = false
				matched = true
			}
		}
		if matched {
			out.rules = append(out.rules, binaryRule{pattern: pattern, binary: binary})
		}
	}
	return out, scanner.Err()
}

// IsBinary returns true if the path matches a rule that classifies it as binary.
// Later matching rules override earlier ones (git semantics).
func (g *GitAttributes) IsBinary(path string) bool {
	norm := filepath.ToSlash(path)
	binary := false
	for _, r := range g.rules {
		if matchAttributePattern(r.pattern, norm) {
			binary = r.binary
		}
	}
	return binary
}

func matchAttributePattern(pattern, path string) bool {
	// Simplified matcher: support shell glob via filepath.Match against full path
	// and against the basename. Real git supports more, but this covers MVP.
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
		return true
	}
	return false
}
