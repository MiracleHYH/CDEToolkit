//go:build linux && (amd64 || arm64)

package gdsutil

import (
	"fmt"
	"syscall"
	"unsafe"
)

func renameCommit(from, to string, exists bool) (bool, error) {
	old, err := syscall.BytePtrFromString(from)
	if err != nil {
		return false, err
	}
	new, err := syscall.BytePtrFromString(to)
	if err != nil {
		return false, err
	}
	flags := uintptr(1) // RENAME_NOREPLACE
	if exists {
		flags = 2 // RENAME_EXCHANGE retains the displaced inode at from.
	}
	// AT_FDCWD is signed -100; convert through int at runtime to avoid an
	// overflowing uintptr constant while retaining the Linux syscall ABI.
	cwd := -100
	_, _, errno := syscall.Syscall6(renameat2Number, uintptr(cwd), uintptr(unsafe.Pointer(old)), uintptr(cwd), uintptr(unsafe.Pointer(new)), flags, 0)
	if errno != 0 {
		if errno == syscall.ENOSYS || errno == syscall.EINVAL || errno == syscall.EOPNOTSUPP {
			return false, fmt.Errorf("内核或文件系统不支持 GDS 所需的 renameat2 原子提交: %w", errno)
		}
		return false, errno
	}
	return exists, nil
}
