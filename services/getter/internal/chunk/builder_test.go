package chunk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestBuilder_SingleSmallBlob(t *testing.T) {
	b := NewBuilder(BuilderConfig{
		RowSizeTargetBytes: 1024,
		OverlapLines:       2,
	})
	b.Begin(BlobSource([]byte("sha1"), []*v1.CommitFileRef{
		{CommitSha: []byte("c1"), Path: "a.go"},
	}))
	for i := 1; i <= 5; i++ {
		require.NoError(t, b.AddLine(int32(i), int64(i*10), []byte(fmt.Sprintf("line%d", i))))
	}
	chunks, err := b.Finish()
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, []byte("sha1"), chunks[0].BlobSha)
	require.Equal(t, int32(1), chunks[0].StartLine)
	require.Equal(t, int32(5), chunks[0].EndLine)
	require.Equal(t, int32(5), chunks[0].TotalLines)
	require.Equal(t, int32(0), chunks[0].ChunkIndex)
	require.Equal(t, int32(1), chunks[0].ChunkCount)
	require.Len(t, chunks[0].Rows, 5)
	require.Equal(t, v1.ChunkKind_BLOB, chunks[0].Kind)
}

func TestBuilder_SplitsWithOverlap(t *testing.T) {
	// Each line is 100 bytes, target 250 bytes → ~2 lines per chunk.
	b := NewBuilder(BuilderConfig{
		RowSizeTargetBytes: 250,
		OverlapLines:       1,
	})
	b.Begin(BlobSource([]byte("sha2"), []*v1.CommitFileRef{{CommitSha: []byte("c"), Path: "big.txt"}}))

	line := strings.Repeat("a", 100)
	for i := 1; i <= 6; i++ {
		require.NoError(t, b.AddLine(int32(i), int64((i-1)*101), []byte(line)))
	}
	chunks, err := b.Finish()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(chunks), 2)

	// Every consecutive chunk pair should share the overlap line(s).
	for i := 0; i < len(chunks)-1; i++ {
		last := chunks[i].EndLine
		first := chunks[i+1].StartLine
		require.LessOrEqual(t, first, last, "chunk[%d].EndLine=%d, chunk[%d].StartLine=%d (expected overlap)", i, last, i+1, first)
	}
	// Reconstruction: union of unique line numbers covers 1..6.
	seen := map[int32]bool{}
	for _, ch := range chunks {
		for _, r := range ch.Rows {
			seen[r.LineNumber] = true
		}
	}
	for i := int32(1); i <= 6; i++ {
		require.True(t, seen[i], "line %d should appear in at least one chunk", i)
	}
	// chunk_count is consistent
	require.Equal(t, int32(len(chunks)), chunks[0].ChunkCount)
}

func TestBuilder_DiffWindowKind(t *testing.T) {
	b := NewBuilder(BuilderConfig{RowSizeTargetBytes: 1024, OverlapLines: 0})
	b.Begin(DiffWindowSource([]byte("commit-abc"), "src/foo.go", 30, 30))
	require.NoError(t, b.AddLine(10, 0, []byte("hunk line 1")))
	require.NoError(t, b.AddLine(11, 12, []byte("hunk line 2")))
	chunks, err := b.Finish()
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, v1.ChunkKind_DIFF_WINDOW, chunks[0].Kind)
	require.Equal(t, []byte("commit-abc"), chunks[0].CommitSha)
	require.Equal(t, "src/foo.go", chunks[0].FilePath)
	require.Equal(t, int32(30), chunks[0].ContextLinesAbove)
}

func TestBuilder_EmptyFinish(t *testing.T) {
	b := NewBuilder(BuilderConfig{RowSizeTargetBytes: 1024, OverlapLines: 2})
	b.Begin(BlobSource([]byte("sha"), nil))
	chunks, err := b.Finish()
	require.NoError(t, err)
	require.Empty(t, chunks) // empty file: no chunks emitted
}

func TestBuilder_MultilineSecretAlwaysCaughtInOneChunk(t *testing.T) {
	// Simulate a PEM block straddling chunk boundaries. The builder must
	// guarantee that, for any multi-line secret of length <= OverlapLines+1,
	// at least one chunk contains the FULL secret as a contiguous slice.
	const overlap = 4

	// Build a "file" of 30 short lines, with a 5-line PEM somewhere inside.
	lines := make([]string, 0, 30)
	for i := 0; i < 12; i++ {
		lines = append(lines, "before line")
	}
	pem := []string{
		"-----BEGIN PRIVATE KEY-----",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"BBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		"CCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		"-----END PRIVATE KEY-----",
	}
	lines = append(lines, pem...)
	for i := 0; i < 13; i++ {
		lines = append(lines, "after line")
	}

	// Small RowSize forces multiple chunks.
	b := NewBuilder(BuilderConfig{
		RowSizeTargetBytes: 80,
		OverlapLines:       overlap,
	})
	b.Begin(BlobSource([]byte("sha"), nil))
	off := int64(0)
	for i, l := range lines {
		require.NoError(t, b.AddLine(int32(i+1), off, []byte(l)))
		off += int64(len(l) + 1)
	}
	chunks, err := b.Finish()
	require.NoError(t, err)

	// Some chunk must contain the entire PEM (5 lines) contiguously.
	found := false
	for _, ch := range chunks {
		joined := joinRows(ch.Rows)
		if strings.Contains(joined, strings.Join(pem, "\n")) {
			found = true
			break
		}
	}
	require.True(t, found, "expected at least one chunk to contain the complete PEM block")
}

func joinRows(rows []*v1.GitRow) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = string(r.Content)
	}
	return strings.Join(parts, "\n")
}
