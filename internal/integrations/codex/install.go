package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const LatestURL = "https://api.github.com/repos/openai/codex/releases/latest"

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

func (s Service) installerEnv(c Config, proxy map[string]string, temp, bin string) ([]string, error) {
	// The caller supplies the proxy route for this Nido operation. Never let
	// inherited shell variables add another route when the chosen route is
	// direct, or leave mixed lowercase/uppercase values behind.
	base := s.environment()
	env := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if !proxyKey(strings.ToLower(key)) {
			env = append(env, entry)
		}
	}
	normalized := make(map[string]string, len(proxy))
	for key, value := range proxy {
		lower := strings.ToLower(key)
		if previous, found := normalized[lower]; found && previous != value {
			return nil, fmt.Errorf("代理环境变量大小写形式存在冲突")
		}
		normalized[lower] = value
	}
	for lower, value := range normalized {
		switch lower {
		case "http_proxy", "https_proxy", "all_proxy":
			if value != "" {
				if err := validProxy(value); err != nil {
					return nil, err
				}
			}
		case "no_proxy":
			if strings.ContainsAny(value, "\x00\r\n") {
				return nil, fmt.Errorf("no_proxy 配置无效")
			}
		default:
			return nil, fmt.Errorf("不支持的代理环境变量")
		}
		env = putEnv(env, lower, value)
		env = putEnv(env, strings.ToUpper(lower), value)
	}
	if temp != "" {
		for _, key := range []string{"HOME", "TMPDIR", "CURL_HOME", "XDG_CONFIG_HOME"} {
			env = putEnv(env, key, temp)
		}
	}
	if c.Home != "" {
		env = putEnv(env, "CODEX_HOME", c.Home)
	}
	if bin != "" {
		env = putEnv(env, "CODEX_INSTALL_DIR", bin)
	}
	// Keep unrelated CODEX_* values intact. This integration only owns these
	// explicit upstream installer inputs, not Codex runtime configuration.
	return putEnv(env, "CODEX_NON_INTERACTIVE", "1"), nil
}

type installTarget struct {
	config              Config
	bin                 string
	homeGuard, binGuard locationGuard
	entryGuard          entryGuard
}

// CheckInstall is a read-only preflight. It deliberately does not contact the
// network, execute Codex, create directories, or alter a saved association.
func (s Service) CheckInstall(c Config, update bool) (Config, error) {
	target, err := s.prepareInstall(c, update)
	if err != nil {
		return c, err
	}
	return target.config, nil
}

func (s Service) prepareInstall(c Config, update bool) (installTarget, error) {
	fail := func(err error) (installTarget, error) { return installTarget{}, err }
	if !filepath.IsAbs(s.UserHome) || invalidPath(s.UserHome) {
		return fail(fmt.Errorf("用户 HOME 必须为绝对路径"))
	}
	home, err := s.home(c.Home, false)
	if err != nil {
		return fail(err)
	}
	c.Home = home
	if err := s.ValidateConfig(c); err != nil {
		return fail(err)
	}
	c = s.resolveCommand(c)
	if err := s.ValidateConfig(c); err != nil {
		return fail(err)
	}
	if update && c.Command == "" {
		return fail(fmt.Errorf("已关联 CODEX_HOME，但未发现 Codex 程序入口；请使用官方安装在已关联目录安装或恢复入口，数据目录保持原位"))
	}
	if c.Command != "" {
		if !executable(c.Command) {
			return fail(fmt.Errorf("已关联的 Codex 程序入口不存在或不可执行；未改用其他命令，请重新确认入口"))
		}
		switch c.Channel {
		case "standalone":
		case "npm", "homebrew":
			return fail(fmt.Errorf("已发现 %s 渠道的 Codex；请使用原安装渠道更新，未运行官方脚本", c.Channel))
		default:
			return fail(fmt.Errorf("已发现 Codex 程序，但安装渠道无法确认；请确认原安装方式，未运行官方脚本或覆盖入口"))
		}
		if !update {
			return fail(fmt.Errorf("已发现 standalone Codex；请使用官方更新，不重复安装或覆盖现有入口"))
		}
	}
	bin := filepath.Join(s.UserHome, ".local", "bin")
	if c.Command != "" {
		// Updating an already-associated standalone installation must update
		// its actual entry, not silently install a second command elsewhere.
		if !filepath.IsAbs(c.Command) || invalidPath(c.Command) || filepath.Base(c.Command) != "codex" {
			return fail(fmt.Errorf("Codex 命令必须为绝对路径"))
		}
		bin = filepath.Dir(c.Command)
		if !executable(c.Command) {
			return fail(fmt.Errorf("Codex 命令不存在，未尝试重新安装"))
		}
		if channel(c.Command, c.Home) != "standalone" {
			return fail(fmt.Errorf("现有 Codex 命令已不属于关联目录的 standalone 安装；请重新关联并确认安装渠道"))
		}
	}
	if strings.Contains(bin, ":") || invalidPath(bin) {
		return fail(fmt.Errorf("Codex 程序目录不能包含 PATH 分隔符或控制字符"))
	}
	root := ""
	if s.Root != "" {
		var e error
		root, e = canonical(s.Root)
		resolved, e2 := canonical(bin)
		if e != nil || e2 != nil || inside(root, resolved) {
			return fail(fmt.Errorf("Codex 程序目录必须位于 Nido 软件目录之外"))
		}
	}
	homeGuard, err := guardLocation(c.Home)
	if err != nil {
		return fail(err)
	}
	binGuard, err := guardLocation(bin)
	if err != nil {
		return fail(err)
	}
	bin = binGuard.expected
	// The upstream package directory is not an install entry directory. Using
	// a release's own bin as CODEX_INSTALL_DIR can overwrite its executable
	// with the installer-created link. Require the ordinary user entry instead.
	if inside(filepath.Join(c.Home, "packages"), bin) {
		return fail(fmt.Errorf("Codex 程序入口不能使用内部 packages 目录；请关联正常的用户命令入口"))
	}
	entryGuard, err := guardEntry(filepath.Join(bin, "codex"), root)
	if err != nil {
		return fail(err)
	}
	if _, e := os.Lstat(filepath.Join(bin, "codex")); e == nil && c.Command == "" {
		return fail(fmt.Errorf("Codex 目标入口已存在；请先关联并确认安装渠道"))
	} else if e != nil && !os.IsNotExist(e) {
		return fail(fmt.Errorf("无法检查 Codex 目标入口"))
	}
	return installTarget{config: c, bin: bin, homeGuard: homeGuard, binGuard: binGuard, entryGuard: entryGuard}, nil
}

func (s Service) Install(c Config, proxy map[string]string) (Config, error) {
	return s.install(c, proxy, false)
}

func (s Service) install(c Config, proxy map[string]string, update bool) (Config, error) {
	target, err := s.prepareInstall(c, update)
	if err != nil {
		return c, err
	}
	c, bin := target.config, target.bin
	temp, err := os.MkdirTemp("", "nido-codex-installer-*")
	if err != nil {
		return c, fmt.Errorf("无法创建安装器临时目录")
	}
	defer os.RemoveAll(temp)
	env, err := s.installerEnv(c, proxy, temp, bin)
	if err != nil {
		return c, err
	}
	script, err := s.run("curl", []string{"-q", "-fsSL", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "10", "--max-time", "60", InstallerURL}, env, nil)
	if err != nil {
		return c, fmt.Errorf("获取 Codex 官方安装器失败")
	}
	if len(bytes.TrimSpace(script)) == 0 {
		return c, fmt.Errorf("Codex 官方安装器内容为空，未执行")
	}
	if checked, err := s.home(c.Home, false); err != nil || checked != c.Home {
		return c, fmt.Errorf("CODEX_HOME 在下载期间发生变化，未执行安装器")
	}
	for _, check := range []func() error{target.homeGuard.check, target.binGuard.check, target.entryGuard.check} {
		if err := check(); err != nil {
			return c, err
		}
	}
	if err = os.MkdirAll(c.Home, 0700); err != nil {
		return c, fmt.Errorf("无法创建 CODEX_HOME")
	}
	if err = os.MkdirAll(bin, 0755); err != nil {
		return c, fmt.Errorf("无法创建 Codex 程序目录")
	}
	if _, err = s.run("/bin/sh", []string{"-s"}, env, script); err != nil {
		return c, fmt.Errorf("Codex 官方安装器执行失败；现有数据未由 Nido 清理")
	}
	command := filepath.Join(bin, "codex")
	if !executable(command) {
		return c, fmt.Errorf("官方安装器返回成功，但未找到 Codex 可执行入口")
	}
	if channel(command, c.Home) != "standalone" {
		return c, fmt.Errorf("官方安装器返回成功，但入口未指向所选 CODEX_HOME 的 standalone 程序；未登记安装结果")
	}
	c.Command, c.Channel = command, "standalone"
	return c, nil
}

func (s Service) Update(c Config, proxy map[string]string) (Config, error) {
	return s.install(c, proxy, true)
}

func (s Service) Latest(proxy map[string]string) (string, error) {
	temp, err := os.MkdirTemp("", "nido-codex-version-*")
	if err != nil {
		return "", fmt.Errorf("无法创建版本查询临时目录")
	}
	defer os.RemoveAll(temp)
	env, err := s.installerEnv(Config{}, proxy, temp, "")
	if err != nil {
		return "", err
	}
	b, err := s.run("curl", []string{"-q", "-fsSL", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "10", "--max-time", "30", LatestURL}, env, nil)
	if err != nil {
		return "", fmt.Errorf("查询 Codex 最新版本失败")
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if json.Unmarshal(b, &release) != nil {
		return "", fmt.Errorf("Codex 最新版本响应无效")
	}
	version := strings.TrimPrefix(release.Tag, "rust-v")
	if !releaseVersion.MatchString(version) {
		return "", fmt.Errorf("Codex 最新版本格式无效")
	}
	return version, nil
}
