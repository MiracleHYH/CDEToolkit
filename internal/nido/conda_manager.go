package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
)

// CondaReport separates saved intent from activation in the calling shell.
// Ordinary status never executes conda, pip, or Python.
type CondaReport struct {
	Configured  bool              `json:"configured"`
	State       string            `json:"state"`
	Config      conda.Config      `json:"config"`
	Environment map[string]string `json:"environment"`
	MirrorFile  string            `json:"mirror_file,omitempty"`
	Issues      []string          `json:"issues"`
}

func (m *Manager) CondaStatus() CondaReport {
	cfg := m.Config.Conda
	vars := m.Conda.Variables(cfg)
	path, _ := m.Conda.MirrorFile(cfg)
	issues := m.Conda.Issues(cfg)
	if issues == nil {
		issues = []string{}
	}
	if path != "" {
		if err := m.checkCondaMirror(cfg, true); err != nil {
			issues = append(issues, err.Error())
		}
	}
	state := "未配置"
	if cfg.Defaults.CondaCaptured || cfg.Defaults.PipCaptured {
		state = "已记录 default"
	}
	configured := len(vars) != 0 || cfg.EnvsDir != "" || cfg.PkgsDir != "" || cfg.PipCacheDir != ""
	if configured {
		state = "已配置"
	}
	if len(issues) != 0 {
		state = "待检查"
	}
	return CondaReport{Configured: configured, State: state, Config: cfg, Environment: publicCondaVariables(cfg, vars), MirrorFile: path, Issues: issues}
}

// CheckConda performs the same read-only checks as a mutation. Directories for
// environments and caches belong to the upstream tools and are never created.
func (m *Manager) checkCondaState() error {
	if m.configBytes == nil {
		return fmt.Errorf("尚未初始化，请先运行 nido init")
	}
	if err := m.checkRoot(); err != nil {
		return err
	}
	if err := unchanged(filepath.Join(m.Root, "config.toml"), m.configBytes); err != nil {
		return err
	}
	if err := m.checkEnv(); err != nil {
		return err
	}
	return nil
}

func (m *Manager) CheckConda(next conda.Config, scope ...conda.NativeScope) error {
	if err := m.checkCondaState(); err != nil {
		return err
	}
	if err := m.Conda.ValidateConfig(next); err != nil {
		return err
	}
	if len(scope) == 0 || len(scope) == 1 && (scope[0].CondaMirror || scope[0].EnvsDir || scope[0].PkgsDir) {
		if err := m.Conda.CheckSource(next); err != nil {
			return err
		}
	}
	for i, path := range []string{next.EnvsDir, next.PkgsDir, next.PipCacheDir} {
		if len(scope) == 1 && ![]bool{scope[0].EnvsDir, scope[0].PkgsDir, scope[0].PipCacheDir}[i] {
			continue
		}
		if path == "" {
			continue
		}
		normalized, err := m.Conda.NormalizePath(path)
		if err != nil {
			return err
		}
		if normalized != path {
			return fmt.Errorf("外部目录的物理位置已变化，请重新选择该目录或撤销管理: %s", path)
		}
	}
	if err := m.checkCondaMirror(next, false); err != nil {
		return err
	}
	return m.Conda.CheckPaths(m.Config.Conda, next, scope...)
}

func (m *Manager) checkCondaMirror(cfg conda.Config, required bool) error {
	path, content := m.Conda.MirrorFile(cfg)
	if path == "" {
		return nil
	}
	if err := safeParents(filepath.Dir(path)); err != nil {
		return err
	}
	actual, err := readFile(path, 1<<20)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil || !bytes.Equal(actual, content) {
		return fmt.Errorf("Conda 镜像文件缺失、已修改或不是普通文件；未覆盖，请检查 %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("Conda 镜像文件必须保持私有权限；未覆盖，请检查 %s", path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return fmt.Errorf("Conda 镜像文件所有者异常或存在硬链接；未覆盖，请检查 %s", path)
	}
	return nil
}

// Mirror resources are immutable and published without replacing an existing
// file. Native channels are updated separately after the first default is saved.
func (m *Manager) publishCondaMirror(cfg conda.Config) error {
	path, content := m.Conda.MirrorFile(cfg)
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return m.checkCondaMirror(cfg, true)
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	if err := safeParents(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	parent, err := os.Lstat(dir)
	if err != nil || !parent.IsDir() {
		return fmt.Errorf("Conda 配置目录无效")
	}
	f, err := os.CreateTemp(dir, ".nido-conda-*")
	if err != nil {
		return err
	}
	created, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	defer func() {
		currentParent, parentErr := os.Lstat(dir)
		current, fileErr := os.Lstat(f.Name())
		if parentErr == nil && fileErr == nil && safeParents(dir) == nil && os.SameFile(parent, currentParent) && os.SameFile(created, current) {
			_ = os.Remove(f.Name())
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(content)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = m.checkRoot(); err != nil {
		return err
	}
	if err = safeParents(dir); err != nil {
		return err
	}
	currentParent, parentErr := os.Lstat(dir)
	current, fileErr := os.Lstat(f.Name())
	if parentErr != nil || fileErr != nil || !os.SameFile(parent, currentParent) || !os.SameFile(created, current) || !current.Mode().IsRegular() || current.Mode().Perm() != 0600 {
		return fmt.Errorf("Conda 配置目录或临时文件已变化，未保存")
	}
	if err := unchanged(f.Name(), content); err != nil {
		return err
	}
	// Link is an atomic no-replace publication on supported Unix platforms.
	if err = os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("Conda 镜像文件同时创建或无法写入，未覆盖: %w", err)
	}
	return nil
}

func (m *Manager) ConfigureConda(next conda.Config, scope ...conda.NativeScope) error {
	return m.mutate(false, func() error {
		var err error
		next, err = m.prepareCondaSource(next, scope...)
		if err != nil {
			return err
		}
		// A caller cannot replace or forget the first recorded defaults.
		prior := m.Config.Conda.Defaults
		nativePrevious := m.Config.Conda
		if prior.CondaCaptured {
			next.Defaults.CondaCaptured = true
			next.Defaults.CondaYAML = prior.CondaYAML
			next.Defaults.CondaVariables = prior.CondaVariables
		}
		if prior.PipCaptured {
			next.Defaults.PipCaptured = true
			next.Defaults.PipIndexURL = prior.PipIndexURL
			next.Defaults.PipVariables = prior.PipVariables
		}
		var captureGuard func() error
		for _, tool := range []string{"conda", "pip"} {
			if tool == "conda" && next.CondaMirror == "" || tool == "pip" && conda.EffectivePipIndex(next) == "" && next.PipMirror != "default" {
				continue
			}
			captured, guard, err := m.Conda.CaptureAtSourceWithGuard(next, next.Defaults, tool)
			if err != nil {
				return err
			}
			next.Defaults = captured
			if guard != nil {
				captureGuard = guard
			}
		}
		var presetGuard func() error
		next, presetGuard, err = m.prepareCondaPresetChecked(next, scope...)
		if err != nil {
			return err
		}
		if presetGuard != nil {
			captureGuard = presetGuard
		}
		if err := m.CheckConda(next, scope...); err != nil {
			return err
		}
		// Persist the original mirror before the first native channel write.
		// An interruption may leave an unapplied selection, but cannot erase
		// the only copy of the machine's original mirror information.
		if !reflect.DeepEqual(prior, next.Defaults) {
			var source []string
			if nativePrevious.CondaMirror == "" {
				source = []string{next.SourceCONDARC}
			}
			if err := m.recordCondaDefaults(next.Defaults, source...); err != nil {
				return err
			}
		}
		if err := m.publishCondaMirror(next); err != nil {
			return err
		}
		undo, err := m.Conda.ChangePathsChecked(nativePrevious, next, captureGuard, scope...)
		if err != nil {
			return err
		}
		previous := m.Config
		before := append([]byte(nil), m.configBytes...)
		m.Config.Conda = next
		saveErr := m.checkCondaMirror(next, true)
		if saveErr == nil {
			saveErr = m.save()
		}
		if err := saveErr; err != nil {
			if bytes.Equal(before, m.configBytes) {
				m.Config = previous
				if undo != nil {
					if undoErr := undo(); undoErr != nil {
						return fmt.Errorf("%w；本次路径配置无法安全恢复: %v", err, undoErr)
					}
				}
			}
			return err
		}
		return nil
	})
}

// Only an explicit Conda mirror selection may retarget an active source.
// Pip/path/off/reset operations retain the association instead of silently
// changing other settings when the caller temporarily overrides CONDARC.
func (m *Manager) prepareCondaSource(next conda.Config, scope ...conda.NativeScope) (conda.Config, error) {
	selecting := next.CondaMirror != "" && (next.CondaMirror != m.Config.Conda.CondaMirror || next.SourceCONDARC != m.Config.Conda.SourceCONDARC)
	if len(scope) == 1 {
		selecting = next.CondaMirror != "" && scope[0].CondaMirror
	}
	if !selecting {
		next.SourceCONDARC = m.Config.Conda.SourceCONDARC
		return next, nil
	}
	source, err := m.Conda.ResolveSource(m.Config.Conda, next)
	if err != nil {
		return next, err
	}
	next.SourceCONDARC = source
	return next, nil
}

// Public presets change repository locations while retaining the current
// channel selection. Only default deliberately restores the original selection.
func (m *Manager) prepareCondaPreset(next conda.Config, scope ...conda.NativeScope) (conda.Config, error) {
	next, _, err := m.prepareCondaPresetChecked(next, scope...)
	return next, err
}

func (m *Manager) prepareCondaPresetChecked(next conda.Config, scope ...conda.NativeScope) (conda.Config, func() error, error) {
	if next.CondaMirror == "" || next.CondaMirror == "default" {
		next.PresetChannels = nil
		next.PresetChannelsSet = false
		next.PresetContext = ""
		return next, nil, nil
	}
	explicit := len(scope) == 1 && scope[0].CondaMirror
	if !explicit && next.CondaMirror == m.Config.Conda.CondaMirror && next.SourceCONDARC == m.Config.Conda.SourceCONDARC {
		return next, nil, nil
	}
	current, guard, err := m.Conda.CaptureAtSourceWithGuard(next, conda.MirrorDefaults{}, "conda")
	if err != nil {
		return next, nil, fmt.Errorf("无法读取当前频道；未切换镜像: %w", err)
	}
	if !m.Config.Conda.Defaults.CondaCaptured {
		next.Defaults.CondaCaptured = current.CondaCaptured
		next.Defaults.CondaYAML = current.CondaYAML
		next.Defaults.CondaVariables = current.CondaVariables
	}
	next.PresetChannels = conda.PresetChannels(current, next.CondaMirror)
	next.PresetChannelsSet = true
	next.PresetContext = current.CondaYAML
	return next, guard, nil
}

func publicCondaVariables(cfg conda.Config, vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars))
	for key, value := range vars {
		out[key] = value
		if cfg.CondaMirror == "default" && key != "CONDARC" || cfg.PipMirror == "default" && key == "PIP_INDEX_URL" {
			out[key] = "<已记录的原始值>"
		}
	}
	return out
}

// RecordCondaDefaults only records missing first-use mirror information. Setup
// must not reapply an active profile or modify native paths/channels.
func (m *Manager) RecordCondaDefaults(next conda.MirrorDefaults, source ...string) error {
	return m.mutate(false, func() error {
		return m.recordCondaDefaults(next, source...)
	})
}

// CheckCondaDefaults gives setup previews the same ownership and state checks
// as saving a snapshot, without touching native paths or creating resources.
func (m *Manager) CheckCondaDefaults(next conda.MirrorDefaults, source ...string) error {
	if len(source) > 1 {
		return fmt.Errorf("只能指定一个原始 CONDARC")
	}
	if err := m.checkCondaState(); err != nil {
		return err
	}
	if err := conda.ValidateDefaults(next); err != nil {
		return err
	}
	cfg := m.Config.Conda
	cfg.Defaults = next
	if len(source) == 1 {
		if cfg.CondaMirror != "" && cfg.SourceCONDARC != source[0] {
			return fmt.Errorf("setup 不能更换已激活的 CONDARC；请使用显式 Conda 镜像命令选择来源")
		}
		cfg.SourceCONDARC = source[0]
		if err := m.Conda.CheckSourcePath(source[0]); err != nil {
			return err
		}
	}
	if err := m.Conda.ValidateConfig(cfg); err != nil {
		return err
	}
	return m.checkCondaMirror(cfg, false)
}

func (m *Manager) recordCondaDefaults(next conda.MirrorDefaults, source ...string) error {
	prior := m.Config.Conda.Defaults
	if prior.CondaCaptured {
		next.CondaCaptured = true
		next.CondaYAML = prior.CondaYAML
		next.CondaVariables = prior.CondaVariables
	}
	if prior.PipCaptured {
		next.PipCaptured = true
		next.PipIndexURL = prior.PipIndexURL
		next.PipVariables = prior.PipVariables
	}
	if err := m.CheckCondaDefaults(next, source...); err != nil {
		return err
	}
	previous := m.Config
	before := append([]byte(nil), m.configBytes...)
	m.Config.Conda.Defaults = next
	if len(source) == 1 {
		m.Config.Conda.SourceCONDARC = source[0]
	}
	if err := m.publishCondaMirror(m.Config.Conda); err != nil {
		m.Config = previous
		return err
	}
	if err := m.save(); err != nil {
		if bytes.Equal(before, m.configBytes) {
			m.Config = previous
		}
		return err
	}
	// A synced temporary file alone does not make its subsequent rename
	// durable. Flush the directory entry before any native mirror overwrite.
	dir, err := os.OpenFile(m.Root, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil || !os.SameFile(m.rootInfo, info) {
		return fmt.Errorf("default 保存后 Nido 目录发生变化；未修改原生配置")
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("default 目录同步失败；未修改原生配置: %w", err)
	}
	return nil
}
