package engine

import (
	"io"
	"os"
)

// offsetWriter adapts an *os.File into an io.Writer that appends to a tracked
// offset via WriteAt, so each segment streams into its own region of the .part
// file with no shared write cursor. off advances by the number of bytes written.
//
// It exists so the copy loop can use io.CopyBuffer while keeping the pooled
// buffer: passing *os.File directly to io.CopyBuffer would take the ReaderFrom
// fast path, which both bypasses our buffer and ignores the per-segment offset.
type offsetWriter struct {
	f   *os.File
	off int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	n, err := w.f.WriteAt(p, w.off)
	w.off += int64(n)
	return n, err
}

// copySegment streams src into dst through buf, returning the number of bytes
// copied. dst must NOT be an *os.File (use offsetWriter): io.CopyBuffer would
// otherwise detect ReaderFrom and bypass buf, breaking the zero-alloc guarantee
// and the offset tracking.
//
// This is the per-chunk hot path: with a non-nil buf and a dst/src that expose
// neither ReaderFrom nor WriterTo, io.CopyBuffer reuses buf for every iteration
// and allocates nothing, as proven by BenchmarkCopySegment.
func copySegment(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	return io.CopyBuffer(dst, src, buf)
}
