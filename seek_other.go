//go:build !(darwin || freebsd || linux)

package unsparse

import "os"

// nextData treats the rest of the file as data, as holes can't be detected.
func nextData(_ *os.File, off, size int64) (start, end int64, err error) {
	return off, size, nil
}
