package stk

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func executable(path string) bool {
	s, e := os.Stat(path)
	return e == nil && s.Mode().IsRegular() && s.Mode().Perm()&0111 != 0
}

func regular(path string) bool { s, e := os.Stat(path); return e == nil && s.Mode().IsRegular() }

func isManagedLauncher(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 512))
	return bytes.Contains(b, []byte("# server-toolkit-go managed launcher"))
}

func (m *Manager) codexHome() string {
	if m.Options.CodexHome != "" {
		return m.Options.CodexHome
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = filepath.Join(m.Roots["local"], ".codex")
	}
	if os.Getenv("CODEX_HOME") == "" && !directory(home) && directory(filepath.Join(m.UserHome, ".codex")) {
		home = filepath.Join(m.UserHome, ".codex")
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		return real
	}
	return home
}

func (m *Manager) proxyData() string {
	if m.Options.ProxyData != "" {
		return m.Options.ProxyData
	}
	legacy := filepath.Join(m.Roots["local"], "server-toolkit", "proxy", "workdir")
	if directory(legacy) {
		return legacy
	}
	return filepath.Join(m.Home, "run", "proxy")
}

func (m *Manager) proxyConfig() string {
	if m.Options.ProxyConfig != "" {
		return m.Options.ProxyConfig
	}
	for _, p := range []string{filepath.Join(m.Home, "secrets", "proxy.yaml"), filepath.Join(m.Roots["local"], "config", "server-toolkit", "proxy.yaml"), filepath.Join(m.Roots["local"], "server-toolkit", "proxy", "config.yaml")} {
		if regular(p) {
			return p
		}
	}
	return ""
}

func (m *Manager) realBinary(path string) (string, map[string]string, error) {
	links := map[string]string{}
	current := path
	for i := 0; i < 32; i++ {
		// Resolve parent links too (e.g. packages/standalone/current/bin/codex).
		parts := strings.Split(strings.TrimPrefix(current, string(os.PathSeparator)), string(os.PathSeparator))
		prefix := string(os.PathSeparator)
		changed := false
		for j, part := range parts {
			prefix = filepath.Join(prefix, part)
			s, e := os.Lstat(prefix)
			if e != nil {
				return "", nil, e
			}
			if s.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, e := os.Readlink(prefix)
			if e != nil {
				return "", nil, e
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(prefix), target)
			}
			rebased := m.rebase(filepath.Clean(target))
			links[prefix] = rebased
			current = filepath.Join(append([]string{rebased}, parts[j+1:]...)...)
			changed = true
			break
		}
		if changed {
			continue
		}
		if !executable(current) {
			return "", nil, fmt.Errorf("不是可执行普通文件: %s", current)
		}
		if filepath.Base(current) == "codex-launcher.sh" || filepath.Base(current) == "stk" || filepath.Base(current) == "cdet" || current == m.Executable || within(filepath.Join(m.Home, "bin"), current) {
			return "", nil, fmt.Errorf("候选为 STK 启动入口")
		}
		if isManagedLauncher(current) {
			return "", nil, fmt.Errorf("候选为另一个 STK 实例的启动器")
		}
		return current, links, nil
	}
	return "", nil, fmt.Errorf("符号链接循环: %s", path)
}

func (m *Manager) candidate(name, path, home, config string) (Candidate, error) {
	if err := absolute(path); err != nil {
		return Candidate{}, err
	}
	resolved, links, err := m.realBinary(path)
	if err != nil {
		return Candidate{}, err
	}
	provider := "external"
	if strings.Contains(resolved, "node_modules") {
		provider = "npm"
	}
	if within(filepath.Join(m.Home, "tools"), resolved) {
		provider = "stk"
	}
	if name == "codex" && home != "" && within(filepath.Join(home, "packages", "standalone"), resolved) {
		provider = "standalone"
	}
	return Candidate{Binary: resolved, Home: home, Config: config, Provider: provider, Links: links, Entry: path}, nil
}

func (m *Manager) Discover(name string) ([]Candidate, error) {
	if !knownModule(name) {
		return nil, fmt.Errorf("未知模块: %s", name)
	}
	var paths []string
	home, config := "", ""
	if old, ok := m.State.Modules[name]; ok {
		located, _, err := m.locatedModule(name, old)
		if err != nil {
			return nil, err
		}
		old = located
		home, _ = m.resolve(old.Home)
		config, _ = m.resolve(old.Config)
		p, err := m.resolve(old.Binary)
		if err != nil {
			return nil, err
		}
		if m.Options.Binaries[name] == "" {
			if c, e := m.candidate(name, p, home, config); e == nil {
				return []Candidate{c}, nil
			}
			// Missing registered installation is not permission to select another.
			return nil, fmt.Errorf("%s 已登记的安装不可用: %s；请修复根目录或明确指定 --binary", name, p)
		}
	}
	if name == "codex" {
		if m.Options.CodexHome != "" {
			home = m.Options.CodexHome
		}
		if home == "" {
			home = m.codexHome()
		}
		if directory(home) {
			real, e := filepath.EvalSymlinks(home)
			if e != nil {
				return nil, e
			}
			home = real
		}
		// Prefer a valid active entry over inactive package versions.
		for _, p := range []string{filepath.Join(home, "bin", "codex"), filepath.Join(home, "packages", "standalone", "current", "bin", "codex")} {
			if c, e := m.candidate(name, p, home, config); e == nil && m.Options.Binaries[name] == "" {
				return []Candidate{c}, nil
			}
		}
		paths = append(paths, filepath.Join(home, "packages", "server-toolkit", "bin", "codex"))
		versions, _ := filepath.Glob(filepath.Join(home, "packages", "standalone", "*", "bin", "codex"))
		paths = append(paths, versions...)
	}
	if name == "proxy" {
		if m.Options.ProxyData != "" {
			home = m.Options.ProxyData
		}
		if home == "" {
			home = m.proxyData()
		}
		if m.Options.ProxyConfig != "" {
			config = m.Options.ProxyConfig
		}
		if config == "" {
			config = m.proxyConfig()
		}
		paths = append(paths, filepath.Join(m.Roots["local"], "server-toolkit", "proxy", "core", "mihomo"), filepath.Join(m.Home, "tools", "proxy", "mihomo"))
	}
	if name == "gds" {
		paths = append(paths, filepath.Join(m.Roots["local"], "server-toolkit", "gds", "gds.sh"))
	}
	if p := m.Options.Binaries[name]; p != "" {
		c, e := m.candidate(name, p, home, config)
		if e != nil {
			return nil, e
		}
		return []Candidate{c}, nil
	}
	command := moduleCommand(name)
	for _, dir := range filepath.SplitList(m.Path) {
		if filepath.IsAbs(dir) {
			paths = append(paths, filepath.Join(dir, command))
		}
	}
	for _, root := range m.Options.Search {
		if err := absolute(root); err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(root, command), filepath.Join(root, "bin", command))
		if name == "codex" {
			paths = append(paths, filepath.Join(root, ".codex", "bin", "codex"))
		}
	}
	seen := map[string]bool{}
	var found []Candidate
	for _, p := range paths {
		c, e := m.candidate(name, p, home, config)
		if e == nil && !seen[c.Binary] {
			found = append(found, c)
			seen[c.Binary] = true
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Binary < found[j].Binary })
	return found, nil
}

func directory(path string) bool { s, e := os.Stat(path); return e == nil && s.IsDir() }

func (m *Manager) Status() []Status {
	var rows []Status
	for _, name := range []string{"proxy", "codex", "gds"} {
		module, ok := m.State.Modules[name]
		if !ok {
			rows = append(rows, Status{Module: name, State: "未管理"})
			continue
		}
		if !m.Config.Modules[name].Enabled {
			rows = append(rows, Status{Module: name, Managed: true, State: "未启用"})
			continue
		}
		path, err := m.resolve(module.Binary)
		row := Status{Module: name, Managed: true, State: "可用（运行态未查询）", Binary: path}
		if err != nil {
			row.State = "路径异常"
			row.Detail = err.Error()
		} else if !executable(path) {
			row.State = "安装或存储不可用"
		}
		for _, ref := range []Ref{module.Home, module.Config} {
			p, e := m.resolve(ref)
			if e != nil {
				row.State = "路径异常"
				row.Detail = e.Error()
			} else if p != "" {
				if _, e := os.Stat(p); e != nil {
					row.State = "配置或数据目录不可用"
				}
				if ref == module.Home && !directory(p) || ref == module.Config && !regular(p) {
					row.State = "配置或数据目录不可用"
				}
			}
		}
		if m.rootsChanged() {
			row.State = "路径待重建"
			row.Detail = "目录根已变化，请先 repair"
		}
		rows = append(rows, row)
	}
	return rows
}

// FindHome only inspects conventional relative locations, never recursive data.
func FindHome(hint, instance string, search []string) (string, error) {
	var found []string
	seen := map[string]bool{}
	for _, base := range append([]string{hint}, search...) {
		if base == "" {
			continue
		}
		for _, p := range []string{base, filepath.Join(base, "cdetoolkit"), filepath.Join(base, ".local", "cdetoolkit"), filepath.Join(base, "stk"), filepath.Join(base, ".local", "stk")} {
			if seen[p] {
				continue
			}
			seen[p] = true
			b, e := readRegular(filepath.Join(p, "state.json"))
			if e != nil {
				continue
			}
			var s State
			if jsonDecode(b, &s) != nil || s.Schema != Schema || s.Instance == "" {
				continue
			}
			if instance == "" || s.Instance == instance {
				found = append(found, p)
			}
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("找到 %d 个管理目录候选，请使用 --home 明确指定: %v", len(found), found)
	}
	return found[0], nil
}
