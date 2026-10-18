//go:build linux

package gdsutil

import (
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Process = gdsapi.Process

func BootID() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", e
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("系统启动标识为空")
	}
	return v, nil
}
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
		return "", linuxProcessDirectoryError(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != p.UID || fields[19] != p.Started {
		return "", fmt.Errorf("进程检查期间出生或用户信息发生变化")
	}
	return fields[0], nil
}

// Only a whole PID-directory lookup can directly confirm disappearance. An
// ESRCH from reading a procfs child still requires the metadata recheck above:
// the leader may be exiting while other threads remain alive.
func linuxProcessDirectoryError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return os.ErrNotExist
	}
	return err
}

func inspectLinuxProcess(pid int) (Process, error) {
	if pid <= 1 {
		return Process{}, fmt.Errorf("进程 PID 无效")
	}
	base := filepath.Join("/proc", strconv.Itoa(pid))
	info, err := os.Stat(base)
	if err != nil {
		return Process{}, linuxProcessDirectoryError(err)
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

func signalProcess(p Process, signal syscall.Signal) error {
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
	if err != nil || !SameBirth(actual, p) || actual.Executable != p.Executable {
		return fmt.Errorf("无法确认 GDS 进程归属，未发送停止信号")
	}
	if errno == 0 {
		_, _, e := syscall.Syscall6(424, fd, uintptr(signal), 0, 0, 0, 0)
		if e != 0 && e != syscall.ESRCH {
			return e
		}
		return nil
	}
	// There remains a check-to-kill race on kernels without pidfd support.
	if e := syscall.Kill(p.PID, signal); e != nil && e != syscall.ESRCH {
		return e
	}
	return nil
}
