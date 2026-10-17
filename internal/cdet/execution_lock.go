//go:build linux || darwin

package cdet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
)

func (m *Manager) componentLock(name string, mode int) (*os.File, error) {
	if !knownModule(name) {
		return nil, fmt.Errorf("未知组件: %s", name)
	}
	if e := absolute(m.Store); e != nil {
		return nil, e
	}
	state, _, e := readState(m.Store)
	if e != nil || state.ID != m.State.ID || state.InstallID != m.State.InstallID {
		return nil, fmt.Errorf("组件存储不可用或身份变化，未重建旧挂载目录")
	}
	dir := filepath.Join(m.metadataDir(), "locks")
	if e := makeDirectories(dir); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, name)
	if e := noSymlinkParents(path); e != nil {
		return nil, e
	}
	if !absent(path) && !regular(path) {
		return nil, fmt.Errorf("组件锁不是普通文件: %s", name)
	}
	f, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, fmt.Errorf("无法打开组件运行锁: %s", name)
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("组件运行锁类型无效: %s", name)
	}
	if e = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s 正在运行或进行管理操作；请先退出组件及 daemon 后重试", name)
	}
	return f, nil
}

// Writers hold these exclusive locks for the entire operation. Lock order is
// deterministic and partial acquisition is always unwound without waiting.
func (m *Manager) lockComponents(names []string) (func(), error) {
	unique := map[string]bool{}
	var ordered []string
	for _, name := range names {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知组件: %s", name)
		}
		if !unique[name] {
			unique[name] = true
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	var held []*os.File
	var once sync.Once
	release := func() {
		once.Do(func() {
			for i := len(held) - 1; i >= 0; i-- {
				_ = syscall.Flock(int(held[i].Fd()), syscall.LOCK_UN)
				_ = held[i].Close()
			}
		})
	}
	for _, name := range ordered {
		f, e := m.componentLock(name, syscall.LOCK_EX)
		if e != nil {
			release()
			return nil, e
		}
		held = append(held, f)
	}
	return release, nil
}

// A managed CLI invocation holds a shared lease across syscall.Exec. Its
// lifetime, including an inheriting daemon, prevents concurrent replacement or
// relocation; closing the returned file releases this caller's reference.
func (m *Manager) ExecutionLease(name string) (*os.File, error) {
	if e := m.requireReady(); e != nil {
		return nil, e
	}
	if !knownModule(name) || !m.State.Components[name].Installed {
		return nil, fmt.Errorf("组件未安装或名称无效: %s", name)
	}
	f, e := m.componentLock(name, syscall.LOCK_SH)
	if e != nil {
		return nil, e
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFD, 0)
	if errno == 0 {
		_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_SETFD, flags&^syscall.FD_CLOEXEC)
	}
	if errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("无法将 %s 运行锁传给组件进程", name)
	}
	return f, nil
}
