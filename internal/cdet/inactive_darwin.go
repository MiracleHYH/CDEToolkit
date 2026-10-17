//go:build darwin

package cdet

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func darwinProcessArgs(pid int, buf []byte) ([]string, []string, error) {
	clear(buf)
	size := uintptr(len(buf))
	// CTL_KERN / KERN_PROCARGS2 returns NUL-delimited argv and environment,
	// preserving paths with spaces without printing any private values.
	mib := [3]int32{1, 49, int32(pid)}
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	runtime.KeepAlive(buf)
	runtime.KeepAlive(mib)
	if errno != 0 {
		return nil, nil, errno
	}
	if size > uintptr(len(buf)) {
		return nil, nil, fmt.Errorf("进程参数越界")
	}
	return parseDarwinProcessArgs(buf[:size])
}

func parseDarwinProcessArgs(buf []byte) ([]string, []string, error) {
	if len(buf) < 4 {
		return nil, nil, fmt.Errorf("进程参数不完整")
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc < 1 || argc > len(buf) {
		return nil, nil, fmt.Errorf("进程参数数量无效")
	}
	buf = buf[4:]
	executable, rest, ok := bytes.Cut(buf, []byte{0})
	if !ok {
		return nil, nil, fmt.Errorf("进程程序路径不完整")
	}
	buf = bytes.TrimLeft(rest, "\x00")
	args := []string{string(executable)}
	for i := 0; i < argc; i++ {
		arg, rest, ok := bytes.Cut(buf, []byte{0})
		if !ok {
			return nil, nil, fmt.Errorf("进程参数不完整")
		}
		args, buf = append(args, string(arg)), rest
	}
	var env []string
	// macOS can return a zero-padded argument buffer. Trim padding before
	// splitting, so idle processes don't allocate one slice per padding byte.
	for _, entry := range bytes.Split(bytes.TrimRight(buf, "\x00"), []byte{0}) {
		if len(entry) != 0 {
			env = append(env, string(entry))
		}
	}
	return args, env, nil
}

func darwinOpenFiles(m inactiveMatcher) (map[int]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-u", strconv.Itoa(os.Geteuid()), "-F0pfn")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	var exit *exec.ExitError
	if stderr.Len() != 0 || e != nil && (!errors.As(e, &exit) || exit.ExitCode() != 1 || len(out) != 0) {
		return nil, fmt.Errorf("无法确认组件打开文件，请检查 lsof；未执行组件变更")
	}
	cwds := map[int]string{}
	pid, fd := 0, ""
	for _, part := range bytes.Split(out, []byte{0}) {
		field := strings.TrimLeft(string(part), "\r\n")
		if field == "" {
			continue
		}
		switch field[0] {
		case 'p':
			var pe error
			pid, pe = strconv.Atoi(field[1:])
			if pe != nil || pid <= 0 {
				return nil, fmt.Errorf("lsof 进程标识无效，未执行组件变更")
			}
			fd = ""
		case 'f':
			fd = field[1:]
		case 'n':
			if pid == 0 || pid == os.Getpid() {
				continue
			}
			if fd == "cwd" {
				cwds[pid] = field[1:]
			}
			if m.matches(field[1:]) {
				return nil, activeComponent(pid)
			}
		}
	}
	return cwds, nil
}

func checkInactive(paths []string) error {
	m, e := inactivePaths(paths)
	if e != nil || len(m.roots) == 0 {
		return e
	}
	cwds, e := darwinOpenFiles(m)
	if e != nil {
		return e
	}
	limit, e := syscall.SysctlUint32("kern.argmax")
	if e != nil || limit < 4096 || limit > 16<<20 {
		return fmt.Errorf("无法读取进程参数容量，未执行组件变更")
	}
	argsBuffer := make([]byte, int(limit))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,uid=,stat=").Output()
	if e != nil {
		return fmt.Errorf("无法枚举组件进程，未执行组件变更")
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return fmt.Errorf("无法解析组件进程清单，未执行组件变更")
		}
		pid, pe := strconv.Atoi(fields[0])
		uid, ue := strconv.Atoi(fields[1])
		if pe != nil || ue != nil {
			return fmt.Errorf("无法解析组件进程归属，未执行组件变更")
		}
		if pid == os.Getpid() || uid != os.Geteuid() || strings.Contains(fields[2], "Z") {
			continue
		}
		args, env, pe := darwinProcessArgs(pid, argsBuffer)
		if errors.Is(pe, syscall.ESRCH) {
			continue // exited between enumeration and inspection
		}
		if errors.Is(pe, syscall.EINVAL) {
			// A process can become a zombie after the ps snapshot. Confirm
			// that transition instead of treating every inspection failure as
			// an exited process.
			status, se := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
			if se == nil && strings.HasPrefix(strings.TrimSpace(string(status)), "Z") || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				continue
			}
		}
		if pe != nil {
			return fmt.Errorf("无法检查组件进程环境（PID %d），未执行组件变更", pid)
		}
		if m.process(args, env, cwds[pid]) {
			return activeComponent(pid)
		}
	}
	return nil
}
