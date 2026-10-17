//go:build linux

package builtin

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
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

// Inspect only this recorded process's file descriptors and socket table.
// There is no full-machine process discovery or cross-user process operation.
func processOwnsEndpoint(pid int, endpoint string) (bool, error) {
	u, _ := url.Parse(endpoint)
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
		if actual.Equal(ip) || actual.IsUnspecified() {
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
