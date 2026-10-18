//go:build linux && amd64

package gdsruntime

import (
	"fmt"
	"syscall"
	"unsafe"
)

func bootTimeNS() (int64, error) {
	const billion int64 = 1000000000
	var ts syscall.Timespec
	_, _, errno := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, 7, uintptr(unsafe.Pointer(&ts)), 0)
	if errno != 0 {
		return 0, fmt.Errorf("CLOCK_BOOTTIME: %w", errno)
	}
	if ts.Sec < 0 || ts.Nsec < 0 || ts.Nsec >= billion || ts.Sec > (1<<63-1)/billion || ts.Sec == (1<<63-1)/billion && ts.Nsec > (1<<63-1)%billion {
		return 0, fmt.Errorf("CLOCK_BOOTTIME out of range")
	}
	return ts.Sec*billion + ts.Nsec, nil
}
