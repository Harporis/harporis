package resource

import "sync"

// LineBufferPool holds [256]byte slices for typical source lines.
var LineBufferPool = sync.Pool{
	New: func() any { b := make([]byte, 0, 256); return &b },
}
