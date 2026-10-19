//go:build !linux || (!amd64 && !arm64)

package gdsutil

import "os"

// GDS execution is unsupported on these platforms. This path permits portable
// CPU service tests; Linux production always requires the preserving syscall.
func renameCommit(from, to string, exists bool) (bool, error) {
	return false, os.Rename(from, to)
}
