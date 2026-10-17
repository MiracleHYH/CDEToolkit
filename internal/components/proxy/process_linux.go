//go:build linux

package proxy

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func inspectProcess(pid int) (Process, error) {
	var p Process
	var err error
	// An exec or group exit can span the two procfs observations. Re-read a
	// few times locally; callers retain their own bounded lifecycle deadline.
	for attempt := 0; attempt < 8; attempt++ {
		p, err = inspectLinuxProcess(pid)
		if !errors.Is(err, errProcessTransition) {
			return p, err
		}
		if attempt < 7 {
			time.Sleep(time.Millisecond)
		}
	}
	return p, err
}

func linuxProcessStat(base string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(base, "stat"))
	if err != nil {
		return nil, processMetadataError(base, err)
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return nil, fmt.Errorf("进程信息不完整")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return nil, fmt.Errorf("进程出生标识不完整")
	}
	return fields, nil
}

func linuxProcessState(base string, p Process) (string, error) {
	fields, err := linuxProcessStat(base)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(base)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != p.UID || fields[19] != p.Started {
		return "", fmt.Errorf("进程检查期间出生或用户信息发生变化")
	}
	return fields[0], nil
}

func inspectLinuxProcess(pid int) (Process, error) {
	if pid <= 1 {
		return Process{}, fmt.Errorf("进程 PID 无效")
	}
	base := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(base)
	if err != nil {
		return Process{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return Process{}, fmt.Errorf("进程不属于当前用户")
	}
	fields, err := linuxProcessStat(base)
	if err != nil {
		return Process{}, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || len(strings.TrimSpace(string(boot))) == 0 {
		return Process{}, fmt.Errorf("无法确认系统启动标识")
	}
	p := Process{PID: pid, UID: stat.Uid, Boot: strings.TrimSpace(string(boot)), Started: fields[19]}
	if fields[0] == "Z" || fields[0] == "X" {
		// A zombie leader with live worker threads must not authorize a new
		// launch. Only a confirmed single-thread dead process is absent.
		status, e := os.ReadFile(filepath.Join(base, "status"))
		if e != nil {
			return p, processMetadataError(base, e)
		}
		afterState, e := linuxProcessState(base, p)
		if e != nil {
			return p, e
		}
		if (afterState == "Z" || afterState == "X") && singleProcessThread(status) {
			return Process{}, os.ErrNotExist
		}
		// Threads:0 can also mean that procfs could not acquire sighand's
		// lock; missing/invalid values likewise never prove a dead group.
		return p, fmt.Errorf("进程退出线程信息尚未稳定: %w", errProcessTransition)
	}
	image, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return p, processMetadataError(base, err)
	}
	// procfs keeps the path of an unlinked running image, followed by its
	// own deleted marker. Preserve this state rather than treating the exact
	// recorded birth as uninspectable: disk replacement must not prevent Stop.
	deleted := strings.HasSuffix(image, " (deleted)")
	program := strings.TrimSuffix(image, " (deleted)")
	if !filepath.IsAbs(program) {
		return Process{}, fmt.Errorf("进程程序路径无效")
	}
	afterState, err := linuxProcessState(base, p)
	if err != nil {
		return p, err
	}
	if afterState == "Z" || afterState == "X" {
		return p, fmt.Errorf("进程检查期间开始退出: %w", errProcessTransition)
	}
	afterProgram, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return p, processMetadataError(base, err)
	}
	if afterProgram != image {
		return p, fmt.Errorf("进程检查期间程序发生变化: %w", errProcessTransition)
	}
	p.Executable, p.ExecutableDeleted = program, deleted
	return p, nil
}

func signalProcess(p Process) error {
	// pidfd_open and pidfd_send_signal have these numbers on supported Linux
	// amd64/arm64. Acquire the handle before checking birth, so a later PID
	// reuse cannot redirect the signal. Old kernels fall back explicitly.
	fd, _, errno := syscall.Syscall(434, uintptr(p.PID), 0, 0)
	if errno == syscall.ESRCH {
		return nil
	}
	if errno != 0 && errno != syscall.ENOSYS && errno != syscall.EINVAL {
		return errno
	}
	if errno == 0 {
		defer syscall.Close(int(fd))
	}
	actual, err := inspectProcess(p.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if errors.Is(err, errProcessTransition) && sameBirth(actual, p) {
		return err
	}
	if err != nil || actual != p {
		return fmt.Errorf("无法确认 Proxy 进程归属，未发送停止信号")
	}
	if errno == 0 {
		_, _, e := syscall.Syscall6(424, fd, uintptr(syscall.SIGTERM), 0, 0, 0, 0)
		if e != 0 && e != syscall.ESRCH {
			return e
		}
		return nil
	}
	// There remains a check-to-kill race on kernels without pidfd support.
	if e := syscall.Kill(p.PID, syscall.SIGTERM); e != nil && e != syscall.ESRCH {
		return e
	}
	return nil
}

// Inspect only the recorded process's FDs and its network namespace table.
// A listening socket on an unrelated process is insufficient for readiness.
func processOwnsEndpoint(pid int, endpoint string) (bool, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false, err
	}
	ip := net.ParseIP(u.Hostname())
	port, err := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || err != nil {
		return false, nil
	}
	base := filepath.Join("/proc", strconv.Itoa(pid))
	table := "tcp"
	if ip.To4() == nil {
		table = "tcp6"
	}
	b, err := os.ReadFile(filepath.Join(base, "net", table))
	if err != nil {
		return false, err
	}
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		host, p, ok := strings.Cut(f[1], ":")
		n, e := strconv.ParseUint(p, 16, 16)
		if !ok || e != nil || int(n) != port {
			continue
		}
		encoded, e := hex.DecodeString(host)
		if e != nil || len(encoded) != 4 && len(encoded) != 16 {
			continue
		}
		for i := 0; i < len(encoded); i += 4 {
			encoded[i], encoded[i+3] = encoded[i+3], encoded[i]
			encoded[i+1], encoded[i+2] = encoded[i+2], encoded[i+1]
		}
		actual := net.IP(encoded)
		// A wildcard or other address family is not the configured loopback
		// listener, even if connections to the endpoint happen to work.
		if actual.Equal(ip) {
			inodes[f[9]] = true
		}
	}
	fds, err := os.ReadDir(filepath.Join(base, "fd"))
	if err != nil {
		return false, err
	}
	for _, fd := range fds {
		link, e := os.Readlink(filepath.Join(base, "fd", fd.Name()))
		if e == nil && strings.HasPrefix(link, "socket:[") && strings.HasSuffix(link, "]") && inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
			return true, nil
		}
	}
	return false, nil
}
