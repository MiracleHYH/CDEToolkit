//go:build linux

package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func inspectProcess(pid int) (Process, error) {
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
	b, err := os.ReadFile(filepath.Join(base, "stat"))
	if err != nil {
		return Process{}, err
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return Process{}, fmt.Errorf("进程信息不完整")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return Process{}, fmt.Errorf("进程启动标识不完整")
	}
	if fields[0] == "Z" || fields[0] == "X" {
		// A zombie thread-group leader can still have live worker threads.
		// Only a single-threaded dead process is safe to treat as absent.
		status, e := os.ReadFile(filepath.Join(base, "status"))
		if e != nil {
			return Process{}, e
		}
		threads := ""
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Threads:") {
				if threads != "" {
					return Process{}, fmt.Errorf("进程线程状态不明确")
				}
				threads = strings.TrimSpace(strings.TrimPrefix(line, "Threads:"))
			}
		}
		if threads == "1" {
			return Process{}, os.ErrNotExist
		}
		return Process{}, fmt.Errorf("进程仍可能有活动线程")
	}
	program, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return Process{}, err
	}
	if !filepath.IsAbs(program) || strings.HasSuffix(program, " (deleted)") {
		return Process{}, fmt.Errorf("进程程序路径无效")
	}
	return Process{PID: pid, Executable: program, Started: fields[19]}, nil
}
