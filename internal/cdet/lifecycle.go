package cdet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func componentNames(names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if !knownModule(name) || seen[name] {
			return fmt.Errorf("未知或重复组件: %s", name)
		}
		seen[name] = true
	}
	return nil
}
func (m *Manager) activePaths(names []string) []string {
	var paths []string
	for _, name := range names {
		if !knownModule(name) {
			continue
		}
		paths = append(paths, filepath.Join(m.Store, name), filepath.Join(m.Store, programPath(name)), filepath.Join(m.Prefix, "bin", moduleCommand(name)))
		if m.Relocated && m.Config.Store != "" && m.Config.Store != m.Store {
			paths = append(paths, filepath.Join(m.Config.Store, name), filepath.Join(m.Config.Store, programPath(name)))
		}
	}
	return paths
}
func launcher(name string) []byte {
	return []byte("#!/bin/sh\n# CDEToolkit managed launcher\nCDET_PREFIX=$(CDPATH= cd -- \"$(dirname -- \"$0\")/..\" && pwd -P) || exit\nexec \"$CDET_PREFIX/bin/cdet\" --prefix \"$CDET_PREFIX\" exec " + name + " -- \"$@\"\n")
}
func (m *Manager) environmentScript() []byte {
	return []byte("# CDEToolkit\nexport CDET_PREFIX=" + quote(m.Prefix) + "\nexport CDET_STORE=" + quote(m.Store) + "\nexport PATH=" + quote(filepath.Join(m.Prefix, "bin")) + ":\"$PATH\"\n")
}
func (m *Manager) entriesReady() bool {
	for path, want := range m.State.Generated {
		f, e := fingerprint(filepath.Join(m.Prefix, path))
		if e != nil || f != (Fingerprint{Kind: "file", Digest: want, Mode: 0755}) {
			return false
		}
	}
	for name, c := range m.State.Components {
		if c.Installed && m.State.Generated[filepath.Join("bin", moduleCommand(name))] != digest(launcher(name)) {
			return false
		}
	}
	return m.State.Generated["env.sh"] == digest(m.environmentScript()) && m.State.Generated["bin/cdet"] != ""
}
func (m *Manager) validateGenerated(path string, b []byte) error {
	target := filepath.Join(m.Prefix, path)
	if e := noSymlinkParents(target); e != nil {
		return e
	}
	f, e := fingerprint(target)
	if e != nil {
		return e
	}
	// Exact prospective content permits retry after an interrupted metadata save.
	if f.Kind != "absent" && (f.Kind != "file" || f.Mode != 0755 || (f.Digest != m.State.Generated[path] && f.Digest != digest(b))) {
		return fmt.Errorf("受管入口被占用或修改: %s", target)
	}
	return nil
}
func (m *Manager) generated(path string, b []byte) error {
	if e := m.checkManagedRoots(); e != nil {
		return e
	}
	if e := m.validateGenerated(path, b); e != nil {
		return e
	}
	if e := atomicWriteIn(m.Prefix, filepath.Join(m.Prefix, path), b, 0755); e != nil {
		return e
	}
	m.State.Generated[path] = digest(b)
	return nil
}
func (m *Manager) render() error {
	for _, name := range sortedNames(m.State.Components) {
		if m.State.Components[name].Installed {
			if e := m.generated(filepath.Join("bin", moduleCommand(name)), launcher(name)); e != nil {
				return e
			}
		}
	}
	return nil
}
func emptyDestination(path string, store bool) error {
	if absent(path) {
		return nil
	}
	entries, e := os.ReadDir(path)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() == "lock" && !store {
			continue
		}
		if entry.Name() == ".instance" && !store {
			if _, e := readInstallID(path); e != nil {
				return e
			}
			continue
		}
		if store && entry.Name() == ".cdet" {
			children, e := os.ReadDir(filepath.Join(path, ".cdet"))
			if e != nil {
				return e
			}
			for _, c := range children {
				if c.Name() != "lock" {
					return fmt.Errorf("存储目录含未登记内容")
				}
			}
			continue
		}
		if entry.IsDir() {
			children, e := os.ReadDir(filepath.Join(path, entry.Name()))
			if e == nil && len(children) == 0 {
				continue
			}
		}
		return fmt.Errorf("目标目录含未登记内容，请选择独立目录: %s", path)
	}
	return nil
}
func (m *Manager) Init() (*Plan, error) {
	if m.Relocated {
		return nil, fmt.Errorf("位置已变化，请先 repair")
	}
	if !m.Initialized && m.stateDigest == "" {
		if e := emptyDestination(m.Prefix, false); e != nil {
			return nil, e
		}
		if e := emptyDestination(m.Store, true); e != nil {
			return nil, e
		}
	}
	b, e := readRegularLarge(m.Executable)
	if e != nil {
		return nil, e
	}
	p := m.plan("init")
	if e = m.validateGenerated("bin/cdet", b); e != nil {
		return nil, e
	}
	if e = m.validateGenerated("env.sh", m.environmentScript()); e != nil {
		return nil, e
	}
	if m.Initialized && m.entriesReady() && m.State.Generated["bin/cdet"] == digest(b) {
		return p, nil
	}
	p.Steps = append(p.Steps, Step{"self", "init", "初始化双目录和命令入口；不安装组件或启动服务"})
	p.run = func() error {
		if m.stateDigest == "" {
			if e := emptyDestination(m.Prefix, false); e != nil {
				return e
			}
			if e := emptyDestination(m.Store, true); e != nil {
				return e
			}
		}
		if m.identityDigest == "" {
			raw := []byte(m.State.InstallID + "\n")
			if e := atomicWriteIn(m.Prefix, filepath.Join(m.Prefix, ".instance"), raw, 0600); e != nil {
				return e
			}
			m.identityDigest = digest(raw)
		}
		// Persist ownership first: subsequent init can retry without a transaction log.
		if e := m.saveState(); e != nil {
			return e
		}
		if e := m.generated("bin/cdet", b); e != nil {
			return e
		}
		if e := m.generated("env.sh", m.environmentScript()); e != nil {
			return e
		}
		if e := m.render(); e != nil {
			return e
		}
		if e := m.saveState(); e != nil {
			return e
		}
		return m.saveBootstrap()
	}
	return p, nil
}
func (m *Manager) dataRootOK(name string) error {
	data := filepath.Join(m.Store, name)
	if !directory(data) {
		return fmt.Errorf("%s 数据目录缺失，不会创建空目录替代", name)
	}
	if e := noSymlinkParents(filepath.Join(data, "sentinel")); e != nil {
		return e
	}
	return nil
}
func (m *Manager) componentOK(name string, c Component) error {
	if !c.Installed {
		return fmt.Errorf("%s 未安装", name)
	}
	if e := m.dataRootOK(name); e != nil {
		return e
	}
	data := filepath.Join(m.Store, name)
	program := filepath.Join(m.Store, programPath(name))
	if e := noSymlinkParents(program); e != nil {
		return e
	}
	if name == "codex" {
		if e := m.codexLinks(false); e != nil {
			return e
		}
		if m.Relocated {
			return nil
		} // Known old absolute links have been validated at the new root.
	}
	info, e := os.Stat(program)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%s 受管程序缺失或不可执行", name)
	}
	// Upstream may replace its own program. A remembered checksum is not a lock.
	if l, e := os.Lstat(program); e != nil {
		return e
	} else if l.Mode()&os.ModeSymlink != 0 {
		real, e := filepath.EvalSymlinks(program)
		if e != nil || !within(data, real) {
			return fmt.Errorf("%s 程序链接超出受管组件目录", name)
		}
	}
	return nil
}
func backend(name string) string {
	if name == "codex" {
		return "official"
	}
	if name == "proxy" {
		return "mihomo"
	}
	return "artifact"
}
func (m *Manager) unusedProgram(name string) error {
	paths := []string{filepath.Join(m.Store, programPath(name))}
	if name == "codex" {
		paths = append(paths, filepath.Join(m.Store, ".cdet", "bin", "codex-code-mode-host"))
	}
	for _, path := range paths {
		if e := noSymlinkParents(path); e != nil {
			return e
		}
		if !absent(path) {
			return fmt.Errorf("%s 未登记的程序入口已存在，保留原件: %s", name, path)
		}
	}
	return nil
}
func (m *Manager) Install(names []string) (*Plan, error) {
	if e := m.requireReady(); e != nil {
		return nil, e
	}
	if e := componentNames(names); e != nil {
		return nil, e
	}
	if m.Options.Artifacts["codex"] != "" || m.Options.Digests["codex"] != "" {
		return nil, fmt.Errorf("Codex 仅通过官方脚本安装，不接受自定义程序包")
	}
	type task struct {
		name       string
		candidate  Candidate
		tree       *Tree
		binary     Fingerprint
		registered bool
	}
	var tasks []task
	var entries []string
	p := m.plan("install")
	for _, name := range names {
		entry := filepath.Join("bin", moduleCommand(name))
		if e := m.validateGenerated(entry, launcher(name)); e != nil {
			return nil, e
		}
		c, registered := m.State.Components[name]
		if registered && m.Options.From[name] != "" {
			return nil, fmt.Errorf("%s 已有受管数据，不能再次迁入覆盖", name)
		}
		if c.Installed {
			if m.Options.Artifacts[name] != "" {
				return nil, fmt.Errorf("%s 已安装，请使用 update", name)
			}
			if e := m.componentOK(name, c); e != nil {
				return nil, e
			}
			f, e := fingerprint(filepath.Join(m.Prefix, entry))
			if e != nil {
				return nil, e
			}
			if f.Kind == "absent" || m.State.Generated[entry] != digest(launcher(name)) {
				entries = append(entries, name)
				p.Steps = append(p.Steps, Step{name, "repair-entry", "仅补受管启动入口；不重新安装程序或修改组件数据"})
			}
			continue
		}
		t := task{name: name, registered: registered, tree: emptyTree()}
		if registered {
			if e := m.dataRootOK(name); e != nil {
				return nil, e
			}
		} else {
			if e := m.unusedProgram(name); e != nil {
				return nil, e
			}
			resumed, e := m.resumeImport(name)
			if e != nil {
				return nil, e
			}
			if !resumed {
				t.candidate, e = m.Discover(name)
				if e != nil {
					return nil, e
				}
				if e = checkInactive([]string{t.candidate.Data, t.candidate.Binary}); e != nil {
					return nil, e
				}
				if t.candidate.Data != "" {
					if e = validateImportConfig(name, t.candidate.Data); e != nil {
						return nil, e
					}
					t.tree, e = snapshotTree(t.candidate.Data)
					if e != nil {
						return nil, e
					}
					p.Imports = append(p.Imports, Import{name, t.candidate.Data, t.tree.Files, t.tree.Bytes, t.tree.Digest})
				}
				if t.candidate.Binary != "" {
					t.binary, e = fingerprint(t.candidate.Binary)
					if e != nil {
						return nil, e
					}
				}
			}
		}
		if name == "gds" && t.candidate.Binary == "" && m.Options.Artifacts[name] == "" {
			return nil, fmt.Errorf("GDS 为可选模块，请显式提供 --artifact gds=文件")
		}
		tasks = append(tasks, t)
		detail := "完整迁入或创建组件目录，安装独立程序；不启动服务"
		if name == "codex" {
			detail = "完整 CODEX_HOME 迁入；由官方脚本安装，隔离 HOME 并注入受管目录与代理"
		}
		p.Steps = append(p.Steps, Step{name, "install", detail})
	}
	if len(p.Imports) > 0 {
		p.Warnings = append(p.Warnings, "迁入源保持不变；请先停止源 Codex/daemon 或其他组件进程。")
	}
	if len(tasks) == 0 && len(entries) == 0 {
		return p, nil
	}
	var changed []string
	for _, t := range tasks {
		changed = append(changed, t.name)
	}
	if e := checkInactive(m.activePaths(changed)); e != nil {
		return nil, e
	}
	p.run = func() error {
		release, e := m.lockComponents(changed)
		if e != nil {
			return e
		}
		defer release()
		if e := checkInactive(m.activePaths(changed)); e != nil {
			return e
		}
		for _, t := range tasks {
			name := t.name
			if e := m.checkManagedRoots(); e != nil {
				return e
			}
			if e := m.validateGenerated(filepath.Join("bin", moduleCommand(name)), launcher(name)); e != nil {
				return e
			}
			if e := checkInactive([]string{t.candidate.Data, t.candidate.Binary}); e != nil {
				return e
			}
			if !t.registered {
				if e := m.unusedProgram(name); e != nil {
					return e
				}
				if e := m.publishData(name, t.tree); e != nil {
					return e
				}
				origin := "installed"
				if t.candidate.Data != "" {
					origin = "imported"
				}
				m.State.Components[name] = Component{Backend: backend(name), Origin: origin}
				if e := m.saveState(); e != nil {
					return e
				}
				if !absent(m.receiptPath(name)) {
					if e := removeSynced(m.receiptPath(name)); e != nil {
						return e
					}
				}
			}
			if e := m.dataRootOK(name); e != nil {
				return e
			}
			var hash string
			if name == "codex" {
				if e := m.installCodex(); e != nil {
					return e
				}
				if e := m.codexLinks(true); e != nil {
					return e
				}
			} else {
				var b []byte
				var e error
				if t.candidate.Binary != "" {
					f, er := fingerprint(t.candidate.Binary)
					if er != nil || f != t.binary {
						return fmt.Errorf("%s 源程序已变化", name)
					}
					b, e = m.releases().artifactBytes(Artifact{Source: t.candidate.Binary}, name)
					if e == nil && digest(b) != t.binary.Digest {
						return fmt.Errorf("%s 读取期间源程序已变化", name)
					}
				} else {
					b, e = m.downloadProgram(name)
				}
				if e != nil {
					return e
				}
				if e = m.checkManagedRoots(); e != nil {
					return e
				}
				if e = m.dataRootOK(name); e != nil {
					return e
				}
				target := filepath.Join(m.Store, programPath(name))
				c := m.State.Components[name]
				if !absent(target) {
					f, e := fingerprint(target)
					if e != nil || f.Kind != "file" || (f.Digest != c.Digest && f.Digest != digest(b)) {
						return fmt.Errorf("%s 未登记的程序入口已存在，拒绝覆盖", name)
					}
				}
				// Record only the pending standalone payload, allowing a failed
				// state save after rename to be retried without adopting a foreign file.
				if absent(target) {
					c.Digest = digest(b)
					m.State.Components[name] = c
					if e = m.saveState(); e != nil {
						return e
					}
				}
				if e = atomicWriteIn(m.Store, target, b, 0755); e != nil {
					return e
				}
				hash = digest(b)
			}
			c := m.State.Components[name]
			c.Installed = true
			c.Digest = hash
			m.State.Components[name] = c
			if e := m.componentOK(name, c); e != nil {
				return e
			}
			// Persist successful program installation before the retryable CLI entry.
			if e := m.saveState(); e != nil {
				return e
			}
			if e := m.generated(filepath.Join("bin", moduleCommand(name)), launcher(name)); e != nil {
				return e
			}
			if e := m.saveState(); e != nil {
				return e
			}
		}
		for _, name := range entries {
			if e := m.componentOK(name, m.State.Components[name]); e != nil {
				return e
			}
			if e := m.generated(filepath.Join("bin", moduleCommand(name)), launcher(name)); e != nil {
				return e
			}
			if e := m.saveState(); e != nil {
				return e
			}
		}
		return nil
	}
	return p, nil
}
func (m *Manager) downloadProgram(name string) ([]byte, error) {
	source := m.Options.Artifacts[name]
	if source == "" {
		source = "latest"
	}
	a, e := m.artifact(name, source)
	if e != nil {
		return nil, e
	}
	c := m.releases()
	if strings.HasPrefix(a.Source, "https://") {
		c, e = m.networkReleases()
		if e != nil {
			return nil, e
		}
	}
	return c.artifactBytes(a, name)
}
func (m *Manager) Repair() (*Plan, error) {
	if !m.Initialized {
		return nil, fmt.Errorf("尚未初始化，请先 init")
	}
	names := sortedNames(m.State.Components)
	if e := checkInactive(m.activePaths(names)); e != nil {
		return nil, e
	}
	for _, name := range names {
		c := m.State.Components[name]
		if c.Installed {
			if e := m.componentOK(name, c); e != nil {
				return nil, e
			}
		}
	}
	p := m.plan("repair")
	for _, name := range names {
		if m.State.Components[name].Installed {
			if e := m.validateGenerated(filepath.Join("bin", moduleCommand(name)), launcher(name)); e != nil {
				return nil, e
			}
		}
	}
	if e := m.validateGenerated("env.sh", m.environmentScript()); e != nil {
		return nil, e
	}
	if !m.Relocated && m.entriesReady() {
		return p, nil
	}
	p.Steps = append(p.Steps, Step{"self", "repair", "按存储身份重建两根定位、环境和相对启动入口；不改组件数据"})
	p.run = func() error {
		release, e := m.lockComponents(names)
		if e != nil {
			return e
		}
		defer release()
		if e := checkInactive(m.activePaths(names)); e != nil {
			return e
		}
		if e := m.checkManagedRoots(); e != nil {
			return e
		}
		if m.State.Components["codex"].Installed {
			if e := m.codexLinks(true); e != nil {
				return e
			}
		}
		for _, name := range names {
			c := m.State.Components[name]
			if c.Installed {
				if e := m.componentOK(name, c); e != nil {
					return e
				}
			}
		}
		core, e := fingerprint(m.executablePath())
		if e != nil {
			return e
		}
		expected := m.State.Generated["bin/cdet"]
		if expected == "" {
			return fmt.Errorf("管理入口已移除，请使用 init 恢复")
		}
		if core.Kind == "absent" {
			b, e := readRegularLarge(m.Executable)
			if e != nil || digest(b) != expected {
				return fmt.Errorf("管理程序缺失，请使用可信程序执行 init")
			}
			if e = m.generated("bin/cdet", b); e != nil {
				return e
			}
		} else if core != (Fingerprint{Kind: "file", Digest: expected, Mode: 0755}) {
			return fmt.Errorf("管理程序已被修改，停止修复")
		}
		if e := m.render(); e != nil {
			return e
		}
		if e := m.generated("env.sh", m.environmentScript()); e != nil {
			return e
		}
		if e := m.saveState(); e != nil {
			return e
		}
		return m.saveBootstrap()
	}
	return p, nil
}
func (m *Manager) Update(names []string) (*Plan, error) {
	if e := m.requireReady(); e != nil {
		return nil, e
	}
	if len(names) == 0 {
		for _, n := range []string{"codex", "proxy"} {
			if m.State.Components[n].Installed {
				names = append(names, n)
			}
		}
	}
	if e := componentNames(names); e != nil {
		return nil, e
	}
	if m.Options.Artifacts["codex"] != "" || m.Options.Digests["codex"] != "" {
		return nil, fmt.Errorf("Codex 更新仅调用官方脚本")
	}
	p := m.plan("update")
	// An online upgrade can restart Mihomo. Do not introduce that route after
	// the user has already reviewed a stopped-program replacement plan.
	onlineUpdate := map[string]bool{}
	for _, name := range names {
		if e := m.componentOK(name, m.State.Components[name]); e != nil {
			return nil, e
		}
		if name == "gds" && m.Options.Artifacts[name] == "" {
			return nil, fmt.Errorf("GDS 更新需显式提供程序包")
		}
		detail := "官方更新不适用时，校验安装包并原子替换单个程序"
		if name == "codex" {
			detail = "调用官方脚本更新；注入受管目录和代理环境"
		} else if name == "proxy" && m.Options.Artifacts[name] == "" {
			info, e := m.mihomoController()
			if e != nil {
				return nil, e
			}
			if info != nil {
				onlineUpdate[name] = true
				detail = "使用受管 mihomo 官方 /upgrade 接口"
				p.Warnings = append(p.Warnings, "mihomo 官方升级成功后会自行重启；不会另行启动或停止服务。")
			}
		}
		p.Steps = append(p.Steps, Step{name, "update", detail})
	}
	var versionNames []string
	for _, name := range names {
		if name != "gds" {
			versionNames = append(versionNames, name)
		}
	}
	if len(versionNames) > 0 {
		v, e := m.Versions(versionNames)
		if e != nil {
			p.Warnings = append(p.Warnings, "无法查询最新版本: "+e.Error())
		} else {
			p.Versions = v
		}
	}
	p.run = func() error {
		for _, name := range names {
			if e := m.checkManagedRoots(); e != nil {
				return e
			}
			if e := m.componentOK(name, m.State.Components[name]); e != nil {
				return e
			}
			if name == "codex" {
				release, e := m.lockComponents([]string{name})
				if e != nil {
					return e
				}
				e = func() error {
					defer release()
					if e := checkInactive(m.activePaths([]string{name})); e != nil {
						return e
					}
					if e := m.installCodex(); e != nil {
						return e
					}
					return m.codexLinks(true)
				}()
				if e != nil {
					return e
				}
			} else {
				handled := false
				if name == "proxy" && onlineUpdate[name] {
					var e error
					handled, e = m.updateMihomoOfficial()
					if e != nil {
						return e
					}
				}
				if !handled {
					release, e := m.lockComponents([]string{name})
					if e != nil {
						return e
					}
					// Retain the lease through download, final process check and rename.
					defer release()
					if e := checkInactive(m.activePaths([]string{name})); e != nil {
						return e
					}
					b, e := m.downloadProgram(name)
					if e != nil {
						return e
					}
					if e = checkInactive(m.activePaths([]string{name})); e != nil {
						return e
					}
					if e = m.checkManagedRoots(); e != nil {
						return e
					}
					if e = m.componentOK(name, m.State.Components[name]); e != nil {
						return e
					}
					if e = atomicWriteIn(m.Store, filepath.Join(m.Store, programPath(name)), b, 0755); e != nil {
						return e
					}
					c := m.State.Components[name]
					c.Digest = digest(b)
					m.State.Components[name] = c
				} else {
					c := m.State.Components[name]
					c.Digest = ""
					m.State.Components[name] = c
				}
			}
			if e := m.componentOK(name, m.State.Components[name]); e != nil {
				return e
			}
			if e := m.saveState(); e != nil {
				return e
			}
		}
		return nil
	}
	return p, nil
}
func (m *Manager) removeGenerated(path string) error {
	if e := m.checkManagedRoots(); e != nil {
		return e
	}
	target := filepath.Join(m.Prefix, path)
	if e := noSymlinkParents(target); e != nil {
		return e
	}
	f, e := fingerprint(target)
	if e != nil {
		return e
	}
	if f.Kind != "absent" {
		if f.Kind != "file" || f.Digest != m.State.Generated[path] || f.Mode != 0755 {
			return fmt.Errorf("入口已修改，停止卸载: %s", path)
		}
		if e = removeSynced(target); e != nil {
			return e
		}
	}
	delete(m.State.Generated, path)
	return nil
}
func (m *Manager) Remove(names []string) (*Plan, error) {
	if e := m.requireReady(); e != nil {
		return nil, e
	}
	self := len(names) == 1 && names[0] == "self"
	if !self {
		if e := componentNames(names); e != nil {
			return nil, e
		}
	}
	active := names
	if self {
		active = sortedNames(m.State.Components)
	}
	if e := checkInactive(m.activePaths(active)); e != nil {
		return nil, e
	}
	p := m.plan("remove")
	for _, name := range names {
		p.Steps = append(p.Steps, Step{name, "remove", "只移除受管命令入口和独立程序；保留完整组件目录"})
	}
	p.Warnings = append(p.Warnings, "所有组件配置、认证、会话、包缓存和未知文件保留；不清理原迁入源。")
	p.run = func() error {
		release, e := m.lockComponents(active)
		if e != nil {
			return e
		}
		defer release()
		if e := checkInactive(m.activePaths(active)); e != nil {
			return e
		}
		if self {
			for _, path := range []string{"bin/codex", "bin/mihomo", "bin/gds", "env.sh", "bin/cdet"} {
				if _, ok := m.State.Generated[path]; ok {
					if e := m.removeGenerated(path); e != nil {
						return e
					}
					if e := m.saveState(); e != nil {
						return e
					}
				}
			}
			return nil
		}
		for _, name := range names {
			c, ok := m.State.Components[name]
			if !ok {
				continue
			}
			// Verify generated entry ownership before removing any program.
			entry := filepath.Join("bin", moduleCommand(name))
			if e := m.removeGenerated(entry); e != nil {
				return e
			}
			target := filepath.Join(m.Store, programPath(name))
			if e := noSymlinkParents(target); e != nil {
				return e
			}
			if !absent(target) {
				if e := m.checkManagedRoots(); e != nil {
					return e
				}
				if !c.Installed && c.Digest == "" {
					return fmt.Errorf("%s 安装未完成，程序入口归属尚未确认；保留程序和完整数据，请先重试 install", name)
				}
				if !c.Installed {
					f, e := fingerprint(target)
					if e != nil || f.Kind != "file" || f.Digest != c.Digest {
						return fmt.Errorf("%s 未完成安装的程序已变化，保留现场", name)
					}
				}
				info, e := os.Lstat(target)
				if e != nil {
					return e
				}
				if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
					return fmt.Errorf("组件程序入口类型异常，拒绝卸载")
				}
				// Unlink only; never follow an upstream-managed symlink into CODEX_HOME.
				if e = removeSynced(target); e != nil {
					return e
				}
			}
			if name == "codex" {
				host := filepath.Join(m.Store, ".cdet", "bin", "codex-code-mode-host")
				if info, e := os.Lstat(host); e == nil && info.Mode()&os.ModeSymlink != 0 {
					if real, e := filepath.EvalSymlinks(host); e == nil && within(filepath.Join(m.Store, "codex", "packages", "standalone"), real) {
						if e = removeSynced(host); e != nil {
							return e
						}
					}
				}
			}
			c.Installed = false
			c.Digest = ""
			m.State.Components[name] = c
			if e := m.saveState(); e != nil {
				return e
			}
		}
		return nil
	}
	return p, nil
}
