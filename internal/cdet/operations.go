package cdet

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Plans describe one invocation. They are never persisted or replayed.
type Step struct {
	Module string `json:"module"`
	Action string `json:"action"`
	Detail string `json:"detail"`
}
type Import struct {
	Module string `json:"module"`
	Source string `json:"source"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Digest string `json:"digest"`
}
type Plan struct {
	Prefix                                    string        `json:"prefix"`
	Store                                     string        `json:"store"`
	Operation                                 string        `json:"operation"`
	Steps                                     []Step        `json:"steps"`
	Imports                                   []Import      `json:"imports,omitempty"`
	Versions                                  []VersionInfo `json:"versions,omitempty"`
	Warnings                                  []string      `json:"warnings,omitempty"`
	owner                                     *Manager
	run                                       func() error
	configDigest, stateDigest, identityDigest string
	applied                                   bool
}

func Apply(p *Plan) error {
	if p == nil || p.owner == nil {
		return fmt.Errorf("计划无效")
	}
	return p.owner.Apply(p)
}

func (m *Manager) plan(operation string) *Plan {
	return &Plan{Prefix: m.Prefix, Store: m.Store, Operation: operation, owner: m, configDigest: m.configDigest, stateDigest: m.stateDigest, identityDigest: m.identityDigest}
}
func metadataUnchanged(path, want string) error {
	b, e := readRegular(path)
	if want == "" && os.IsNotExist(e) {
		return nil
	}
	if e != nil || digest(b) != want {
		return fmt.Errorf("计划生成后管理元数据已变化，请重新运行命令: %s", path)
	}
	return nil
}

func (m *Manager) checkInstallRoot() error {
	id, e := readInstallID(m.Prefix)
	if e != nil || id != m.State.InstallID {
		return fmt.Errorf("安装目录不可用或身份变化，未重建旧路径")
	}
	return metadataUnchanged(filepath.Join(m.Prefix, "config.toml"), m.configDigest)
}
func (m *Manager) checkManagedRoots() error {
	if e := m.checkInstallRoot(); e != nil {
		return e
	}
	if m.stateDigest == "" {
		return fmt.Errorf("组件存储尚未登记")
	}
	return metadataUnchanged(filepath.Join(m.metadataDir(), "state.json"), m.stateDigest)
}

func (m *Manager) Apply(p *Plan) error {
	if p == nil || p.owner != m || p.Prefix != m.Prefix || p.Store != m.Store || p.applied {
		return fmt.Errorf("计划无效或已执行")
	}
	if m.Options.DryRun {
		return nil
	}
	if e := validateRoots(m.Prefix, m.Store); e != nil {
		return e
	}
	create := !m.Initialized && m.stateDigest == ""
	a, e := lockDir(m.Prefix, create)
	if e != nil {
		return e
	}
	defer unlock(a)
	b, e := lockDir(m.metadataDir(), create)
	if e != nil {
		return e
	}
	defer unlock(b)
	for _, v := range [][2]string{{filepath.Join(m.Prefix, "config.toml"), p.configDigest}, {filepath.Join(m.Prefix, ".instance"), p.identityDigest}, {filepath.Join(m.metadataDir(), "state.json"), p.stateDigest}} {
		if e = metadataUnchanged(v[0], v[1]); e != nil {
			return e
		}
	}
	p.applied = true
	if m.Initialized {
		if e = m.checkManagedRoots(); e != nil {
			return e
		}
	}
	if p.run != nil {
		return p.run()
	}
	return nil
}
func (m *Manager) saveState() error {
	if e := m.checkInstallRoot(); e != nil {
		return e
	}
	if m.stateDigest != "" {
		if e := metadataUnchanged(filepath.Join(m.metadataDir(), "state.json"), m.stateDigest); e != nil {
			return e
		}
	}
	if e := validateState(m.State); e != nil {
		return e
	}
	b, e := json.MarshalIndent(m.State, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if len(b) > maxMetadata {
		return fmt.Errorf("组件清单过大")
	}
	if e = atomicWriteIn(m.Store, filepath.Join(m.metadataDir(), "state.json"), b, 0600); e != nil {
		return e
	}
	m.stateDigest = digest(b)
	return nil
}
func (m *Manager) saveBootstrap() error {
	if e := m.checkManagedRoots(); e != nil {
		return e
	}
	cfg := m.bootstrap()
	b, e := toml.Marshal(cfg)
	if e != nil {
		return e
	}
	if e = atomicWriteIn(m.Prefix, filepath.Join(m.Prefix, "config.toml"), b, 0600); e != nil {
		return e
	}
	m.Config = cfg
	m.configDigest = digest(b)
	m.Initialized = true
	m.Relocated = false
	return nil
}

// Only unpublished private staging directories can be recursively cleaned.
func cleanupStaging(path string) error {
	if absent(path) {
		return nil
	}
	e := filepath.WalkDir(path, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.Chmod(p, 0700)
		}
		return nil
	})
	if e != nil {
		return e
	}
	return os.RemoveAll(path)
}

// A tiny import receipt identifies a complete copy until its ownership is saved.
// It cannot replay writes, remove a HOME or restore an older program.
type importReceipt struct {
	StoreID   string `json:"store_id"`
	InstallID string `json:"install_id"`
	Digest    string `json:"digest"`
}

func (m *Manager) receiptPath(name string) string {
	return filepath.Join(m.metadataDir(), "import-"+name+".json")
}
func (m *Manager) resumeImport(name string) (bool, error) {
	target := filepath.Join(m.Store, name)
	if absent(target) {
		return false, nil
	}
	raw, e := readRegular(m.receiptPath(name))
	if e != nil {
		return false, fmt.Errorf("%s 目标数据已存在但未登记，拒绝覆盖", name)
	}
	var r importReceipt
	if strictJSON(raw, &r) != nil || r.StoreID != m.State.ID || r.InstallID != m.State.InstallID || !validDigest(r.Digest) {
		return false, fmt.Errorf("迁入收据无效")
	}
	tree, e := snapshotTree(target)
	if e != nil || tree.RawDigest != r.Digest {
		return false, fmt.Errorf("%s 未登记的迁入副本已变化，请检查并保留该副本", name)
	}
	return true, nil
}
func (m *Manager) publishData(name string, t *Tree) (result error) {
	if e := m.checkManagedRoots(); e != nil {
		return e
	}
	target := filepath.Join(m.Store, name)
	if ok, e := m.resumeImport(name); e != nil {
		return e
	} else if ok {
		return nil
	}
	if t.Source != "" {
		if e := checkInactive([]string{t.Source}); e != nil {
			return e
		}
		s, e := t.sourceSnapshot()
		if e != nil || s.RawDigest != t.RawDigest {
			return fmt.Errorf("%s 迁入源已变化，请重新检查", name)
		}
	}
	id, e := instanceID()
	if e != nil {
		return e
	}
	stage := filepath.Join(m.metadataDir(), "import-"+id)
	defer func() {
		if e := cleanupStaging(stage); e != nil {
			if result == nil {
				result = fmt.Errorf("迁入临时副本清理失败，保留位置: %s", stage)
			} else {
				result = fmt.Errorf("%w；临时副本保留在 %s", result, stage)
			}
		}
	}()
	if e = copyTree(t, stage); e != nil {
		return e
	}
	if t.Source != "" {
		s, er := t.sourceSnapshot()
		if er != nil || s.RawDigest != t.RawDigest {
			return fmt.Errorf("%s 复制期间迁入源已变化", name)
		}
		if e = checkInactive([]string{t.Source, target}); e != nil {
			return e
		}
	}
	raw, _ := json.Marshal(importReceipt{m.State.ID, m.State.InstallID, t.Digest})
	if e = m.checkManagedRoots(); e != nil {
		return e
	}
	if e = atomicWriteIn(m.Store, m.receiptPath(name), append(raw, '\n'), 0600); e != nil {
		return e
	}
	if !absent(target) {
		return fmt.Errorf("迁入目标已被占用，原数据和副本均保留")
	}
	if e = os.Rename(stage, target); e != nil {
		return e
	}
	return syncParent(target)
}
