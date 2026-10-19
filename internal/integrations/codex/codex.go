// Package codex provides a small, external integration. It never moves a Codex
// home, examines its private storage, or owns the Codex process lifecycle.
package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const InstallerURL = "https://chatgpt.com/codex/install.sh"

type Config struct {
	Home       string `toml:"home" json:"home"`
	Command    string `toml:"command,omitempty" json:"command,omitempty"`
	Channel    string `toml:"channel,omitempty" json:"channel,omitempty"`
	ProxyBlock string `toml:"proxy_block,omitempty" json:"-"`
}

type Runner func(path string, args []string, env []string, input []byte) ([]byte, error)

type Service struct {
	UserHome string
	Root     string
	Env      []string
	Run      Runner
	// Ctx applies only to commands started by this operation. Cancellation
	// never targets an existing Codex process or daemon.
	Ctx context.Context
}

type Candidate struct {
	Home   string `json:"home"`
	Source string `json:"source"`
}

type Status struct {
	State           string `json:"state"`
	Home            string `json:"home"`
	Command         string `json:"command"`
	Version         string `json:"version"`
	Detail          string `json:"detail"`
	ProxyConfigured bool   `json:"proxy_configured"`
}

func (s Service) environment() []string {
	if s.Env != nil {
		return append([]string(nil), s.Env...)
	}
	return os.Environ()
}

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

func putEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return append(out, key+"="+value)
}

func (s Service) run(path string, args []string, env []string, input []byte) ([]byte, error) {
	var out []byte
	var err error
	if s.Run != nil {
		out, err = s.Run(path, args, env, input)
	} else {
		out, err = runCommandContext(s.Ctx, path, args, env, input)
	}
	if err != nil {
		return nil, fmt.Errorf("外部命令执行失败")
	}
	if len(out) > maxCommandOutput {
		return nil, fmt.Errorf("外部命令输出过大")
	}
	return out, nil
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canonical also resolves an existing parent of a not-yet-created home.
func canonical(path string) (string, error) {
	if invalidPath(path) {
		return "", fmt.Errorf("路径包含控制字符")
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("Codex 目录必须为绝对路径")
	}
	parent, suffix := path, []string{}
	for {
		if _, err := os.Lstat(parent); err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", fmt.Errorf("Codex 目录包含失效链接")
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("无法访问 Codex 目录")
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("无法定位 Codex 目录")
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = next
	}
}

func invalidPath(path string) bool {
	return strings.IndexFunc(path, func(c rune) bool { return c < 32 || c == 127 }) >= 0
}

func (s Service) homePath(path string) (string, error) {
	if path == "~" {
		path = s.UserHome
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(s.UserHome, path[2:])
	}
	if path == "" {
		return "", fmt.Errorf("请明确指定外部 CODEX_HOME")
	}
	path, err := canonical(path)
	if err != nil {
		return "", err
	}
	if path == string(filepath.Separator) {
		return "", fmt.Errorf("CODEX_HOME 不能使用文件系统根目录")
	}
	if s.UserHome != "" {
		userHome, err := canonical(s.UserHome)
		if err != nil {
			return "", fmt.Errorf("用户 HOME 无效")
		}
		if inside(path, userHome) {
			return "", fmt.Errorf("CODEX_HOME 不能使用用户 HOME 或其父目录")
		}
	}
	if s.Root != "" {
		root, e := canonical(s.Root)
		if e != nil {
			return "", fmt.Errorf("Nido 软件目录无效")
		}
		if inside(root, path) || inside(path, root) {
			return "", fmt.Errorf("CODEX_HOME 必须位于 Nido 软件目录之外，且不能包含 Nido 软件目录")
		}
	}
	return path, nil
}

func (s Service) home(path string, existing bool) (string, error) {
	path, err := s.homePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if !existing && os.IsNotExist(err) {
			return path, nil
		}
		return "", fmt.Errorf("Codex 目录不存在或无法访问")
	}
	if !info.IsDir() {
		return "", fmt.Errorf("CODEX_HOME 必须是目录")
	}
	return path, nil
}

// ValidateConfig validates Nido's small integration record, not Codex's data.
// Missing external directories and commands remain readable so status and
// detachment can still work after the user removes an external installation.
func (s Service) ValidateConfig(c Config) error {
	return s.validateConfig(c, true)
}

// ValidateRecord checks the saved record without resolving external paths.
// An unavailable mount or broken external link is a component problem, and
// must not prevent opening Nido or configuring unrelated integrations.
func (s Service) ValidateRecord(c Config) error {
	return s.validateConfig(c, false)
}

func (s Service) validateConfig(c Config, checkPaths bool) error {
	if c.Home == "" {
		if c.Command != "" || c.Channel != "" || c.ProxyBlock != "" {
			return fmt.Errorf("未关联 Codex 时不能记录命令、渠道或代理配置")
		}
		return nil
	}
	if !filepath.IsAbs(c.Home) || filepath.Clean(c.Home) != c.Home || invalidPath(c.Home) {
		return fmt.Errorf("CODEX_HOME 必须为规范的绝对路径")
	}
	if checkPaths {
		if _, err := s.homePath(c.Home); err != nil {
			return err
		}
	} else {
		if c.Home == "/" || s.UserHome != "" && inside(c.Home, filepath.Clean(s.UserHome)) {
			return fmt.Errorf("CODEX_HOME 不能使用用户 HOME 或其父目录")
		}
		if s.Root != "" && (inside(s.Root, c.Home) || inside(c.Home, s.Root)) {
			return fmt.Errorf("CODEX_HOME 必须位于 Nido 软件目录之外")
		}
	}
	if c.Command == "" {
		if c.Channel != "" {
			return fmt.Errorf("未记录 Codex 命令时不能指定安装渠道")
		}
	} else {
		if !filepath.IsAbs(c.Command) || filepath.Clean(c.Command) != c.Command || invalidPath(c.Command) || strings.Contains(c.Command, ":") || filepath.Base(c.Command) != "codex" {
			return fmt.Errorf("Codex 命令必须为规范的绝对路径")
		}
		switch c.Channel {
		case "", "standalone", "npm", "homebrew", "unknown":
		default:
			return fmt.Errorf("Codex 安装渠道无效")
		}
		if s.Root != "" {
			root, parent := filepath.Clean(s.Root), filepath.Dir(c.Command)
			if checkPaths {
				var err, e error
				root, err = canonical(s.Root)
				parent, e = canonical(parent)
				if err != nil || e != nil {
					return fmt.Errorf("Codex 命令目录不可用")
				}
			}
			if inside(root, parent) {
				return fmt.Errorf("Codex 命令必须位于 Nido 软件目录之外")
			}
			if checkPaths {
				if target, e := filepath.EvalSymlinks(c.Command); e == nil && inside(root, target) {
					return fmt.Errorf("Codex 命令不能指向 Nido 软件目录")
				}
			}
		}
	}
	return validateProxyBlock(c.ProxyBlock)
}

func (s Service) Discover() ([]Candidate, error) {
	for _, c := range []Candidate{{envValue(s.environment(), "CODEX_HOME"), "CODEX_HOME"}, {filepath.Join(s.UserHome, ".codex"), "default"}} {
		if c.Home == "" {
			continue
		}
		home, err := s.home(c.Home, true)
		if err != nil {
			continue
		}
		return []Candidate{{Home: home, Source: c.Source}}, nil
	}
	return nil, nil
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func (s Service) command() string {
	for _, dir := range filepath.SplitList(envValue(s.environment(), "PATH")) {
		if !filepath.IsAbs(dir) || invalidPath(dir) {
			continue
		}
		path := filepath.Join(dir, "codex")
		if executable(path) {
			return path
		}
	}
	path := filepath.Join(s.UserHome, ".local", "bin", "codex")
	if executable(path) {
		return path
	}
	return ""
}

func channel(path, home string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "unknown"
	}
	if inside(filepath.Join(home, "packages", "standalone"), resolved) {
		return "standalone"
	}
	for _, p := range []string{path, resolved} {
		p = filepath.ToSlash(p)
		if strings.Contains(p, "/node_modules/") {
			return "npm"
		}
		if strings.Contains(p, "/Cellar/") || strings.Contains(p, "/Caskroom/") {
			return "homebrew"
		}
	}
	return "unknown"
}

func (s Service) Associate(home string) (Config, error) {
	home, err := s.home(home, true)
	if err != nil {
		return Config{}, err
	}
	command := s.command()
	c := Config{Home: home, Command: command}
	if command != "" {
		c.Channel = channel(command, home)
	}
	if err := s.ValidateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

var softwareVersion = regexp.MustCompile(`(?m)^codex(?:-cli)?\s+v?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\s*$`)

func (s Service) Status(c Config) Status {
	st := Status{State: "unlinked", Home: c.Home, Command: c.Command}
	if c.Home == "" {
		return st
	}
	if err := s.ValidateConfig(c); err != nil {
		st.State, st.Detail = "unavailable", err.Error()
		return st
	}
	if _, err := s.home(c.Home, true); err != nil {
		st.State = "unavailable"
		st.Detail = err.Error()
		return st
	}
	st.State = "linked"
	if file, err := readEnv(c.Home); err != nil {
		st.Detail = err.Error()
	} else if c.ProxyBlock != "" {
		if _, _, err := stripOwned(file.content, c.ProxyBlock); err != nil {
			st.Detail = err.Error()
		} else {
			st.ProxyConfigured = true
		}
	}
	if c.Command == "" {
		c.Command = s.command()
		st.Command = c.Command
	}
	if err := s.ValidateConfig(c); err != nil {
		st.Detail = strings.TrimSpace(st.Detail + " " + err.Error())
		return st
	}
	if c.Command == "" || !filepath.IsAbs(c.Command) || invalidPath(c.Command) || !executable(c.Command) {
		st.Detail = strings.TrimSpace(st.Detail + " Codex 命令不存在")
		return st
	}
	env := putEnv(s.environment(), "CODEX_HOME", c.Home)
	out, err := s.run(c.Command, []string{"--version"}, env, nil)
	if err != nil {
		st.Detail = strings.TrimSpace(st.Detail + " 无法查询 Codex 版本")
		return st
	}
	if match := softwareVersion.FindSubmatch(out); len(match) == 2 {
		st.Version = string(match[1])
	} else {
		st.Detail = strings.TrimSpace(st.Detail + " 无法识别 Codex 版本")
	}
	return st
}
