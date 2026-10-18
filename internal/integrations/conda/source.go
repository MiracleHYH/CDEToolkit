package conda

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

func (s Service) validateSourcePath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, "$") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return fmt.Errorf("原始 CONDARC 必须是规范绝对文件路径，不能包含变量或控制字符")
	}
	if s.Root != "" && contains(s.Root, path) {
		return fmt.Errorf("原始 CONDARC 不能位于 Nido 软件目录内")
	}
	if !nativeCondaFilename(path) {
		return fmt.Errorf("原始 CONDARC 文件名必须为 .condarc、condarc 或以 .yaml/.yml 结尾")
	}
	return nil
}

// ResolveSource retains the explicit configuration's original search position.
// A managed overlay must never become its own native source. Remembering the
// path also preserves the association in a fresh shell without exported markers.
func (s Service) ResolveSource(previous, next Config) (string, error) {
	current, _ := envValue(s.environment(), "CONDARC")
	path := next.SourceCONDARC
	if path == "" {
		path = previous.SourceCONDARC
	}
	if current != "" {
		if s.Root != "" && contains(s.Root, filepath.Clean(current)) {
			if set, _ := envValue(s.environment(), "_NIDO_CONDA_CONDARC_ORIGINAL_SET"); set == "x" {
				if original, _ := envValue(s.environment(), "_NIDO_CONDA_CONDARC_ORIGINAL"); original != "" {
					path = original
				}
			}
		} else {
			path = current
		}
	}
	if path == "" {
		return "", nil
	}
	if err := s.validateSourcePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// CheckSource reads only metadata and bounded bytes; it never copies arbitrary
// application settings into Nido's state or generated mirror resource.
func (s Service) CheckSource(c Config) error {
	if c.CondaMirror == "" || c.SourceCONDARC == "" {
		return nil
	}
	return s.CheckSourcePath(c.SourceCONDARC)
}

// CheckSourcePath also validates inactive setup associations. Remembering a
// missing source would make a later fresh-shell activation silently fall back
// to unrelated settings or fail after an apparently successful setup.
func (s Service) CheckSourcePath(path string) error {
	if path == "" {
		return nil
	}
	if err := s.validateSourcePath(path); err != nil {
		return err
	}
	snapshot, err := s.readNativePath(path)
	if err != nil {
		return fmt.Errorf("原始 CONDARC 无法安全读取；未切换镜像: %w", err)
	}
	if snapshot.info == nil {
		return fmt.Errorf("原始 CONDARC 不存在；请先修复文件或使用有效的原始配置")
	}
	return nil
}

// CaptureAtSource selects the source being configured for read-only discovery.
// Validation deliberately continues to use the caller's actual environment.
// This matters in a new shell, where a saved source may not yet be exported.
func (s Service) CaptureAtSource(c Config, defaults MirrorDefaults, tool string) (MirrorDefaults, error) {
	captured, _, err := s.CaptureAtSourceWithGuard(c, defaults, tool)
	return captured, err
}

// CaptureAtSourceWithGuard ties discovery to the native files that a later
// mutation will edit. A slow upstream query cannot silently discard a private
// channel that the user added after its read. The returned guard is checked
// again after preparing the native writes, before publishing any of them.
func (s Service) CaptureAtSourceWithGuard(c Config, defaults MirrorDefaults, tool string) (MirrorDefaults, func() error, error) {
	needsConda := (tool == "conda" || tool == "all") && !defaults.CondaCaptured
	if c.SourceCONDARC != "" && needsConda {
		if err := s.CheckSourcePath(c.SourceCONDARC); err != nil {
			return defaults, nil, err
		}
		env := make([]string, 0, len(s.environment())+1)
		for _, entry := range s.environment() {
			if !strings.HasPrefix(entry, "CONDARC=") {
				env = append(env, entry)
			}
		}
		s.Env = append(env, "CONDARC="+c.SourceCONDARC)
	}
	var snapshots []nativePathSnapshot
	if needsConda {
		paths, err := s.nativeCondaPaths(c)
		if err != nil {
			return defaults, nil, err
		}
		for _, path := range paths {
			snapshot, err := s.readNativePath(path)
			if err != nil {
				return defaults, nil, err
			}
			snapshots = append(snapshots, snapshot)
		}
	}
	var guard func() error
	if len(snapshots) != 0 {
		guard = func() error {
			for _, snapshot := range snapshots {
				if err := snapshot.unchanged(); err != nil {
					return fmt.Errorf("读取镜像期间原生配置已变化；未覆盖，请重新运行: %w", err)
				}
			}
			return nil
		}
	}
	captured, err := s.CaptureDefaults(defaults, tool)
	if err != nil {
		return defaults, nil, err
	}
	if guard != nil {
		if err := guard(); err != nil {
			return defaults, nil, err
		}
	}
	return captured, guard, nil
}
