package chunk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLineScanner_LF(t *testing.T) {
	data := "line1\nline2\nline3\n"
	sc := NewLineScanner(strings.NewReader(data), 64*1024)
	var got []string
	for sc.Scan() {
		got = append(got, string(sc.Bytes()))
	}
	require.NoError(t, sc.Err())
	require.Equal(t, []string{"line1", "line2", "line3"}, got)
}

func TestLineScanner_CRLF(t *testing.T) {
	data := "a\r\nb\r\nc\r\n"
	sc := NewLineScanner(strings.NewReader(data), 64*1024)
	var got []string
	for sc.Scan() {
		got = append(got, string(sc.Bytes()))
	}
	require.NoError(t, sc.Err())
	require.Equal(t, []string{"a", "b", "c"}, got)
}

func TestLineScanner_NoTrailingNewline(t *testing.T) {
	data := "no\ntrailing"
	sc := NewLineScanner(strings.NewReader(data), 64*1024)
	var got []string
	for sc.Scan() {
		got = append(got, string(sc.Bytes()))
	}
	require.NoError(t, sc.Err())
	require.Equal(t, []string{"no", "trailing"}, got)
}

func TestLineScanner_VeryLongLine(t *testing.T) {
	long := strings.Repeat("x", 200*1024)
	sc := NewLineScanner(strings.NewReader(long+"\nend\n"), 512*1024)
	var got []string
	for sc.Scan() {
		got = append(got, string(sc.Bytes()))
	}
	require.NoError(t, sc.Err())
	require.Equal(t, long, got[0])
	require.Equal(t, "end", got[1])
}

func TestLineScanner_Empty(t *testing.T) {
	sc := NewLineScanner(strings.NewReader(""), 64*1024)
	require.False(t, sc.Scan())
	require.NoError(t, sc.Err())
}
