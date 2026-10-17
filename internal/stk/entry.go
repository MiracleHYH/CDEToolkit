package stk

import (
	"fmt"
	"path/filepath"
)

// The public command is an explicitly selected link to our owned executable.
// Its relative target travels with same-disk mount changes; its state reference
// lets repair rebuild it and uninstall remove only the unchanged owned link.
func (m *Manager) manageEntry(plan *Plan, requested string) error {
	entry := m.State.Entry
	if entry == nil && requested == "" {
		return nil
	}
	if entry == nil {
		entry = &ManagedLink{Path: m.ref(requested), Target: Ref{Root: "stk", Path: m.selfPath()}}
	}
	path, err := m.resolve(entry.Path)
	if err != nil {
		return err
	}
	if requested != "" && requested != path {
		return fmt.Errorf("已登记另一个 cdet 入口，请先显式处理原入口: %s", path)
	}
	if filepath.Base(path) != "cdet" || within(m.Home, path) {
		return fmt.Errorf("命令入口须为管理目录外的 cdet 文件路径")
	}
	target, err := m.resolve(entry.Target)
	if err != nil {
		return err
	}
	before, err := fingerprint(path)
	if err != nil {
		return err
	}
	if before.Kind != "absent" && (before.Kind != "link" || !m.linkTargetMatches(path, before.Digest, entry.Target)) {
		return fmt.Errorf("cdet 入口不属于当前实例或已被修改: %s", path)
	}
	relative, err := filepath.Rel(filepath.Dir(path), target)
	if err != nil {
		return err
	}
	plan.Guards = append(plan.Guards, Guard{Path: path, Expected: before})
	if err := plan.add(path, "link", "重建 cdet 命令入口", nil, relative, 0); err != nil {
		return err
	}
	m.State.Entry = entry
	return nil
}

func (m *Manager) removeEntry(plan *Plan) error {
	if m.State.Entry == nil {
		return nil
	}
	path, err := m.resolve(m.State.Entry.Path)
	if err != nil {
		return err
	}
	before, err := fingerprint(path)
	if err != nil {
		return err
	}
	if before.Kind != "absent" && (before.Kind != "link" || !m.linkTargetMatches(path, before.Digest, m.State.Entry.Target)) {
		return fmt.Errorf("已登记 cdet 入口被修改，拒绝卸载: %s", path)
	}
	plan.Guards = append(plan.Guards, Guard{Path: path, Expected: before})
	if err := plan.add(path, "remove", "移除未被修改的 cdet 命令链接", nil, "", 0); err != nil {
		return err
	}
	m.State.Entry = nil
	return nil
}
