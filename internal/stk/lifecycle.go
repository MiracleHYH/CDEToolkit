package stk

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func (m *Manager) checkGenerated(relative string) error {
	_, err := m.ownedFingerprint(relative)
	return err
}

func (m *Manager) ownedFingerprint(relative string) (Fingerprint, error) {
	path := filepath.Join(m.Home, relative)
	if filepath.IsAbs(relative) || !within(m.Home, path) || path == m.Home {
		return Fingerprint{}, fmt.Errorf("生成文件路径越界")
	}
	before, err := fingerprint(path)
	if err != nil {
		return before, err
	}
	if before.Kind == "absent" {
		return before, nil
	}
	if hash, ok := m.State.Generated[relative]; !ok || before.Kind != "file" || before.Digest != hash {
		return before, fmt.Errorf("文件不属于 STK 或已被外部修改: %s", path)
	}
	return before, nil
}

func (m *Manager) toolPath(name string) string {
	if name == "stk" {
		return "stk"
	}
	return filepath.Join("tools", name, moduleCommand(name))
}

func (m *Manager) stageArtifact(plan *Plan, name, source string) (string, *Release, error) {
	a, err := m.artifact(name, source)
	if err != nil {
		return "", nil, err
	}
	rel := m.toolPath(name)
	target := filepath.Join(m.Home, rel)
	if err = m.checkGenerated(rel); err != nil {
		return "", nil, err
	}
	// A release digest refers to the compressed upstream asset, while the
	// owned-file digest identifies the selected, unpacked executable.
	mod, managed := m.State.Modules[name]
	if managed && source == "latest" && mod.Release != nil && mod.Release.Repository == a.Repository && olderRelease(name, a.Version, mod.Release.Version) {
		return "", nil, fmt.Errorf("latest 比已安装版本旧；降级需显式指定版本")
	}
	if managed && mod.OwnedBinary && a.Repository != "" && reflect.DeepEqual(mod.Release, &a.Release) {
		fp, e := fingerprint(target)
		if e != nil {
			return "", nil, e
		}
		if fp.Kind == "file" && fp.Digest == mod.BinaryDigest {
			plan.Downloads = append(plan.Downloads, Download{Module: name, Target: target, Release: a.Release})
			return mod.BinaryDigest, &a.Release, nil
		}
	}
	plan.Downloads = append(plan.Downloads, Download{Module: name, Target: target, Release: a.Release, Needed: true})
	if m.Options.DryRun && strings.HasPrefix(a.Source, "https://") {
		plan.Deferred = true
		return "", &a.Release, nil
	}
	data, err := m.releases().artifactBytes(a, name)
	if err != nil {
		return "", nil, err
	}
	if err = m.generated(plan, rel, data, 0755); err != nil {
		return "", nil, err
	}
	return digest(data), &a.Release, nil
}

func (m *Manager) installArtifact(plan *Plan, name, source string) error {
	mod, existed := m.State.Modules[name]
	var err error
	if !existed {
		if name == "codex" {
			h := m.codexHome()
			if err = absolute(h); err != nil {
				return err
			}
			if within(h, m.Home) {
				return fmt.Errorf("Codex 数据目录不能包含 STK 管理目录")
			}
			if m.Options.CodexHome == "" && os.Getenv("CODEX_HOME") != "" && !directory(h) {
				return fmt.Errorf("CODEX_HOME 指向不可用目录；请修复挂载或显式指定 --codex-home，避免创建替代空环境")
			}
			mod.Home = m.ref(h)
			if !directory(h) {
				if err = plan.add(h, "dir", "首次安装创建空 Codex 主目录", nil, "", 0700); err != nil {
					return err
				}
			}
		}
		if name == "proxy" {
			config := m.proxyConfig()
			if !regular(config) {
				return fmt.Errorf("代理需要现有配置 --proxy-config FILE；请先提供配置再下载程序")
			}
			mod.Config = m.ref(config)
			mod.Home = m.ref(m.proxyData())
		}
	} else if name == "codex" {
		home, e := m.resolve(mod.Home)
		if e != nil || !directory(home) {
			return fmt.Errorf("Codex 主目录不可用，先修复挂载路径；update 不创建替代数据目录")
		}
	}
	hash, release, err := m.stageArtifact(plan, name, source)
	if err != nil {
		return err
	}
	mod.Binary = m.ref(filepath.Join(m.Home, m.toolPath(name)))
	if existed && !mod.OwnedBinary {
		mod.Links = nil
	}
	mod.Provider, mod.OwnedBinary, mod.BinaryDigest, mod.Release = "stk", true, hash, release
	m.State.Modules[name] = mod
	return nil
}

// Install uses the exact same setup/adoption path. Only a genuinely absent
// component triggers the official downloader; an ambiguous/broken registered
// component is never permission to install a second copy.
func (m *Manager) Install(names []string) (*Plan, error) {
	if len(names) == 0 {
		names = []string{"proxy", "codex"}
	}
	n := *m
	n.Options = m.Options
	n.Options.Artifacts = map[string]string{}
	for k, v := range m.Options.Artifacts {
		n.Options.Artifacts[k] = v
	}
	for _, name := range names {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知模块: %s", name)
		}
		if n.Options.Artifacts[name] != "" {
			continue
		}
		cs, err := m.Discover(name)
		if err != nil {
			return nil, err
		}
		if len(cs) == 0 {
			if name == "gds" {
				return nil, fmt.Errorf("GDS 是可选本地模块，请提供 --binary gds=FILE 或 --install gds=ARTIFACT")
			}
			n.Options.Artifacts[name] = "latest"
		}
	}
	return n.Setup(names)
}

func (m *Manager) Update(names []string) (*Plan, error) {
	if !m.Initialized {
		return nil, fmt.Errorf("尚未登记，先执行 install/setup")
	}
	if err := m.requireCurrentRoots(); err != nil {
		return nil, err
	}
	if m.Options.Takeover && len(names) == 0 {
		return nil, fmt.Errorf("--takeover 需明确指定模块名")
	}
	if len(names) == 0 {
		for name, mod := range m.State.Modules {
			if mod.OwnedBinary && name != "gds" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("没有 STK 所有的在线模块；外部安装需由原安装器更新，或明确 --takeover")
		}
	}
	n := m.clone()
	plan := &Plan{Home: m.Home, Instance: m.State.Instance, Operation: "update"}
	if len(names) == 1 && names[0] == "stk" {
		source := m.Options.Artifacts["stk"]
		if source == "" {
			return nil, fmt.Errorf("STK 尚无配置的发布仓库；请用 --install stk=FILE/HTTPS --sha256 stk=HASH 更新单文件，禁止通过 git pull 更新运行环境")
		}
		if _, err := os.Lstat(filepath.Join(m.Home, "stk")); err == nil {
			if _, owned := m.State.Generated["stk"]; !owned {
				return nil, fmt.Errorf("当前管理程序不是 STK 所有")
			}
		}
		if _, _, err := n.stageArtifact(plan, "stk", source); err != nil {
			return nil, err
		}
		return n.finish(plan)
	}
	for _, name := range names {
		mod, ok := n.State.Modules[name]
		if !knownModule(name) || !ok {
			return nil, fmt.Errorf("%s 未接管或不是组件模块", name)
		}
		if !mod.OwnedBinary && !m.Options.Takeover {
			return nil, fmt.Errorf("%s 是外部安装；用原安装器更新，或以 update %s --takeover 明确改用 STK 副本（保留原程序和数据）", name, name)
		}
		if mod.OwnedBinary {
			binary, _ := n.resolve(mod.Binary)
			if binary != filepath.Join(m.Home, n.toolPath(name)) {
				return nil, fmt.Errorf("%s 所有权路径不符；请先 repair/重新接管", name)
			}
		}
		if name == "proxy" {
			home, e := m.resolve(mod.Home)
			if e != nil || !directory(home) {
				return nil, fmt.Errorf("代理数据目录不可用，请先修复路径；update 不重建数据目录")
			}
		}
		source := m.Options.Artifacts[name]
		if source == "" {
			source = "latest"
		}
		if name == "gds" && source == "latest" {
			return nil, fmt.Errorf("GDS 无内置在线发布源，更新需指定 artifact")
		}
		if err := n.installArtifact(plan, name, source); err != nil {
			return nil, err
		}
		if n.Config.Modules[name].Enabled {
			if err := n.generateModule(plan, name); err != nil {
				return nil, err
			}
		}
		if mod.Service.Backend != "" {
			plan.Warnings = append(plan.Warnings, name+" 已登记服务未停止、重载或重启；文件替换后运行进程仍可能使用旧版本")
			if !mod.OwnedBinary {
				plan.Warnings = append(plan.Warnings, name+" 已改用 STK 副本；外部服务定义仍指向原安装，需后续显式切换")
			}
		}
	}
	return n.finish(plan)
}

func moduleFiles(name string) []string {
	rels := []string{filepath.Join("bin", moduleCommand(name)), filepath.Join("tools", name, moduleCommand(name))}
	if name == "proxy" || name == "gds" {
		rels = append(rels, filepath.Join("services", name+".service"), filepath.Join("services", name+".supervisor.conf"))
	}
	return rels
}

func (m *Manager) removeGenerated(plan *Plan, relative string) error {
	if _, ok := m.State.Generated[relative]; !ok {
		return nil
	}
	before, err := m.ownedFingerprint(relative)
	if err != nil {
		return err
	}
	plan.Guards = append(plan.Guards, Guard{Path: filepath.Join(m.Home, relative), Expected: before})
	if err := plan.add(filepath.Join(m.Home, relative), "remove", "移除有归属记录且未被修改的 STK 文件", nil, "", 0); err != nil {
		return err
	}
	delete(m.State.Generated, relative)
	return nil
}

// Uninstall never recursively deletes a directory. Module homes, configs,
// credentials, sessions, caches and external service definitions survive.
func (m *Manager) Uninstall(names []string) (*Plan, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("需要模块名；uninstall stk 移除整个 Go 管理入口并保留数据")
	}
	core := len(names) == 1 && names[0] == "stk"
	for _, name := range names {
		if !knownModule(name) && !core {
			return nil, fmt.Errorf("未知模块: %s", name)
		}
	}
	if !m.Initialized {
		return &Plan{Home: m.Home}, nil
	}
	if err := m.validateRoots(); err != nil {
		return nil, err
	}
	if core {
		names = []string{"proxy", "codex", "gds"}
	}
	n := m.clone()
	plan := &Plan{Home: m.Home, Instance: m.State.Instance}
	for _, name := range names {
		if !knownModule(name) {
			return nil, fmt.Errorf("未知模块: %s", name)
		}
		if mod, ok := n.State.Modules[name]; ok && mod.Service.Backend != "" {
			plan.Warnings = append(plan.Warnings, name+" 已登记服务未停止或移除；请在实际卸载前显式停止并解除服务注册")
		}
		for _, rel := range moduleFiles(name) {
			if err := n.removeGenerated(plan, rel); err != nil {
				return nil, err
			}
		}
		delete(n.State.Modules, name)
		delete(n.Config.Modules, name)
	}
	if core {
		// Only the fixed generated-file allowlist is eligible for removal.
		for _, rel := range []string{"env.sh", "stk"} {
			if err := n.removeGenerated(plan, rel); err != nil {
				return nil, err
			}
		}
		if len(n.State.Generated) != 0 {
			return nil, fmt.Errorf("存在未知生成文件记录，拒绝扩大卸载范围")
		}
		if err := m.removeShellHooks(plan); err != nil {
			return nil, err
		}
		n.State.ShellFiles = nil
		return n.finish(plan)
	}
	if len(plan.Changes) == 0 && reflect.DeepEqual(n.State.Modules, m.State.Modules) {
		return n.finish(plan)
	}
	if err := n.environment(plan); err != nil {
		return nil, err
	}
	return n.finish(plan)
}
