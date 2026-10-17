package stk

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Schema = 1

// Ref keeps relocatable paths independent of their current mount point.
type Ref struct {
	Root string `json:"root,omitempty" toml:"root,omitempty"`
	Path string `json:"path" toml:"path"`
}

type Root struct {
	Path         string `json:"path"`
	RelativeHome string `json:"relative_home,omitempty"`
	Mount        string `json:"mount,omitempty"`
	FSType       string `json:"fs_type,omitempty"`
}

type Service struct {
	Backend    string `json:"backend,omitempty" toml:"backend,omitempty"`
	Name       string `json:"name,omitempty" toml:"name,omitempty"`
	Controller Ref    `json:"controller" toml:"controller"`
	Config     Ref    `json:"config" toml:"config"`
	Definition Ref    `json:"definition" toml:"definition"`
	System     bool   `json:"system,omitempty" toml:"system,omitempty"`
}

type Module struct {
	Binary       Ref           `json:"binary"`
	Home         Ref           `json:"home"`
	Config       Ref           `json:"config"`
	Provider     string        `json:"provider"`
	OwnedBinary  bool          `json:"owned_binary"`
	BinaryDigest string        `json:"binary_digest"`
	Release      *Release      `json:"release,omitempty"`
	Links        []ManagedLink `json:"links,omitempty"`
	ManageConfig bool          `json:"manage_config,omitempty"`
	Service      Service       `json:"service"`
}

type ManagedLink struct {
	Path   Ref `json:"path"`
	Target Ref `json:"target"`
}

type State struct {
	Schema     int               `json:"schema"`
	Instance   string            `json:"instance"`
	Generation int               `json:"generation"`
	Roots      map[string]Root   `json:"roots"`
	Modules    map[string]Module `json:"modules"`
	Generated  map[string]string `json:"generated"`
	ShellFiles []Ref             `json:"shell_files,omitempty"`
	Aliases    map[string]Ref    `json:"path_aliases,omitempty"`
}

type ModuleConfig struct {
	Enabled  bool   `toml:"enabled"`
	Endpoint string `toml:"endpoint,omitempty"`
}

type Config struct {
	Schema  int                     `toml:"schema"`
	Roots   map[string]string       `toml:"roots"`
	Modules map[string]ModuleConfig `toml:"modules"`
}

type Candidate struct {
	Binary   string            `json:"binary"`
	Home     string            `json:"home,omitempty"`
	Config   string            `json:"config,omitempty"`
	Provider string            `json:"provider"`
	Links    map[string]string `json:"links,omitempty"`
	Entry    string            `json:"entry,omitempty"`
}

type Status struct {
	Module  string `json:"module"`
	Managed bool   `json:"managed"`
	State   string `json:"state"`
	Binary  string `json:"binary,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type Options struct {
	Home         string
	Roots        map[string]string
	Search       []string
	Binaries     map[string]string
	Artifacts    map[string]string
	Digests      map[string]string
	CodexHome    string
	ProxyConfig  string
	ProxyData    string
	Endpoint     string
	Endpoints    map[string]string
	Services     map[string]Service
	DryRun       bool
	ManageConfig bool
	ShellFiles   []string
	ExecCheck    bool
	Takeover     bool
}

type Manager struct {
	Home        string
	Executable  string
	UserHome    string
	Path        string
	Config      Config
	State       State
	Roots       map[string]string
	Options     Options
	Initialized bool
	Releases    *ReleaseClient
	inputs      map[string]Fingerprint
}

func moduleCommand(name string) string {
	if name == "proxy" {
		return "mihomo"
	}
	return name
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func within(base, path string) bool {
	r, err := filepath.Rel(base, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(os.PathSeparator)) && !filepath.IsAbs(r)
}

func absolute(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("路径必须是规范的非根绝对路径: %q", path)
	}
	return nil
}

func (m *Manager) ref(path string) Ref {
	if path == "" {
		return Ref{}
	}
	best := Ref{Path: path}
	length := 0
	names := make([]string, 0, len(m.Roots))
	for name := range m.Roots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		root := m.Roots[name]
		if len(root) > length && within(root, path) {
			rel, _ := filepath.Rel(root, path)
			best = Ref{Root: name, Path: rel}
			length = len(root)
		}
	}
	return best
}

func (m *Manager) resolve(ref Ref) (string, error) {
	if ref.Path == "" {
		if ref.Root != "" {
			return "", fmt.Errorf("目录根引用缺少路径")
		}
		return "", nil
	}
	if ref.Root == "" {
		return ref.Path, absolute(ref.Path)
	}
	root, ok := m.Roots[ref.Root]
	if !ok {
		return "", fmt.Errorf("未知目录根: %s", ref.Root)
	}
	path := filepath.Join(root, ref.Path)
	if filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || strings.ContainsAny(ref.Path, "\x00\r\n") || !within(root, path) {
		return "", fmt.Errorf("路径引用越界: %s", ref.Root)
	}
	return path, nil
}

func (m *Manager) rebase(path string) string {
	for _, mapping := range m.pathMappings() {
		if within(mapping.old, path) {
			rel, _ := filepath.Rel(mapping.old, path)
			return filepath.Join(mapping.current, rel)
		}
	}
	return path
}

type pathMapping struct{ old, current string }

// Historical prefixes remain useful after an unowned/disabled configuration
// was deliberately left untouched. Current roots act as identity mappings so
// repeated repairs cannot rewrite a new path through an older parent prefix.
func (m *Manager) pathMappings() []pathMapping {
	paths := map[string]string{}
	for old, ref := range m.State.Aliases {
		if current, err := m.resolve(ref); err == nil {
			paths[old] = current
		}
	}
	for name, root := range m.State.Roots {
		if current := m.Roots[name]; current != "" {
			paths[root.Path] = current
		}
	}
	for _, root := range m.Roots {
		paths[root] = root
	}
	var result []pathMapping
	for old, current := range paths {
		result = append(result, pathMapping{old, current})
	}
	sort.Slice(result, func(i, j int) bool {
		if len(result[i].old) == len(result[j].old) {
			return result[i].old < result[j].old
		}
		return len(result[i].old) > len(result[j].old)
	})
	return result
}

func (m *Manager) rootPaths(name string) []string {
	paths := map[string]bool{}
	if root := m.Roots[name]; root != "" {
		paths[root] = true
	}
	if old := m.State.Roots[name].Path; old != "" {
		paths[old] = true
	}
	for old, ref := range m.State.Aliases {
		if ref.Root == name && ref.Path == "." {
			paths[old] = true
		}
	}
	var result []string
	for p := range paths {
		result = append(result, p)
	}
	sort.Strings(result)
	return result
}
