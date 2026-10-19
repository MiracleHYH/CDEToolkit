// Package proxy manages one explicitly operated, local Mihomo component.
// It has no dependency on Nido's CLI, Codex, or a service manager.
package proxy

import (
	"bytes"
	"context"
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
	Root string
	Env  []string
	HTTP *http.Client
	// APIHTTP selects a separate explicit network route for official metadata.
	// It does not change release file routing or enable accelerator fallback.
	APIHTTP *http.Client
	Runner  Runner
	// GitHubProxy routes public release downloads only. Empty preserves direct
	// operation for independently constructed components.
	GitHubProxy string
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
	return s.InstallContext(context.Background(), artifact, checksum)
}

func (s *Service) InstallContext(ctx context.Context, artifact, checksum string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.withLock(func(directory directorySnapshot) error { return s.installContext(ctx, artifact, checksum, directory) })
}

// Update uses the running core's native release updater. Artifact selection is
// an installation-only operation; Nido never downloads a second update path.
func (s *Service) Update(artifact, checksum string) error {
	if artifact != "" && artifact != "latest" || checksum != "" {
		return fmt.Errorf("Proxy 更新仅支持运行核心的原生 release 更新；本地 artifact/SHA-256 仅用于首次 install")
	}
	return s.Upgrade()
}

func (s *Service) install(artifact, checksum string, directory directorySnapshot) error {
	return s.installContext(context.Background(), artifact, checksum, directory)
}

func (s *Service) installContext(ctx context.Context, artifact, checksum string, directory directorySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if current.info != nil {
		return fmt.Errorf("Proxy 已安装；请使用 update")
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
	prepared, err := s.artifactContext(ctx, artifact, checksum)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
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
	if err = ctx.Err(); err != nil {
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
	version, err := s.versionContext(ctx, path)
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
		if err = s.validateConfigContext(ctx, candidate, config.data, directory); err != nil {
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
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(path, program)
}

// Remove stops only the recorded birth, confirms its exit, then removes the
// ordinary executable under the same component lock. Configuration, process
// history, log files, caches, and unknown files are retained.
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
		// The explicit fixed program path is the uninstall boundary. Native
		// format does not prove origin, but unknown notes/scripts are user data.
		if before.info != nil {
			if err = s.checkExecutable(before); err != nil {
				return err
			}
		}
		r, record, recordErr := s.readRecordSnapshot()
		if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
			return recordErr
		}
		if err = directory.check(); err != nil {
			return err
		}
		if err = before.check(); err != nil {
			return err
		}
		if recordErr == nil {
			// Keep this operation's committed snapshot through deletion. Reading
			// process.json again here could adopt an unrelated new instance.
			record, err = s.stopRecordSnapshot(r, record, directory)
			if err != nil {
				return err
			}
		}
		if err = before.check(); err != nil {
			return err
		}
		if err = record.check(); err != nil {
			return fmt.Errorf("Proxy 进程记录在卸载期间发生变化；保留程序: %w", err)
		}
		if err = directory.check(); err != nil {
			return err
		}
		if before.info == nil {
			return nil
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
	return s.withLock(func(directory directorySnapshot) error {
		return s.configure(source.data, &source, directory, false, nil)
	})
}

func privateConfigInput(source fileSnapshot) error {
	stat, ok := source.info.Sys().(*syscall.Stat_t)
	if source.info.Mode().Perm()&0077 != 0 || source.info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("配置输入必须是当前用户拥有、禁止 group/other 访问且无特殊权限位的普通文件")
	}
	return nil
}

func privateConfigBackup(backup fileSnapshot) error {
	if backup.info == nil {
		return nil
	}
	if err := privateConfigInput(backup); err != nil {
		return fmt.Errorf("配置备份不可安全读取：%w", err)
	}
	return nil
}

func (s *Service) ConfigureBytes(data []byte) error {
	data = append([]byte(nil), data...)
	return s.withLock(func(directory directorySnapshot) error { return s.configure(data, nil, directory, false, nil) })
}

// ConfigureInitialBytes is for the first interactive quick start. A file that
// appeared while collecting input belongs to its writer and must be preserved.
func (s *Service) ConfigureInitialBytes(data []byte) error {
	data = append([]byte(nil), data...)
	return s.withLock(func(directory directorySnapshot) error { return s.configure(data, nil, directory, true, nil) })
}

// ConfigRevision is an opaque snapshot captured before interactive input.
// It authorizes replacing that exact file (or creating the still absent file),
// never a different writer's configuration appearing during the conversation.
type ConfigRevision struct {
	config    fileSnapshot
	backup    fileSnapshot
	directory directorySnapshot
}

func (r ConfigRevision) Exists() bool { return r.config.info != nil }

func (s *Service) CaptureConfigRevision() (ConfigRevision, error) {
	dir, _, err := s.paths()
	if err != nil {
		return ConfigRevision{}, err
	}
	path := filepath.Join(dir, "config.yaml")
	if err = safePath(s.Root, path); err != nil {
		return ConfigRevision{}, err
	}
	directory, err := captureDirectory(dir)
	if err != nil {
		return ConfigRevision{}, err
	}
	config, err := capture(path, maxConfig, true)
	backupPath := filepath.Join(dir, "config.yaml.back")
	if err == nil {
		err = safePath(s.Root, backupPath)
	}
	var backup fileSnapshot
	if err == nil {
		backup, err = capture(backupPath, maxConfig, true)
	}
	if err == nil {
		err = privateConfigBackup(backup)
	}
	if err == nil {
		err = directory.check()
	}
	return ConfigRevision{config: config, backup: backup, directory: directory}, err
}

func (s *Service) ConfigureIfUnchanged(file string, revision ConfigRevision) error {
	source, err := s.CaptureConfigSource(file)
	if err != nil {
		return err
	}
	return s.ConfigureSourceIfUnchanged(source, revision)
}

// ConfigSource retains the exact private YAML file selected before confirmation.
type ConfigSource struct{ file fileSnapshot }

func (s *Service) CaptureConfigSource(file string) (ConfigSource, error) {
	if !filepath.IsAbs(file) {
		return ConfigSource{}, fmt.Errorf("配置来源必须使用绝对路径")
	}
	source, err := capture(file, maxConfig, false)
	if err != nil {
		return ConfigSource{}, fmt.Errorf("无法读取候选配置：%w", err)
	}
	if err = privateConfigInput(source); err != nil {
		return ConfigSource{}, err
	}
	dir, _, err := s.paths()
	if err != nil {
		return ConfigSource{}, err
	}
	if _, err = ParseConfig(source.data, dir); err != nil {
		return ConfigSource{}, err
	}
	return ConfigSource{file: source}, nil
}

func (s *Service) ConfigureSourceIfUnchanged(source ConfigSource, revision ConfigRevision) error {
	if source.file.info == nil {
		return fmt.Errorf("配置输入快照无效")
	}
	return s.withLock(func(directory directorySnapshot) error {
		if err := source.file.check(); err != nil {
			return err
		}
		return s.configure(source.file.data, &source.file, directory, false, &revision)
	})
}

func (s *Service) ConfigureBytesIfUnchanged(data []byte, revision ConfigRevision) error {
	data = append([]byte(nil), data...)
	return s.withLock(func(directory directorySnapshot) error {
		return s.configure(data, nil, directory, false, &revision)
	})
}

func (s *Service) configure(data []byte, source *fileSnapshot, directory directorySnapshot, initial bool, revision *ConfigRevision) error {
	dir, _, err := s.paths()
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
	if revision != nil {
		if revision.config.path != path {
			return fmt.Errorf("配置快照不属于当前 Proxy 目录")
		}
		if err = revision.directory.check(); err != nil {
			return err
		}
		if err = revision.config.check(); err != nil {
			return fmt.Errorf("输入期间配置已变化；保留现有配置：%w", err)
		}
		if revision.backup.path != filepath.Join(dir, "config.yaml.back") {
			return fmt.Errorf("配置备份快照不属于当前 Proxy 目录")
		}
		if err = revision.backup.check(); err != nil {
			return fmt.Errorf("输入期间配置备份已变化；保留现有文件：%w", err)
		}
	}
	var before fileSnapshot
	if revision != nil {
		before = revision.config
	} else {
		before, err = capture(path, maxConfig, true)
		if err != nil {
			return err
		}
	}
	if initial && before.info != nil {
		return fmt.Errorf("quick start 期间配置已创建；保留现有配置，请查看 config-path")
	}
	backupPath := filepath.Join(dir, "config.yaml.back")
	if err = safePath(s.Root, backupPath); err != nil {
		return err
	}
	var backup fileSnapshot
	if revision != nil {
		backup = revision.backup
	} else {
		backup, err = capture(backupPath, maxConfig, true)
		if err != nil {
			return err
		}
	}
	if err = privateConfigBackup(backup); err != nil {
		return err
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
	if err = s.validateConfig(core, data, directory); err != nil {
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
	if bytes.Equal(before.data, data) && before.info != nil {
		return nil
	}
	if before.info != nil {
		// The backup is committed first, as exact original bytes. Each rename is
		// atomic, but the pair is not a transaction across crashes/power loss.
		if err = backup.check(); err != nil {
			return err
		}
		savedBackup, saveErr := atomicWriteSnapshotExpected(backupPath, before.data, 0600, backup, directory)
		if saveErr != nil {
			return fmt.Errorf("无法保存原配置备份；主配置未替换：%w", saveErr)
		}
		if err = s.requireStopped(); err != nil {
			return err
		}
		if err = core.check(); err != nil {
			return err
		}
		if err = savedBackup.check(); err != nil {
			return err
		}
		// Restoring config.yaml.back is a valid import: our own preceding
		// backup write intentionally replaced that source inode. Its captured
		// bytes were already checked immediately before saving the backup.
		if source != nil && (backup.info == nil || !os.SameFile(source.info, backup.info)) {
			if err = source.check(); err != nil {
				return err
			}
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
	case "reload":
		if artifact != "" || checksum != "" {
			return fmt.Errorf("reload 仅使用固定 config.yaml，不接受程序包参数")
		}
		controller, e := s.controllerSession(true, true)
		if e != nil {
			return e
		}
		controller.close()
		return nil
	case "update":
		if artifact != "" && artifact != "latest" || checksum != "" {
			return fmt.Errorf("原生 Proxy 更新不接受 artifact 或 SHA-256 参数")
		}
		if s.platform() != "linux/amd64" && s.platform() != "linux/arm64" {
			return fmt.Errorf("原生 Proxy 更新的运行映像确认仅支持 Linux amd64/arm64")
		}
		if _, ok := s.runner().(runningImageObserver); !ok {
			return fmt.Errorf("当前运行器不支持 Linux 运行映像确认")
		}
		controller, e := s.controllerSession(false, true)
		if e != nil {
			return e
		}
		controller.close()
		return s.nativeUpdatePaths()
	case "install":
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
		if before.info != nil {
			return fmt.Errorf("Proxy 已安装；请使用 update")
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
		if _, err = s.programSnapshot(); err != nil {
			return err
		}
		if err = safePath(s.Root, filepath.Join(dir, "config.yaml")); err != nil {
			return err
		}
		if _, err = capture(filepath.Join(dir, "config.yaml"), maxConfig, true); err != nil {
			return err
		}
		if err = safePath(s.Root, filepath.Join(dir, "config.yaml.back")); err != nil {
			return err
		}
		backup, e := capture(filepath.Join(dir, "config.yaml.back"), maxConfig, true)
		if e != nil {
			return e
		}
		if err = privateConfigBackup(backup); err != nil {
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
		if err = safePath(s.Root, program); err != nil {
			return err
		}
		before, e := capture(program, maxProgram, true)
		if e != nil {
			return e
		}
		if before.info != nil {
			if err = s.checkExecutable(before); err != nil {
				return err
			}
		}
		if err = s.preflightStop(); err != nil {
			return err
		}
		return before.check()
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
		// A live or unverifiable recorded birth may share provider/cache data.
		// Do not run a parallel -t; this result is explicitly static-only.
		stopped := s.requireStopped() == nil
		if err = core.check(); err != nil {
			return err
		}
		if err = before.check(); err != nil {
			return err
		}
		if stopped {
			if err = s.validateConfig(core, config.Data, directory); err != nil {
				return err
			}
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
