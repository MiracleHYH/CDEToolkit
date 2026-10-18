package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
)

// safePath permits OS aliases above Root (for example /var on macOS), but
// rejects links within the selected software root, including Root itself.
func safePath(root, path string) error {
	root, path = filepath.Clean(root), filepath.Clean(path)
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return fmt.Errorf("受管路径必须是绝对路径")
	}
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Nido 软件目录不能是符号链接")
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			path = filepath.Join(real, rel)
		}
		root = real
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("路径超出 Nido 软件目录")
	}
	for p := root; ; {
		info, e := os.Lstat(p)
		if e == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("受管路径不能通过符号链接访问")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if p == path {
			return nil
		}
		rest, _ := filepath.Rel(p, path)
		part, _, _ := strings.Cut(rest, string(filepath.Separator))
		if info != nil && !info.IsDir() {
			return fmt.Errorf("受管路径的父路径不是目录")
		}
		p = filepath.Join(p, part)
	}
}

func singleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func sameMetadata(a, b os.FileInfo) bool {
	if a == nil || b == nil || !os.SameFile(a, b) || a.Mode() != b.Mode() || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	if !xok || !yok || x.Nlink != y.Nlink || x.Uid != y.Uid || x.Gid != y.Gid {
		return false
	}
	// Darwin and Linux name their structured change-time fields differently.
	// Ignore access time, since reading our own snapshot may legitimately change it.
	for _, name := range []string{"Ctim", "Ctimespec"} {
		xv, yv := reflect.ValueOf(x).Elem().FieldByName(name), reflect.ValueOf(y).Elem().FieldByName(name)
		if xv.IsValid() && yv.IsValid() && !reflect.DeepEqual(xv.Interface(), yv.Interface()) {
			return false
		}
	}
	return true
}

type fileSnapshot struct {
	path string
	info os.FileInfo
	data []byte
	max  int64
}

// capture reads exactly one non-shared ordinary inode. NONBLOCK also protects
// the lstat/open race from a FIFO substituted by an uncooperative editor.
func capture(path string, max int64, optional bool) (fileSnapshot, error) {
	s := fileSnapshot{path: path, max: max}
	before, err := os.Lstat(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !before.Mode().IsRegular() || !singleLink(before) || before.Size() > max {
		return s, fmt.Errorf("文件必须是大小受限、无共享硬链接的普通文件")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return s, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameMetadata(before, opened) {
		return s, fmt.Errorf("打开时文件发生变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max || int64(len(b)) != before.Size() {
		return s, fmt.Errorf("文件读取失败或大小越界")
	}
	after, err := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !sameMetadata(before, after) || !sameMetadata(before, current) {
		return s, fmt.Errorf("读取时文件发生变化")
	}
	s.info, s.data = before, b
	return s, nil
}

func (s fileSnapshot) check() error {
	current, err := capture(s.path, s.max, true)
	if err != nil {
		return err
	}
	if s.info == nil && current.info == nil {
		return nil
	}
	if !sameMetadata(s.info, current.info) || hash(s.data) != hash(current.data) {
		return fmt.Errorf("文件在操作期间发生变化；未提交替换")
	}
	return nil
}

func readRegular(path string, max int64) ([]byte, error) {
	s, err := capture(path, max, false)
	return s.data, err
}

type directorySnapshot struct {
	path         string
	info, parent os.FileInfo
	lock         os.FileInfo
}

func captureDirectory(path string) (directorySnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return directorySnapshot{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return directorySnapshot{}, fmt.Errorf("Proxy 目录必须是普通目录")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return directorySnapshot{}, fmt.Errorf("Proxy 父目录必须是普通目录")
	}
	lock, err := os.Lstat(filepath.Join(path, ".lock"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return directorySnapshot{}, err
	}
	if lock != nil && (!lock.Mode().IsRegular() || !singleLink(lock)) {
		return directorySnapshot{}, fmt.Errorf("Proxy 操作锁必须是无共享硬链接的普通文件")
	}
	return directorySnapshot{path: path, info: info, parent: parent, lock: lock}, nil
}

func sameDirectory(a, b os.FileInfo) bool {
	if a == nil || b == nil || !a.IsDir() || !b.IsDir() || !os.SameFile(a, b) || a.Mode() != b.Mode() {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Uid == y.Uid && x.Gid == y.Gid
}

func (s directorySnapshot) check() error {
	info, err := os.Lstat(s.path)
	if err != nil || !sameDirectory(s.info, info) {
		return fmt.Errorf("Proxy 目录或归属在操作期间发生变化")
	}
	parent, err := os.Lstat(filepath.Dir(s.path))
	if err != nil || !sameDirectory(s.parent, parent) {
		return fmt.Errorf("Proxy 父目录或归属在操作期间发生变化")
	}
	// A flock protects its opened inode, not a newly created file at .lock.
	// Snapshots taken after locking must retain that same lock through waits.
	if s.lock != nil {
		lock, err := os.Lstat(filepath.Join(s.path, ".lock"))
		if err != nil || !sameMetadata(s.lock, lock) {
			return fmt.Errorf("Proxy 操作锁在操作期间发生变化")
		}
	}
	return nil
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) withLock(action func(directorySnapshot) error) error {
	dir, _, err := s.paths()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, ".lock")
	if err = safePath(s.Root, path); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = safePath(s.Root, path); err != nil {
		return err
	}
	directory, err := captureDirectory(dir)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !singleLink(info) {
		return fmt.Errorf("Proxy 操作锁必须是无共享硬链接的普通文件")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("Proxy 有另一项 Nido 操作正在进行")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	current, err := os.Lstat(path)
	if err != nil || !sameMetadata(info, current) {
		return fmt.Errorf("Proxy 操作锁发生变化")
	}
	if err = directory.check(); err != nil {
		return err
	}
	// Bind the guard to the inode actually held by flock, including the first
	// operation that created .lock. Callers keep this guard across every wait.
	directory.lock = info
	if err = directory.check(); err != nil {
		return err
	}
	return action(directory)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	before, err := capture(path, maxProgram, true)
	if err != nil {
		return err
	}
	return atomicWriteExpected(path, data, mode, before)
}

func atomicWriteExpected(path string, data []byte, mode os.FileMode, before fileSnapshot, guards ...directorySnapshot) error {
	_, err := atomicWriteSnapshotExpected(path, data, mode, before, guards...)
	return err
}

// Return the inode this operation committed, rather than a later writer's
// identical bytes. Rename may update ctime, so capture its new metadata while
// retaining the temporary inode's identity, expected bytes and mode.
func atomicWriteSnapshotExpected(path string, data []byte, mode os.FileMode, before fileSnapshot, guards ...directorySnapshot) (fileSnapshot, error) {
	if before.path != path {
		return fileSnapshot{}, fmt.Errorf("原子写入目标与预期快照不一致")
	}
	if int64(len(data)) > before.max {
		return fileSnapshot{}, fmt.Errorf("原子写入数据超过预期文件大小限制")
	}
	if err := before.check(); err != nil {
		return fileSnapshot{}, err
	}
	for _, guard := range guards {
		if err := guard.check(); err != nil {
			return fileSnapshot{}, err
		}
	}
	dir, err := captureDirectory(filepath.Dir(path))
	if err != nil {
		return fileSnapshot{}, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nido-")
	if err != nil {
		return fileSnapshot{}, err
	}
	tmp := f.Name()
	created, err := f.Stat()
	if err != nil {
		f.Close()
		return fileSnapshot{}, err
	}
	defer removeCreated(tmp, created)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fileSnapshot{}, err
	}
	if closeErr != nil {
		return fileSnapshot{}, closeErr
	}
	candidate, err := capture(tmp, before.max, false)
	if err != nil {
		return fileSnapshot{}, err
	}
	if !os.SameFile(created, candidate.info) || candidate.info.Mode().Perm() != mode.Perm() || hash(candidate.data) != hash(data) {
		return fileSnapshot{}, fmt.Errorf("原子写入的临时文件在准备期间发生变化")
	}
	if err = dir.check(); err != nil {
		return fileSnapshot{}, err
	}
	if err = before.check(); err != nil {
		return fileSnapshot{}, err
	}
	if err = candidate.check(); err != nil {
		return fileSnapshot{}, err
	}
	for _, guard := range guards {
		if err := guard.check(); err != nil {
			return fileSnapshot{}, err
		}
	}
	if before.info == nil {
		// Missing-file snapshots authorize exclusive creation, never replacing
		// an editor's file appearing after the final snapshot check.
		if err = os.Link(tmp, path); err != nil {
			return fileSnapshot{}, err
		}
		if err = os.Remove(tmp); err != nil {
			return fileSnapshot{}, err
		}
	} else if err = os.Rename(tmp, path); err != nil {
		return fileSnapshot{}, err
	}
	installed, err := capture(path, before.max, false)
	if err != nil {
		return fileSnapshot{}, err
	}
	if !os.SameFile(created, installed.info) || installed.info.Mode().Perm() != mode.Perm() || hash(installed.data) != hash(data) {
		return fileSnapshot{}, fmt.Errorf("原子写入的目标在提交期间发生变化")
	}
	for _, guard := range guards {
		if err = guard.check(); err != nil {
			return fileSnapshot{}, err
		}
	}
	return installed, nil
}

// A random temporary name is not ownership by itself: only remove the inode
// this operation created if the path still refers to that exact object.
func removeCreated(path string, created os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(created, current) {
		_ = os.Remove(path)
	}
}
