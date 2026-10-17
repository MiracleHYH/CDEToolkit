package codex

import (
	"fmt"
	"os"
	"path/filepath"
)

// A download may take time. Remember just the directories and command entry
// touched by this invocation, rather than inspecting any Codex private files.
type locationGuard struct {
	path, expected, anchor string
	info, anchorInfo       os.FileInfo
}

func guardLocation(path string) (locationGuard, error) {
	expected, err := canonical(path)
	if err != nil {
		return locationGuard{}, err
	}
	g := locationGuard{path: path, expected: expected, anchor: expected}
	info, err := os.Stat(expected)
	if err == nil {
		if !info.IsDir() {
			return g, fmt.Errorf("安装目标必须为目录")
		}
		g.info = info
	} else if !os.IsNotExist(err) {
		return g, fmt.Errorf("无法访问安装目标")
	}
	for {
		info, err = os.Stat(g.anchor)
		if err == nil {
			if !info.IsDir() {
				return g, fmt.Errorf("安装目标父路径不是目录")
			}
			g.anchorInfo = info
			return g, nil
		}
		if !os.IsNotExist(err) {
			return g, fmt.Errorf("无法访问安装目标父目录")
		}
		g.anchor = filepath.Dir(g.anchor)
	}
}

func (g locationGuard) check() error {
	current, err := canonical(g.path)
	if err != nil || current != g.expected {
		return fmt.Errorf("安装目标路径在下载期间发生变化，未执行安装器")
	}
	anchor, err := os.Stat(g.anchor)
	if err != nil || !os.SameFile(g.anchorInfo, anchor) {
		return fmt.Errorf("安装目标父目录在下载期间发生变化，未执行安装器")
	}
	info, err := os.Stat(g.expected)
	if g.info == nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("安装目标在下载期间被创建或替换，未执行安装器")
	}
	if err != nil || !os.SameFile(g.info, info) || !info.IsDir() {
		return fmt.Errorf("安装目标目录在下载期间发生变化，未执行安装器")
	}
	return nil
}

type entryGuard struct {
	path, resolved   string
	info, targetInfo os.FileInfo
}

func guardEntry(path, root string) (entryGuard, error) {
	g := entryGuard{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return g, nil
	}
	if err != nil {
		return g, fmt.Errorf("无法检查 Codex 入口")
	}
	g.info = info
	g.resolved, err = filepath.EvalSymlinks(path)
	if err != nil {
		return g, fmt.Errorf("Codex 入口链接失效，未运行安装器")
	}
	if root != "" && inside(root, g.resolved) {
		return g, fmt.Errorf("Codex 入口不能指向 Nido 软件目录")
	}
	g.targetInfo, err = os.Stat(g.resolved)
	if err != nil || !g.targetInfo.Mode().IsRegular() {
		return g, fmt.Errorf("Codex 入口不是普通程序文件")
	}
	return g, nil
}

func (g entryGuard) check() error {
	info, err := os.Lstat(g.path)
	if g.info == nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("Codex 入口在下载期间被创建，未执行安装器")
	}
	if err != nil || !os.SameFile(g.info, info) {
		return fmt.Errorf("Codex 入口在下载期间发生变化，未执行安装器")
	}
	resolved, err := filepath.EvalSymlinks(g.path)
	if err != nil || resolved != g.resolved {
		return fmt.Errorf("Codex 入口目标在下载期间发生变化，未执行安装器")
	}
	target, err := os.Stat(resolved)
	if err != nil || !os.SameFile(g.targetInfo, target) {
		return fmt.Errorf("Codex 程序在下载期间发生变化，未执行安装器")
	}
	return nil
}
