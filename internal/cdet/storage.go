package cdet

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

	"github.com/pelletier/go-toml/v2"
)

const maxMetadata = 2 << 20

func digest(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func directory(p string) bool { s, e := os.Lstat(p); return e == nil && s.IsDir() }
func regular(p string) bool   { s, e := os.Lstat(p); return e == nil && s.Mode().IsRegular() }
func executable(p string) bool {
	s, e := os.Lstat(p)
	return e == nil && s.Mode().IsRegular() && s.Mode().Perm()&0111 != 0
}
func absent(p string) bool { _, e := os.Lstat(p); return errors.Is(e, os.ErrNotExist) }

func noSymlinkParents(p string) error {
	for p = filepath.Dir(p); p != filepath.Dir(p); p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if !s.IsDir() {
			return fmt.Errorf("目录经过符号链接或非目录: %s", p)
		}
	}
	return nil
}

func readRegularLimit(p string, limit int64) ([]byte, error) {
	if e := noSymlinkParents(p); e != nil {
		return nil, e
	}
	s, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Size() > limit {
		return nil, fmt.Errorf("文件类型或大小不支持: %s", p)
	}
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(s, opened) {
		return nil, fmt.Errorf("读取期间文件被替换: %s", p)
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("文件大小越界: %s", p)
	}
	return b, e
}
func readRegular(p string) ([]byte, error)      { return readRegularLimit(p, maxMetadata) }
func readRegularLarge(p string) ([]byte, error) { return readRegularLimit(p, maxArtifact) }

// The install identity travels with its root and allows a partial init to retry
// without binding an unrelated installation to this store.
func readInstallID(prefix string) (string, error) {
	b, e := readRegular(filepath.Join(prefix, ".instance"))
	if e != nil {
		return "", e
	}
	id := strings.TrimSuffix(string(b), "\n")
	if !validID(id) || string(b) != id+"\n" {
		return "", fmt.Errorf("安装目录身份无效")
	}
	s, e := os.Lstat(filepath.Join(prefix, ".instance"))
	if e != nil || s.Mode().Perm() != 0600 {
		return "", fmt.Errorf("安装目录身份权限无效")
	}
	return id, nil
}

func strictJSON(b []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(value); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("JSON 含额外内容")
	}
	return nil
}

func readState(store string) (State, []byte, error) {
	var s State
	b, e := readRegular(filepath.Join(store, ".cdet", "state.json"))
	if e != nil {
		return s, nil, e
	}
	if e = strictJSON(b, &s); e != nil {
		return s, nil, fmt.Errorf("组件清单无效: %w", e)
	}
	if e = validateState(s); e != nil {
		return s, nil, e
	}
	return s, b, nil
}

func validateState(s State) error {
	if s.Schema != Schema || !validID(s.ID) || !validID(s.InstallID) || s.Components == nil || s.Generated == nil {
		return fmt.Errorf("不支持或不完整的组件清单；请使用独立双目录实例，原目录保持不变")
	}
	for name, c := range s.Components {
		backend := "artifact"
		if name == "codex" {
			backend = "official"
		}
		if name == "proxy" {
			backend = "mihomo"
		}
		if !knownModule(name) || c.Backend != backend || (c.Digest != "" && !validDigest(c.Digest)) || (c.Origin != "installed" && c.Origin != "imported") {
			return fmt.Errorf("无效组件记录: %s", name)
		}
	}
	allowed := map[string]bool{"bin/cdet": true, "env.sh": true}
	for _, n := range []string{"codex", "proxy", "gds"} {
		allowed[filepath.Join("bin", moduleCommand(n))] = true
	}
	for p, h := range s.Generated {
		if !allowed[p] || !validDigest(h) {
			return fmt.Errorf("受管入口清单无效")
		}
	}
	return nil
}

func New(opts Options) (*Manager, error) {
	h, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	if p, e := filepath.EvalSymlinks(exe); e == nil {
		exe = p
	}
	prefix := opts.Prefix
	if prefix == "" && filepath.Base(filepath.Dir(exe)) == "bin" && regular(filepath.Join(filepath.Dir(filepath.Dir(exe)), "config.toml")) {
		prefix = filepath.Dir(filepath.Dir(exe))
	}
	if prefix == "" {
		prefix = os.Getenv("CDET_PREFIX")
	}
	if prefix == "" {
		prefix = filepath.Join(h, ".cdet")
	}
	if e := absolute(prefix); e != nil {
		return nil, e
	}
	if e := noSymlinkParents(filepath.Join(prefix, "config.toml")); e != nil {
		return nil, e
	}
	m := &Manager{Prefix: prefix, Executable: exe, UserHome: h, Path: os.Getenv("PATH"), Options: opts, State: State{Schema: Schema, Components: map[string]Component{}, Generated: map[string]string{}}}
	b, e := readRegular(filepath.Join(prefix, "config.toml"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if e == nil {
		if e = toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&m.Config); e != nil || m.Config.Schema != Schema || !validID(m.Config.InstallID) || !validID(m.Config.StoreID) || absolute(m.Config.Store) != nil || filepath.IsAbs(m.Config.RelativeStore) || filepath.Clean(m.Config.RelativeStore) != m.Config.RelativeStore || strings.ContainsAny(m.Config.RelativeStore, "\x00\r\n") {
			return nil, fmt.Errorf("引导配置无效或属于旧版；请使用独立双目录实例")
		}
		iid, er := readInstallID(prefix)
		if er != nil || iid != m.Config.InstallID {
			return nil, fmt.Errorf("安装目录身份缺失、损坏或不匹配")
		}
		m.configDigest = digest(b)
		m.identityDigest = digest([]byte(iid + "\n"))
		candidates := []string{opts.Store}
		if opts.Store == "" {
			candidates = []string{m.Config.Store, filepath.Clean(filepath.Join(prefix, m.Config.RelativeStore))}
		}
		seen := map[string]bool{}
		for _, p := range candidates {
			if seen[p] {
				continue
			}
			seen[p] = true
			if validateRoots(prefix, p) != nil {
				continue
			}
			s, raw, er := readState(p)
			if er != nil || s.ID != m.Config.StoreID || s.InstallID != iid {
				continue
			}
			if m.Store != "" {
				return nil, fmt.Errorf("找到同一存储实例的多个副本，请用 --store 明确选择")
			}
			m.Store, m.State, m.stateDigest = p, s, digest(raw)
		}
		if m.Store == "" {
			return nil, fmt.Errorf("组件存储不可用或身份不符；请恢复挂载或用 repair --store 指定新位置；未创建空存储")
		}
		m.Initialized = true
		m.Relocated = m.bootstrap() != m.Config
	} else {
		if !absent(filepath.Join(prefix, "state.json")) {
			return nil, fmt.Errorf("检测到旧版目录，请选择新的双目录实例")
		}
		m.Store = opts.Store
		if m.Store == "" {
			m.Store = os.Getenv("CDET_STORE")
		}
		if m.Store == "" {
			m.Store = filepath.Join(h, ".cdet-store")
		}
		if e = validateRoots(prefix, m.Store); e != nil {
			return nil, e
		}
		iid, er := readInstallID(prefix)
		if er != nil && !errors.Is(er, os.ErrNotExist) {
			return nil, er
		}
		if er == nil {
			m.State.InstallID = iid
			m.identityDigest = digest([]byte(iid + "\n"))
		} else {
			m.State.InstallID, e = instanceID()
			if e != nil {
				return nil, e
			}
		}
		if !absent(filepath.Join(m.Store, ".cdet", "state.json")) {
			s, raw, er := readState(m.Store)
			if er != nil {
				return nil, er
			}
			if iid == "" || s.InstallID != iid {
				return nil, fmt.Errorf("组件存储已绑定其他安装实例，不能隐式接管")
			}
			m.State, m.stateDigest = s, digest(raw)
		} else {
			m.State.ID, e = instanceID()
			if e != nil {
				return nil, e
			}
		}
	}
	return m, nil
}
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
