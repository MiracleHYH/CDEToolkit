package nido

import (
	"os"
	"syscall"
	"unsafe"
)

func terminalInput(file *os.File) bool {
	var state syscall.Termios
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&state)))
	return err == 0
}
