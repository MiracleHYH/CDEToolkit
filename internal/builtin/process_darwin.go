//go:build darwin

package builtin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
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
	raw, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "uid=", "-o", "lstart=", "-o", "stat=").Output()
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

func processOwnsEndpoint(pid int, endpoint string) (bool, error) {
	u, _ := url.Parse(endpoint)
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP:"+u.Port(), "-sTCP:LISTEN", "-F0n").Output()
	if err != nil {
		return false, nil
	}
	for _, part := range bytes.Split(b, []byte{0}) {
		field := strings.TrimLeft(string(part), "\r\n")
		if !strings.HasPrefix(field, "n") {
			continue
		}
		host, port, e := net.SplitHostPort(field[1:])
		actual := net.ParseIP(host)
		if e == nil && port == u.Port() && (host == "*" || actual != nil && (actual.Equal(ip) || actual.IsUnspecified())) {
			return true, nil
		}
	}
	return false, nil
}
