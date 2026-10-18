//go:build linux

package gdsutil

import (
	"fmt"
	"syscall"
	"unsafe"
)

// BootTimeNS includes suspend time and shares a boot/time namespace across the
// monitor, worker and management client.
func BootTimeNS() (int64, error) {
	var ts syscall.Timespec
	_, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, 7, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		return 0, fmt.Errorf("CLOCK_BOOTTIME 不可用: %w", errno)
	}
	return ts.Sec*1e9 + ts.Nsec, nil
}
