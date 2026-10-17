package cdet

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const Schema = 4

// Only two roots are configurable. Every component location is derived.
type Bootstrap struct {
	Schema        int    `toml:"schema"`
	InstallID     string `toml:"install_id"`
	Store         string `toml:"store"`
	StoreID       string `toml:"store_id"`
	RelativeStore string `toml:"relative_store"`
}
type Component struct {
	Installed bool   `json:"installed"`
	Backend   string `json:"backend"`
	Digest    string `json:"digest,omitempty"` // Last directly installed program, never a HOME checksum.
	Origin    string `json:"origin"`
}
type State struct {
	Schema     int                  `json:"schema"`
	ID         string               `json:"id"`
	InstallID  string               `json:"install_id"`
	Components map[string]Component `json:"components"`
	Generated  map[string]string    `json:"generated"`
}
type Options struct {
	Prefix, Store string
	From          map[string]string
	Artifacts     map[string]string
	Digests       map[string]string
	Fresh, DryRun bool
	Yes           bool
}
type Manager struct {
	Prefix, Store                             string
	Executable                                string
	UserHome                                  string
	Path                                      string
	Config                                    Bootstrap
	State                                     State
	Options                                   Options
	Initialized                               bool
	Relocated                                 bool
	Releases                                  *ReleaseClient
	Runner                                    CommandRunner
	configDigest, stateDigest, identityDigest string
}
type Status struct {
	Module  string `json:"module"`
	Managed bool   `json:"managed"`
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

func moduleCommand(name string) string {
	if name == "proxy" {
		return "mihomo"
	}
	if name == "self" {
		return "cdet"
	}
	return name
}
func knownModule(name string) bool { return name == "codex" || name == "proxy" || name == "gds" }
func validID(value string) bool    { b, e := hex.DecodeString(value); return e == nil && len(b) == 16 }
func instanceID() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func within(base, path string) bool {
	r, e := filepath.Rel(base, path)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator)) && !filepath.IsAbs(r)
}
func absolute(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("需要规范的非根绝对路径: %q", path)
	}
	return nil
}
func relative(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n") && within("/boundary", filepath.Join("/boundary", path))
}
func validateRoots(prefix, store string) error {
	for _, p := range []string{prefix, store} {
		if e := absolute(p); e != nil {
			return e
		}
		if e := noSymlinkParents(filepath.Join(p, "sentinel")); e != nil {
			return e
		}
	}
	if within(prefix, store) || within(store, prefix) {
		return fmt.Errorf("安装目录与组件存储目录必须互不包含")
	}
	for _, pair := range [][2]string{{prefix, store}, {store, prefix}} {
		base, e := os.Stat(pair[0])
		if e != nil {
			continue
		}
		for p := pair[1]; ; p = filepath.Dir(p) {
			if info, e := os.Stat(p); e == nil && os.SameFile(base, info) {
				return fmt.Errorf("安装目录和存储目录实际指向同一目录或相互包含")
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	return nil
}
func dataPath(name string) string         { return name }
func programPath(name string) string      { return filepath.Join(".cdet", "bin", moduleCommand(name)) }
func (m *Manager) metadataDir() string    { return filepath.Join(m.Store, ".cdet") }
func (m *Manager) executablePath() string { return filepath.Join(m.Prefix, "bin", "cdet") }
func (m *Manager) bootstrap() Bootstrap {
	r, _ := filepath.Rel(m.Prefix, m.Store)
	return Bootstrap{Schema, m.State.InstallID, m.Store, m.State.ID, r}
}
func (m *Manager) requireReady() error {
	if !m.Initialized {
		return fmt.Errorf("尚未初始化；请先指定 --prefix 和 --store 执行 init")
	}
	if m.Relocated {
		return fmt.Errorf("目录位置已变化，请先执行 repair；不会重建旧挂载目录")
	}
	return nil
}
