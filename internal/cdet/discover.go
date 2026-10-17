package cdet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

// Candidate is transient. Its source paths are never copied into State.
type Candidate struct {
	Module string `json:"module"`
	Source string `json:"source,omitempty"`
	Binary string `json:"binary,omitempty"`
	Data   string `json:"data,omitempty"`
}

func physical(p string) (string, error) {
	if e := absolute(p); e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}

func (m *Manager) pathBinaries(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(m.Path) {
		if !filepath.IsAbs(d) {
			continue
		}
		p, e := physical(filepath.Join(d, moduleCommand(name)))
		if e != nil || !executable(p) || within(m.Prefix, p) || within(m.Store, p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func (m *Manager) candidate(name, source string, binaries []string) (Candidate, error) {
	c := Candidate{Module: name, Source: source}
	if source != "" {
		p, e := physical(source)
		if e != nil || !directory(p) {
			return c, fmt.Errorf("%s 迁入源不存在或不是目录: %s", name, source)
		}
		home, homeErr := os.Stat(m.UserHome)
		sourceInfo, sourceErr := os.Stat(p)
		sameHome := p == m.UserHome || homeErr == nil && sourceErr == nil && os.SameFile(home, sourceInfo)
		if within(m.Store, p) || within(p, m.Store) || within(m.Prefix, p) || within(p, m.Prefix) || sameHome {
			return c, fmt.Errorf("迁入源与受管目录重叠或范围过大")
		}
		// The selected directory is always the complete data root. Inferring
		// data/ or .codex/ from its name can silently omit credentials/sessions.
		c.Source, c.Data = p, p
		// Codex owns its program installation. Import HOME opaquely, then ask
		// its official installer to establish a working managed command.
		if name == "codex" {
			if sqliteHome := os.Getenv("CODEX_SQLITE_HOME"); sqliteHome != "" {
				root, e := physical(sqliteHome)
				if e != nil || root != c.Data {
					return c, fmt.Errorf("CODEX_SQLITE_HOME 指向独立数据目录，当前迁入源不完整")
				}
			}
			return c, nil
		}
		var probes []string
		if name == "proxy" {
			probes = []string{filepath.Join(p, "bin", "mihomo"), filepath.Join(p, "mihomo"), filepath.Join(p, "core", "mihomo")}
		} else {
			probes = []string{filepath.Join(p, "bin", "gds"), filepath.Join(p, "gds"), filepath.Join(p, "scheduler")}
		}
		for _, probe := range probes {
			if absent(probe) {
				continue
			}
			real, er := physical(probe)
			if er != nil || !executable(real) {
				return c, fmt.Errorf("%s 现有程序入口失效，请明确整理迁入源: %s", name, probe)
			}
			c.Binary = real
			break
		}
	}
	if name == "codex" {
		return c, nil
	}
	if c.Binary == "" && len(binaries) > 1 {
		return c, fmt.Errorf("%s 在 PATH 中有多个安装，请用 --from %s=目录选择迁入源", name, name)
	}
	if c.Binary == "" && len(binaries) == 1 {
		c.Binary = binaries[0]
	}
	if c.Binary != "" {
		b, e := readRegularLarge(c.Binary)
		if e != nil {
			return c, e
		}
		if name != "gds" {
			if e = verifyPlatform(b, m.releases().OS+"/"+m.releases().Arch); e != nil {
				return c, fmt.Errorf("%s 迁入程序必须是当前平台的独立 ELF/Mach-O 程序", name)
			}
		} else if bytes.HasPrefix(b, []byte("#!")) {
			line, _, _ := bytes.Cut(b, []byte("\n"))
			if bytes.Contains(line, []byte("node")) || bytes.Contains(line, []byte("python")) {
				return c, fmt.Errorf("%s 是依赖外部语言环境的入口，请选择包含独立可执行程序的迁入源", name)
			}
		}
	}
	return c, nil
}

func (m *Manager) Discover(name string) (Candidate, error) {
	if !knownModule(name) {
		return Candidate{}, fmt.Errorf("未知组件: %s", name)
	}
	if source := m.Options.From[name]; source != "" {
		return m.candidate(name, source, m.pathBinaries(name))
	}
	if m.Options.Fresh {
		return Candidate{Module: name}, nil
	}
	var dirs []string
	if name == "codex" {
		if p := os.Getenv("CODEX_HOME"); p != "" {
			return m.candidate(name, p, m.pathBinaries(name))
		}
		dirs = []string{filepath.Join(m.UserHome, ".codex")}
	} else if name == "proxy" {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(m.UserHome, ".config")
		}
		dirs = []string{filepath.Join(xdg, "mihomo"), filepath.Join(xdg, "clash")}
	}
	seen := map[string]bool{}
	var found []string
	for _, p := range dirs {
		if absent(p) {
			continue
		}
		real, e := physical(p)
		if e != nil {
			return Candidate{}, fmt.Errorf("现有组件目录失效: %s", p)
		}
		if within(m.Store, real) {
			continue
		}
		if !seen[real] {
			found = append(found, real)
			seen[real] = true
		}
	}
	if len(found) > 1 {
		return Candidate{}, fmt.Errorf("%s 存在多个数据目录，请用 --from %s=目录选择迁入源", name, name)
	}
	source := ""
	if len(found) == 1 {
		source = found[0]
	}
	return m.candidate(name, source, m.pathBinaries(name))
}

func (m *Manager) Scan() ([]Candidate, error) {
	var out []Candidate
	for _, name := range []string{"codex", "proxy"} {
		if c, ok := m.State.Components[name]; ok && c.Installed {
			continue
		}
		c, e := m.Discover(name)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}

func validateImportConfig(name, data string) error {
	file := "config.toml"
	if name == "proxy" {
		file = "config.yaml"
	}
	p := filepath.Join(data, file)
	if absent(p) {
		return nil
	}
	b, e := readRegular(p)
	if e != nil {
		return e
	}
	if bytes.Contains(b, []byte(data+string(os.PathSeparator))) {
		return fmt.Errorf("%s 配置仍引用迁入源的绝对路径；请在独立副本中改用相对路径再迁入，原件未改动", name)
	}
	if name == "codex" {
		var config map[string]any
		if e = toml.Unmarshal(b, &config); e != nil {
			return fmt.Errorf("Codex 配置不是有效 TOML")
		}
		if _, ok := config["sqlite_home"]; ok {
			return fmt.Errorf("Codex sqlite_home 显式指定了数据库位置，迁入后将依赖旧路径；请先在独立副本中整理为默认数据根，原件未改动")
		}
		return nil
	}
	if name != "proxy" {
		return nil
	}
	var root yaml.Node
	if e = yaml.Unmarshal(b, &root); e != nil {
		return fmt.Errorf("Proxy 配置不是有效 YAML")
	}
	var walk func(*yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == "path" || k.Value == "external-ui" {
					seen := map[*yaml.Node]bool{}
					for v.Kind == yaml.AliasNode {
						if seen[v] || v.Alias == nil {
							return fmt.Errorf("Proxy 资源路径别名无效")
						}
						seen[v] = true
						v = v.Alias
					}
					if v.Kind == yaml.ScalarNode && v.Value != "" && (filepath.IsAbs(v.Value) || strings.HasPrefix(v.Value, "~") || !within(data, filepath.Join(data, v.Value))) {
						return fmt.Errorf("Proxy 配置资源路径超出数据目录；请在独立副本中改用目录内的相对路径再迁入")
					}
				}
			}
		}
		for _, child := range n.Content {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(&root)
}

func sortedNames(m map[string]Component) []string {
	var names []string
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
