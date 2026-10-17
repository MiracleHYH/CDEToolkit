//go:build linux

package cdet

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func mihomoListeningInodes(raw []byte, expected net.IP, port int) map[string]bool {
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != "0A" {
			continue
		}
		address, encodedPort, ok := strings.Cut(fields[1], ":")
		p, e := strconv.ParseUint(encodedPort, 16, 16)
		if !ok || e != nil || int(p) != port {
			continue
		}
		ip, e := hex.DecodeString(address)
		if e != nil || len(ip) != 4 && len(ip) != 16 {
			continue
		}
		for i := 0; i < len(ip); i += 4 {
			ip[i], ip[i+3] = ip[i+3], ip[i]
			ip[i+1], ip[i+2] = ip[i+2], ip[i+1]
		}
		actual := net.IP(ip)
		if len(ip) == 4 && expected.To4() == nil || len(ip) == 16 && expected.To4() != nil {
			continue
		}
		if actual.Equal(expected) || actual.IsUnspecified() {
			inodes[fields[9]] = true
		}
	}
	return inodes
}

func mihomoSocketOwner(endpoint, program string) (bool, error) {
	ip, port, e := mihomoAddress(endpoint)
	if e != nil {
		return false, e
	}
	table := "/proc/net/tcp"
	if ip.To4() == nil {
		table += "6"
	}
	raw, e := os.ReadFile(table)
	if e != nil {
		return false, fmt.Errorf("无法核验控制器监听套接字")
	}
	inodes := mihomoListeningInodes(raw, ip, port)
	if len(inodes) != 1 {
		return false, nil
	}
	procs, e := os.ReadDir("/proc")
	if e != nil {
		return false, fmt.Errorf("无法核验控制器进程")
	}
	owner := 0
	for _, proc := range procs {
		pid, pe := strconv.Atoi(proc.Name())
		if pe != nil {
			continue
		}
		base := filepath.Join("/proc", proc.Name())
		actual, pe := os.Readlink(filepath.Join(base, "exe"))
		if pe != nil || !sameMihomoExecutable(actual, program) {
			continue
		}
		fds, pe := os.ReadDir(filepath.Join(base, "fd"))
		if pe != nil {
			return false, fmt.Errorf("无法核验控制器程序的监听归属")
		}
		for _, fd := range fds {
			link, pe := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			if pe != nil || !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
				continue
			}
			if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				if owner != 0 && owner != pid {
					return false, nil
				}
				owner = pid
			}
		}
	}
	return owner != 0, nil
}
