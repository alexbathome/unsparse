//go:build darwin || freebsd || linux

package unsparse

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// nextData returns the next data region [start, end) at or after off, using
// SEEK_DATA/SEEK_HOLE so holes are never read. It returns io.EOF when only a
// hole remains. It moves f's read/write offset.
func nextData(f *os.File, off, size int64) (start, end int64, err error) {
	start, err = f.Seek(off, unix.SEEK_DATA)
	if errors.Is(err, unix.ENXIO) {
		return 0, 0, io.EOF
	}
	if errors.Is(err, unix.EINVAL) {
		// Filesystem can't report holes; treat the rest as data.
		return off, size, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if start >= size {
		return 0, 0, io.EOF
	}
	end, err = f.Seek(start, unix.SEEK_HOLE)
	if err != nil {
		return 0, 0, err
	}
	return start, min(end, size), nil
}
