package detect

import "testing"

func TestLineIndex_SingleLine(t *testing.T) {
	idx := NewLineIndex([]int{10}) // one line, 10 bytes
	if got := idx.LineAt(0); got != 0 {
		t.Errorf("LineAt(0) = %d, want 0", got)
	}
	if got := idx.LineAt(9); got != 0 {
		t.Errorf("LineAt(9) = %d, want 0", got)
	}
}

func TestLineIndex_MultipleLines(t *testing.T) {
	// 3 lines of 5 bytes each, joined by \n → offsets 0-4 line 0, 6-10 line 1, 12-16 line 2.
	idx := NewLineIndex([]int{5, 5, 5})
	tests := []struct {
		off  int
		line int
	}{
		{0, 0},
		{4, 0},
		{5, 0},  // the \n itself belongs to line 0 by convention
		{6, 1},
		{10, 1},
		{11, 1}, // second \n
		{12, 2},
		{16, 2},
	}
	for _, tt := range tests {
		if got := idx.LineAt(tt.off); got != tt.line {
			t.Errorf("LineAt(%d) = %d, want %d", tt.off, got, tt.line)
		}
	}
}

func TestLineIndex_OffsetPastEnd(t *testing.T) {
	idx := NewLineIndex([]int{5, 5})
	// Past-end offsets clamp to the last line.
	if got := idx.LineAt(1000); got != 1 {
		t.Errorf("LineAt(1000) = %d, want 1", got)
	}
}
