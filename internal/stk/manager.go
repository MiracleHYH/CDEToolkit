package stk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func (m *Manager) generated(plan *Plan, relative string, data []byte, mode os.FileMode) error {
	path := filepath.Join(m.Home, relative)
	if !within(m.Home, path) {
		return fmt.Errorf("生成文件路径越界")
	}
	before, e := fingerprint(path)
	if e != nil {
		return e
	}
	if before.Kind != "absent" {
		if old, ok := m.State.Generated[relative]; !ok || before.Kind != "file" || old != before.Digest {
			return fmt.Errorf("文件不属于 STK 或已被外部修改: %s", path)
		}
	}
	plan.Guards = append(plan.Guards, Guard{Path: path, Expected: before})
	if e = plan.add(path, "write", "生成受管入口或服务配置", data, "", mode); e != nil {
		return e
	}
	m.State.Generated[relative] = digest(data)
	return nil
}

func (m *Manager) buildPlan(names []string, repair bool) (*Plan, error) {
	if repair && !m.Initialized {
		return nil, fmt.Errorf("尚未登记，先执行 setup")
	}
	if err := m.validateRoots(); err != nil {
		return nil, err
	}
	n := m.clone()
	plan := &Plan{Home: m.Home, Instance: m.State.Instance}
	for _, path := range m.Options.ShellFiles {
		if err := absolute(path); err != nil {
			return nil, err
		}
		ref := n.ref(path)
		exists := false
		for _, old := range n.State.ShellFiles {
			if old == ref {
				exists = true
			}
		}
		if !exists {
			n.State.ShellFiles = append(n.State.ShellFiles, ref)
		}
	}
	if len(names) == 0 {
		for name := range m.State.Modules {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		if repair {
			return m.complete(plan, n, true)
		}
		return nil, fmt.Errorf("请指定 proxy、codex 或可选 gds")
	}
	for _, name := range names {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知模块: %s", name)
		}
		if _, ok := n.State.Modules[name]; repair && !ok {
			return nil, fmt.Errorf("%s 未接管，repair 不会安装", name)
		}
		if !repair {
			if artifact := m.Options.Artifacts[name]; artifact != "" {
				if _, exists := n.State.Modules[name]; exists {
					return nil, fmt.Errorf("已管理组件的替换需显式 update；setup 不升级")
				}
				if e := n.installArtifact(plan, name, artifact); e != nil {
					return nil, e
				}
			} else {
				candidates, e := m.Discover(name)
				if e != nil {
					return nil, e
				}
				if len(candidates) != 1 {
					return nil, fmt.Errorf("%s 有 %d 个有效候选；请使用 --binary %s=/absolute/path（缺失时可 --install %s=FILE）: %v", name, len(candidates), name, name, candidatePaths(candidates))
				}
				c := candidates[0]
				mod, had := n.State.Modules[name]
				if had {
					located, moves, e := n.locatedModule(name, mod)
					if e != nil {
						return nil, e
					}
					mod = located
					for _, move := range moves {
						n.rememberPath(move.old, move.current)
					}
				}
				binaryChanged := had && mod.Binary != n.ref(c.Binary)
				if binaryChanged {
					mod.OwnedBinary, mod.Release, mod.Links = false, nil, nil
				}
				mod.Binary = n.ref(c.Binary)
				mod.Home = n.ref(c.Home)
				mod.Config = n.ref(c.Config)
				mod.Provider = c.Provider
				if !had {
					mod.OwnedBinary = false
				}
				if name == "codex" && !binaryChanged {
					discoverCodexLinks(&c)
				}
				for path, target := range c.Links {
					// An entry file is part of the selected component. A shared
					// parent directory link outside its home is never taken over.
					if path != c.Entry && (name != "codex" || c.Home == "" || !within(c.Home, path)) {
						continue
					}
					addLink(&mod, ManagedLink{n.ref(path), n.ref(target)})
				}
				n.State.Modules[name] = mod
			}
			mod := n.State.Modules[name]
			if service, ok := m.Options.Services[name]; ok {
				if err := validateService(service); err != nil {
					return nil, err
				}
				if service.Controller.Path != "" {
					service.Controller = n.ref(service.Controller.Path)
				}
				if service.Config.Path != "" {
					service.Config = n.ref(service.Config.Path)
				}
				if service.Definition.Path != "" {
					service.Definition = n.ref(service.Definition.Path)
				}
				mod.Service = service
			}
			mod.ManageConfig = mod.ManageConfig || m.Options.ManageConfig
			n.State.Modules[name] = mod
			cfg := n.Config.Modules[name]
			cfg.Enabled = true
			if name == "proxy" || name == "codex" {
				if m.Options.Endpoint != "" {
					cfg.Endpoint = m.Options.Endpoint
				}
			}
			if endpoint := m.Options.Endpoints[name]; endpoint != "" {
				if err := validEndpoint(endpoint); err != nil {
					return nil, err
				}
				cfg.Endpoint = endpoint
			}
			n.Config.Modules[name] = cfg
		}
	}
	return m.complete(plan, n, repair)
}

func (m *Manager) complete(plan *Plan, n *Manager, repair bool) (*Plan, error) {
	// Include every managed module in the generated environment and service
	// templates, even when setup added only one module.
	all := make([]string, 0, len(n.State.Modules))
	for name := range n.State.Modules {
		all = append(all, name)
	}
	sort.Strings(all)
	for _, name := range all {
		if !n.Config.Modules[name].Enabled {
			continue
		}
		mod := n.State.Modules[name]
		if repair {
			located, moves, e := n.locatedModule(name, mod)
			if e != nil {
				return nil, e
			}
			mod = located
			for _, move := range moves {
				n.rememberPath(move.old, move.current)
			}
		}
		binary, e := n.resolve(mod.Binary)
		if e != nil {
			return nil, e
		}
		home, e := n.resolve(mod.Home)
		if e != nil {
			return nil, e
		}
		config, e := n.resolve(mod.Config)
		if e != nil {
			return nil, e
		}
		if repair {
			if p := m.Options.Binaries[name]; p != "" {
				candidate, e := m.candidate(name, p, home, config)
				if e != nil {
					return nil, e
				}
				binary = candidate.Binary
				if mod.Binary != n.ref(binary) {
					mod.OwnedBinary, mod.Release, mod.Links = false, nil, nil
				}
				mod.Binary = n.ref(binary)
				mod.Provider = candidate.Provider
			}
			if !executable(binary) && len(m.Options.Search) > 0 {
				command := moduleCommand(name)
				matches := map[string]bool{}
				for _, root := range m.Options.Search {
					if err := absolute(root); err != nil {
						return nil, err
					}
					for _, p := range []string{filepath.Join(root, command), filepath.Join(root, "bin", command)} {
						if candidate, e := m.candidate(name, p, home, config); e == nil {
							if fp, e := fingerprint(candidate.Binary); e == nil && fp.Digest == mod.BinaryDigest {
								matches[candidate.Binary] = true
							}
						}
					}
				}
				if len(matches) != 1 {
					return nil, fmt.Errorf("%s 找到 %d 个身份匹配的安装，拒绝猜测", name, len(matches))
				}
				for p := range matches {
					binary = p
					mod.Binary = n.ref(p)
				}
			}
		}
		pendingBinary := false
		for _, d := range plan.Downloads {
			if d.Target == binary && d.Needed && plan.Deferred {
				pendingBinary = true
			}
		}
		for _, change := range plan.Changes {
			if change.Path == binary && change.Kind == "write" {
				pendingBinary = true
			}
		}
		if !pendingBinary {
			fp, e := fingerprint(binary)
			if e != nil || !executable(binary) {
				return nil, fmt.Errorf("%s 已登记程序不可用；请映射根目录、提供 --search 或明确 --binary，repair 不会重装", name)
			}
			if old, ok := m.State.Modules[name]; ok && m.Options.Binaries[name] == "" {
				oldPath := old.Binary.Path
				if old.Binary.Root != "" {
					oldPath = filepath.Join(m.State.Roots[old.Binary.Root].Path, old.Binary.Path)
				}
				if oldPath != binary && old.BinaryDigest != fp.Digest {
					return nil, fmt.Errorf("%s 新位置的程序身份与记录不符；请用 --binary 明确选择", name)
				}
			}
			if mod.OwnedBinary {
				if e = n.checkGenerated(n.toolPath(name)); e != nil {
					return nil, e
				}
				if binary != filepath.Join(n.Home, n.toolPath(name)) {
					return nil, fmt.Errorf("%s 的所有权路径不符", name)
				}
			}
			mod.BinaryDigest = fp.Digest
		}
		n.State.Modules[name] = mod
		if name == "codex" && !directory(home) {
			creatingHome := false
			for _, change := range plan.Changes {
				if change.Path == home && change.Kind == "dir" {
					creatingHome = true
				}
			}
			if !creatingHome {
				return nil, fmt.Errorf("Codex 主目录不存在；请指定 --codex-home，避免覆盖原数据: %s", home)
			}
		}
		if name == "proxy" {
			_, previouslyManaged := m.State.Modules[name]
			if !directory(home) && (previouslyManaged || home != filepath.Join(m.Home, "run", "proxy") || m.Options.ProxyData != "") {
				return nil, fmt.Errorf("已有代理数据目录不可用: %s", home)
			}
			if !regular(config) {
				return nil, fmt.Errorf("代理需要现有配置 --proxy-config FILE；不会生成包含虚构凭据的配置")
			}
			endpoint, e := proxyEndpoint(config, n.Config.Modules[name].Endpoint)
			if e != nil {
				return nil, e
			}
			cfg := n.Config.Modules[name]
			cfg.Endpoint = endpoint
			n.Config.Modules[name] = cfg
		}
		if name == "proxy" || name == "gds" {
			if definition, e := n.resolve(mod.Service.Definition); e != nil {
				return nil, e
			} else if definition != "" {
				before, e := readRegular(definition)
				if e != nil {
					return nil, e
				}
				after, e := n.rewriteService(mod.Service, before)
				if e != nil {
					return nil, e
				}
				if string(before) != string(after) {
					s, _ := os.Stat(definition)
					plan.guardBytes(definition, before, s.Mode().Perm())
					if e = plan.add(definition, "write", "重建显式接管的服务定义路径（不加载运行态）", after, "", s.Mode().Perm()); e != nil {
						return nil, e
					}
				}
			}
		}

		for _, link := range mod.Links {
			path, _ := n.resolve(link.Path)
			target, _ := n.resolve(link.Target)
			s, e := os.Lstat(path)
			if e == nil && s.Mode()&os.ModeSymlink == 0 {
				return nil, fmt.Errorf("原链接已变成普通文件，拒绝接管: %s", path)
			}
			if e != nil && !os.IsNotExist(e) {
				return nil, e
			}
			rel, e := filepath.Rel(filepath.Dir(path), target)
			if e != nil {
				return nil, e
			}
			if s != nil {
				actual, e := os.Readlink(path)
				if e != nil {
					return nil, e
				}
				plan.Guards = append(plan.Guards, Guard{Path: path, Expected: Fingerprint{Kind: "link", Digest: actual, Mode: uint32(s.Mode().Perm())}})
				previouslyManaged := false
				oldModule, _, e := n.locatedModule(name, m.State.Modules[name])
				if e != nil {
					return nil, e
				}
				for _, old := range oldModule.Links {
					if old.Path == link.Path {
						previouslyManaged = true
					}
				}
				allowed := n.linkTargetMatches(path, actual, link.Target)
				if previouslyManaged && !allowed {
					return nil, fmt.Errorf("组件链接已被外部修改: %s", path)
				}
				actualPath := actual
				if !filepath.IsAbs(actualPath) {
					actualPath = filepath.Join(filepath.Dir(path), actualPath)
				}
				if filepath.Clean(actualPath) == target {
					if _, e := os.Stat(target); e == nil {
						continue
					}
				}
			}
			if e = plan.add(path, "link", "修复已登记的组件链接", nil, rel, 0); e != nil {
				return nil, e
			}
		}

		if mod.ManageConfig || repair {
			cfgPath := config
			if name == "codex" {
				cfgPath = filepath.Join(home, "config.toml")
			}
			if regular(cfgPath) {
				before, e := readRegular(cfgPath)
				if e != nil {
					return nil, e
				}
				after, e := n.rewriteConfig(name, before)
				if e != nil {
					if mod.ManageConfig {
						return nil, e
					}
					plan.Warnings = append(plan.Warnings, name+" 外部配置无法检查内部路径，内容保持不变")
					after = before
				}
				if string(before) != string(after) {
					if !mod.ManageConfig {
						plan.Warnings = append(plan.Warnings, name+" 外部配置存在旧路径；未接管配置内容，本次保留。使用 setup --manage-config 明确接管后再修复")
					} else {
						s, _ := os.Stat(cfgPath)
						plan.guardBytes(cfgPath, before, s.Mode().Perm())
						if e = plan.add(cfgPath, "write", "按模块规则更新配置路径（已显式接管配置）", after, "", s.Mode().Perm()); e != nil {
							return nil, e
						}
					}
				}
			}
		}
	}
	return m.render(plan, n)
}

func (m *Manager) render(plan *Plan, n *Manager) (*Plan, error) {
	all := make([]string, 0, len(n.State.Modules))
	for name := range n.State.Modules {
		all = append(all, name)
	}
	sort.Strings(all)
	for _, name := range all {
		if !n.Config.Modules[name].Enabled {
			continue
		}
		if name == "proxy" || name == "gds" {
			if err := plan.add(filepath.Join(m.Home, "run", name), "dir", "准备受管服务运行目录", nil, "", 0700); err != nil {
				return nil, err
			}
		}
		if err := n.generateModule(plan, name); err != nil {
			return nil, err
		}
	}
	self := n.selfPath()
	if _, ok := n.State.Generated[self]; !ok || !regular(filepath.Join(m.Home, self)) {
		exe, e := readRegularLarge(m.Executable)
		if e != nil {
			return nil, e
		}
		if !m.Initialized && m.Executable == filepath.Join(m.Home, self) {
			n.State.Generated[self] = digest(exe)
		}
		if e = n.generated(plan, self, exe, 0755); e != nil {
			return nil, e
		}
	} else if e := n.checkGenerated(self); e != nil {
		return nil, e
	}
	if e := n.manageEntry(plan, n.Options.Entry); e != nil {
		return nil, e
	}
	if e := n.environment(plan); e != nil {
		return nil, e
	}
	for _, ref := range n.State.ShellFiles {
		path, e := n.resolve(ref)
		if e != nil {
			return nil, e
		}
		before, e := readRegular(path)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		after, e := shellHook(before, m.Home, m.rootPaths("stk")...)
		if e != nil {
			return nil, e
		}
		mode := os.FileMode(0600)
		if s, e := os.Stat(path); e == nil {
			mode = s.Mode().Perm()
		}
		if before != nil {
			plan.guardBytes(path, before, mode)
		} else {
			plan.Guards = append(plan.Guards, Guard{Path: path, Expected: Fingerprint{Kind: "absent"}})
		}
		if e = plan.add(path, "write", "更新显式登记的 Shell 环境块", after, "", mode); e != nil {
			return nil, e
		}
	}
	return n.finish(plan)
}

func (m *Manager) generateModule(plan *Plan, name string) error {
	mod := m.State.Modules[name]
	binary, err := m.resolve(mod.Binary)
	if err != nil {
		return err
	}
	config, err := m.resolve(mod.Config)
	if err != nil {
		return err
	}
	wrapper := "#!/bin/sh\n# server-toolkit-go managed launcher\nSTK_HOME=$(CDPATH= cd -- \"$(dirname -- \"$0\")/..\" && pwd -P) || exit\nexec \"$STK_HOME/" + m.selfPath() + "\" --home \"$STK_HOME\" exec " + name + " -- \"$@\"\n"
	command := moduleCommand(name)
	if err := m.generated(plan, filepath.Join("bin", command), []byte(wrapper), 0755); err != nil {
		return err
	}
	if name == "proxy" || name == "gds" {
		content, e := m.serviceTemplate(name, binary, config)
		if e != nil {
			return e
		}
		if e = m.generated(plan, filepath.Join("services", name+".supervisor.conf"), []byte(content), 0600); e != nil {
			return e
		}
		unit, e := m.systemdTemplate(name, binary, config)
		if e != nil {
			return e
		}
		if e = m.generated(plan, filepath.Join("services", name+".service"), []byte(unit), 0600); e != nil {
			return e
		}
	}
	return nil
}

func (n *Manager) finish(plan *Plan) (*Plan, error) {
	for _, name := range []string{"config.toml", "state.json"} {
		plan.Guards = append(plan.Guards, Guard{Path: filepath.Join(n.Home, name), Expected: n.inputs[name]})
	}
	if plan.Deferred {
		plan.Warnings = append(plan.Warnings, "在线预览仅解析发布元数据，不下载程序；程序摘要和最终元数据将在实际执行时校验生成。此预览不能直接 Apply。")
		return plan, nil
	}
	// Generation changes only if the semantic metadata or generated files did.
	meta, e := n.metadata()
	if e != nil {
		return nil, e
	}
	plan.Instance = n.State.Instance
	changed := len(plan.Changes) > 0
	for name, data := range meta {
		if !sameBytes(filepath.Join(n.Home, name), data) {
			changed = true
		}
	}
	if changed {
		n.State.Generation++
		meta, e = n.metadata()
		if e != nil {
			return nil, e
		}
	}
	for _, name := range []string{"config.toml", "state.json"} {
		if e = plan.add(filepath.Join(n.Home, name), "write", "记录配置与组件管理关系", meta[name], "", 0600); e != nil {
			return nil, e
		}
	}
	if plan.Operation == "update" {
		if e = n.rememberUpdate(plan); e != nil {
			return nil, e
		}
	}
	return plan, nil
}

func candidatePaths(cs []Candidate) []string {
	var result []string
	for _, c := range cs {
		result = append(result, c.Binary)
	}
	return result
}

func addLink(mod *Module, link ManagedLink) {
	for i, l := range mod.Links {
		if l.Path == link.Path {
			mod.Links[i] = link
			return
		}
	}
	mod.Links = append(mod.Links, link)
}

func discoverCodexLinks(c *Candidate) {
	if c.Links == nil {
		c.Links = map[string]string{}
	}
	entry := filepath.Join(c.Home, "bin", "codex")
	if s, e := os.Lstat(entry); e == nil && s.Mode()&os.ModeSymlink != 0 && !executable(entry) {
		old, _ := os.Readlink(entry)
		if strings.Contains(old, "/packages/standalone/") || strings.Contains(old, "/packages/server-toolkit/") {
			c.Links[entry] = c.Binary
		}
	}
	current := filepath.Join(c.Home, "packages", "standalone", "current")
	if within(filepath.Dir(current), c.Binary) {
		if s, e := os.Lstat(current); e == nil && s.Mode()&os.ModeSymlink != 0 && !directory(current) {
			c.Links[current] = filepath.Dir(filepath.Dir(c.Binary))
		}
	}
}

func (m *Manager) Setup(names []string) (*Plan, error) { return m.buildPlan(names, false) }
func (m *Manager) Repair() (*Plan, error)              { return m.buildPlan(nil, true) }
