// Package builtin manages the small programs shipped alongside Nido. It does
// not import external installations or interpret another application's data.
package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

type Manager struct {
	Root   string
	Env    []string
	HTTP   *http.Client
	Runner Runner
	// API and Platform are per-instance seams for offline release tests.
	API      string
	Platform string
}

type Status struct {
	Name       string `json:"name"`
	Directory  string `json:"directory"`
	Program    string `json:"program"`
	Installed  bool   `json:"installed"`
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	Problem    string `json:"problem,omitempty"`
}

func (m *Manager) paths(name string) (dir, program string, err error) {
	component, ok := definition(name)
	if !ok {
		return "", "", fmt.Errorf("未知内置组件 %q", name)
	}
	if !filepath.IsAbs(m.Root) || filepath.Clean(m.Root) == string(filepath.Separator) {
		return "", "", fmt.Errorf("Nido 软件目录必须是非根目录的绝对路径")
	}
	root := filepath.Clean(m.Root)
	if info, e := os.Lstat(root); e == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("Nido 软件目录不能是符号链接")
		}
		if real, e := filepath.EvalSymlinks(root); e == nil {
			root = real
		}
	}
	dir = filepath.Join(root, name)
	return dir, filepath.Join(dir, component.Program), nil
}

func (m *Manager) platform() string {
	if m.Platform != "" {
		return m.Platform
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

func (m *Manager) runner() Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return osRunner{}
}

// Install copies one executable into its fixed component directory. Existing
// programs require Update, and configuration or data are never overwritten.
func (m *Manager) Install(name, artifact, checksum string) error {
	return m.withLock(name, func() error { return m.install(name, artifact, checksum, false) })
}

func (m *Manager) install(name, artifact, checksum string, update bool) error {
	dir, program, err := m.paths(name)
	if err != nil {
		return err
	}
	if err = safePath(m.Root, filepath.Join(dir, "sentinel")); err != nil {
		return err
	}
	info, statErr := os.Lstat(program)
	if statErr == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("受管程序位置被非普通文件占用")
	}
	if !update && statErr == nil {
		return fmt.Errorf("%s 已安装；请使用 update", name)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if update && errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("%s 尚未安装", name)
	}
	if err = m.requireStopped(name); err != nil {
		return err
	}
	data, err := m.artifact(name, artifact, checksum)
	if err != nil {
		return err
	}
	// Recheck after a potentially slow download; installation never stops or
	// starts components as a side effect.
	if err = m.requireStopped(name); err != nil {
		return err
	}
	if err = safePath(m.Root, program); err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return atomicWrite(program, data, 0700)
}

// Update replaces a stopped optional component without changing its data.
func (m *Manager) Update(name, artifact, checksum string) error {
	return m.withLock(name, func() error { return m.update(name, artifact, checksum) })
}

func (m *Manager) update(name, artifact, checksum string) error {
	if _, _, err := m.paths(name); err != nil {
		return err
	}
	return m.install(name, artifact, checksum, true)
}

// Remove deletes only the component executable and its inactive process
// record. All configuration, logs, data and unknown files remain in place.
func (m *Manager) Remove(name string) error {
	return m.withLock(name, func() error { return m.remove(name) })
}

func (m *Manager) remove(name string) error {
	dir, program, err := m.paths(name)
	if err != nil {
		return err
	}
	if err = safePath(m.Root, program); err != nil {
		return err
	}
	if err = m.requireStopped(name); err != nil {
		return err
	}
	if info, e := os.Lstat(program); e == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("受管程序不是普通文件")
		}
		if err = os.Remove(program); err != nil {
			return err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return removeRecord(filepath.Join(dir, "process.json"))
}

// Status is a local, read-only observation. It never downloads, invokes the
// program, cleans stale records, or claims that network/authentication works.
func (m *Manager) Status(name string) Status {
	s := Status{Name: name}
	dir, program, err := m.paths(name)
	s.Directory, s.Program = dir, program
	if err != nil {
		s.Problem = err.Error()
		return s
	}
	if err = safePath(m.Root, program); err != nil {
		s.Problem = err.Error()
		return s
	}
	if info, e := os.Lstat(program); e == nil {
		s.Installed = info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
		if !s.Installed {
			s.Problem = "受管程序不是普通可执行文件"
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		s.Problem = e.Error()
	}
	component, _ := definition(name)
	s.Configured = !component.NeedsConfig && s.Installed
	if name == "gds" && s.Installed {
		s.Configured = false
		s.Problem = gdsUnavailable
	}

	p, e := m.process(name)
	if e != nil {
		s.Problem = e.Error()
	} else {
		s.Running = p.PID != 0
	}
	return s
}

// Start remains unavailable until GDS has its own scheduler adapter.
func (m *Manager) Start(name string) error {
	if _, _, err := m.paths(name); err != nil {
		return err
	}
	return errors.New(gdsUnavailable)
}

const gdsUnavailable = "GDS 调度运行尚未适配；仓库 scheduler 仅为 CUDA worker，不能直接启动"

func (m *Manager) Stop(name string) error {
	return m.withLock(name, func() error { return m.stop(name) })
}

func (m *Manager) stop(name string) error {
	dir, _, err := m.paths(name)
	if err != nil {
		return err
	}
	p, err := m.process(name)
	if err != nil {
		return err
	}
	if p.PID != 0 {
		if err = m.runner().Stop(p); err != nil {
			return err
		}
	}
	return removeRecord(filepath.Join(dir, "process.json"))
}

func (m *Manager) requireStopped(name string) error {
	p, err := m.process(name)
	if err != nil {
		return err
	}
	if p.PID != 0 {
		return fmt.Errorf("%s 正在运行；请先停止组件", name)
	}
	return nil
}

func (m *Manager) process(name string) (Process, error) {
	dir, program, err := m.paths(name)
	if err != nil {
		return Process{}, err
	}
	path := filepath.Join(dir, "process.json")
	if err = safePath(m.Root, path); err != nil {
		return Process{}, err
	}
	b, err := readRegular(path, 4096)
	if errors.Is(err, os.ErrNotExist) {
		return Process{}, nil
	}
	if err != nil {
		return Process{}, fmt.Errorf("无法读取 %s 进程记录", name)
	}
	var p Process
	if json.Unmarshal(b, &p) != nil || p.PID <= 1 || p.Executable != program || p.Started == "" {
		return Process{}, fmt.Errorf("%s 进程记录无效；未执行进程操作", name)
	}
	actual, err := m.runner().Inspect(p.PID)
	if errors.Is(err, os.ErrNotExist) {
		return Process{}, nil
	}
	if err != nil {
		return Process{}, fmt.Errorf("无法核验 %s 进程归属", name)
	}
	if actual != p {
		// The recorded process has exited and its PID has been reused. This
		// authorizes no signals, but must not permanently block this component.
		return Process{}, nil
	}
	return p, nil
}

func removeRecord(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
