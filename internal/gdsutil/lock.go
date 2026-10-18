package gdsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// EnsureDir creates only the requested directory; existing ownership and mode
// must already be private. It never repairs a foreign/shared directory.
func EnsureDir(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("GDS 目录必须是绝对路径")
	}
	err := os.Mkdir(path, 0700)
	if err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
		return fmt.Errorf("GDS 目录必须由当前用户持有且权限为 0700")
	}
	return nil
}

// Lock returns the same stable lock inode on every call. Closing it releases
// flock; callers must never unlink a lock file to unlock it.
func Lock(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && (!before.Mode().IsRegular() || !SingleLink(before)) {
		return nil, fmt.Errorf("GDS 锁必须是单链接普通文件")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) { f.Close(); return nil, err }
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm() != 0600 {
		return fail(fmt.Errorf("GDS 锁归属、类型或权限无效"))
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(fmt.Errorf("GDS 锁已被持有: %w", err))
	}
	current, err := os.Lstat(path)
	if err != nil || !SameMetadata(info, current) {
		return fail(fmt.Errorf("GDS 锁在打开期间发生变化"))
	}
	return f, nil
}
