package engine

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// plainReader hides any WriterTo the underlying reader might expose (bytes.Reader
// does), forcing io.CopyBuffer down the buffered path the hot loop relies on.
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

func newPartFile(t testing.TB) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "out.part"))
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestCopySegmentWritesAtOffset(t *testing.T) {
	f := newPartFile(t)
	if err := f.Truncate(16); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	src := plainReader{r: bytes.NewReader([]byte("ABCDEF"))}
	w := &offsetWriter{f: f, off: 4}
	buf := make([]byte, 3)
	n, err := copySegment(w, src, buf)
	if err != nil {
		t.Fatalf("copySegment: %v", err)
	}
	if n != 6 {
		t.Fatalf("n = %d, want 6", n)
	}

	got := make([]byte, 16)
	if _, err := f.ReadAt(got, 0); err != nil {
		t.Fatalf("readat: %v", err)
	}
	want := append(append([]byte{0, 0, 0, 0}, []byte("ABCDEF")...), make([]byte, 6)...)
	if !bytes.Equal(got, want) {
		t.Errorf("file = %v, want %v", got, want)
	}
}

// The offsetWriter must NOT implement io.ReaderFrom: that would let io.CopyBuffer
// hand the whole copy to *os.File.ReadFrom, bypassing both the pooled buffer and
// the per-segment offset.
func TestOffsetWriterIsNotReaderFrom(t *testing.T) {
	var w any = &offsetWriter{}
	if _, ok := w.(io.ReaderFrom); ok {
		t.Fatal("offsetWriter implements io.ReaderFrom; copy loop would bypass the pooled buffer and offset")
	}
}

// resettableReader streams size bytes per pass and is reset between passes, so
// the copy loop sees a fresh source without any per-pass allocation. It exposes
// no WriterTo, keeping io.CopyBuffer on the buffered (zero-alloc) path.
type resettableReader struct {
	size int64
	pos  int64
}

func (r *resettableReader) reset() { r.pos = 0 }

func (r *resettableReader) Read(b []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	n := int64(len(b))
	if rem := r.size - r.pos; n > rem {
		n = rem
	}
	for i := int64(0); i < n; i++ {
		b[i] = 'x'
	}
	r.pos += n
	return int(n), nil
}

// BenchmarkCopySegment proves the per-chunk copy loop is zero-alloc: setup
// (reader, writer, buffer) is allocated once, and each iteration only resets and
// re-runs the loop, so any nonzero allocs/op comes from the loop itself.
func BenchmarkCopySegment(b *testing.B) {
	const size = 1 << 20
	f := newPartFile(b)
	if err := f.Truncate(size); err != nil {
		b.Fatalf("truncate: %v", err)
	}
	src := &resettableReader{size: size}
	w := &offsetWriter{f: f}
	buf := make([]byte, 64*1024)

	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		src.reset()
		w.off = 0
		if _, err := copySegment(w, src, buf); err != nil {
			b.Fatalf("copySegment: %v", err)
		}
	}
}

// TestCopySegmentZeroAllocs asserts the loop itself allocates nothing per run by
// reusing pre-allocated setup, mirroring the engine's pooled-buffer hot path.
func TestCopySegmentZeroAllocs(t *testing.T) {
	const size = 256 * 1024
	f := newPartFile(t)
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	src := &resettableReader{size: size}
	w := &offsetWriter{f: f}
	buf := make([]byte, 64*1024)

	allocs := testing.AllocsPerRun(100, func() {
		src.reset()
		w.off = 0
		if _, err := copySegment(w, src, buf); err != nil {
			t.Fatalf("copySegment: %v", err)
		}
	})
	if allocs != 0 {
		t.Errorf("copySegment allocations = %v per run, want 0", allocs)
	}
}
