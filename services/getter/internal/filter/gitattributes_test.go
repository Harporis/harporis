package filter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitAttributes_IsBinary(t *testing.T) {
	source := `
# comments and blanks ignored
*.png binary
*.bin binary
src/special/*.dat -text
docs/* text
`
	attrs, err := ParseGitAttributes(strings.NewReader(source))
	require.NoError(t, err)

	cases := []struct {
		path string
		want bool
	}{
		{"foo.png", true},
		{"img/bar.bin", true},
		{"src/special/file.dat", true},
		{"src/main.go", false},
		{"docs/readme.md", false}, // explicit text
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.want, attrs.IsBinary(tc.path))
		})
	}
}

func TestGitAttributes_Empty(t *testing.T) {
	attrs, err := ParseGitAttributes(strings.NewReader(""))
	require.NoError(t, err)
	require.False(t, attrs.IsBinary("any/file.txt"))
}
