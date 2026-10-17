package cdet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codexLinks handles only the upstream installer's three public package links.
// It never scans or interprets configuration, databases, or conversation files.
func (m *Manager) codexLinks(repair bool) error {
	packages := filepath.Join(m.Store, dataPath("codex"), "packages", "standalone")
	oldPackages := packages
	if m.Config.Store != "" {
		oldPackages = filepath.Join(m.Config.Store, dataPath("codex"), "packages", "standalone")
	}
	visible := filepath.Join(m.Store, programPath("codex"))
	current := filepath.Join(packages, "current")
	paths := []string{current, visible, filepath.Join(filepath.Dir(visible), "codex-code-mode-host")}
	known := map[string]bool{}
	for _, path := range paths {
		known[path] = true
	}
	mapTarget := func(path, target string) (string, error) {
		if !filepath.IsAbs(target) {
			target = filepath.Dir(path) + string(os.PathSeparator) + target
		}
		if within(oldPackages, filepath.Clean(target)) && oldPackages != packages {
			rel, err := filepath.Rel(oldPackages, filepath.Clean(target))
			if err != nil {
				return "", err
			}
			target = filepath.Join(packages, rel)
		}
		if !within(packages, filepath.Clean(target)) {
			return "", fmt.Errorf("Codex 官方程序链接超出受管包目录")
		}
		return target, nil
	}
	var resolve func(string, int) (string, error)
	resolve = func(path string, budget int) (string, error) {
		if budget == 0 {
			return "", fmt.Errorf("Codex 官方程序链接循环或过深")
		}
		parts, cursor := strings.Split(path, string(os.PathSeparator)), string(os.PathSeparator)
		for i, part := range parts {
			switch part {
			case "", ".":
				continue
			case "..":
				cursor = filepath.Dir(cursor)
				continue
			}
			next := filepath.Join(cursor, part)
			info, err := os.Lstat(next)
			if err != nil {
				return "", fmt.Errorf("Codex 官方程序包链接目标缺失")
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(next)
				if err != nil {
					return "", err
				}
				if known[next] {
					target, err = mapTarget(next, target)
				} else {
					if !filepath.IsAbs(target) {
						target = filepath.Dir(next) + string(os.PathSeparator) + target
					}
					if !within(packages, filepath.Clean(target)) {
						err = fmt.Errorf("Codex 官方程序包依赖目录外链接")
					}
				}
				if err != nil {
					return "", err
				}
				if i+1 < len(parts) {
					target += string(os.PathSeparator) + strings.Join(parts[i+1:], string(os.PathSeparator))
				}
				return resolve(target, budget-1)
			}
			if i+1 < len(parts) && !info.IsDir() {
				return "", fmt.Errorf("Codex 官方程序包链接经过非目录")
			}
			cursor = next
		}
		if !within(packages, cursor) {
			return "", fmt.Errorf("Codex 官方程序链接解析后超出受管包目录")
		}
		return cursor, nil
	}
	type rewrite struct{ path, target string }
	var changes []rewrite
	for _, path := range paths {
		if err := noSymlinkParents(path); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) && path != visible {
			continue
		}
		if err != nil {
			return fmt.Errorf("Codex 官方程序入口缺失")
		}
		if info.Mode()&os.ModeSymlink == 0 {
			// A local standalone program is valid too. Ordinary files are never
			// rewritten, including a HOME's unknown, unused package entries.
			if path != current && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0) {
				return fmt.Errorf("Codex 程序入口不是可执行文件")
			}
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		mapped, err := mapTarget(path, target)
		if err != nil {
			return err
		}
		resolved, err := resolve(mapped, 40)
		if err != nil {
			return err
		}
		cleanResolved, err := resolve(filepath.Clean(mapped), 40)
		if err != nil || cleanResolved != resolved {
			return fmt.Errorf("Codex 官方程序链接包含无法安全规范化的分段")
		}
		final, err := os.Lstat(resolved)
		if err != nil || path == current && !final.IsDir() || path != current && (!final.Mode().IsRegular() || final.Mode().Perm()&0111 == 0) {
			return fmt.Errorf("Codex 官方程序链接目标类型或权限无效")
		}
		relativeTarget, err := filepath.Rel(filepath.Dir(path), filepath.Clean(mapped))
		if err != nil {
			return err
		}
		if target != relativeTarget {
			changes = append(changes, rewrite{path, relativeTarget})
		}
	}
	if repair {
		for _, change := range changes {
			if err := replaceLink(change.path, change.target); err != nil {
				return err
			}
		}
	}
	return nil
}
