package chunk

import (
	"github.com/google/uuid"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

type BuilderConfig struct {
	RowSizeTargetBytes int
	OverlapLines       int
	ScanID             string
}

type Source struct {
	Kind              v1.ChunkKind
	BlobSHA           []byte
	Refs              []*v1.CommitFileRef
	CommitSHA         []byte
	FilePath          string
	ContextLinesAbove int32
	ContextLinesBelow int32
}

func BlobSource(blobSHA []byte, refs []*v1.CommitFileRef) Source {
	return Source{Kind: v1.ChunkKind_BLOB, BlobSHA: blobSHA, Refs: refs}
}

func DiffWindowSource(commitSHA []byte, filePath string, above, below int32) Source {
	return Source{
		Kind:              v1.ChunkKind_DIFF_WINDOW,
		CommitSHA:         commitSHA,
		FilePath:          filePath,
		ContextLinesAbove: above,
		ContextLinesBelow: below,
	}
}

// Builder accumulates GitRows for a single source and emits chunks
// once the byte budget is reached. Use one Builder per source.
type Builder struct {
	cfg       BuilderConfig
	src       Source
	pending   []*v1.GitRow
	pendBytes int
	chunks    []*v1.GitRowChunk
	totalLine int32
}

func NewBuilder(cfg BuilderConfig) *Builder {
	return &Builder{cfg: cfg}
}

func (b *Builder) Begin(s Source) {
	b.src = s
	b.pending = b.pending[:0]
	b.pendBytes = 0
	b.chunks = b.chunks[:0]
	b.totalLine = 0
}

func (b *Builder) AddLine(lineNumber int32, byteOffset int64, content []byte) error {
	row := &v1.GitRow{
		LineNumber: lineNumber,
		ByteOffset: byteOffset,
		Content:    append([]byte(nil), content...), // own the bytes
	}
	b.pending = append(b.pending, row)
	b.pendBytes += len(content)
	b.totalLine = lineNumber
	if b.pendBytes >= b.cfg.RowSizeTargetBytes {
		b.emit(false)
	}
	return nil
}

// Finish emits any remaining pending lines, sets ChunkCount on all chunks,
// and returns them in order.
func (b *Builder) Finish() ([]*v1.GitRowChunk, error) {
	if len(b.pending) > 0 {
		b.emit(true)
	}
	for i, ch := range b.chunks {
		ch.ChunkIndex = int32(i)
		ch.ChunkCount = int32(len(b.chunks))
		ch.TotalLines = b.totalLine
	}
	return b.chunks, nil
}

func (b *Builder) emit(final bool) {
	rows := append([]*v1.GitRow(nil), b.pending...)
	ch := &v1.GitRowChunk{
		ScanId:         b.cfg.ScanID,
		ChunkId:        uuid.NewString(),
		SequenceNumber: int64(len(b.chunks)), // global seq assigned by caller pipeline later
		Kind:           b.src.Kind,
		Rows:           rows,
		StartLine:      rows[0].LineNumber,
		EndLine:        rows[len(rows)-1].LineNumber,
	}
	switch b.src.Kind {
	case v1.ChunkKind_BLOB:
		ch.BlobSha = b.src.BlobSHA
		ch.Refs = b.src.Refs
	case v1.ChunkKind_DIFF_WINDOW:
		ch.CommitSha = b.src.CommitSHA
		ch.FilePath = b.src.FilePath
		ch.ContextLinesAbove = b.src.ContextLinesAbove
		ch.ContextLinesBelow = b.src.ContextLinesBelow
	}
	b.chunks = append(b.chunks, ch)

	if final {
		b.pending = b.pending[:0]
		b.pendBytes = 0
		return
	}
	// Carry last OverlapLines as the head of the next chunk.
	overlap := b.cfg.OverlapLines
	if overlap > len(rows) {
		overlap = len(rows)
	}
	tail := append([]*v1.GitRow(nil), rows[len(rows)-overlap:]...)
	b.pending = tail
	b.pendBytes = 0
	for _, r := range tail {
		b.pendBytes += len(r.Content)
	}
}
