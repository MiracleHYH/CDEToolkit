//go:build linux || darwin

package cdet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type inactiveMatcher struct{ roots []string }

func inactivePaths(paths []string) (inactiveMatcher, error) {
	m := inactiveMatcher{}
	for _, path := range paths {
		if path == "" {
			continue // a fresh component has no source program or data root
		}
		if e := absolute(path); e != nil {
			return m, e
		}
		candidates := []string{path}
		if real, e := filepath.EvalSymlinks(path); e == nil {
			candidates = append(candidates, real)
		} else if !os.IsNotExist(e) {
			return m, fmt.Errorf("无法确认组件路径的真实位置: %s", path)
		}
		for _, candidate := range candidates {
			covered := false
			for _, root := range m.roots {
				covered = covered || within(root, candidate)
			}
			if !covered {
				m.roots = append(m.roots, candidate)
			}
		}
	}
	return m, nil
}

func (m inactiveMatcher) matches(path string) bool {
	path = strings.TrimSuffix(path, " (deleted)")
	if !filepath.IsAbs(path) {
		return false
	}
	path = filepath.Clean(path)
	for _, root := range m.roots {
		if path == root || strings.HasPrefix(path, root) && len(path) > len(root) && path[len(root)] == os.PathSeparator {
			return true
		}
	}
	return false
}

func (m inactiveMatcher) processPath(path string) bool {
	if m.matches(path) {
		return true
	}
	if filepath.IsAbs(path) {
		if real, e := filepath.EvalSymlinks(path); e == nil {
			return m.matches(real)
		}
	}
	return false
}

func (m inactiveMatcher) process(args, env []string, cwd string) bool {
	codex, shell := false, false
	for i, arg := range args {
		base := filepath.Base(arg)
		codex = codex || base == "codex" || base == "codex.js"
		if i == 0 {
			switch strings.TrimPrefix(base, "-") {
			case "sh", "bash", "zsh", "dash", "fish", "ksh":
				shell = true
			}
		}
		if m.processPath(arg) {
			return true
		}
		if _, value, ok := strings.Cut(arg, "="); ok && m.processPath(value) {
			return true
		}
	}
	values := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	// An invoking shell can export CODEX_HOME without running Codex. Its
	// executable, cwd and open files remain subject to the other checks.
	if !shell || codex {
		for _, key := range []string{"CODEX_HOME", "CODEX_SQLITE_HOME"} {
			path := values[key]
			if path != "" && !filepath.IsAbs(path) && cwd != "" {
				path = filepath.Join(cwd, path)
			}
			if m.processPath(path) {
				return true
			}
		}
	}
	if codex && values["CODEX_HOME"] == "" && values["HOME"] != "" && m.processPath(filepath.Join(values["HOME"], ".codex")) {
		return true
	}
	return false
}

func activeComponent(pid int) error {
	return fmt.Errorf("组件仍被进程使用（PID %d）；请先退出 Codex 及其 daemon/app-server 或其他相关组件，再重试；未停止任何进程", pid)
}
