package builtin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The selected installation root may have ordinary OS symlinks in its parent
// path (e.g. macOS /var). Within Nido's root no managed path may be a symlink.
func safePath(root, path string) error {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Nido 软件目录不能是符号链接")
		}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("路径超出 Nido 软件目录")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("受管路径不能通过符号链接访问")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if p == root {
			return nil
		}
	}
}

// One non-blocking OS lock per built-in serializes install/start/stop/update.
// It is not a transaction log, daemon lock, or persistent installed-state DB.
func (m *Manager) withLock(name string, action func() error) error {
	dir, _, err := m.paths(name)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, ".lock")
	if err = safePath(m.Root, path); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("组件操作锁不是普通文件")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("此组件有另一项 Nido 操作正在进行")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return action()
}

func readRegular(path string, max int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > max {
		return nil, fmt.Errorf("文件不是普通文件或大小越界")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, fmt.Errorf("读取时文件发生变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max || int64(len(b)) != before.Size() {
		return nil, fmt.Errorf("文件读取失败或大小越界")
	}
	return b, nil
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".nido-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
