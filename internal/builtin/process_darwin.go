//go:build darwin

package builtin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func inspectProcess(pid int) (Process, error) {
	if pid <= 1 {
		return Process{}, fmt.Errorf("进程 PID 无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "uid=", "-o", "lstart=", "-o", "stat=")
	cmd.Env = []string{"LC_ALL=C", "PATH=/usr/bin:/bin"}
	raw, err := cmd.Output()
	if err != nil {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return Process{}, os.ErrNotExist
		}
		return Process{}, fmt.Errorf("无法检查进程启动信息")
	}
	f := strings.Fields(string(raw))
	if len(f) != 7 || f[0] != strconv.Itoa(os.Geteuid()) {
		return Process{}, fmt.Errorf("进程不属于当前用户或启动信息不完整")
	}
	if strings.HasPrefix(f[6], "Z") {
		return Process{}, os.ErrNotExist
	}
	// KERN_PROCARGS2 contains the executable path before argv. Only that path
	// is parsed; neither arguments nor the environment are exposed or retained.
	limit, err := syscall.SysctlUint32("kern.argmax")
	if err != nil || limit < 4096 || limit > 16<<20 {
		return Process{}, fmt.Errorf("无法读取进程程序路径")
	}
	buf := make([]byte, limit)
	size := uintptr(len(buf))
	mib := [3]int32{1, 49, int32(pid)}
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	runtime.KeepAlive(buf)
	runtime.KeepAlive(mib)
	if errno == syscall.ESRCH {
		return Process{}, os.ErrNotExist
	}
	if errno != 0 || size <= 4 || size > uintptr(len(buf)) {
		return Process{}, fmt.Errorf("无法确认进程程序路径")
	}
	program, _, ok := bytes.Cut(buf[4:size], []byte{0})
	if !ok || !filepath.IsAbs(string(program)) {
		return Process{}, fmt.Errorf("进程程序路径无效")
	}
	return Process{PID: pid, Executable: string(program), Started: strings.Join(f[1:6], " ")}, nil
}
