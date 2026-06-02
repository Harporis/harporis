package detect

// LineIndex maps a byte offset within a joined-text representation of a
// GitRowChunk back to a 0-based line number. The chunk's rows[].content
// values are joined with '\n'; line N starts at sum(len(rows[0..N-1])) + N
// (the +N accounts for the joining newlines).
type LineIndex struct {
	// starts[i] = first byte offset of line i in the joined text.
	starts []int
}

// NewLineIndex builds the index from row lengths (one int per row,
// in order). Cost: O(rows). Memory: O(rows).
func NewLineIndex(rowLengths []int) *LineIndex {
	starts := make([]int, len(rowLengths))
	cursor := 0
	for i, l := range rowLengths {
		starts[i] = cursor
		cursor += l + 1 // +1 for the joining '\n'
	}
	return &LineIndex{starts: starts}
}

// LineAt returns the 0-based line number that contains the given byte
// offset. Offsets past the end clamp to the last line.
func (li *LineIndex) LineAt(off int) int {
	// Binary search for the largest starts[i] <= off.
	lo, hi := 0, len(li.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li.starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}
