//go:build !linux || !amd64

package gdsruntime

import (
	"fmt"
	"os"
)

func deviceLockPath(uuid string) string { return "" }
func validateDeviceLock(f *os.File, uuid string) error {
	return fmt.Errorf("GDS device locking requires Linux amd64")
}
func retainDeviceLock(f *os.File) error {
	return fmt.Errorf("GDS device locking requires Linux amd64")
}
