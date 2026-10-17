//go:build darwin

package cdet

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func mihomoSocketOwner(endpoint, program string) (bool, error) {
	ip, port, e := mihomoAddress(endpoint)
	if e != nil {
		return false, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-F0pn")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, e := cmd.Output()
	if e != nil || stderr.Len() != 0 {
		return false, nil
	}
	owners := map[int]bool{}
	pid := 0
	for _, part := range bytes.Split(raw, []byte{0}) {
		field := strings.TrimLeft(string(part), "\r\n")
		if field == "" {
			continue
		}
		if field[0] == 'p' {
			pid, e = strconv.Atoi(field[1:])
			if e != nil {
				return false, fmt.Errorf("控制器进程标识无效")
			}
		} else if field[0] == 'n' {
			host, p, pe := net.SplitHostPort(field[1:])
			actual := net.ParseIP(host)
			if pe == nil && p == strconv.Itoa(port) && (host == "*" || actual != nil && (actual.Equal(ip) || actual.IsUnspecified())) {
				owners[pid] = true
			}
		}
	}
	if len(owners) != 1 || owners[0] {
		return false, nil
	}
	for owner := range owners {
		limit, pe := syscall.SysctlUint32("kern.argmax")
		if pe != nil || limit < 4096 || limit > 16<<20 {
			return false, nil
		}
		args, _, pe := darwinProcessArgs(owner, make([]byte, int(limit)))
		if pe != nil || len(args) == 0 {
			return false, nil
		}
		return sameMihomoExecutable(args[0], program), nil
	}
	return false, nil
}
