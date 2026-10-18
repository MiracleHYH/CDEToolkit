//go:build darwin

package gdsutil

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

type Process = gdsapi.Process

func BootID() (string, error) { return systemBootIdentity() }

// procBSDInfo is XNU's public struct proc_bsdinfo (PROC_PIDTBSDINFO).
// Birth comes directly from the kernel; no locale-sensitive ps date parsing.
type procBSDInfo struct {
	Flags, Status, ExitStatus, PID, PPID  uint32
	UID, GID, RUID, RGID, SVUID, SVGID    uint32
	Reserved                              uint32
	Comm                                  [16]byte
	Name                                  [32]byte
	NFiles, PGID, JobCount, Device, TPGID uint32
	Nice                                  int32
	StartSeconds, StartMicroseconds       uint64
}

func procInfo(pid, flavor int, buffer unsafe.Pointer, size uintptr) (uintptr, error) {
	// SYS_proc_info(callnum=PROC_INFO_CALL_PIDINFO, pid, flavor, arg=0,
	//               user buffer, buffer size).
	n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 2, uintptr(pid), uintptr(flavor), 0, uintptr(buffer), size)
	if errno == syscall.ESRCH {
		return 0, os.ErrNotExist
	}
	if errno != 0 {
		return 0, errno
	}
	return n, nil
}

func inspectProcess(pid int) (Process, error) {
	if pid <= 1 {
		return Process{}, fmt.Errorf("进程 PID 无效")
	}
	var info procBSDInfo
	n, err := procInfo(pid, 3, unsafe.Pointer(&info), unsafe.Sizeof(info))
	if err != nil {
		return Process{}, err
	}
	if n != unsafe.Sizeof(info) || info.PID != uint32(pid) || info.UID != uint32(os.Geteuid()) || info.StartSeconds == 0 {
		return Process{}, fmt.Errorf("无法确认进程用户或出生信息")
	}
	if info.Status == 5 {
		return Process{}, os.ErrNotExist
	} // SZOMB
	buf := make([]byte, 4096)
	n, err = procInfo(pid, 11, unsafe.Pointer(&buf[0]), uintptr(len(buf))) // PROC_PIDPATHINFO
	runtime.KeepAlive(buf)
	if err != nil {
		return Process{}, err
	}
	// PROC_PIDPATHINFO reports success with a zero syscall return value on
	// supported XNU versions; libproc derives the length from the NUL-terminated
	// buffer as well. Do not confuse retval with the BSDINFO byte-count flavor.
	path, _, terminated := bytes.Cut(buf, []byte{0})
	if !terminated || len(path) == 0 {
		return Process{}, fmt.Errorf("无法确认进程程序路径")
	}
	program := string(path)
	if !filepath.IsAbs(program) {
		return Process{}, fmt.Errorf("进程程序路径无效")
	}
	boot, err := systemBootIdentity()
	if err != nil {
		return Process{}, err
	}
	var after procBSDInfo
	n, err = procInfo(pid, 3, unsafe.Pointer(&after), unsafe.Sizeof(after))
	if err != nil {
		return Process{}, err
	}
	if n != unsafe.Sizeof(after) || after.PID != info.PID || after.UID != info.UID || after.StartSeconds != info.StartSeconds || after.StartMicroseconds != info.StartMicroseconds {
		return Process{}, fmt.Errorf("进程检查期间出生信息发生变化")
	}
	return Process{PID: pid, UID: info.UID, Boot: boot, Started: fmt.Sprintf("%d:%d", info.StartSeconds, info.StartMicroseconds), Executable: program}, nil
}

func systemBootIdentity() (string, error) {
	// Use the stable numeric MIB for kern.boottime. Resolving sysctl names
	// through sysctl.name2oid may be denied independently by an app sandbox.
	mib := [2]int32{1, 21} // CTL_KERN, KERN_BOOTTIME
	var boot syscall.Timeval
	size := unsafe.Sizeof(boot)
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), 2, uintptr(unsafe.Pointer(&boot)), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 {
		return "", fmt.Errorf("无法确认系统启动标识: %w", errno)
	}
	if size != unsafe.Sizeof(boot) || boot.Sec == 0 {
		return "", fmt.Errorf("系统启动标识不完整")
	}
	return fmt.Sprintf("%d:%d", boot.Sec, boot.Usec), nil
}

func signalProcess(p Process, signal syscall.Signal) error {
	actual, err := inspectProcess(p.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !SameBirth(actual, p) || actual.Executable != p.Executable {
		return fmt.Errorf("无法确认 GDS 进程归属，未发送停止信号")
	}
	// Darwin has no pidfd. Birth verification narrows but cannot eliminate
	// the small check-to-signal race; never claim it provides atomic identity.
	if err := syscall.Kill(p.PID, signal); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
