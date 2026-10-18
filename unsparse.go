// Package unsparse provides tooling to [Unsparse] a sparse file.
//
// Some tools are unable to correctly represent sparse files, as such you can
// use this package to reformat the sparse file to be represented in the
// [Unsparsed] binary format.
//
// To move an [Unsparsed] file to a sparse file, you can use the [Sparse]
// function.
//
// Example:
//
//	f, err := os.Open("mysparsefile")
//	if err != nil {
//		panic(err)
//	}
//	sr, err := unsparse.SparseReaderFile(f)
//	if err != nil {
//		panic(err)
//	}
//
//	buf := bytes.NewBuffer(nil)
//	if err := unsparse.Unsparse(buf, sr); err != nil {
//		panic(err)
//	}
//
//	// buf now holds the [Unsparsed] format
package unsparse

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Magic identifies a file as being in the [Unsparsed] format. Like PNG's, it
// starts with a non-ASCII byte so text tools don't mistake it for text, and
// ends in a newline so line-ending translation is detectable.
//
// For file(1), the equivalent magic(5) entry is:
//
//	0	string	\x89UNSPRS\n	Unsparsed sparse file data
//	>8	beshort	x		\b, version %d
//	>12	bequad	x		\b, apparent size %lld bytes
var Magic = [8]byte{0x89, 'U', 'N', 'S', 'P', 'R', 'S', '\n'}

// Version is the current version of the [Unsparsed] format.
const Version uint16 = 1

// ErrNotUnsparsed is returned, wrapped, when data does not start with [Magic].
var ErrNotUnsparsed = errors.New("unsparse: not an unsparsed file (bad magic)")

// Unsparsed is the header of the binary format written by [Unsparse] and
// read by [Sparse].
//
// Since a sparse file is represented with data and holes, the format simply
// removes the holes. It is an Unsparsed header followed by zero or more
// extents, each an [Extent] header followed by its data:
//
//	[8]byte          // Unsparsed.Magic
//	[2]byte          // Unsparsed.Version
//	[2]byte          // Unsparsed.Flags
//	[8]byte          // Unsparsed.ApparentSize
//	[8]byte          // Extant.Offset
//	[8]byte          // Extant.Size
//	[Size]byte       // extent data
//	...              // repeated for each extent, until EOF
//
// All integers are big-endian. Extents are in increasing offset order and
// never overlap; anything not covered by an extent is a hole.
type Unsparsed struct {
	// Magic is always [Magic].
	Magic [8]byte
	// Version is the format version, currently [Version].
	Version uint16
	// Flags is reserved; it is zero in version 1 and readers must reject
	// flags they don't understand.
	Flags uint16
	// ApparentSize is the size of the original file, including holes.
	ApparentSize int64
}

// ReadHeader reads and validates an [Unsparsed] header from r, leaving r
// positioned at the first extent. It returns an error wrapping
// [ErrNotUnsparsed] if r doesn't start with [Magic], so it can be used to
// detect the format.
func ReadHeader(r io.Reader) (Unsparsed, error) {
	var hdr Unsparsed
	if err := binary.Read(r, byteOrder, &hdr); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return hdr, fmt.Errorf("%w: short header", ErrNotUnsparsed)
		}
		return hdr, fmt.Errorf("unsparse: reading header: %w", err)
	}
	switch {
	case hdr.Magic != Magic:
		return hdr, ErrNotUnsparsed
	case hdr.Version != Version:
		return hdr, fmt.Errorf("unsparse: unsupported version %d", hdr.Version)
	case hdr.Flags != 0:
		return hdr, fmt.Errorf("unsparse: unsupported flags %#x", hdr.Flags)
	case hdr.ApparentSize < 0:
		return hdr, fmt.Errorf("unsparse: invalid apparent size %d", hdr.ApparentSize)
	}
	return hdr, nil
}

// Extent is the header for one extent of data in the [Unsparsed] format. It
// is followed by Size bytes of data.
type Extent struct {
	// Offset is where the data starts in the original file.
	Offset int64
	// Size is the number of data bytes that follow.
	Size int64
}

var byteOrder = binary.BigEndian

// SparseReader is a source for [Unsparse], such as [*Buffer],
// *io.SectionReader or *bytes.Reader. Holes are detected when it implements
// [DataFinder], or is an *io.SectionReader over one or over an *os.File; any
// other source is treated as all data. Use [SparseReaderFile] to get one for
// an *os.File.
type SparseReader interface {
	io.ReaderAt
	// Size returns the apparent size, including holes.
	Size() int64
}

// SparseReaderFile is a convenience function to return a [SparseReader] from
// an [*os.File].
func SparseReaderFile(f *os.File) (SparseReader, error) {
	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("unsparse: statting file %q: %w", f.Name(), err)
	}
	return io.NewSectionReader(f, 0, stat.Size()), nil
}

// Unsparse reads the sparse file src and writes it to dst in the [Unsparsed]
// format, leaving out its holes. See [SparseReader] for which sources have
// their holes detected.
//
// Some tools are unable to correctly understand sparse files. Such as
// Golang's tar.Differ
//
// Example:
//
//	f, err := os.Open("mysparsefile")
//	if err != nil {
//		panic(err)
//	}
//	sr, err := unsparse.SparseReaderFile(f)
//	if err != nil {
//		panic(err)
//	}
//
//	buf := bytes.NewBuffer(nil)
//	if err := unsparse.Unsparse(buf, sr); err != nil {
//		panic(err)
//	}
//
//	// buf now holds the [Unsparsed] format
func Unsparse(dst io.Writer, src SparseReader) error {
	size := src.Size()
	findData := dataFinder(src, size)

	bw := bufio.NewWriter(dst)
	if err := binary.Write(bw, byteOrder, Unsparsed{Magic: Magic, Version: Version, ApparentSize: size}); err != nil {
		return err
	}

	for off := int64(0); off < size; {
		start, end, err := findData(off)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := binary.Write(bw, byteOrder, Extent{Offset: start, Size: end - start}); err != nil {
			return err
		}
		if _, err := io.CopyN(bw, io.NewSectionReader(src, start, end-start), end-start); err != nil {
			return fmt.Errorf("unsparse: copying extent at %d: %w", start, err)
		}
		off = end
	}
	return bw.Flush()
}

// DataFinder is implemented by sources that know where their data is, such
// as [*Buffer]. [Unsparse] uses it to skip holes.
type DataFinder interface {
	// NextData returns the next data region [start, end) at or after off,
	// or io.EOF if only a hole remains.
	NextData(off int64) (start, end int64, err error)
}

// dataFinder returns a function reporting the next data region [start, end)
// of src at or after off, or io.EOF when only a hole remains.
func dataFinder(src SparseReader, size int64) func(off int64) (start, end int64, err error) {
	// Unwrap (possibly nested) sections, tracking their offset.
	var r io.ReaderAt = src
	var base int64
	for {
		sr, ok := r.(*io.SectionReader)
		if !ok {
			break
		}
		var off int64
		r, off, _ = sr.Outer()
		base += off
	}

	var next func(off int64) (int64, int64, error)
	switch r := r.(type) {
	case DataFinder:
		next = r.NextData
	case *os.File:
		next = func(off int64) (int64, int64, error) {
			return nextData(r, off, base+size)
		}
	default:
		return func(off int64) (int64, int64, error) {
			return off, size, nil
		}
	}

	// Translate to and from the section, clamping to its size.
	return func(off int64) (int64, int64, error) {
		start, end, err := next(base + off)
		if err != nil {
			return 0, 0, err
		}
		if start >= base+size {
			return 0, 0, io.EOF
		}
		return start - base, min(end, base+size) - base, nil
	}
}

// TruncateWriter is a destination for [Sparse], such as *os.File or
// [*Buffer].
type TruncateWriter interface {
	// Truncate changes the size of the destination. Growing it must leave
	// the new region reading as zeroes, ideally as a hole.
	Truncate(size int64) error
	io.WriterAt
}

// Sparse is the opposite of [Unsparse]: it reads the [Unsparsed] format from
// src and writes the original file to dst.
//
// dst is truncated before writing, and regions between extents are left as
// holes. Sparse may read past the end of the [Unsparsed] data in src.
//
// Example:
//
//	src, err := os.Open("myunsparsedfile")
//	if err != nil {
//		panic(err)
//	}
//	defer src.Close()
//
//	dst, err := os.Create("mysparsefile")
//	if err != nil {
//		panic(err)
//	}
//	defer dst.Close()
//
//	if err := unsparse.Sparse(dst, src); err != nil {
//		panic(err)
//	}
//
//	// dst is now the original sparse file, holes included
func Sparse(dst TruncateWriter, src io.Reader) error {
	br := bufio.NewReader(src)

	hdr, err := ReadHeader(br)
	if err != nil {
		return err
	}
	if err := dst.Truncate(0); err != nil {
		return err
	}

	var end int64
	for {
		var e Extent
		err := binary.Read(br, byteOrder, &e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("unsparse: reading extent header: %w", err)
		}
		if e.Offset < end || e.Size < 0 || e.Size > hdr.ApparentSize-e.Offset {
			return fmt.Errorf("unsparse: invalid extent %+v (previous end %d, apparent size %d)", e, end, hdr.ApparentSize)
		}
		if _, err := io.CopyN(io.NewOffsetWriter(dst, e.Offset), br, e.Size); err != nil {
			return fmt.Errorf("unsparse: copying extent at %d: %w", e.Offset, err)
		}
		end = e.Offset + e.Size
	}

	// Extends dst to its apparent size, leaving any trailing hole.
	return dst.Truncate(hdr.ApparentSize)
}
