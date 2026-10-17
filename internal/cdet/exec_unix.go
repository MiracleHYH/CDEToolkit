//go:build linux || darwin

package cdet

import (
	"fmt"
	"io"
	"syscall"
)

func execProcess(path string, args, env []string, stderr io.Writer) int {
	if err := syscall.Exec(path, args, env); err != nil {
		fmt.Fprintln(stderr, "启动失败:", err)
		return 127
	}
	return 0
}
