//go:build linux

package cdet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func procDisappeared(base string, _ error) bool {
	// Exiting processes can also return EACCES while /proc destroys their
	// memory or file table. Confirm disappearance or a terminal state rather
	// than inferring safety from the original error. A live or unknown process
	// still fails closed after this bounded check of the error path only.
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			return true
		}
		status, err := os.ReadFile(filepath.Join(base, "status"))
		if err == nil && processExited(string(status)) {
			return true
		}
		if attempt < 2 {
			time.Sleep(time.Millisecond)
		}
	}
	return false
}

func processExited(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "State:") {
			fields := strings.Fields(strings.TrimPrefix(line, "State:"))
			return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
		}
	}
	return false
}

func processUID(status string) (int, bool) {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) < 2 {
				return 0, false
			}
			uid, e := strconv.Atoi(fields[1])
			return uid, e == nil
		}
	}
	return 0, false
}

// Read-only users and idle daemons must stop too. Arguments/environment are
// inspected in memory and never included in errors or command output.
func checkInactive(paths []string) error {
	m, e := inactivePaths(paths)
	if e != nil || len(m.roots) == 0 {
		return e
	}
	procs, e := os.ReadDir("/proc")
	if e != nil {
		return fmt.Errorf("无法检查组件运行状态: %w", e)
	}
	for _, proc := range procs {
		pid, er := strconv.Atoi(proc.Name())
		if er != nil || pid == os.Getpid() {
			continue
		}
		base := filepath.Join("/proc", proc.Name())
		status, er := os.ReadFile(filepath.Join(base, "status"))
		if er != nil {
			if procDisappeared(base, er) {
				continue
			}
			return fmt.Errorf("无法确认进程状态（PID %d），未执行组件变更", pid)
		}
		uid, ok := processUID(string(status))
		if !ok {
			return fmt.Errorf("无法确认进程归属（PID %d），未执行组件变更", pid)
		}
		if os.Geteuid() != 0 && uid != os.Geteuid() {
			continue
		}
		if processExited(string(status)) {
			continue
		}
		var cwd string
		for _, entry := range []string{"exe", "cwd"} {
			path, pe := os.Readlink(filepath.Join(base, entry))
			if pe != nil && !errors.Is(pe, os.ErrNotExist) {
				if procDisappeared(base, pe) {
					continue
				}
				return fmt.Errorf("无法检查进程程序或工作目录（PID %d），未执行组件变更", pid)
			}
			if entry == "cwd" {
				cwd = path
			}
			if m.matches(path) {
				return activeComponent(pid)
			}
		}
		var parts [2][]string
		for i, entry := range []string{"cmdline", "environ"} {
			raw, pe := os.ReadFile(filepath.Join(base, entry))
			if pe != nil {
				if procDisappeared(base, pe) {
					continue
				}
				return fmt.Errorf("无法检查组件进程环境（PID %d），未执行组件变更", pid)
			}
			parts[i] = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		}
		if m.process(parts[0], parts[1], cwd) {
			return activeComponent(pid)
		}
		mappings, pe := os.ReadFile(filepath.Join(base, "maps"))
		if pe != nil && !procDisappeared(base, pe) {
			return fmt.Errorf("无法检查组件内存映射（PID %d），未执行组件变更", pid)
		}
		for _, line := range strings.Split(string(mappings), "\n") {
			rest := line
			for i := 0; i < 5; i++ {
				rest = strings.TrimLeft(rest, " \t")
				end := strings.IndexAny(rest, " \t")
				if end < 0 {
					rest = ""
					break
				}
				rest = rest[end:]
			}
			if m.matches(strings.TrimLeft(rest, " \t")) {
				return activeComponent(pid)
			}
		}
		fds, pe := os.ReadDir(filepath.Join(base, "fd"))
		if pe != nil && !procDisappeared(base, pe) {
			return fmt.Errorf("无法检查组件打开文件（PID %d），未执行组件变更", pid)
		}
		for _, fd := range fds {
			path, pe := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			if errors.Is(pe, os.ErrNotExist) {
				continue
			}
			if pe != nil {
				if procDisappeared(base, pe) {
					break
				}
				return fmt.Errorf("无法确认组件打开文件（PID %d），未执行组件变更", pid)
			}
			if m.matches(path) {
				return activeComponent(pid)
			}
		}
	}
	return nil
}
