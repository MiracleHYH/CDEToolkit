// Package conda manages opt-in Conda and pip configuration through a generated
// mirror profiles and native configuration for storage locations. It never
// installs software or moves or removes environments or caches.
package conda

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type Config struct {
	CondaMirror       string         `toml:"conda_mirror,omitempty" json:"conda_mirror,omitempty"`
	SourceCONDARC     string         `toml:"source_condarc,omitempty" json:"-"`
	PresetChannels    []string       `toml:"preset_channels,omitempty" json:"-"`
	PresetChannelsSet bool           `toml:"preset_channels_set,omitempty" json:"-"`
	PresetContext     string         `toml:"preset_context,omitempty" json:"-"`
	PipMirror         string         `toml:"pip_mirror,omitempty" json:"pip_mirror,omitempty"`
	PipIndexURL       string         `toml:"pip_index_url,omitempty" json:"pip_index_url,omitempty"`
	EnvsDir           string         `toml:"envs_dir,omitempty" json:"envs_dir,omitempty"`
	PkgsDir           string         `toml:"pkgs_dir,omitempty" json:"pkgs_dir,omitempty"`
	PipCacheDir       string         `toml:"pip_cache_dir,omitempty" json:"pip_cache_dir,omitempty"`
	Defaults          MirrorDefaults `toml:"defaults" json:"defaults"`
}

type Runner func(name string, args, env []string) ([]byte, error)

type Service struct {
	Root, UserHome string
	Env            []string
	Runner         Runner
	// Ctx bounds read-only upstream discovery to the current interaction.
	// A nil context retains the standalone command's existing timeout.
	Ctx context.Context
}

func (s Service) environment() []string {
	if s.Env != nil {
		return s.Env
	}
	return os.Environ()
}

func envValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], key+"="); ok {
			return value, true
		}
	}
	return "", false
}

func invalidPath(path string) bool {
	return strings.ContainsAny(path, "$:") || strings.IndexFunc(path, unicode.IsControl) >= 0
}

// physical resolves existing ancestors without creating a missing path. This
// accepts ordinary OS aliases such as /var on macOS, but never interprets shell
// or Conda environment variable expansion.
func physical(path string) (string, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		parent, err := physical(filepath.Dir(path))
		if err != nil {
			return "", err
		}
		if info, err := os.Stat(parent); err == nil && !info.IsDir() {
			return "", fmt.Errorf("路径祖先不是目录: %s", parent)
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return filepath.Join(parent, filepath.Base(path)), nil
	} else if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// NormalizePath returns a canonical external data directory without writing it.
// Existing directories are resolved physically so Nido ownership boundaries
// cannot be bypassed through symlink aliases.
func (s Service) NormalizePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home := s.UserHome
		if home == "" {
			home, _ = envValue(s.environment(), "HOME")
		}
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("无法展开用户主目录")
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) || invalidPath(path) {
		return "", fmt.Errorf("目录必须是绝对路径，不能包含环境变量、路径列表分隔符或控制字符")
	}
	path, err := physical(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("无法规范目录: %w", err)
	}
	if path == string(filepath.Separator) || invalidPath(path) {
		return "", fmt.Errorf("目录不能是文件系统根目录或包含不支持的字符")
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return "", fmt.Errorf("目标路径不是目录")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if s.Root != "" {
		if !filepath.IsAbs(s.Root) || invalidPath(s.Root) {
			return "", fmt.Errorf("Nido 软件目录无效")
		}
		root, err := physical(filepath.Clean(s.Root))
		if err != nil {
			return "", err
		}
		if contains(root, path) || contains(path, root) {
			return "", fmt.Errorf("外部数据目录与 Nido 软件目录不能相同或相互包含")
		}
	}
	return path, nil
}

// PipMirror maps known presets or validates an explicit HTTPS simple index.
// It does not add trusted-host, extra-index-url or disable TLS verification.
func PipMirror(value string) (string, error) {
	switch value {
	case "official":
		return "https://pypi.org/simple", nil
	case "tuna":
		return "https://pypi.tuna.tsinghua.edu.cn/simple", nil
	case "ustc":
		return "https://mirrors.ustc.edu.cn/pypi/simple", nil
	}
	if strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || strings.ContainsAny(value, "\\\"'") {
		return "", fmt.Errorf("pip 镜像地址包含不支持的字符")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("pip 镜像必须是不含认证、查询参数或片段的 HTTPS simple 索引地址")
	}
	if strings.HasSuffix(u.Host, ":") || strings.Contains(u.Hostname(), ":") && !strings.HasPrefix(u.Host, "[") || strings.HasPrefix(u.Host, "[") && net.ParseIP(u.Hostname()) == nil {
		return "", fmt.Errorf("pip 镜像主机或端口无效")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("pip 镜像端口无效")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/simple") {
		return "", fmt.Errorf("pip 镜像地址路径必须以 /simple 结尾")
	}
	// Encoded separators or path suffixes must not change the endpoint after
	// normalization. Retain ordinary percent escaping in earlier path segments.
	if u.RawPath != "" && strings.TrimRight(u.EscapedPath(), "/") != u.Path {
		return "", fmt.Errorf("pip 镜像路径不能包含转义字符")
	}
	u.RawPath = ""
	return u.String(), nil
}

func (s Service) ValidateConfig(c Config) error {
	if c.SourceCONDARC != "" {
		if err := s.validateSourcePath(c.SourceCONDARC); err != nil {
			return err
		}
	}
	if c.CondaMirror != "" && c.CondaMirror != "default" && c.CondaMirror != "official" && c.CondaMirror != "tuna" {
		return fmt.Errorf("Conda 镜像仅支持 default、official（conda）或 tuna")
	}
	if err := ValidateDefaults(c.Defaults); err != nil {
		return err
	}
	if err := validatePresetChannels(c); err != nil {
		return err
	}
	if c.CondaMirror == "default" && !c.Defaults.CondaCaptured {
		return fmt.Errorf("尚未记录 Conda default，请先 setup")
	}
	if c.PipMirror == "default" && !c.Defaults.PipCaptured {
		return fmt.Errorf("尚未记录 pip default，请先 setup")
	}
	if c.PipMirror != "" && c.PipMirror != "default" && c.PipMirror != "official" && c.PipMirror != "tuna" && c.PipMirror != "ustc" && c.PipMirror != "custom" {
		return fmt.Errorf("pip 镜像选择无效")
	}
	if c.PipIndexURL != "" {
		value, err := PipMirror(c.PipIndexURL)
		if err != nil || value != c.PipIndexURL {
			return fmt.Errorf("pip 镜像配置不是规范的 HTTPS simple 索引地址")
		}
	}
	switch c.PipMirror {
	case "default":
		if c.PipIndexURL != "" {
			return fmt.Errorf("pip default 只能使用已记录的原始地址，不能另存索引地址")
		}
	case "official", "tuna", "ustc":
		expected, _ := PipMirror(c.PipMirror)
		if c.PipIndexURL != expected {
			return fmt.Errorf("pip 镜像预设与保存的索引地址不一致")
		}
	case "custom":
		if c.PipIndexURL == "" {
			return fmt.Errorf("pip 自定义镜像缺少索引地址")
		}
	}
	if strings.Contains(c.PkgsDir, ",") {
		return fmt.Errorf("Conda 包缓存目录不能包含逗号；CONDA_PKGS_DIRS 使用逗号分隔多个路径")
	}
	if c.EnvsDir != strings.TrimSpace(c.EnvsDir) || c.PkgsDir != strings.TrimSpace(c.PkgsDir) {
		return fmt.Errorf("Conda 环境或包缓存目录不能包含首尾空白；Conda 会自动去除路径列表片段的首尾空白")
	}
	for _, path := range []string{c.EnvsDir, c.PkgsDir, c.PipCacheDir} {
		if path == "" {
			continue
		}
		// Validation during Nido startup checks syntax and recorded ownership
		// only. A disconnected disk or replaced data path must not prevent
		// unrelated commands or resetting this configuration.
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) || invalidPath(path) {
			return fmt.Errorf("保存的目录不是规范绝对路径: %s", path)
		}
		if s.Root != "" && (contains(s.Root, path) || contains(path, s.Root)) {
			return fmt.Errorf("外部数据目录与 Nido 软件目录不能相同或相互包含")
		}
	}
	return nil
}

func mirrorChannels(mirror string) ([]string, map[string]string) {
	base, cloud := "https://repo.anaconda.com/pkgs/", "https://conda.anaconda.org"
	if mirror == "tuna" {
		base, cloud = "https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/", "https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud"
	}
	return []string{base + "main", base + "r", base + "msys2"}, map[string]string{"conda-forge": cloud, "bioconda": cloud}
}

// MirrorFile computes an immutable, content-addressed overlay. The caller must
// validate configuration and publish these exact bytes before exporting it.
// CONDARC participates in Conda's normal merge; this is not an isolated config.
func (s Service) MirrorFile(c Config) (string, []byte) {
	if c.CondaMirror == "" {
		return "", nil
	}
	if c.CondaMirror == "default" {
		return s.DefaultMirrorFile(c.Defaults)
	}
	content, err := PresetMirrorYAML(c)
	if err != nil {
		return "", nil
	}
	hash := sha256.Sum256(content)
	return filepath.Join(s.Root, "conda", fmt.Sprintf("mirror-%x.yaml", hash)), content
}

// Variables returns only configured values. Empty fields do not export empty
// overrides and never clear variables that belong to the user's current shell.
func (s Service) Variables(c Config) map[string]string {
	variables := map[string]string{}
	if path, _ := s.MirrorFile(c); path != "" {
		if c.SourceCONDARC != "" {
			path = c.SourceCONDARC
		}
		variables["CONDARC"] = path
	}
	if c.CondaMirror == "default" {
		for key, value := range c.Defaults.CondaVariables {
			variables[key] = value
		}
	}
	for key, value := range map[string]string{"PIP_INDEX_URL": EffectivePipIndex(c)} {
		if value != "" {
			variables[key] = value
		}
	}
	return variables
}

func EffectivePipIndex(c Config) string {
	if c.PipMirror == "default" {
		return c.Defaults.PipIndexURL
	}
	return c.PipIndexURL
}

// A selected Conda profile owns its mirror overrides as well as CONDARC. A
// default profile replays only the original recorded values; other profiles
// clear those overrides so native precedence cannot silently keep an old URL.
func (s Service) UnsetVariables(c Config) []string {
	if c.CondaMirror == "" {
		return nil
	}
	vars := s.Variables(c)
	var names []string
	for _, key := range MirrorVariableNames() {
		if _, ok := vars[key]; !ok {
			names = append(names, key)
		}
	}
	return names
}

// Issues is entirely static. It does not execute Conda or Python, infer merged
// native settings, probe network access, or create any data directory.
func (s Service) Issues(c Config) []string {
	if err := s.ValidateConfig(c); err != nil {
		return []string{err.Error()}
	}
	var issues []string
	if err := s.CheckSource(c); err != nil {
		issues = append(issues, err.Error())
	}
	env := s.environment()
	for _, row := range []struct{ name, path string }{{"Conda 环境目录", c.EnvsDir}, {"Conda 包缓存目录", c.PkgsDir}, {"pip 缓存目录", c.PipCacheDir}} {
		if row.path == "" {
			continue
		}
		actual, err := s.NormalizePath(row.path)
		if err != nil {
			issues = append(issues, row.name+" 当前不可用: "+err.Error())
		} else if actual != row.path {
			issues = append(issues, row.name+" 当前物理路径已改变；请重新选择或 reset")
		} else if info, err := os.Stat(row.path); err == nil && info.Mode().Perm()&0222 == 0 {
			issues = append(issues, row.name+" 当前没有可写权限")
		}
	}
	variables := s.Variables(c)
	unset := map[string]bool{}
	for _, key := range s.UnsetVariables(c) {
		unset[key] = true
	}
	for _, key := range append([]string{"CONDARC", "PIP_INDEX_URL"}, MirrorVariableNames()...) {
		if expected, ok := variables[key]; ok {
			if actual, exists := envValue(env, key); !exists || actual != expected {
				issues = append(issues, key+" 与 Nido 配置不一致；请在新终端重新加载 env.sh")
			}
			continue
		}
		if unset[key] {
			if _, exists := envValue(env, key); exists {
				issues = append(issues, key+" 与已选择镜像冲突；请重新加载 env.sh")
			}
			continue
		}
		// A reset changes future generated environment code, not its parent
		// shell. Diagnose only a value still proven to be our prior export;
		// a user's subsequent replacement or unset owns its new state.
		active, _ := envValue(env, "_NIDO_CONDA_"+key+"_ACTIVE")
		previous, recorded := envValue(env, "_NIDO_CONDA_"+key+"_VALUE")
		actual, exists := envValue(env, key)
		appliedSet, _ := envValue(env, "_NIDO_CONDA_"+key+"_APPLIED_SET")
		originalSet, _ := envValue(env, "_NIDO_CONDA_"+key+"_ORIGINAL_SET")
		if active == "1" && recorded && (exists && actual == previous || appliedSet == "" && !exists && originalSet == "x") {
			issues = append(issues, "当前终端仍保留已撤销的 "+key+"；请重新加载 env.sh")
		}
	}
	for _, row := range []struct{ key, desired string }{{"CONDA_ENVS_PATH", c.EnvsDir}, {"CONDA_ENVS_DIRS", c.EnvsDir}, {"CONDA_PKGS_DIRS", c.PkgsDir}, {"PIP_CACHE_DIR", c.PipCacheDir}} {
		if value, ok := envValue(env, row.key); row.desired != "" && ok && value != "" && value != row.desired {
			issues = append(issues, row.key+" 正在覆盖原生目录配置；请明确处理该环境变量")
		}
	}
	if EffectivePipIndex(c) != "" {
		if value, ok := envValue(env, "PIP_EXTRA_INDEX_URL"); ok && value != "" {
			issues = append(issues, "PIP_EXTRA_INDEX_URL 增加了额外索引，pip 不会仅使用已选择镜像")
		}
		if value, ok := envValue(env, "PIP_NO_INDEX"); ok && truthy(value) {
			issues = append(issues, "PIP_NO_INDEX 禁用了 pip 索引")
		}
	}
	if c.PipCacheDir != "" {
		if value, ok := envValue(env, "PIP_NO_CACHE_DIR"); ok && value != "" {
			issues = append(issues, "PIP_NO_CACHE_DIR 禁用了 pip 缓存，包括值为 false/no/0 的情况")
		}
	}
	return issues
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "f", "no", "n", "off":
		return false
	default:
		return true
	}
}
