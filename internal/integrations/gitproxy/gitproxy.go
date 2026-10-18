// Package gitproxy manages one explicit user-level Git HTTP proxy setting.
// It does not invoke Git, evaluate includes, or change SSH configuration.
package gitproxy

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const blockStart = "# >>> nido git proxy >>>"
const blockEnd = "# <<< nido git proxy <<<"

// Config is the exact ownership receipt persisted by Nido. Neither an arbitrary
// path nor arbitrary file contents in a modified receipt authorize deletion.
type Config struct {
	ConfigFile string `toml:"config_file,omitempty" json:"config_file,omitempty"`
	ProxyBlock string `toml:"proxy_block,omitempty" json:"proxy_block,omitempty"`
}

type Service struct {
	Root     string
	UserHome string
	Env      []string
}

// ValidateProxyURL validates literal Git/curl-compatible proxy URLs. Credentials
// remain ordinary URL userinfo and are never evaluated as shell expressions.
func ValidateProxyURL(endpoint string) error {
	// strconv.Quote emits Go escapes for non-printable runes. Git's config
	// reader does not accept its \u/\U/\x forms, so reject those inputs before
	// publishing a block that would make the entire user config unreadable.
	if !utf8.ValidString(endpoint) || strings.ContainsAny(endpoint, "'\"\\") || strings.IndexFunc(endpoint, func(r rune) bool { return !unicode.IsPrint(r) || unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return fmt.Errorf("代理地址包含不支持的字符")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("代理地址无效")
	}
	if strings.HasSuffix(u.Host, ":") || strings.Contains(u.Hostname(), ":") && !strings.HasPrefix(u.Host, "[") || strings.HasPrefix(u.Host, "[") && (net.ParseIP(u.Hostname()) == nil || !strings.Contains(u.Hostname(), ":")) {
		return fmt.Errorf("代理端口为空或 IPv6 地址缺少有效方括号")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("代理协议不支持")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("代理端口无效")
		}
	}
	if u.User != nil {
		password, _ := u.User.Password()
		if strings.IndexFunc(u.User.Username()+password, unicode.IsControl) >= 0 {
			return fmt.Errorf("代理认证包含控制字符")
		}
	}
	return nil
}

func generatedBlock(endpoint string, leadingNewline bool) string {
	block := blockStart + "\n[http]\n\tproxy = " + strconv.Quote(endpoint) + "\n" + blockEnd + "\n"
	if leadingNewline {
		block = "\n" + block
	}
	return block
}

func (s Service) ValidateConfig(c Config) error {
	if c.ConfigFile == "" && c.ProxyBlock == "" {
		return nil
	}
	if c.ConfigFile == "" || !filepath.IsAbs(c.ConfigFile) || filepath.Clean(c.ConfigFile) != c.ConfigFile || invalidPath(c.ConfigFile) {
		return fmt.Errorf("Git 代理配置收据路径无效")
	}
	if err := s.outsideRoot(c.ConfigFile); err != nil {
		return err
	}
	if c.ProxyBlock == "" {
		return fmt.Errorf("Git 代理配置收据缺少受管块")
	}
	text := strings.TrimPrefix(c.ProxyBlock, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) != 5 || lines[0] != blockStart || lines[1] != "[http]" || lines[3] != blockEnd || lines[4] != "" {
		return fmt.Errorf("Git 代理配置收据内容无效")
	}
	value, ok := strings.CutPrefix(lines[2], "\tproxy = ")
	endpoint, err := strconv.Unquote(value)
	if !ok || err != nil || ValidateProxyURL(endpoint) != nil || c.ProxyBlock != generatedBlock(endpoint, strings.HasPrefix(c.ProxyBlock, "\n")) {
		return fmt.Errorf("Git 代理配置收据内容无效")
	}
	return nil
}

func invalidPath(path string) bool {
	return strings.IndexFunc(path, unicode.IsControl) >= 0 || path == string(filepath.Separator)
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

// paths follows Git's global selection, but does not read repository or system
// configuration. Their per-remote overrides remain Git's responsibility.
func (s Service) paths() (string, []string, error) {
	env := s.environment()
	for _, key := range []string{"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT"} {
		if value, ok := envValue(env, key); ok && value != "" && !(key == "GIT_CONFIG_COUNT" && value == "0") {
			return "", nil, fmt.Errorf("%s 存在临时 Git 配置，无法安全配置用户级代理", key)
		}
	}
	if value, ok := envValue(env, "GIT_CONFIG_GLOBAL"); ok {
		path, err := resolvePath(value)
		if err == nil {
			err = s.outsideRoot(path)
		}
		return path, nil, err
	}
	home := s.UserHome
	if home == "" {
		home, _ = envValue(env, "HOME")
	}
	if home == "" {
		return "", nil, fmt.Errorf("未提供 Git 用户 HOME")
	}
	home, err := resolveDirectory(home)
	if err != nil {
		return "", nil, err
	}
	xdg, ok := envValue(env, "XDG_CONFIG_HOME")
	if !ok || xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(xdg) || invalidPath(xdg) {
		return "", nil, fmt.Errorf("XDG_CONFIG_HOME 必须为绝对路径")
	}
	path, other := filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config")
	for _, selected := range []string{path, other} {
		if err := s.outsideRoot(selected); err != nil {
			return "", nil, err
		}
	}
	return path, []string{other}, nil
}

var sectionPattern = regexp.MustCompile(`(?i)^\[\s*([a-z][a-z0-9-]*)(?:\s+"(?:[^"\\]|\\.)*"|\.[^\]\r\n]+)?\s*\]\s*(?:[#;].*)?$`)

// checkOther deliberately refuses includes instead of inventing an incomplete
// Git interpreter. It preserves all supported unrelated configuration bytes.
func checkOther(content []byte) error {
	section := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasSuffix(line, "\\") || strings.ContainsRune(line, '\x00') {
			return fmt.Errorf("Git 配置存在不支持的续行或控制字符，未修改")
		}
		if strings.HasPrefix(line, "[") {
			match := sectionPattern.FindStringSubmatch(line)
			if match == nil {
				return fmt.Errorf("Git 配置节格式不明确，未修改")
			}
			section = strings.ToLower(match[1])
			if section == "include" || section == "includeif" {
				return fmt.Errorf("Git 配置含 include/条件 include，请自行明确代理配置")
			}
			continue
		}
		key := strings.ToLower(strings.TrimSpace(strings.SplitN(line, "=", 2)[0]))
		fields := strings.Fields(key)
		if len(fields) == 0 || section == "" {
			return fmt.Errorf("Git 配置键格式不明确，未修改")
		}
		key = fields[0]
		if (section == "http" || section == "remote") && key == "proxy" {
			return fmt.Errorf("Git 已有非 Nido 管理的代理配置，未修改")
		}
	}
	return nil
}

func (s Service) prepare(c Config, endpoint string) (Config, snapshot, []byte, error) {
	if err := s.ValidateConfig(c); err != nil {
		return c, snapshot{}, nil, err
	}
	path, others, err := s.paths()
	if err != nil {
		return c, snapshot{}, nil, err
	}
	if c.ConfigFile != "" && c.ConfigFile != path {
		return c, snapshot{}, nil, fmt.Errorf("Git 用户配置路径与代理收据不一致，未修改")
	}
	before, err := readFile(path, false)
	if err != nil {
		return c, snapshot{}, nil, err
	}
	text := string(before.content)
	if c.ProxyBlock == "" {
		if strings.Contains(text, blockStart) || strings.Contains(text, blockEnd) {
			return c, snapshot{}, nil, fmt.Errorf("Git 配置存在未归属的 Nido 标记，未修改")
		}
	} else {
		if strings.Count(text, c.ProxyBlock) != 1 || strings.Count(text, blockStart) != 1 || strings.Count(text, blockEnd) != 1 || !strings.HasSuffix(text, c.ProxyBlock) {
			return c, snapshot{}, nil, fmt.Errorf("Git 受管块已被修改或后接新内容，未修改")
		}
		if start := strings.Index(text, blockStart); start > 0 && text[start-1] != '\n' {
			return c, snapshot{}, nil, fmt.Errorf("Git 受管标记不在独立行，未修改")
		}
		text = strings.TrimSuffix(text, c.ProxyBlock)
	}
	if endpoint == "" || endpoint == "off" {
		return Config{}, before, []byte(text), nil
	}
	if err := ValidateProxyURL(endpoint); err != nil {
		return c, snapshot{}, nil, err
	}
	if err := checkOther([]byte(text)); err != nil {
		return c, snapshot{}, nil, err
	}
	for _, other := range others {
		file, err := readFile(other, true)
		if err != nil {
			return c, snapshot{}, nil, err
		}
		if err := checkOther(file.content); err != nil {
			return c, snapshot{}, nil, err
		}
		before.dependencies = append(before.dependencies, watchedFile{path: other, snapshot: file})
	}
	block := generatedBlock(endpoint, len(text) > 0 && !strings.HasSuffix(text, "\n"))
	content := []byte(text + block)
	if len(content) > maxConfigSize {
		return c, snapshot{}, nil, fmt.Errorf("Git 配置将超过 1 MiB，未修改")
	}
	return Config{ConfigFile: path, ProxyBlock: block}, before, content, nil
}

// CheckProxy shares all conflict and ownership checks without writing files.
func (s Service) CheckProxy(c Config, endpoint string) error {
	_, _, _, err := s.prepare(c, endpoint)
	return err
}

// ChangeProxy edits only its exact owned block and returns compensation for a
// caller whose receipt save failed. Undo refuses any concurrent user edits.
func (s Service) ChangeProxy(c Config, endpoint string) (Config, func() error, error) {
	next, before, content, err := s.prepare(c, endpoint)
	if err != nil {
		return c, nil, err
	}
	if bytes.Equal(before.content, content) {
		return next, func() error { return nil }, nil
	}
	if err := replaceFile(before, content); err != nil {
		return c, nil, err
	}
	after, readErr := readFile(before.path, false)
	if readErr == nil && !bytes.Equal(after.content, content) {
		readErr = fmt.Errorf("Git 配置在修改后又发生变化")
	}
	var mu sync.Mutex
	done := false
	undo := func() error {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return nil
		}
		if readErr != nil || after.info == nil {
			return fmt.Errorf("Git 配置在修改后又发生变化，未恢复")
		}
		current, err := readFile(before.path, false)
		if err != nil || !sameSnapshot(after, current) {
			return fmt.Errorf("Git 配置在修改后又发生变化，未恢复")
		}
		if before.info == nil {
			if err := os.Remove(before.path); err != nil {
				return fmt.Errorf("无法移除本次新建的 Git 配置")
			}
		} else if err := replaceFile(current, before.content); err != nil {
			return err
		}
		done = true
		return nil
	}
	return next, undo, nil
}
