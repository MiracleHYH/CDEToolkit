//go:build linux

package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// RunningImageMatches proves that this birth currently executes the inode at
// the fixed program path, rather than an old deleted image after replacement.
func (OSRunner) RunningImageMatches(p Process, path string) (bool, error) {
	before, err := inspectProcess(p.PID)
	if err != nil || before != p || before.ExecutableDeleted || before.Executable != path {
		return false, fmt.Errorf("更新运行映像的出生身份无法确认")
	}
	disk, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer disk.Close()
	diskInfo, err := disk.Stat()
	if err != nil || !diskInfo.Mode().IsRegular() || !singleLink(diskInfo) {
		return false, fmt.Errorf("固定核心文件不是普通单链接文件")
	}
	image, err := os.Open(filepath.Join("/proc", strconv.Itoa(p.PID), "exe"))
	if err != nil {
		return false, fmt.Errorf("无法读取 Linux 运行映像")
	}
	defer image.Close()
	info, err := image.Stat()
	if err != nil || !os.SameFile(diskInfo, info) {
		return false, nil
	}
	current, err := os.Lstat(path)
	if err != nil || !sameMetadata(diskInfo, current) {
		return false, fmt.Errorf("固定核心文件在运行映像核验期间发生变化")
	}
	after, err := inspectProcess(p.PID)
	if err != nil || after != p {
		return false, fmt.Errorf("运行映像核验期间进程发生变化")
	}
	return true, nil
}
