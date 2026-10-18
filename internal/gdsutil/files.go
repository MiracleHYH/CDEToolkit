package gdsutil

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

// SafePath permits OS aliases above Root (for example /var on macOS), but
// rejects links within the selected software root, including Root itself.
func SafePath(root, path string) error {
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

func SingleLink(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func SameMetadata(a, b os.FileInfo) bool {
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

type Snapshot struct {
	Path string
	Info os.FileInfo
	Data []byte
	Max  int64
}

// Capture reads exactly one non-shared ordinary inode. NONBLOCK also protects
// the lstat/open race from a FIFO substituted by an uncooperative editor.
func Capture(path string, max int64, optional bool) (Snapshot, error) {
	s := Snapshot{Path: path, Max: max}
	before, err := os.Lstat(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if !before.Mode().IsRegular() || !Owned(before) || !SingleLink(before) || before.Size() > max {
		return s, fmt.Errorf("文件必须是大小受限、无共享硬链接的普通文件")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return s, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !SameMetadata(before, opened) {
		return s, fmt.Errorf("打开时文件发生变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max || int64(len(b)) != before.Size() {
		return s, fmt.Errorf("文件读取失败或大小越界")
	}
	after, err := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !SameMetadata(before, after) || !SameMetadata(before, current) {
		return s, fmt.Errorf("读取时文件发生变化")
	}
	s.Info, s.Data = before, b
	return s, nil
}

func (s Snapshot) Check() error {
	current, err := Capture(s.Path, s.Max, true)
	if err != nil {
		return err
	}
	if s.Info == nil && current.Info == nil {
		return nil
	}
	if !SameMetadata(s.Info, current.Info) || Hash(s.Data) != Hash(current.Data) {
		return fmt.Errorf("文件在操作期间发生变化；未提交替换")
	}
	return nil
}

func Read(path string, max int64) ([]byte, error) {
	s, err := Capture(path, max, false)
	return s.Data, err
}

type DirectorySnapshot struct {
	Path         string
	Info, Parent os.FileInfo
	Lock         os.FileInfo
}

func CaptureDirectory(path string) (DirectorySnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return DirectorySnapshot{}, err
	}
	if !info.IsDir() || !Owned(info) || info.Mode()&os.ModeSymlink != 0 {
		return DirectorySnapshot{}, fmt.Errorf("GDS 目录必须是普通目录")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return DirectorySnapshot{}, fmt.Errorf("GDS 父目录必须是普通目录")
	}
	lock, err := os.Lstat(filepath.Join(path, ".lock"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return DirectorySnapshot{}, err
	}
	if lock != nil && (!lock.Mode().IsRegular() || !SingleLink(lock)) {
		return DirectorySnapshot{}, fmt.Errorf("GDS 操作锁必须是无共享硬链接的普通文件")
	}
	return DirectorySnapshot{Path: path, Info: info, Parent: parent, Lock: lock}, nil
}

func sameDirectory(a, b os.FileInfo) bool {
	if a == nil || b == nil || !a.IsDir() || !b.IsDir() || !os.SameFile(a, b) || a.Mode() != b.Mode() {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Uid == y.Uid && x.Gid == y.Gid
}

func (s DirectorySnapshot) Check() error {
	info, err := os.Lstat(s.Path)
	if err != nil || !sameDirectory(s.Info, info) {
		return fmt.Errorf("GDS 目录或归属在操作期间发生变化")
	}
	parent, err := os.Lstat(filepath.Dir(s.Path))
	if err != nil || !sameDirectory(s.Parent, parent) {
		return fmt.Errorf("GDS 父目录或归属在操作期间发生变化")
	}
	// A flock protects its opened inode, not a newly created file at .lock.
	// Snapshots taken after locking must retain that same lock through waits.
	if s.Lock != nil {
		lock, err := os.Lstat(filepath.Join(s.Path, ".lock"))
		if err != nil || !SameMetadata(s.Lock, lock) {
			return fmt.Errorf("GDS 操作锁在操作期间发生变化")
		}
	}
	return nil
}

func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func Write(path string, data []byte, mode os.FileMode) error {
	before, err := Capture(path, (256 << 20), true)
	if err != nil {
		return err
	}
	return WriteExpected(path, data, mode, before)
}

func WriteExpected(path string, data []byte, mode os.FileMode, before Snapshot, guards ...DirectorySnapshot) error {
	_, err := WriteSnapshotExpected(path, data, mode, before, guards...)
	return err
}

// Return the inode this operation committed, rather than a later writer's
// identical bytes. Rename may update ctime, so Capture its new metadata while
// retaining the temporary inode's identity, expected bytes and mode.
func WriteSnapshotExpected(path string, data []byte, mode os.FileMode, before Snapshot, guards ...DirectorySnapshot) (Snapshot, error) {
	if before.Path != path {
		return Snapshot{}, fmt.Errorf("原子写入目标与预期快照不一致")
	}
	if int64(len(data)) > before.Max {
		return Snapshot{}, fmt.Errorf("原子写入数据超过预期文件大小限制")
	}
	if err := before.Check(); err != nil {
		return Snapshot{}, err
	}
	for _, guard := range guards {
		if err := guard.Check(); err != nil {
			return Snapshot{}, err
		}
	}
	dir, err := CaptureDirectory(filepath.Dir(path))
	if err != nil {
		return Snapshot{}, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nido-")
	if err != nil {
		return Snapshot{}, err
	}
	tmp := f.Name()
	created, err := f.Stat()
	if err != nil {
		f.Close()
		return Snapshot{}, err
	}
	defer RemoveCreated(tmp, created)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Snapshot{}, err
	}
	if closeErr != nil {
		return Snapshot{}, closeErr
	}
	candidate, err := Capture(tmp, before.Max, false)
	if err != nil {
		return Snapshot{}, err
	}
	if !os.SameFile(created, candidate.Info) || candidate.Info.Mode().Perm() != mode.Perm() || Hash(candidate.Data) != Hash(data) {
		return Snapshot{}, fmt.Errorf("原子写入的临时文件在准备期间发生变化")
	}
	if err = dir.Check(); err != nil {
		return Snapshot{}, err
	}
	if err = before.Check(); err != nil {
		return Snapshot{}, err
	}
	if err = candidate.Check(); err != nil {
		return Snapshot{}, err
	}
	for _, guard := range guards {
		if err := guard.Check(); err != nil {
			return Snapshot{}, err
		}
	}
	return CommitCandidate(candidate, before, append(guards, dir)...)
}

// CommitCandidate preserves the displaced inode on Linux. A late replacement
// between the last snapshot check and rename can therefore never be silently
// unlinked. Conflicts fail closed and name the retained recovery file.
func CommitCandidate(candidate, before Snapshot, guards ...DirectorySnapshot) (Snapshot, error) {
	if candidate.Info == nil || filepath.Dir(candidate.Path) != filepath.Dir(before.Path) || candidate.Path == before.Path {
		return Snapshot{}, fmt.Errorf("提交候选与目标必须是同目录的不同文件")
	}
	if err := candidate.Check(); err != nil {
		return Snapshot{}, err
	}
	if err := before.Check(); err != nil {
		return Snapshot{}, err
	}
	for _, guard := range guards {
		if err := guard.Check(); err != nil {
			return Snapshot{}, err
		}
	}
	return commitCandidateChecked(candidate, before, guards...)
}

// Separate the checked commit so deterministic tests can insert the precise
// editor interleaving after the last check, without production race hooks.
func commitCandidateChecked(candidate, before Snapshot, guards ...DirectorySnapshot) (Snapshot, error) {
	preserved, err := renameCommit(candidate.Path, before.Path, before.Info != nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("GDS 原子提交失败；未覆盖迟到文件: %w", err)
	}
	var displaced Snapshot
	if preserved {
		displaced, err = Capture(candidate.Path, before.Max, false)
		if err != nil || !sameRenamedSnapshot(before, displaced) {
			return Snapshot{}, fmt.Errorf("GDS 提交遇到并发修改；被替换内容保留于 %s，当前目标需人工审查", candidate.Path)
		}
	}
	installed, err := Capture(before.Path, candidate.Max, false)
	if err != nil || !sameRenamedSnapshot(candidate, installed) {
		if preserved {
			return Snapshot{}, fmt.Errorf("GDS 目标在提交期间发生变化；先前内容保留于 %s", candidate.Path)
		}
		return Snapshot{}, fmt.Errorf("GDS 目标在提交期间发生变化；保留现场 %s", before.Path)
	}
	for _, guard := range guards {
		if err = guard.Check(); err != nil {
			if preserved {
				return Snapshot{}, fmt.Errorf("%w；先前内容保留于 %s", err, candidate.Path)
			}
			return Snapshot{}, err
		}
	}
	if preserved {
		if err = displaced.Check(); err != nil {
			return Snapshot{}, fmt.Errorf("提交后先前内容又被修改；保留于 %s: %w", candidate.Path, err)
		}
		RemoveCreated(candidate.Path, displaced.Info)
	}
	return installed, nil
}

func sameRenamedSnapshot(before, after Snapshot) bool {
	a, b := before.Info, after.Info
	if a == nil || b == nil || !os.SameFile(a, b) || a.Mode() != b.Mode() || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) || Hash(before.Data) != Hash(after.Data) {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	// Rename legitimately changes ctime, so compare all remaining ownership,
	// sharing and content fields rather than accepting any inode at the path.
	return xok && yok && x.Uid == y.Uid && x.Gid == y.Gid && x.Nlink == y.Nlink
}

// RemoveExpected first moves the target into an unoccupied private recovery
// name. A replacement that arrives after Check is retained there rather than
// being unlinked, while a new file created at the old path is never removed.
func RemoveExpected(before Snapshot, guards ...DirectorySnapshot) error {
	if before.Info == nil {
		return before.Check()
	}
	if err := before.Check(); err != nil {
		return err
	}
	for _, guard := range guards {
		if err := guard.Check(); err != nil {
			return err
		}
	}
	f, err := os.CreateTemp(filepath.Dir(before.Path), ".nido-removed-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return statErr
	}
	RemoveCreated(tmp, info)
	if closeErr != nil {
		return closeErr
	}
	return removeExpectedChecked(before, tmp, guards...)
}

func removeExpectedChecked(before Snapshot, tmp string, guards ...DirectorySnapshot) error {
	if _, err := renameCommit(before.Path, tmp, false); err != nil {
		return fmt.Errorf("GDS 原子移除失败；未删除迟到文件: %w", err)
	}
	moved, err := Capture(tmp, before.Max, false)
	if err != nil || !sameRenamedSnapshot(before, moved) {
		return fmt.Errorf("GDS 移除遇到并发修改；内容保留于 %s", tmp)
	}
	for _, guard := range guards {
		if err := guard.Check(); err != nil {
			return fmt.Errorf("%w；移除前内容保留于 %s", err, tmp)
		}
	}
	if err := moved.Check(); err != nil {
		return fmt.Errorf("移除前内容又被修改；保留于 %s: %w", tmp, err)
	}
	RemoveCreated(tmp, moved.Info)
	return nil
}

// A random temporary name is not ownership by itself: only remove the inode
// this operation created if the path still refers to that exact object.
func RemoveCreated(path string, created os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(created, current) {
		_ = os.Remove(path)
	}
}

func Owned(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}
