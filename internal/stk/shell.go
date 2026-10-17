package stk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) environment(plan *Plan) error {
	env := []byte("# server-toolkit-go generated environment\ncase \":$PATH:\" in *" + quote(":"+filepath.Join(m.Home, "bin")+":") + "*) ;; *) export PATH=" + quote(filepath.Join(m.Home, "bin")) + ":\"$PATH\" ;; esac\n")
	if c, ok := m.State.Modules["codex"]; ok && m.Config.Modules["codex"].Enabled {
		home, err := m.resolve(c.Home)
		if err != nil {
			return err
		}
		env = append(env, []byte("export CODEX_HOME="+quote(home)+"\n")...)
	}
	return m.generated(plan, "env.sh", env, 0600)
}

func (m *Manager) removeShellHooks(plan *Plan) error {
	for _, ref := range m.State.ShellFiles {
		path, err := m.resolve(ref)
		if err != nil {
			return err
		}
		before, err := readRegular(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.Contains(string(before), shellBegin) && !strings.Contains(string(before), shellEnd) {
			continue
		}
		// shellHook checks exact ownership of the block while preserving the
		// surrounding user content, including edits made since setup.
		normal, err := shellHook(before, m.Home, m.rootPaths("stk")...)
		if err != nil {
			return err
		}
		after := normal[len(hookBlock(m.Home)):]
		s, err := os.Stat(path)
		if err != nil {
			return err
		}
		plan.guardBytes(path, before, s.Mode().Perm())
		if err = plan.add(path, "write", "移除 STK Shell 块，保留用户内容", after, "", s.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

const shellBegin = "# >>> server-toolkit-go environment >>>"
const shellEnd = "# <<< server-toolkit-go environment <<<"

func hookBlock(home string) string {
	path := quote(filepath.Join(home, "env.sh"))
	return shellBegin + "\nif [ -r " + path + " ]; then . " + path + "; fi\n" + shellEnd + "\n"
}

func shellHook(before []byte, home string, oldHomes ...string) ([]byte, error) {
	s := string(before)
	beginCount, endCount := strings.Count(s, shellBegin), strings.Count(s, shellEnd)
	if beginCount == 0 && endCount == 0 {
		return []byte(hookBlock(home) + s), nil
	}
	if beginCount != 1 || endCount != 1 {
		return nil, fmt.Errorf("Shell 受管块不完整或重复")
	}
	start, end := strings.Index(s, shellBegin), strings.Index(s, shellEnd)+len(shellEnd)
	if start > end {
		return nil, fmt.Errorf("Shell 受管块顺序错误")
	}
	if end < len(s) && s[end] == '\n' {
		end++
	}
	current := s[start:end]
	allowed := current == hookBlock(home)
	for _, oldHome := range oldHomes {
		if oldHome != "" && current == hookBlock(oldHome) {
			allowed = true
		}
	}
	if !allowed {
		return nil, fmt.Errorf("Shell 受管块已被外部修改")
	}
	// Keep activation above interactive-only early returns while preserving
	// everything outside our own block byte-for-byte.
	return []byte(hookBlock(home) + s[:start] + s[end:]), nil
}
