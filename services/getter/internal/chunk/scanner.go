package chunk

import (
	"bufio"
	"io"
)

// NewLineScanner returns a bufio.Scanner with a custom max line size,
// configured to split on \n and strip a trailing \r. This handles LF,
// CRLF, and mixed line endings uniformly.
func NewLineScanner(r io.Reader, maxLineBytes int) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	sc.Split(splitLinesStripCR)
	return sc
}

func splitLinesStripCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			line := data[:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			return i + 1, line, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil // request more data
}
