package stk

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const maxMetadata = 2 << 20

func readRegular(path string) ([]byte, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Size() > maxMetadata {
		return nil, fmt.Errorf("元数据必须是小型普通文件: %s", path)
	}
	if err := noSymlinkParents(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func noSymlinkParents(path string) error {
	for p := filepath.Dir(path); p != filepath.Dir(p); p = filepath.Dir(p) {
		s, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("父目录不能是符号链接或非目录: %s", p)
		}
	}
	return nil
}

func New(opts Options) (*Manager, error) {
	home := opts.Home
	if err := absolute(home); err != nil {
		return nil, err
	}
	if err := noSymlinkParents(filepath.Join(home, "state.json")); err != nil {
		return nil, err
	}
	u, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	if physical, err := filepath.EvalSymlinks(exe); err == nil {
		exe = physical
	}
	m := &Manager{Home: home, UserHome: u, Executable: exe, Path: os.Getenv("PATH"), Options: opts,
		Config: Config{Schema: Schema, Roots: map[string]string{}, Modules: map[string]ModuleConfig{}},
		State:  State{Schema: Schema, Roots: map[string]Root{}, Modules: map[string]Module{}, Generated: map[string]string{}}, Roots: map[string]string{}}
	cfg, ce := readRegular(filepath.Join(home, "config.toml"))
	state, se := readRegular(filepath.Join(home, "state.json"))
	m.inputs = map[string]Fingerprint{}
	for name, b := range map[string][]byte{"config.toml": cfg, "state.json": state} {
		f := Fingerprint{Kind: "absent"}
		if s, err := os.Lstat(filepath.Join(home, name)); err == nil {
			f = Fingerprint{Kind: "file", Digest: digest(b), Mode: uint32(s.Mode().Perm())}
		}
		m.inputs[name] = f
	}
	if errors.Is(ce, os.ErrNotExist) && errors.Is(se, os.ErrNotExist) {
		m.Roots["local"] = filepath.Dir(home)
		m.Roots["stk"] = home
	} else {
		if ce != nil {
			return nil, fmt.Errorf("读取配置: %w", ce)
		}
		if se != nil {
			return nil, fmt.Errorf("读取管理清单: %w", se)
		}
		if err := toml.NewDecoder(bytes.NewReader(cfg)).DisallowUnknownFields().Decode(&m.Config); err != nil {
			return nil, fmt.Errorf("配置无效: %w", err)
		}
		dec := json.NewDecoder(bytes.NewReader(state))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m.State); err != nil {
			return nil, fmt.Errorf("管理清单无效: %w", err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("管理清单含额外内容")
		}
		if m.Config.Schema != Schema || m.State.Schema != Schema || !validID(m.State.Instance) || m.State.Modules == nil || m.State.Roots == nil || m.Config.Modules == nil {
			return nil, fmt.Errorf("不支持或不完整的配置/管理清单")
		}
		m.Initialized = true
		if m.State.Generated == nil {
			return nil, fmt.Errorf("管理清单缺少文件归属信息")
		}
		for name, root := range m.State.Roots {
			if !validName(name) || absolute(root.Path) != nil || root.RelativeHome != "" && (filepath.IsAbs(root.RelativeHome) || filepath.Clean(root.RelativeHome) != root.RelativeHome) {
				return nil, fmt.Errorf("无效目录根: %s", name)
			}
			m.Roots[name] = root.Path
			if configured, ok := m.Config.Roots[name]; ok && configured != root.Path {
				m.Roots[name] = configured
			} else if root.RelativeHome != "" && !directory(root.Path) {
				candidate := filepath.Clean(filepath.Join(home, root.RelativeHome))
				// This relation was recorded at setup, and travels with this instance.
				m.Roots[name] = candidate
			}
		}
	}
	for name, path := range m.Config.Roots {
		if !validName(name) || name == "stk" || absolute(path) != nil {
			return nil, fmt.Errorf("无效配置目录根: %s", name)
		}
		if _, ok := m.Roots[name]; !ok {
			m.Roots[name] = path
		}
	}
	for name, path := range opts.Roots {
		if !validName(name) || name == "stk" {
			return nil, fmt.Errorf("不可覆盖的目录根: %s", name)
		}
		if err := absolute(path); err != nil {
			return nil, err
		}
		m.Roots[name] = path
	}
	m.Roots["stk"] = home
	if m.Roots["local"] == "" {
		return nil, fmt.Errorf("管理清单缺少 local 目录根")
	}
	for name, path := range m.Roots {
		if err := absolute(path); err != nil {
			return nil, fmt.Errorf("目录根 %s: %w", name, err)
		}
	}
	seenRoots := map[string]string{}
	for name, path := range m.Roots {
		if previous, ok := seenRoots[path]; ok {
			return nil, fmt.Errorf("目录根 %s 与 %s 重复，路径归属有歧义", name, previous)
		}
		seenRoots[path] = name
	}
	for path, ref := range m.State.Aliases {
		if absolute(path) != nil || ref.Path == "" {
			return nil, fmt.Errorf("无效的历史路径映射")
		}
		if _, err := m.resolve(ref); err != nil {
			return nil, err
		}
	}
	if _, old := m.State.Generated["stk"]; old {
		if _, current := m.State.Generated["cdet"]; current {
			return nil, fmt.Errorf("管理程序存在两个归属记录，拒绝猜测入口")
		}
	}
	allowed := map[string]bool{"stk": true, "cdet": true, "env.sh": true}
	if m.State.Entry != nil {
		path, err := m.resolve(m.State.Entry.Path)
		if err != nil || filepath.Base(path) != "cdet" || within(m.Home, path) || !validDigest(m.State.Generated[m.selfPath()]) || m.State.Entry.Target != (Ref{Root: "stk", Path: m.selfPath()}) {
			return nil, fmt.Errorf("管理命令入口记录无效")
		}
	}
	for _, name := range []string{"proxy", "codex", "gds"} {
		for _, path := range moduleFiles(name) {
			allowed[path] = true
		}
	}
	for path, hash := range m.State.Generated {
		if !allowed[path] || !validDigest(hash) {
			return nil, fmt.Errorf("生成文件归属记录无效: %s", path)
		}
	}
	for name, module := range m.State.Modules {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知已登记模块: %s", name)
		}
		if module.Binary.Path == "" || (name == "codex" || name == "proxy") && module.Home.Path == "" || name == "proxy" && module.Config.Path == "" {
			return nil, fmt.Errorf("%s 缺少必要的程序或数据引用", name)
		}
		if _, ok := m.Config.Modules[name]; !ok {
			return nil, fmt.Errorf("%s 缺少配置记录", name)
		}
		if !validDigest(module.BinaryDigest) {
			return nil, fmt.Errorf("%s 缺少有效的程序身份摘要", name)
		}
		if module.OwnedBinary {
			binary, err := m.resolve(module.Binary)
			if err != nil || binary != filepath.Join(m.Home, m.toolPath(name)) || m.State.Generated[m.toolPath(name)] != module.BinaryDigest {
				return nil, fmt.Errorf("%s 的程序所有权记录不一致", name)
			}
		}
		refs := []Ref{module.Binary, module.Home, module.Config, module.Service.Controller, module.Service.Config, module.Service.Definition}
		for _, link := range module.Links {
			refs = append(refs, link.Path, link.Target)
		}
		for _, ref := range refs {
			if _, err := m.resolve(ref); err != nil {
				return nil, err
			}
		}
	}
	for name, cfg := range m.Config.Modules {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知配置模块: %s", name)
		}
		if cfg.Endpoint != "" {
			if err := validEndpoint(cfg.Endpoint); err != nil {
				return nil, err
			}
		}
	}
	for _, ref := range m.State.ShellFiles {
		if _, err := m.resolve(ref); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) rootRecords() map[string]Root {
	result := map[string]Root{}
	local := m.Roots["local"]
	for name, path := range m.Roots {
		r := Root{Path: path}
		// Only the installation and its declared fast-storage siblings follow
		// the home. Data on an independent mount always needs its own mapping.
		if name == "stk" || name == "local" && within(path, m.Home) || name == "projects" && local != "" && within(filepath.Dir(local), path) && within(filepath.Dir(local), m.Home) {
			r.RelativeHome, _ = filepath.Rel(m.Home, path)
		}
		mount := mountAt(path)
		if mount.Target != "/" {
			r.Mount, r.FSType = mount.Target, mount.FSType
		}
		result[name] = r
	}
	return result
}

func (m *Manager) validateRoots() error {
	for name, path := range m.Roots {
		if name != "local" && name != "stk" {
			if _, explicit := m.Options.Roots[name]; !explicit {
				continue
			}
		}
		if name == "stk" && !m.Initialized {
			continue
		}
		s, err := os.Stat(path)
		if err != nil || !s.IsDir() {
			return fmt.Errorf("目录根 %s 不可用: %s；不会创建替代空环境", name, path)
		}
		if old, ok := m.State.Roots[name]; ok && old.Mount != "" && mountAt(path).FSType != old.FSType {
			return fmt.Errorf("目录根 %s 的挂载不可用或类型变化: %s", name, path)
		}
	}
	return nil
}

func (m *Manager) rootsChanged() bool {
	for name, root := range m.State.Roots {
		if m.Roots[name] != root.Path {
			return true
		}
	}
	return false
}

func (m *Manager) requireCurrentRoots() error {
	if err := m.validateRoots(); err != nil {
		return err
	}
	if m.rootsChanged() {
		return fmt.Errorf("目录根已变化，请先 repair 重建路径，再更新、回滚或执行组件")
	}
	return nil
}

func instanceID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

func knownModule(name string) bool { return name == "proxy" || name == "codex" || name == "gds" }

func (m *Manager) clone() *Manager {
	b, _ := json.Marshal(m.State)
	var state State
	_ = json.Unmarshal(b, &state)
	c := m.Config
	c.Roots = map[string]string{}
	c.Modules = map[string]ModuleConfig{}
	for k, v := range m.Config.Modules {
		c.Modules[k] = v
	}
	n := *m
	n.State = state
	n.Config = c
	return &n
}

func (m *Manager) metadata() (map[string][]byte, error) {
	if m.State.Instance == "" {
		id, err := instanceID()
		if err != nil {
			return nil, err
		}
		m.State.Instance = id
	}
	if m.State.Aliases == nil {
		m.State.Aliases = map[string]Ref{}
	}
	for name, root := range m.State.Roots {
		if current := m.Roots[name]; current != "" && current != root.Path {
			m.State.Aliases[root.Path] = Ref{Root: name, Path: "."}
		}
	}
	m.State.Roots = m.rootRecords()
	m.Config.Roots = map[string]string{}
	for k, v := range m.Roots {
		if k != "stk" {
			m.Config.Roots[k] = v
		}
	}
	c, err := toml.Marshal(m.Config)
	if err != nil {
		return nil, err
	}
	s, err := json.MarshalIndent(m.State, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"config.toml": c, "state.json": append(s, '\n')}, nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

type Mount struct{ Target, FSType string }

func parseMounts(data string, path string) Mount {
	best := Mount{Target: "/"}
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 {
			continue
		}
		left, right := strings.Fields(parts[0]), strings.Fields(parts[1])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		target := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\134", "\\").Replace(left[4])
		if within(target, path) && len(target) >= len(best.Target) {
			best = Mount{target, right[0]}
		}
	}
	return best
}

func mountAt(path string) Mount {
	b, _ := os.ReadFile("/proc/self/mountinfo")
	return parseMounts(string(b), path)
}
