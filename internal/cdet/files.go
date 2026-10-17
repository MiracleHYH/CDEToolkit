package cdet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type Fingerprint struct {
	Kind   string `json:"kind"`
	Digest string `json:"digest,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}

func fingerprint(p string) (Fingerprint, error) {
	s, e := os.Lstat(p)
	if errors.Is(e, os.ErrNotExist) {
		return Fingerprint{Kind: "absent"}, nil
	}
	if e != nil {
		return Fingerprint{}, e
	}
	f := Fingerprint{Mode: uint32(s.Mode().Perm())}
	if s.IsDir() {
		f.Kind = "directory"
		return f, nil
	}
	if s.Mode()&os.ModeSymlink != 0 {
		f.Kind = "link"
		// Symlink permission bits differ on Darwin/Linux and do not authorize
		// access to the target. Only the link text participates in ownership.
		f.Mode = 0777
		f.Digest, e = os.Readlink(p)
		return f, e
	}
	if !s.Mode().IsRegular() {
		return f, fmt.Errorf("拒绝特殊文件: %s", p)
	}
	in, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return f, e
	}
	defer in.Close()
	opened, e := in.Stat()
	if e != nil || !os.SameFile(s, opened) {
		return f, fmt.Errorf("读取时文件被替换: %s", p)
	}
	h := sha256.New()
	if _, e = io.Copy(h, in); e != nil {
		return f, e
	}
	f.Kind, f.Digest = "file", hex.EncodeToString(h.Sum(nil))
	return f, nil
}

func syncParent(p string) error {
	return syncDirectory(filepath.Dir(p))
}

func syncDirectory(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
	}
	defer d.Close()
	e = d.Sync()
	if errors.Is(e, syscall.EINVAL) || errors.Is(e, syscall.ENOTSUP) {
		return nil
	}
	return e
}

func makeDirectories(p string) error {
	if e := noSymlinkParents(filepath.Join(p, "sentinel")); e != nil {
		return e
	}
	if directory(p) {
		return nil
	}
	if !absent(p) {
		return fmt.Errorf("目录被占用: %s", p)
	}
	if e := makeDirectories(filepath.Dir(p)); e != nil {
		return e
	}
	if e := os.Mkdir(p, 0700); e != nil {
		return e
	}
	return syncParent(p)
}

func atomicWrite(p string, b []byte, mode os.FileMode) error {
	return atomicWriteFile(p, b, mode, makeDirectories)
}

// Managed writes may create children, but must never recreate a missing mount
// or installation root. This also closes the gap after a preceding root check.
func makeDirectoriesIn(root, p string) error {
	if !within(root, p) {
		return fmt.Errorf("目录超出管理根")
	}
	if e := noSymlinkParents(filepath.Join(p, "sentinel")); e != nil {
		return e
	}
	if directory(p) {
		return nil
	}
	if p == root {
		return fmt.Errorf("管理根不可用，未重建旧路径: %s", root)
	}
	if !absent(p) {
		return fmt.Errorf("目录被占用: %s", p)
	}
	if e := makeDirectoriesIn(root, filepath.Dir(p)); e != nil {
		return e
	}
	if e := os.Mkdir(p, 0700); e != nil {
		return e
	}
	return syncParent(p)
}
func atomicWriteIn(root, p string, b []byte, mode os.FileMode) error {
	if p == root || !within(root, p) {
		return fmt.Errorf("文件超出管理根")
	}
	return atomicWriteFile(p, b, mode, func(parent string) error { return makeDirectoriesIn(root, parent) })
}
func atomicWriteFile(p string, b []byte, mode os.FileMode, parents func(string) error) error {
	if e := noSymlinkParents(p); e != nil {
		return e
	}
	if e := parents(filepath.Dir(p)); e != nil {
		return e
	}
	if !absent(p) && !regular(p) {
		return fmt.Errorf("写入目标不是普通文件: %s", p)
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".cdet-write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, p); e != nil {
		return e
	}
	return syncParent(p)
}

func replaceLink(p, target string) error {
	if e := noSymlinkParents(p); e != nil {
		return e
	}
	if e := makeDirectories(filepath.Dir(p)); e != nil {
		return e
	}
	if !absent(p) {
		f, e := fingerprint(p)
		if e != nil {
			return e
		}
		if f.Kind != "link" {
			return fmt.Errorf("链接目标被占用: %s", p)
		}
	}
	id, e := instanceID()
	if e != nil {
		return e
	}
	tmp := filepath.Join(filepath.Dir(p), ".cdet-link-"+id)
	defer os.Remove(tmp)
	if e = os.Symlink(target, tmp); e != nil {
		return e
	}
	if e = os.Rename(tmp, p); e != nil {
		return e
	}
	return syncParent(p)
}

func removeSynced(p string) error {
	if e := os.Remove(p); e != nil {
		return e
	}
	return syncParent(p)
}

func lockDir(dir string, create bool) (*os.File, error) {
	if create {
		if e := makeDirectories(dir); e != nil {
			return nil, e
		}
	}
	if e := noSymlinkParents(filepath.Join(dir, "lock")); e != nil {
		return nil, e
	}
	p := filepath.Join(dir, "lock")
	if !absent(p) && !regular(p) {
		return nil, fmt.Errorf("锁不是普通文件")
	}
	flags := os.O_RDWR | syscall.O_NOFOLLOW
	if create {
		flags |= os.O_CREATE
	}
	f, e := os.OpenFile(p, flags, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("另一个 cdet 操作正在执行")
	}
	return f, nil
}
func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
