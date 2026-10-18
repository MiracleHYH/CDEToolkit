// Package proxy manages one explicitly operated, local Mihomo component.
// It has no dependency on Nido's CLI, Codex, or a service manager.
package proxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type Service struct {
	Root   string
	Env    []string
	HTTP   *http.Client
	Runner Runner
	// API and Platform support isolated release and architecture tests.
	API          string
	Platform     string
	Launcher     string
	StartTimeout time.Duration
}

type Status struct {
	Directory     string        `json:"directory"`
	ProgramPath   string        `json:"program_path"`
	Program       string        `json:"program"`
	Config        string        `json:"config"`
	Runtime       RuntimeStatus `json:"runtime"`
	ConfigChanged bool          `json:"config_changed"`
	Problems      []string      `json:"problems,omitempty"`
}

func (s *Service) paths() (dir, program string, err error) {
	if !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) == string(filepath.Separator) {
		return "", "", fmt.Errorf("Nido 软件目录必须是非根目录的绝对路径")
	}
	root := filepath.Clean(s.Root)
	if info, e := os.Lstat(root); e == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", "", fmt.Errorf("Nido 软件目录必须是普通目录")
		}
		if real, e := filepath.EvalSymlinks(root); e == nil {
			root = real
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", "", e
	}
	dir = filepath.Join(root, "proxy")
	if err = safePath(s.Root, dir); err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, "mihomo"), nil
}

func (s *Service) platform() string {
	if s.Platform != "" {
		return s.Platform
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

func (s *Service) runner() Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return OSRunner{}
}

func (s *Service) Install(artifact, checksum string) error {
	return s.withLock(func(directory directorySnapshot) error { return s.install(artifact, checksum, false, directory) })
}

// Update requires an already stopped component. No controller request, restart,
// or cleanup of upstream meta-update/meta-backup directories is performed.
func (s *Service) Update(artifact, checksum string) error {
	return s.withLock(func(directory directorySnapshot) error { return s.install(artifact, checksum, true, directory) })
}

func (s *Service) install(artifact, checksum string, update bool, directory directorySnapshot) error {
	dir, program, err := s.paths()
	if err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = safePath(s.Root, program); err != nil {
		return err
	}
	current, err := capture(program, maxProgram, true)
	if err != nil {
		return err
	}
	if !update && current.info != nil {
		return fmt.Errorf("Proxy 已安装；请使用 update")
	}
	if update && current.info == nil {
		return fmt.Errorf("Proxy 尚未安装")
	}
	if update {
		if err = s.checkExecutable(current); err != nil {
			return err
		}
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err = safePath(s.Root, configPath); err != nil {
		return err
	}
	config, err := capture(configPath, maxConfig, true)
	if err != nil {
		return err
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = current.check(); err != nil {
		return err
	}
	if err = config.check(); err != nil {
		return err
	}
	prepared, err := s.artifact(artifact, checksum)
	if err != nil {
		return err
	}
	data := prepared.data
	// Keep only this operation's unpredictable temporary path in our cleanup
	// scope. Existing core data and upstream updater directories are untouched.
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".nido-core-")
	if err != nil {
		return err
	}
	path := f.Name()
	created, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	defer removeCreated(path, created)
	if err = f.Chmod(0700); err == nil {
		_, err = f.Write(data)
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
	candidate, err := capture(path, maxProgram, false)
	if err != nil {
		return err
	}
	if !os.SameFile(created, candidate.info) || candidate.info.Mode().Perm() != 0700 || hash(candidate.data) != hash(data) {
		return fmt.Errorf("候选核心在准备期间发生变化")
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = candidate.check(); err != nil {
		return err
	}
	version, err := s.version(path)
	if err != nil {
		return err
	}
	if prepared.expectedVersion != "" && "v"+strings.TrimPrefix(version, "v") != prepared.expectedVersion {
		return fmt.Errorf("官方候选核心版本与发布信息不一致")
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = candidate.check(); err != nil {
		return err
	}
	if config.info != nil {
		if err = s.validateConfig(path, config.data); err != nil {
			return err
		}
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = safePath(s.Root, program); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = current.check(); err != nil {
		return err
	}
	if err = config.check(); err != nil {
		return err
	}
	if err = candidate.check(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	return os.Rename(path, program)
}

// Remove only removes the ordinary executable. Configuration, process history,
// log files, caches, and unknown files are retained.
func (s *Service) Remove() error {
	return s.withLock(func(directory directorySnapshot) error {
		_, program, err := s.paths()
		if err != nil {
			return err
		}
		if err = directory.check(); err != nil {
			return err
		}
		if err = safePath(s.Root, program); err != nil {
			return err
		}
		before, err := capture(program, maxProgram, true)
		if err != nil {
			return err
		}
		if err = s.requireStopped(); err != nil {
			return err
		}
		if before.info == nil {
			return nil
		}
		// The explicit fixed program path is the uninstall boundary. Native
		// format does not prove origin, but unknown notes/scripts are user data.
		if err = s.checkExecutable(before); err != nil {
			return err
		}
		if err = s.requireStopped(); err != nil {
			return err
		}
		if err = directory.check(); err != nil {
			return err
		}
		if err = before.check(); err != nil {
			return err
		}
		if err = directory.check(); err != nil {
			return err
		}
		return os.Remove(program)
	})
}

func (s *Service) Configure(file string) error {
	if !filepath.IsAbs(file) {
		return fmt.Errorf("配置来源必须使用绝对路径")
	}
	source, err := capture(file, maxConfig, false)
	if err != nil {
		return fmt.Errorf("无法读取候选配置：%w", err)
	}
	if err = privateConfigInput(source); err != nil {
		return err
	}
	return s.withLock(func(directory directorySnapshot) error { return s.configure(source.data, &source, directory, false) })
}

func privateConfigInput(source fileSnapshot) error {
	stat, ok := source.info.Sys().(*syscall.Stat_t)
	if source.info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("配置输入必须是当前用户拥有、禁止 group/other 访问的普通文件")
	}
	return nil
}

func (s *Service) ConfigureBytes(data []byte) error {
	data = append([]byte(nil), data...)
	return s.withLock(func(directory directorySnapshot) error { return s.configure(data, nil, directory, false) })
}

// ConfigureInitialBytes is for the first interactive quick start. A file that
// appeared while collecting input belongs to its writer and must be preserved.
func (s *Service) ConfigureInitialBytes(data []byte) error {
	data = append([]byte(nil), data...)
	return s.withLock(func(directory directorySnapshot) error { return s.configure(data, nil, directory, true) })
}

func (s *Service) configure(data []byte, source *fileSnapshot, directory directorySnapshot, initial bool) error {
	dir, program, err := s.paths()
	if err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")
	if err = safePath(s.Root, path); err != nil {
		return err
	}
	before, err := capture(path, maxConfig, true)
	if err != nil {
		return err
	}
	if initial && before.info != nil {
		return fmt.Errorf("quick start 期间配置已创建；保留现有配置，请查看 config-path")
	}
	core, err := s.programSnapshot()
	if err != nil {
		return err
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = core.check(); err != nil {
		return err
	}
	if err = before.check(); err != nil {
		return err
	}
	if err = s.validateConfig(program, data); err != nil {
		return err
	}
	if err = s.requireStopped(); err != nil {
		return err
	}
	if err = safePath(s.Root, path); err != nil {
		return err
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = before.check(); err != nil {
		return err
	}
	if err = core.check(); err != nil {
		return err
	}
	if source != nil {
		if err = source.check(); err != nil {
			return err
		}
	}
	return atomicWriteExpected(path, data, 0600, before, directory)
}

// Check is the read-only preflight used by dry runs. It intentionally does not
// run Mihomo -t: that command may access providers and core-managed caches.
func (s *Service) Check() error {
	if _, err := s.programSnapshot(); err != nil {
		return err
	}
	_, err := s.ReadConfig()
	return err
}

// Preflight implements dry-run checks without creating a directory or lock,
// executing a core, fetching releases, or changing process history.
func (s *Service) Preflight(action, artifact, checksum string) error {
	dir, program, err := s.paths()
	if err != nil {
		return err
	}
	if checksum != "" && !validChecksum(checksum) {
		return fmt.Errorf("SHA-256 必须是 64 位十六进制摘要")
	}
	if err = safePath(s.Root, filepath.Join(dir, ".lock")); err != nil {
		return err
	}
	if lock, e := os.Lstat(filepath.Join(dir, ".lock")); e == nil {
		if !lock.Mode().IsRegular() || !singleLink(lock) {
			return fmt.Errorf("Proxy 操作锁必须是无共享硬链接的普通文件")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	switch action {
	case "install", "update":
		if err = s.requireStopped(); err != nil {
			return err
		}
		if err = safePath(s.Root, program); err != nil {
			return err
		}
		before, e := capture(program, maxProgram, true)
		if e != nil {
			return e
		}
		if action == "install" && before.info != nil {
			return fmt.Errorf("Proxy 已安装；请使用 update")
		}
		if action == "update" && before.info == nil {
			return fmt.Errorf("Proxy 尚未安装")
		}
		if action == "update" {
			if e = s.checkExecutable(before); e != nil {
				return e
			}
		}
		if e = safePath(s.Root, filepath.Join(dir, "config.yaml")); e != nil {
			return e
		}
		if _, e = capture(filepath.Join(dir, "config.yaml"), maxConfig, true); e != nil {
			return e
		}
		if _, e = s.ReadConfig(); e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if artifact != "" && artifact != "latest" {
			_, e = s.artifact(artifact, checksum)
			return e
		}
		return nil
	case "configure":
		if err = s.requireStopped(); err != nil {
			return err
		}
		if _, err = s.programSnapshot(); err != nil {
			return err
		}
		if err = safePath(s.Root, filepath.Join(dir, "config.yaml")); err != nil {
			return err
		}
		if _, err = capture(filepath.Join(dir, "config.yaml"), maxConfig, true); err != nil {
			return err
		}
		if artifact == "" {
			return nil
		}
		if !filepath.IsAbs(artifact) {
			return fmt.Errorf("配置来源必须使用绝对路径")
		}
		source, e := capture(artifact, maxConfig, false)
		if e != nil {
			return fmt.Errorf("无法读取候选配置：%w", e)
		}
		if e = privateConfigInput(source); e != nil {
			return e
		}
		_, e = ParseConfig(source.data, dir)
		return e
	case "validate":
		return s.Check()
	case "start":
		return s.preflightStart()
	case "restart":
		if err = s.Check(); err != nil {
			return err
		}
		return s.preflightStop()
	case "remove":
		if err = s.requireStopped(); err != nil {
			return err
		}
		if err = safePath(s.Root, program); err != nil {
			return err
		}
		before, e := capture(program, maxProgram, true)
		if e != nil || before.info == nil {
			return e
		}
		return s.checkExecutable(before)
	case "stop":
		return s.preflightStop()
	default:
		return fmt.Errorf("未知 Proxy 预检操作")
	}
}

// Recovery authorization follows kernel identity. A changed/deleted on-disk
// image prevents readiness, but does not prevent stopping that exact birth.
func (s *Service) preflightStop() error {
	r, before, err := s.readRecordSnapshot()
	if errors.Is(err, os.ErrNotExist) {
		return before.check()
	}
	if err != nil {
		return err
	}
	_, err = s.recordActual(r)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return before.check()
}

// Start may be idempotent only for the same ready configuration and program.
// This is the local authorization portion of Start; it never executes a core.
func (s *Service) preflightStart() error {
	core, err := s.programSnapshot()
	if err != nil {
		return err
	}
	config, err := s.ReadConfig()
	if err != nil {
		return err
	}
	r, before, err := s.readRecordSnapshot()
	if errors.Is(err, os.ErrNotExist) {
		return before.check()
	}
	if err != nil {
		return err
	}
	p, err := s.recordActual(r)
	if errors.Is(err, os.ErrNotExist) {
		return before.check()
	}
	if err != nil {
		return err
	}
	if hash(core.data) != r.ProgramHash {
		return fmt.Errorf("Proxy 存活实例的磁盘程序与启动记录不一致；请先停止")
	}
	if config.Digest != r.ConfigHash {
		return fmt.Errorf("Proxy 配置已更改，请使用 restart 应用")
	}
	ready, err := s.recordReady(r, p)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("Proxy 已有存活实例但尚未就绪，保留记录；请检查状态或先停止")
	}
	if err = core.check(); err != nil {
		return err
	}
	return before.check()
}

func (s *Service) Validate() error {
	return s.withLock(func(directory directorySnapshot) error {
		_, program, err := s.paths()
		if err != nil {
			return err
		}
		if err = directory.check(); err != nil {
			return err
		}
		core, err := s.programSnapshot()
		if err != nil {
			return err
		}
		config, err := s.ReadConfig()
		if err != nil {
			return err
		}
		path := filepath.Join(filepath.Dir(program), "config.yaml")
		before, err := capture(path, maxConfig, false)
		if err != nil {
			return err
		}
		if hash(before.data) != config.Digest {
			return fmt.Errorf("配置在验证前发生变化")
		}
		if err = s.validateConfig(program, config.Data); err != nil {
			return err
		}
		if err = core.check(); err != nil {
			return err
		}
		if err = directory.check(); err != nil {
			return err
		}
		return before.check()
	})
}

func (s *Service) programSnapshot() (fileSnapshot, error) {
	_, path, err := s.paths()
	if err != nil {
		return fileSnapshot{}, err
	}
	if err = safePath(s.Root, path); err != nil {
		return fileSnapshot{}, err
	}
	before, err := capture(path, maxProgram, false)
	if err != nil {
		return before, fmt.Errorf("Proxy 核心尚未安装或不可安全读取：%w", err)
	}
	if err = s.checkExecutable(before); err != nil {
		return before, err
	}
	return before, nil
}

func (s *Service) checkExecutable(before fileSnapshot) error {
	if before.info == nil || before.info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("Proxy 程序路径不是可执行核心；保留现有文件")
	}
	if err := nativeProgram(before.data, s.platform()); err != nil {
		return fmt.Errorf("Proxy 程序路径不是受支持的独立核心；保留现有文件")
	}
	return nil
}

// Status only observes local files and the recorded process. A readable config
// has passed static checks; status makes no claim about core/network validation.
func (s *Service) Status() Status {
	result := Status{Program: "missing", Config: "missing", Runtime: RuntimeStatus{State: "stopped"}}
	dir, program, err := s.paths()
	result.Directory, result.ProgramPath = dir, program
	if err != nil {
		result.Program = "invalid"
		result.Problems = append(result.Problems, err.Error())
		return result
	}
	if _, err = os.Lstat(program); err == nil {
		result.Program = "present"
		if _, err = s.programSnapshot(); err != nil {
			result.Program = "invalid"
			result.Problems = append(result.Problems, err.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		result.Program = "invalid"
		result.Problems = append(result.Problems, "无法检查 Proxy 程序")
	}
	if _, err = os.Lstat(filepath.Join(dir, "config.yaml")); err == nil {
		result.Config = "readable"
		if _, err = s.ReadConfig(); err != nil {
			result.Config = "invalid"
			result.Problems = append(result.Problems, err.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		result.Config = "invalid"
		result.Problems = append(result.Problems, "无法检查 Proxy 配置")
	}
	result.Runtime, err = s.Runtime()
	result.ConfigChanged = result.Runtime.ConfigChanged
	if err != nil {
		result.Problems = append(result.Problems, err.Error())
	}
	return result
}

// Logs reads a bounded suffix, and accepts an actively growing ordinary file.
// It never opens FIFO/device paths in blocking mode or writes to the log.
func (s *Service) Logs(tail int) (string, error) {
	if tail < 1 || tail > 10000 {
		return "", fmt.Errorf("日志行数必须为 1 到 10000")
	}
	dir, _, err := s.paths()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "output.log")
	if err = safePath(s.Root, path); err != nil {
		return "", err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || !singleLink(before) {
		return "", fmt.Errorf("Proxy 日志必须是无共享硬链接的普通文件")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !singleLink(opened) || !os.SameFile(before, opened) {
		return "", fmt.Errorf("Proxy 日志在读取前发生变化")
	}
	const maxLog = 4 << 20
	start := opened.Size() - maxLog
	if start < 0 {
		start = 0
	}
	data, err := io.ReadAll(io.NewSectionReader(f, start, opened.Size()-start))
	if err != nil {
		return "", err
	}
	if start > 0 {
		if i := strings.IndexByte(string(data), '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n"), nil
}
