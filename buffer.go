package unsparse

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"sort"
)

// Buffer is an in-memory sparse file. Only written regions use memory;
// everything else reads back as zeroes.
//
// It implements [SparseReader], [TruncateWriter] and [DataFinder], so it can
// be used as either side of [Unsparse] and [Sparse].
//
// The zero value is an empty buffer ready to use. A Buffer is not safe for
// concurrent use.
//
// Example:
//
//	// A 1 TiB buffer with two small regions of data uses only a few bytes.
//	b := unsparse.NewBuffer(1 << 40)
//	b.WriteAt([]byte("hello"), 0)
//	b.WriteAt([]byte("world"), 1<<39)
//
//	var packed bytes.Buffer
//	if err := unsparse.Unsparse(&packed, b); err != nil {
//		panic(err)
//	}
//	// packed holds the [Unsparsed] format: a header and two extents
//
//	restored := unsparse.NewBuffer(0)
//	if err := unsparse.Sparse(restored, &packed); err != nil {
//		panic(err)
//	}
//	// restored.Size() == 1<<40, with the same two regions of data
type Buffer struct {
	size int64
	// extents are sorted by offset, non-empty, and never overlap or touch,
	// so each one is a maximal run of data.
	extents []bufExtent
}

type bufExtent struct {
	off  int64
	data []byte
}

func (e bufExtent) end() int64 { return e.off + int64(len(e.data)) }

// NewBuffer returns a Buffer of the given size that is entirely a hole. No
// memory is allocated for the hole, however large size is.
//
// It panics if size is negative.
//
// Example:
//
//	b := unsparse.NewBuffer(4096)
//	b.WriteAt([]byte("data"), 1024)
//
//	start, end, _ := b.NextData(0)
//	// start == 1024, end == 1028
func NewBuffer(size int64) *Buffer {
	if size < 0 {
		panic("unsparse: NewBuffer: negative size")
	}
	return &Buffer{size: size}
}

var errNegativeOffset = errors.New("unsparse: negative offset")

// Size returns the apparent size of the buffer.
func (b *Buffer) Size() int64 { return b.size }

// search returns the index of the first extent ending after off.
func (b *Buffer) search(off int64) int {
	return sort.Search(len(b.extents), func(i int) bool { return b.extents[i].end() > off })
}

// ReadAt implements [io.ReaderAt].
func (b *Buffer) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errNegativeOffset
	}
	if off >= b.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), b.size-off))
	clear(p[:n])
	for _, e := range b.extents[b.search(off):] {
		if e.off >= off+int64(n) {
			break
		}
		lo := max(e.off, off)
		copy(p[lo-off:n], e.data[lo-e.off:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// WriteAt implements [io.WriterAt], growing the buffer if needed.
func (b *Buffer) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errNegativeOffset
	}
	if len(p) == 0 {
		return 0, nil
	}
	end := off + int64(len(p))
	b.size = max(b.size, end)

	// Extents in [i, j) overlap or touch [off, end] and are merged with p.
	i := sort.Search(len(b.extents), func(i int) bool { return b.extents[i].end() >= off })
	j := sort.Search(len(b.extents), func(j int) bool { return b.extents[j].off > end })

	merged := bufExtent{off: off}
	newEnd := end
	if i < j {
		merged.off = min(off, b.extents[i].off)
		newEnd = max(end, b.extents[j-1].end())
	}
	size := int(newEnd - merged.off)

	if i < j && b.extents[i].off == merged.off {
		// Extend the first extent in place, so sequential writes append
		// rather than copy.
		data := b.extents[i].data
		n := len(data)
		data = slices.Grow(data, size-n)[:size]
		clear(data[n:])
		merged.data = data
	} else {
		merged.data = make([]byte, size)
	}
	for _, e := range b.extents[i:j] {
		copy(merged.data[e.off-merged.off:], e.data)
	}
	copy(merged.data[off-merged.off:], p)

	b.extents = slices.Replace(b.extents, i, j, merged)
	return len(p), nil
}

// Truncate changes the size of the buffer, discarding data past size and
// freeing its memory. Growing it adds a hole.
func (b *Buffer) Truncate(size int64) error {
	if size < 0 {
		return errors.New("unsparse: negative size")
	}
	i := b.search(size)
	if i < len(b.extents) && b.extents[i].off < size {
		e := &b.extents[i]
		if keep := size - e.off; keep < int64(cap(e.data))/2 {
			// Copy so the discarded tail can be freed.
			e.data = bytes.Clone(e.data[:keep])
		} else {
			e.data = e.data[:keep]
		}
		i++
	}
	clear(b.extents[i:])
	b.extents = b.extents[:i]
	b.size = size
	return nil
}

// NextData implements [DataFinder].
func (b *Buffer) NextData(off int64) (start, end int64, err error) {
	i := b.search(off)
	if i == len(b.extents) {
		return 0, 0, io.EOF
	}
	e := b.extents[i]
	return max(off, e.off), e.end(), nil
}
