package gitproxy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxConfigSize = 1 << 20

type snapshot struct {
	path         string
	content      []byte
	info         os.FileInfo
	dir          os.FileInfo
	dependencies []watchedFile
}

type watchedFile struct {
	path     string
	snapshot snapshot
}

// canonicalLocation resolves existing ancestors without creating a missing
// directory. It is used solely to keep Git configuration outside Nido's root.
func canonicalLocation(path string) (string, error) {
	if !filepath.IsAbs(path) || invalidPath(path) {
		return "", fmt.Errorf("Nido/Git 配置位置必须为绝对路径")
	}
	parent := filepath.Clean(path)
	var suffix []string
	for {
		if _, err := os.Lstat(parent); err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", fmt.Errorf("无法确定 Nido/Git 配置位置")
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("无法确定 Nido/Git 配置位置")
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s Service) outsideRoot(path string) error {
	if s.Root == "" {
		return nil
	}
	root, err := canonicalLocation(s.Root)
	if err != nil {
		return err
	}
	location, err := canonicalLocation(path)
	if err != nil {
		return err
	}
	if within(filepath.Clean(s.Root), filepath.Clean(path)) || within(root, location) {
		return fmt.Errorf("Git 配置不能位于 Nido 根目录内")
	}
	return nil
}

func resolveDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || invalidPath(path) {
		return "", fmt.Errorf("Git 配置目录必须为绝对路径")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("无法定位 Git 配置目录")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Git 配置父路径不是目录")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return "", fmt.Errorf("Git 配置目录须归当前用户所有且不能允许其他用户写入")
	}
	return resolved, nil
}

func resolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) || invalidPath(path) {
		return "", fmt.Errorf("Git 用户配置必须为绝对路径")
	}
	parent, err := resolveDirectory(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func readFile(path string, missingParents bool) (snapshot, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		if missingParents {
			if _, statErr := os.Stat(filepath.Dir(path)); os.IsNotExist(statErr) {
				return snapshot{path: path}, nil
			}
		}
		return snapshot{}, err
	}
	dir, err := os.Stat(filepath.Dir(resolved))
	if err != nil {
		return snapshot{}, fmt.Errorf("无法读取 Git 配置目录")
	}
	out := snapshot{path: resolved, dir: dir}
	info, err := os.Lstat(resolved)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigSize {
		return snapshot{}, fmt.Errorf("Git 配置必须是不超过 1 MiB 的普通文件")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return snapshot{}, fmt.Errorf("Git 配置须归当前用户所有，且不能是共享硬链接或允许其他用户写入")
	}
	f, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return snapshot{}, fmt.Errorf("无法读取 Git 配置")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameInfo(info, opened) {
		return snapshot{}, fmt.Errorf("Git 配置在读取期间发生变化")
	}
	content, err := io.ReadAll(io.LimitReader(f, maxConfigSize+1))
	if err != nil || len(content) > maxConfigSize || int64(len(content)) != info.Size() {
		return snapshot{}, fmt.Errorf("Git 配置在读取期间大小发生变化")
	}
	finished, err := f.Stat()
	current, pathErr := os.Lstat(resolved)
	currentDir, dirErr := os.Stat(filepath.Dir(resolved))
	if err != nil || pathErr != nil || dirErr != nil || !sameInfo(info, finished) || !sameInfo(info, current) || !os.SameFile(dir, currentDir) {
		return snapshot{}, fmt.Errorf("Git 配置在读取期间发生变化")
	}
	out.info, out.content = info, content
	return out, nil
}

func sameInfo(a, b os.FileInfo) bool {
	if a == nil || b == nil || !os.SameFile(a, b) || a.Mode() != b.Mode() || !a.ModTime().Equal(b.ModTime()) || a.Size() != b.Size() {
		return false
	}
	left, leftOK := a.Sys().(*syscall.Stat_t)
	right, rightOK := b.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && left.Nlink == 1 && right.Nlink == 1 && left.Uid == uint32(os.Geteuid()) && right.Uid == left.Uid
}

func sameSnapshot(a, b snapshot) bool {
	return a.path == b.path && a.dir != nil && b.dir != nil && os.SameFile(a.dir, b.dir) && bytes.Equal(a.content, b.content) && (a.info == nil && b.info == nil || sameInfo(a.info, b.info))
}

func verifyDependencies(files []watchedFile) error {
	for _, file := range files {
		current, err := readFile(file.path, true)
		unchangedMissing := file.snapshot.dir == nil && current.dir == nil && file.snapshot.info == nil && current.info == nil && file.snapshot.path == current.path
		if err != nil || !unchangedMissing && !sameSnapshot(file.snapshot, current) {
			return fmt.Errorf("Git XDG 配置在准备后发生变化，未修改")
		}
	}
	return nil
}

func replaceFile(original snapshot, next []byte) error {
	file, err := os.CreateTemp(filepath.Dir(original.path), ".nido-git-*")
	if err != nil {
		return fmt.Errorf("无法创建 Git 配置临时文件")
	}
	name := file.Name()
	defer os.Remove(name)
	mode := os.FileMode(0600)
	if original.info != nil {
		mode = original.info.Mode().Perm()
	}
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(next)
	}
	if e := file.Sync(); err == nil {
		err = e
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err != nil {
		return fmt.Errorf("无法写入 Git 配置")
	}
	if err := verifyDependencies(original.dependencies); err != nil {
		return err
	}
	current, err := readFile(original.path, false)
	if err != nil || !sameSnapshot(original, current) {
		return fmt.Errorf("Git 配置同时发生变化，未覆盖")
	}
	if original.info == nil {
		// Exclusive publication prevents replacing a user-created file that
		// appears between the final snapshot check and the filesystem write.
		if err := os.Link(name, original.path); err != nil {
			return fmt.Errorf("Git 配置同时创建或无法写入，未覆盖")
		}
		return nil
	}
	if err := os.Rename(name, original.path); err != nil {
		return fmt.Errorf("无法替换 Git 配置")
	}
	return nil
}
