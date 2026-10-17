package stk

import (
	"fmt"
	"path/filepath"
)

// Directory overrides describe a relocation, not permission to mix the old
// program/package links with a different application's home.
func (m *Manager) locatedModule(name string, mod Module) (Module, []pathMapping, error) {
	mod.Links = append([]ManagedLink(nil), mod.Links...)
	var moves []pathMapping
	home, err := m.resolve(mod.Home)
	if err != nil {
		return mod, nil, err
	}
	selected := ""
	if name == "codex" {
		selected = m.Options.CodexHome
	}
	if name == "proxy" {
		selected = m.Options.ProxyData
	}
	if selected != "" {
		if err := absolute(selected); err != nil {
			return mod, nil, err
		}
		if real, e := filepath.EvalSymlinks(selected); e == nil {
			selected = real
		}
		if name == "codex" && within(selected, m.Home) {
			return mod, nil, fmt.Errorf("Codex 数据目录不能包含 STK 管理目录")
		}
		if selected != home {
			if home != "" {
				moves = append(moves, pathMapping{home, selected})
			}
			move := func(ref Ref) (Ref, error) {
				p, e := m.resolve(ref)
				if e != nil {
					return ref, e
				}
				if home != "" && p != "" && within(home, p) {
					rel, _ := filepath.Rel(home, p)
					return m.ref(filepath.Join(selected, rel)), nil
				}
				return ref, nil
			}
			if name == "codex" {
				mod.Binary, err = move(mod.Binary)
				if err != nil {
					return mod, nil, err
				}
				for i, link := range mod.Links {
					mod.Links[i].Path, err = move(link.Path)
					if err != nil {
						return mod, nil, err
					}
					mod.Links[i].Target, err = move(link.Target)
					if err != nil {
						return mod, nil, err
					}
				}
			}
			mod.Home = m.ref(selected)
		}
	}
	if name == "proxy" && m.Options.ProxyConfig != "" {
		old, e := m.resolve(mod.Config)
		if e != nil {
			return mod, nil, e
		}
		if err := absolute(m.Options.ProxyConfig); err != nil {
			return mod, nil, err
		}
		if old != "" && old != m.Options.ProxyConfig {
			moves = append(moves, pathMapping{old, m.Options.ProxyConfig})
		}
		mod.Config = m.ref(m.Options.ProxyConfig)
	}
	return mod, moves, nil
}

func (m *Manager) rememberPath(old, current string) {
	if old == "" || old == current {
		return
	}
	if m.State.Aliases == nil {
		m.State.Aliases = map[string]Ref{}
	}
	for source, ref := range m.State.Aliases {
		target, err := m.resolve(ref)
		if err == nil && within(old, target) {
			rel, _ := filepath.Rel(old, target)
			m.State.Aliases[source] = m.ref(filepath.Join(current, rel))
		}
	}
	m.State.Aliases[old] = m.ref(current)
}

func (m *Manager) linkTargetMatches(path, actual string, ref Ref) bool {
	target, err := m.resolve(ref)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Dir(path), target)
	if err == nil && actual == rel || actual == target {
		return true
	}
	for _, mapping := range m.pathMappings() {
		if within(mapping.current, target) {
			rel, _ := filepath.Rel(mapping.current, target)
			if actual == filepath.Join(mapping.old, rel) {
				return true
			}
		}
	}
	return false
}
