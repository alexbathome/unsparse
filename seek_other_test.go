//go:build !(darwin || freebsd || linux)

package unsparse

import "os"

func fsKeepsHoles(*os.File, int64) bool { return false }
