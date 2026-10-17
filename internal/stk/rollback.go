package stk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (m *Manager) rememberUpdate(p *Plan) error {
	if len(p.Changes) == 0 || p.Deferred {
		return nil
	}
	id, err := instanceID()
	if err != nil {
		return err
	}
	p.Transaction = id
	path := filepath.Join(p.Home, "last-update.json")
	if _, err = os.Lstat(path); err == nil {
		before, e := fingerprint(path)
		if e != nil {
			return e
		}
		p.Guards = append(p.Guards, Guard{Path: path, Expected: before})
		old, e := readJournalRecord(path)
		if e != nil || old.Instance != p.Instance || old.Operation != "update" {
			return fmt.Errorf("上次更新记录不属于当前实例或已损坏")
		}
		allowedHome := false
		for _, home := range m.rootPaths("stk") {
			if home == old.Home {
				allowedHome = true
			}
		}
		if !allowedHome {
			return fmt.Errorf("旧更新记录的位置不在当前实例的路径历史中")
		}
		// A relocated history is retired, never replayed. Rebase only its
		// private backup file locations so the next update stays bounded.
		previousHome := old.Home
		for i, b := range old.Entries {
			if !within(previousHome, b.Change.Path) {
				if m.State.Entry == nil {
					return fmt.Errorf("更新记录包含管理目录外目标，拒绝迁移")
				}
				entry, e := m.resolve(m.State.Entry.Path)
				if e != nil || m.rebase(b.Change.Path) != entry || b.Change.Kind != "link" {
					return fmt.Errorf("更新记录包含非受管命令入口的外部目标")
				}
				old.Entries[i].Change.Path = entry
			} else {
				rel, _ := filepath.Rel(previousHome, b.Change.Path)
				old.Entries[i].Change.Path = filepath.Join(p.Home, rel)
			}
			if b.File != "" {
				rel, _ := filepath.Rel(previousHome, b.File)
				old.Entries[i].File = filepath.Join(p.Home, rel)
			}
		}
		old.Home = p.Home
		p.retiredUpdate = &old
	} else if !os.IsNotExist(err) {
		return err
	}
	j := journalFor(p)
	if err = validateJournal(j, p.Home); err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if len(raw) > maxMetadata {
		return fmt.Errorf("更新回滚记录过大")
	}
	return p.add(path, "write", "记录最近一次更新及其经过校验的备份", raw, "", 0600)
}

// Rollback is deliberately one-step and optimistic: any later change to an
// affected file or metadata stops the operation. It never overwrites a newer
// configuration or guesses an old physical mount location.
func (m *Manager) Rollback() (*Plan, error) {
	if !m.Initialized {
		return nil, fmt.Errorf("尚未登记")
	}
	if err := m.requireCurrentRoots(); err != nil {
		return nil, err
	}
	before, err := fingerprint(filepath.Join(m.Home, "last-update.json"))
	if err != nil {
		return nil, err
	}
	j, err := readJournal(filepath.Join(m.Home, "last-update.json"), m.Home)
	if err != nil {
		return nil, err
	}
	if j.Operation != "update" || j.Instance != m.State.Instance {
		return nil, fmt.Errorf("没有属于当前实例的更新回滚记录")
	}
	if err = validateRestore(j, m.Home, true); err != nil {
		return nil, err
	}
	p := &Plan{Home: m.Home, Instance: m.State.Instance, Operation: "rollback", rollbackSource: &j}
	p.Guards = append(p.Guards, Guard{Path: filepath.Join(m.Home, "last-update.json"), Expected: before})
	for _, name := range []string{"config.toml", "state.json"} {
		p.Guards = append(p.Guards, Guard{Path: filepath.Join(m.Home, name), Expected: m.inputs[name]})
	}
	for i := len(j.Entries) - 1; i >= 0; i-- {
		if err = appendRollbackChange(p, j.Entries[i]); err != nil {
			return nil, err
		}
	}
	if err = p.add(filepath.Join(m.Home, "last-update.json"), "remove", "消耗一次更新回滚记录", nil, "", 0); err != nil {
		return nil, err
	}
	return p, nil
}

func appendRollbackChange(p *Plan, b backup) error {
	c := b.Change
	// The update's After fingerprint authorizes restoration. A fresh read
	// during plan construction must never authorize overwriting a later edit.
	actual, err := fingerprint(c.Path)
	if err != nil || actual != c.After {
		return fmt.Errorf("回滚计划生成期间目标发生变化: %s", c.Path)
	}
	p.Guards = append(p.Guards, Guard{Path: c.Path, Expected: c.After})
	switch c.Before.Kind {
	case "file":
		data, err := readBackup(b)
		if err != nil {
			return err
		}
		return p.add(c.Path, "write", "回滚最近一次更新（严格匹配更新后的内容）", data, "", os.FileMode(c.Before.Mode))
	case "link":
		return p.add(c.Path, "link", "恢复更新前的链接", nil, c.Before.Digest, 0)
	case "absent":
		if c.After.Kind == "directory" {
			return fmt.Errorf("更新包含目录创建，拒绝自动删除数据目录；请显式安装旧版本")
		}
		return p.add(c.Path, "remove", "移除本次更新新增的 STK 程序", nil, "", 0)
	default:
		return fmt.Errorf("回滚记录含不支持的目标")
	}
}
