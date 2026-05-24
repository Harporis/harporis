package filter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchAnyGlob(t *testing.T) {
	patterns := []string{".git/", "node_modules/", "dist/"}
	cases := []struct {
		path string
		want bool
	}{
		{".git/HEAD", true},
		{"src/.git/x", true}, // glob applies anywhere in path
		{"node_modules/foo/bar.js", true},
		{"src/main.go", false},
		{"distance.md", false}, // "dist/" is dir-style, must end with /
		{"dist/app.js", true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.want, MatchAnyGlob(tc.path, patterns))
		})
	}
}

func TestIsBinaryExtension(t *testing.T) {
	exts := []string{".png", ".jpg", ".PDF"} // case-insensitive lookup
	cases := []struct {
		path string
		want bool
	}{
		{"img.png", true},
		{"img.PNG", true},
		{"doc.pdf", true},
		{"src/foo.go", false},
		{"NoExtension", false},
		{".bashrc", false}, // hidden file with no ext after dot is not in list
	}
	set := BuildExtensionSet(exts)
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.want, IsBinaryExtension(tc.path, set))
		})
	}
}

func TestHasNULByte(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"plain text", []byte("hello world\nline 2\n"), false},
		{"UTF-8 multibyte", []byte("привет\n"), false},
		{"contains NUL", []byte("text\x00more"), true},
		{"empty", []byte{}, false},
		{"only NUL", []byte{0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, HasNULByte(tc.data))
		})
	}
}

func TestShouldScan_Layered(t *testing.T) {
	flt := &Filter{
		PathExclusions:   []string{".git/", "node_modules/"},
		BinaryExtensions: BuildExtensionSet([]string{".png", ".jpg"}),
		MaxFileSize:      int64(10 * 1024 * 1024),
		GitAttrs:         emptyAttrs(t),
	}
	cases := []struct {
		name       string
		path       string
		size       int64
		sample     []byte
		wantOK     bool
		wantReason SkipReason
	}{
		{"text source file", "src/main.go", 200, []byte("package main\n"), true, ""},
		{"excluded path", ".git/HEAD", 50, []byte("ref:"), false, ReasonPathExcluded},
		{"binary extension", "img.png", 100, []byte("\x89PNG"), false, ReasonBinaryExtension},
		{"too big", "big.txt", 11 * 1024 * 1024, []byte("hi"), false, ReasonSizeCap},
		{"NUL byte", "weird.txt", 50, []byte("text\x00bytes"), false, ReasonNULByte},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := flt.ShouldScan(tc.path, tc.size, tc.sample)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantReason, reason)
		})
	}
}

func emptyAttrs(t *testing.T) *GitAttributes {
	a, err := ParseGitAttributes(strings.NewReader(""))
	require.NoError(t, err)
	return a
}
