package filter

import (
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
