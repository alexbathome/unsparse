//go:build darwin || freebsd || linux

package unsparse

import (
	"os"

	"golang.org/x/sys/unix"
)

// fsKeepsHoles reports whether the filesystem reports a hole in f. Some
// filesystems (e.g. APFS) may allocate holes once data is flushed.
func fsKeepsHoles(f *os.File, size int64) bool {
	f.Sync()
	hole, err := f.Seek(0, unix.SEEK_HOLE)
	f.Seek(0, 0)
	return err == nil && hole < size
}
