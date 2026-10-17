package stk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Fingerprint struct {
	Kind   string `json:"kind"`
	Digest string `json:"digest,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}
type Change struct {
	Path   string      `json:"path"`
	Kind   string      `json:"kind"`
	Reason string      `json:"reason"`
	Before Fingerprint `json:"before"`
	After  Fingerprint `json:"after"`
	Mode   os.FileMode `json:"-"`
	Data   []byte      `json:"-"`
	Target string      `json:"-"`
}
type Plan struct {
	Home           string     `json:"home"`
	Instance       string     `json:"instance,omitempty"`
	Changes        []Change   `json:"changes"`
	Warnings       []string   `json:"warnings,omitempty"`
	Downloads      []Download `json:"downloads,omitempty"`
	Deferred       bool       `json:"deferred,omitempty"`
	Guards         []Guard    `json:"-"`
	Operation      string     `json:"operation,omitempty"`
	Transaction    string     `json:"transaction,omitempty"`
	rollbackSource *journal
	retiredUpdate  *journal
	testHook       func(string, int) error
}

type Guard struct {
	Path     string
	Expected Fingerprint
}

func (p *Plan) guardBytes(path string, data []byte, mode os.FileMode) {
	p.Guards = append(p.Guards, Guard{Path: path, Expected: Fingerprint{Kind: "file", Digest: digest(data), Mode: uint32(mode)}})
}

func jsonDecode(b []byte, v any) error { return json.Unmarshal(b, v) }

func fingerprint(path string) (Fingerprint, error) {
	s, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return Fingerprint{Kind: "absent"}, nil
	}
	if e != nil {
		return Fingerprint{}, e
	}
	f := Fingerprint{Mode: uint32(s.Mode().Perm())}
	if s.IsDir() {
		f.Kind = "directory"
		return f, nil
	}
	if s.Mode()&os.ModeSymlink != 0 {
		target, e := os.Readlink(path)
		f.Kind = "link"
		f.Digest = target
		return f, e
	}
	if !s.Mode().IsRegular() {
		return f, fmt.Errorf("拒绝替换非普通文件: %s", path)
	}
	file, e := os.Open(path)
	if e != nil {
		return f, e
	}
	defer file.Close()
	h := sha256.New()
	if _, e = io.Copy(h, file); e != nil {
		return f, e
	}
	f.Kind = "file"
	f.Digest = hex.EncodeToString(h.Sum(nil))
	return f, nil
}

func (p *Plan) add(path, kind, reason string, data []byte, target string, mode os.FileMode) error {
	if kind != "write" && kind != "link" && kind != "dir" && kind != "remove" {
		return fmt.Errorf("未知变更类型: %s", kind)
	}
	if err := absolute(path); err != nil {
		return err
	}
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	before, e := fingerprint(path)
	if e != nil {
		return e
	}
	if kind == "remove" && before.Kind == "absent" {
		return nil
	}
	if kind == "dir" && before.Kind == "directory" {
		return nil
	}
	if kind == "dir" && before.Kind != "absent" {
		return fmt.Errorf("不能创建目录: %s", path)
	}
	if kind != "dir" && before.Kind == "directory" {
		return fmt.Errorf("拒绝替换目录: %s", path)
	}
	if kind == "write" {
		if before.Kind == "link" {
			return fmt.Errorf("受管文件是外部符号链接: %s", path)
		}
		s := sha256.Sum256(data)
		if before.Kind == "file" && before.Digest == hex.EncodeToString(s[:]) && before.Mode == uint32(mode) {
			return nil
		}
	} else if kind == "link" && before.Kind == "link" && before.Digest == target {
		return nil
	}
	for _, c := range p.Changes {
		if c.Path == path {
			return fmt.Errorf("重复变更目标: %s", path)
		}
	}
	after := Fingerprint{Kind: "file", Digest: digest(data), Mode: uint32(mode)}
	if kind == "link" {
		after = Fingerprint{Kind: "link", Digest: target, Mode: 0777}
	}
	if kind == "dir" {
		after = Fingerprint{Kind: "directory", Mode: 0700}
	}
	if kind == "remove" {
		after = Fingerprint{Kind: "absent"}
	}
	p.Changes = append(p.Changes, Change{Path: path, Kind: kind, Reason: reason, Before: before, After: after, Mode: mode, Data: data, Target: target})
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	if err := makeDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".stk-write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if original, err := os.Lstat(path); err == nil {
		if !original.Mode().IsRegular() {
			f.Close()
			return fmt.Errorf("原子写入目标不是普通文件: %s", path)
		}
		if owner, ok := original.Sys().(*syscall.Stat_t); ok {
			current, e := f.Stat()
			if e != nil {
				f.Close()
				return e
			}
			if temporary, ok := current.Sys().(*syscall.Stat_t); ok && (temporary.Uid != owner.Uid || temporary.Gid != owner.Gid) {
				if e = f.Chown(int(owner.Uid), int(owner.Gid)); e != nil {
					f.Close()
					return fmt.Errorf("无法保留原文件归属: %w", e)
				}
			}
		}
	} else if !os.IsNotExist(err) {
		f.Close()
		return err
	}
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	return syncParent(path)
}

func syncParent(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	err = d.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// Persist namespace changes before retiring their recovery material. Syncing
// just a newly created leaf does not persist the ancestors made for it.
func makeDirectories(path string) error {
	s, err := os.Lstat(path)
	if err == nil {
		if !s.IsDir() {
			return fmt.Errorf("目录位置已被其他文件占用: %s", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err = makeDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	if err = os.Mkdir(path, 0700); err != nil {
		if !os.IsExist(err) {
			return err
		}
		s, err = os.Lstat(path)
		if err != nil || !s.IsDir() {
			return fmt.Errorf("创建目录期间位置发生变化: %s", path)
		}
	}
	return syncParent(path)
}

func removeSynced(path string) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncParent(path)
}

const transactionSchema = 2

type backup struct {
	Change Change `json:"change"`
	File   string `json:"file,omitempty"`
}

type journal struct {
	Schema    int      `json:"schema"`
	Home      string   `json:"home"`
	ID        string   `json:"id"`
	Instance  string   `json:"instance"`
	Operation string   `json:"operation,omitempty"`
	Entries   []backup `json:"entries"`
}

func validID(value string) bool { b, err := hex.DecodeString(value); return err == nil && len(b) == 16 }

func journalFor(p *Plan) journal {
	j := journal{Schema: transactionSchema, Home: p.Home, ID: p.Transaction, Instance: p.Instance, Operation: p.Operation}
	for i, c := range p.Changes {
		b := backup{Change: c}
		if c.Before.Kind == "file" {
			b.File = filepath.Join(p.Home, "backups", p.Transaction, fmt.Sprintf("%04d", i))
		}
		j.Entries = append(j.Entries, b)
	}
	return j
}

func validFingerprint(f Fingerprint) bool {
	if f.Mode > 0777 {
		return false
	}
	switch f.Kind {
	case "absent":
		return f.Mode == 0 && f.Digest == ""
	case "file":
		return validDigest(f.Digest)
	case "directory":
		return f.Digest == ""
	case "link":
		return f.Digest != "" && !strings.ContainsRune(f.Digest, 0)
	}
	return false
}

func validateJournal(j journal, home string) error {
	if j.Schema != transactionSchema || j.Home != home || !validID(j.ID) || !validID(j.Instance) || len(j.Entries) == 0 || len(j.Entries) > 1024 {
		return fmt.Errorf("恢复记录版本、位置或身份无效；旧版记录需使用对应版本处理")
	}
	if j.Operation != "" && j.Operation != "update" && j.Operation != "rollback" {
		return fmt.Errorf("无效事务类型")
	}
	seen := map[string]bool{}
	for i, b := range j.Entries {
		c := b.Change
		if absolute(c.Path) != nil || seen[c.Path] || within(c.Path, home) || within(filepath.Join(home, "backups"), c.Path) || c.Path == filepath.Join(home, "lock") || c.Path == filepath.Join(home, "pending.json") {
			return fmt.Errorf("恢复目标越界、重复或涉及事务自身")
		}
		seen[c.Path] = true
		if !validFingerprint(c.Before) || !validFingerprint(c.After) {
			return fmt.Errorf("无效恢复指纹")
		}
		valid := false
		switch c.Kind {
		case "write":
			valid = (c.Before.Kind == "absent" || c.Before.Kind == "file") && c.After.Kind == "file"
		case "link":
			valid = c.Before.Kind != "directory" && c.After.Kind == "link"
		case "dir":
			valid = c.Before.Kind == "absent" && c.After.Kind == "directory"
		case "remove":
			valid = (c.Before.Kind == "file" || c.Before.Kind == "link") && c.After.Kind == "absent"
		}
		if !valid {
			return fmt.Errorf("恢复记录中的变更类型无效")
		}
		expected := ""
		if c.Before.Kind == "file" {
			expected = filepath.Join(home, "backups", j.ID, fmt.Sprintf("%04d", i))
		}
		if b.File != expected {
			return fmt.Errorf("恢复备份路径与事务不符")
		}
	}
	return nil
}

func readJournal(path, home string) (journal, error) {
	j, err := readJournalRecord(path)
	if err == nil && j.Home != home {
		err = fmt.Errorf("恢复记录属于其他物理位置，不会重放旧挂载路径")
	}
	return j, err
}

func readJournalRecord(path string) (journal, error) {
	var j journal
	b, err := readRegular(path)
	if err != nil {
		return j, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&j); err != nil {
		return j, fmt.Errorf("恢复记录格式无效")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return j, fmt.Errorf("恢复记录含额外内容")
	}
	if absolute(j.Home) != nil {
		return j, fmt.Errorf("恢复记录位置无效")
	}
	return j, validateJournal(j, j.Home)
}

func journalIdentity(j journal, home string) error {
	b, err := readRegular(filepath.Join(home, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} // An interrupted first setup may not have written state yet.
	if err != nil {
		return err
	}
	var state State
	if json.Unmarshal(b, &state) != nil || state.Instance != j.Instance {
		return fmt.Errorf("恢复记录不属于当前 STK 实例")
	}
	return nil
}

func snapshot(b backup) error {
	if b.Change.Before.Kind != "file" {
		return nil
	}
	data, err := readRegularLarge(b.Change.Path)
	if err != nil {
		return err
	}
	fp, err := fingerprint(b.Change.Path)
	if err != nil || fp != b.Change.Before || digest(data) != b.Change.Before.Digest {
		return fmt.Errorf("备份期间目标发生变化: %s", b.Change.Path)
	}
	return atomicWrite(b.File, data, 0600)
}

func readBackup(b backup) ([]byte, error) {
	data, err := readRegularLarge(b.File)
	if err != nil {
		return nil, err
	}
	if digest(data) != b.Change.Before.Digest {
		return nil, fmt.Errorf("恢复备份校验失败")
	}
	return data, nil
}

func validateRestore(j journal, home string, strict bool) error {
	if err := validateJournal(j, home); err != nil {
		return err
	}
	if err := journalIdentity(j, home); err != nil {
		return err
	}
	for _, b := range j.Entries {
		c := b.Change
		if err := noSymlinkParents(c.Path); err != nil {
			return err
		}
		actual, err := fingerprint(c.Path)
		if err != nil {
			return err
		}
		if actual != c.After && (strict || actual != c.Before) {
			return fmt.Errorf("恢复目标已被外部修改，保留恢复记录: %s", c.Path)
		}
		if actual != c.Before && c.Before.Kind != "absent" && !directory(filepath.Dir(c.Path)) {
			return fmt.Errorf("原恢复位置不可用；不会创建旧挂载: %s", c.Path)
		}
		if c.Before.Kind == "file" {
			if _, err := readBackup(b); err != nil {
				return err
			}
		}
		if actual.Kind == "directory" && c.Before.Kind == "absent" {
			entries, err := os.ReadDir(c.Path)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				path := filepath.Join(c.Path, entry.Name())
				owned := false
				for _, other := range j.Entries {
					if other.Change.Path == path && other.Change.Before.Kind == "absent" {
						owned = true
						break
					}
				}
				if !owned {
					return fmt.Errorf("恢复目录出现未登记内容，保留目录和恢复记录: %s", c.Path)
				}
			}
		}
	}
	return nil
}

func restore(j journal, home string) error {
	if err := validateRestore(j, home, false); err != nil {
		return err
	}
	for i := len(j.Entries) - 1; i >= 0; i-- {
		b := j.Entries[i]
		c := b.Change
		actual, err := fingerprint(c.Path)
		if err != nil {
			return err
		}
		if actual == c.Before {
			continue
		}
		if actual != c.After {
			return fmt.Errorf("恢复期间出现外部修改: %s", c.Path)
		}
		if !directory(filepath.Dir(c.Path)) {
			return fmt.Errorf("原恢复位置不可用；不会创建旧挂载: %s", c.Path)
		}
		switch c.Before.Kind {
		case "absent":
			if err = noSymlinkParents(c.Path); err == nil {
				err = removeSynced(c.Path)
			}
		case "file":
			var data []byte
			data, err = readBackup(b)
			if err == nil {
				actual, err = fingerprint(c.Path)
				if err == nil && (actual != c.After || !directory(filepath.Dir(c.Path))) {
					err = fmt.Errorf("恢复读取备份后目标或父目录发生变化: %s", c.Path)
				}
			}
			if err == nil {
				err = atomicWrite(c.Path, data, os.FileMode(c.Before.Mode))
			}
		case "link":
			err = replaceLink(c.Path, c.Before.Digest)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Only verified files named by this journal are eligible for backup cleanup.
// Unexpected files are preserved, and directories are removed only if empty.
func cleanupJournal(j journal) {
	cleanupBackupFiles(j, nil)
}

func cleanupBackupFiles(j journal, keep map[string]bool) {
	if validateJournal(j, j.Home) != nil {
		return
	}
	for _, b := range j.Entries {
		if b.File == "" {
			continue
		}
		if noSymlinkParents(b.File) != nil {
			return
		}
		fp, err := fingerprint(b.File)
		if os.IsNotExist(err) || fp.Kind == "absent" {
			continue
		}
		if err != nil || fp.Kind != "file" || fp.Digest != b.Change.Before.Digest || fp.Mode != 0600 {
			return
		}
	}
	for _, b := range j.Entries {
		if b.File != "" && !keep[b.File] {
			if noSymlinkParents(b.File) != nil {
				return
			}
			fp, err := fingerprint(b.File)
			if fp.Kind == "absent" {
				continue
			}
			if err != nil || fp != (Fingerprint{Kind: "file", Digest: b.Change.Before.Digest, Mode: 0600}) {
				return
			}
			if removeSynced(b.File) != nil {
				return
			}
		}
	}
	_ = removeSynced(filepath.Join(j.Home, "backups", j.ID))
}

func readRegularLarge(path string) ([]byte, error) {
	if err := noSymlinkParents(path); err != nil {
		return nil, err
	}
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Size() > maxArtifact {
		return nil, fmt.Errorf("无效安装文件: %s", path)
	}
	return os.ReadFile(path)
}

func replaceLink(path, target string) error {
	if err := noSymlinkParents(path); err != nil {
		return err
	}
	if err := makeDirectories(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".stk-link-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Close()
	_ = os.Remove(tmp)
	defer os.Remove(tmp)
	if err = os.Symlink(target, tmp); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncParent(path)
}

func lockHome(home string, create bool) (*os.File, error) {
	if err := absolute(home); err != nil {
		return nil, err
	}
	if err := noSymlinkParents(filepath.Join(home, "lock")); err != nil {
		return nil, err
	}
	if create {
		if err := makeDirectories(home); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(home, "lock")
	if s, err := os.Lstat(path); err == nil && !s.Mode().IsRegular() {
		return nil, fmt.Errorf("锁不是普通文件")
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("另一个 STK 操作正在执行")
	}
	return f, nil
}

func unlockHome(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }

func planPreconditions(p *Plan) error {
	for _, g := range p.Guards {
		actual, err := fingerprint(g.Path)
		if err != nil || actual != g.Expected {
			return fmt.Errorf("读取后管理清单或配置发生变化，请重新生成计划: %s", g.Path)
		}
	}
	for _, c := range p.Changes {
		if err := noSymlinkParents(c.Path); err != nil {
			return err
		}
		actual, err := fingerprint(c.Path)
		if err != nil {
			return err
		}
		if actual != c.Before {
			return fmt.Errorf("预览后文件发生变化，拒绝执行: %s", c.Path)
		}
	}
	return nil
}

func Apply(p *Plan) error {
	if p.Deferred {
		return fmt.Errorf("在线预览不含下载内容，不能直接执行；请重新运行命令")
	}
	if len(p.Changes) == 0 {
		return nil
	}
	lock, err := lockHome(p.Home, true)
	if err != nil {
		return err
	}
	defer unlockHome(lock)
	if _, err = os.Lstat(filepath.Join(p.Home, "pending.json")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("存在未完成或不可读的事务记录，请先 repair --recover")
	}
	if !validID(p.Instance) {
		b, e := readRegular(filepath.Join(p.Home, "state.json"))
		if e == nil {
			var state State
			if json.Unmarshal(b, &state) == nil {
				p.Instance = state.Instance
			}
		}
		if !validID(p.Instance) {
			p.Instance, err = instanceID()
			if err != nil {
				return err
			}
		}
	}
	if p.Transaction == "" {
		p.Transaction, err = instanceID()
		if err != nil {
			return err
		}
	}
	j := journalFor(p)
	if err = validateJournal(j, p.Home); err != nil {
		return err
	}
	if err = journalIdentity(j, p.Home); err != nil {
		return err
	}
	for _, c := range p.Changes {
		if c.Kind == "write" && (digest(c.Data) != c.After.Digest || uint32(c.Mode) != c.After.Mode) || c.Kind == "link" && c.Target != c.After.Digest {
			return fmt.Errorf("执行负载与计划摘要不符")
		}
	}
	if err = planPreconditions(p); err != nil {
		return err
	}
	backupDir := filepath.Join(p.Home, "backups", j.ID)
	if err = noSymlinkParents(filepath.Join(backupDir, "entry")); err != nil {
		return err
	}
	if err = makeDirectories(filepath.Dir(backupDir)); err != nil {
		return err
	}
	if err = os.Mkdir(backupDir, 0700); err != nil {
		return fmt.Errorf("事务备份目录已存在或不可创建，请重新生成计划")
	}
	pending := false
	defer func() {
		if !pending {
			cleanupJournal(j)
		}
	}()
	if err = syncParent(backupDir); err != nil {
		return err
	}
	for i, b := range j.Entries {
		if err = snapshot(b); err != nil {
			return err
		}
		if p.testHook != nil {
			if err = p.testHook("snapshot", i); err != nil {
				return err
			}
		}
	}
	// Snapshotting a large program can take time. Check all inputs again
	// before recording a pending transaction or replacing the first target.
	if err = planPreconditions(p); err != nil {
		return err
	}
	raw, _ := json.Marshal(j)
	if len(raw) > maxMetadata {
		return fmt.Errorf("事务记录过大")
	}
	if err = atomicWrite(filepath.Join(p.Home, "pending.json"), raw, 0600); err != nil {
		if _, e := os.Lstat(filepath.Join(p.Home, "pending.json")); !os.IsNotExist(e) {
			pending = true
		}
		return err
	}
	pending = true
	for i, c := range p.Changes {
		var actual Fingerprint
		actual, err = fingerprint(c.Path)
		if err == nil && actual != c.Before {
			err = fmt.Errorf("执行期间目标发生变化: %s", c.Path)
		}
		if err == nil {
			err = noSymlinkParents(c.Path)
		}
		if err == nil {
			switch c.Kind {
			case "write":
				err = atomicWrite(c.Path, c.Data, c.Mode)
			case "link":
				err = replaceLink(c.Path, c.Target)
			case "dir":
				err = makeDirectories(c.Path)
			case "remove":
				err = removeSynced(c.Path)
			}
		}
		if err == nil && p.testHook != nil {
			err = p.testHook("change", i)
		}
		if err != nil {
			if recovery := restore(j, p.Home); recovery != nil {
				return fmt.Errorf("执行失败: %v；恢复停止，保留 pending.json: %w", err, recovery)
			}
			if e := removeSynced(filepath.Join(p.Home, "pending.json")); e != nil {
				return e
			}
			pending = false
			return err
		}
	}
	if err = removeSynced(filepath.Join(p.Home, "pending.json")); err != nil {
		return err
	}
	pending = false
	if p.Operation == "update" {
		// last-update excludes its own replacement to avoid recursive records.
		// Its previous bytes are needed only while this transaction is pending.
		keep := map[string]bool{}
		for _, b := range j.Entries {
			if b.File != "" && b.Change.Path != filepath.Join(p.Home, "last-update.json") {
				keep[b.File] = true
			}
		}
		cleanupBackupFiles(j, keep)
		// Keep this generation's backups for one-step rollback. Only retire
		// a previous, same-instance update's verified backup files.
		if p.retiredUpdate != nil {
			cleanupJournal(*p.retiredUpdate)
		}
		pending = true // Successful update backups are intentionally retained.
	} else if p.rollbackSource != nil {
		cleanupJournal(*p.rollbackSource)
	}
	return nil
}

func Recover(home string) error {
	lock, err := lockHome(home, false)
	if err != nil {
		return err
	}
	defer unlockHome(lock)
	j, err := readJournal(filepath.Join(home, "pending.json"), home)
	if err != nil {
		return err
	}
	if err = restore(j, home); err != nil {
		return err
	}
	if err = removeSynced(filepath.Join(home, "pending.json")); err != nil {
		return err
	}
	cleanupJournal(j)
	return nil
}

func sameBytes(path string, data []byte) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, data)
}
