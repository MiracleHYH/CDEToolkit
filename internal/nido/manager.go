// Package nido connects a small CLI to independent integrations and builtins.
// External applications own their data; Nido owns only its installation root.
package nido

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MiracleHYH/CDEToolkit/internal/builtin"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/pelletier/go-toml/v2"
)

var Version = "dev"

const schema = 1
const lockContents = "Nido management lock\n"

type Config struct {
	Schema  int    `toml:"schema"`
	Product string `toml:"product"`
	// Only our generated shell file has an ownership receipt. This is not an
	// application checksum, installation inventory or history.
	EnvDigest string       `toml:"env_digest,omitempty"`
	Codex     codex.Config `toml:"codex"`
}

type Manager struct {
	Root, UserHome, Executable string
	Config                     Config
	Env                        []string
	Codex                      codex.Service
	Builtins                   builtin.Manager
	configBytes                []byte
	rootInfo                   os.FileInfo
}

func New(root string) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if real, e := filepath.EvalSymlinks(exe); e == nil {
		exe = real
	}
	if root == "" && filepath.Base(filepath.Dir(exe)) == "bin" {
		candidate := filepath.Dir(filepath.Dir(exe))
		if _, e := os.Lstat(filepath.Join(candidate, "config.toml")); e == nil {
			root = candidate
		}
	}
	if root == "" {
		root = os.Getenv("NIDO_HOME")
	}
	if root == "" {
		root = filepath.Join(home, ".nido")
	}
	root = expand(root, home)
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || invalidShellPath(root) {
		return nil, fmt.Errorf("软件目录需要规范的非根绝对路径")
	}
	// Resolve ordinary OS aliases (e.g. /var on macOS) once, not historical
	// mount paths. No directory is created during construction or status.
	root, err = physical(root)
	if err != nil {
		return nil, err
	}
	if root == "/" || invalidShellPath(root) {
		return nil, fmt.Errorf("软件目录不能包含 PATH 分隔符或控制字符")
	}
	actualHome, e := physical(home)
	if e != nil {
		return nil, e
	}
	if root == actualHome {
		return nil, fmt.Errorf("不能将用户主目录本身作为 Nido 软件目录")
	}
	m := &Manager{Root: root, UserHome: home, Executable: exe, Env: os.Environ(), Config: Config{Schema: schema, Product: "nido"}}
	if info, e := os.Lstat(root); e == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("Nido 软件目录必须是目录")
		}
		m.rootInfo = info
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	m.Codex = codex.Service{Root: root, UserHome: home, Env: m.Env}
	m.Builtins = builtin.Manager{Root: root, Env: m.Env}
	b, err := readFile(filepath.Join(root, "config.toml"), 1<<20)
	if err == nil {
		var config Config
		if e := toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&config); e != nil || config.Schema != schema || config.Product != "nido" {
			return nil, fmt.Errorf("该目录不是有效的 Nido 单目录配置；不会转换或修改旧 toolkit")
		}
		if config.EnvDigest != "" {
			d, e := hex.DecodeString(config.EnvDigest)
			if e != nil || len(d) != sha256.Size {
				return nil, fmt.Errorf("Nido 环境文件归属记录无效")
			}
		}
		if e := m.Codex.ValidateConfig(config.Codex); e != nil {
			return nil, fmt.Errorf("Codex 关联配置无效: %w", e)
		}
		m.Config = config
		m.configBytes = b
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return m, nil
}

func invalidShellPath(path string) bool {
	return strings.Contains(path, ":") || strings.IndexFunc(path, func(r rune) bool { return r < 32 || r == 127 }) >= 0
}

func physical(path string) (string, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		parent, e := physical(filepath.Dir(path))
		if e != nil {
			return "", e
		}
		return filepath.Join(parent, filepath.Base(path)), nil
	} else if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}

func expand(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func readFile(path string, limit int64) ([]byte, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Size() > limit {
		return nil, fmt.Errorf("需要大小受限的普通文件: %s", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(s, opened) {
		return nil, fmt.Errorf("读取期间文件发生变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit || int64(len(b)) != s.Size() {
		return nil, fmt.Errorf("读取期间文件大小变化")
	}
	return b, nil
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// replaceFile prepares an atomic write, then checks the captured destination
// immediately before replacement. It does not attempt to undo user edits.
func replaceFile(path string, b []byte, mode os.FileMode, check func() error) error {
	if err := safeParents(filepath.Dir(path)); err != nil {
		return err
	}
	if s, e := os.Lstat(path); e == nil && !s.Mode().IsRegular() {
		return fmt.Errorf("拒绝覆盖非普通文件: %s", path)
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nido-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		if check != nil {
			err = check()
		}
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

func unchanged(path string, expected []byte) error {
	return unchangedFile(path, expected, 1<<20)
}

func unchangedFile(path string, expected []byte, limit int64) error {
	actual, err := readFile(path, limit)
	if os.IsNotExist(err) && expected == nil {
		return nil
	}
	if err != nil || !bytes.Equal(actual, expected) {
		return fmt.Errorf("操作期间 %s 已变化，未覆盖用户编辑", filepath.Base(path))
	}
	return nil
}

func (m *Manager) checkRoot() error {
	actual, err := os.Lstat(m.Root)
	if err != nil || !actual.IsDir() || m.rootInfo == nil || !os.SameFile(m.rootInfo, actual) {
		return fmt.Errorf("Nido 软件目录在操作期间发生变化，请重新运行命令")
	}
	return safeParents(m.Root)
}

func safeParents(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e == nil && !s.IsDir() {
			return fmt.Errorf("写入路径经过链接或非目录: %s", p)
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (m *Manager) Environment() string {
	s := "# Generated by Nido; edit configuration through nido.\nexport NIDO_HOME=" + shellQuote(m.Root) + "\n"
	// Put selected entries first without deleting any user PATH entries. A
	// single prefix makes repeated sourcing idempotent even with two entries.
	bins := []string{filepath.Join(m.Root, "bin")}
	if m.Config.Codex.Home != "" && filepath.IsAbs(m.Config.Codex.Command) {
		bins = append(bins, filepath.Dir(m.Config.Codex.Command))
	}
	prefix := shellQuote(strings.Join(bins, ":"))
	s += "case \"${PATH-}\" in " + prefix + "|" + prefix + ":*) ;; *) export PATH=" + prefix + "${PATH:+:$PATH} ;; esac\n"
	if m.Config.Codex.Home != "" {
		s += "export CODEX_HOME=" + shellQuote(m.Config.Codex.Home) + "\n"
	}
	return s
}

func (m *Manager) checkEnv() error {
	b, err := readFile(filepath.Join(m.Root, "env.sh"), 1<<20)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.Equal(b, []byte(m.Environment())) || (m.Config.EnvDigest != "" && digest(b) == m.Config.EnvDigest) {
		return nil
	}
	return fmt.Errorf("env.sh 不属于当前生成内容或已被修改，未覆盖")
}

func (m *Manager) save() error {
	// An editor does not participate in flock. Recheck immediately after any
	// slow upstream operation, before replacing configuration or environment.
	if err := m.checkRoot(); err != nil {
		return err
	}
	configPath := filepath.Join(m.Root, "config.toml")
	envPath := filepath.Join(m.Root, "env.sh")
	if err := unchanged(configPath, m.configBytes); err != nil {
		return err
	}
	if err := m.checkEnv(); err != nil {
		return err
	}
	previousEnv, err := readFile(envPath, 1<<20)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Persist the new intent with the old receipt first. An interrupted env
	// refresh can be repeated by init; no transaction journal is necessary.
	b, err := toml.Marshal(m.Config)
	if err != nil {
		return err
	}
	if err = replaceFile(configPath, b, 0600, func() error {
		if err := m.checkRoot(); err != nil {
			return err
		}
		return unchanged(configPath, m.configBytes)
	}); err != nil {
		return err
	}
	m.configBytes = b
	env := []byte(m.Environment())
	if err = replaceFile(envPath, env, 0600, func() error {
		if err := m.checkRoot(); err != nil {
			return err
		}
		if err := unchanged(configPath, m.configBytes); err != nil {
			return err
		}
		return unchanged(envPath, previousEnv)
	}); err != nil {
		return fmt.Errorf("配置已保存，环境文件写入失败；可重试 init: %w", err)
	}
	m.Config.EnvDigest = digest(env)
	b, err = toml.Marshal(m.Config)
	if err != nil {
		return err
	}
	if err = replaceFile(configPath, b, 0600, func() error {
		if err := m.checkRoot(); err != nil {
			return err
		}
		if err := unchanged(envPath, env); err != nil {
			return err
		}
		return unchanged(configPath, m.configBytes)
	}); err == nil {
		m.configBytes = b
	}
	return err
}

func (m *Manager) mutate(create bool, fn func() error) error {
	if !create && m.configBytes == nil {
		return fmt.Errorf("尚未初始化，请先运行 nido init")
	}
	if err := safeParents(m.Root); err != nil {
		return err
	}
	if create {
		if err := os.MkdirAll(m.Root, 0700); err != nil {
			return err
		}
	}
	if m.rootInfo == nil {
		m.rootInfo, _ = os.Lstat(m.Root)
	}
	if err := m.checkRoot(); err != nil {
		return err
	}
	lockPath := filepath.Join(m.Root, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	created := err == nil
	if os.IsExist(err) {
		f, err = os.OpenFile(lockPath, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("Nido 锁必须是普通文件")
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Nlink != 1 {
		return fmt.Errorf("Nido 锁不能是硬链接")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("另一个 Nido 管理操作正在执行")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	current, err := os.Lstat(lockPath)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("Nido 锁在操作期间发生变化")
	}
	if created {
		if _, err = f.WriteString(lockContents); err != nil {
			return err
		}
	} else if b, err := readFile(lockPath, 128); err != nil || string(b) != lockContents {
		return fmt.Errorf("Nido 锁不属于当前工具，未修改")
	}
	if m.configBytes == nil {
		if err := m.emptyRoot(); err != nil {
			return err
		}
	}
	actual, err := readFile(filepath.Join(m.Root, "config.toml"), 1<<20)
	if !(os.IsNotExist(err) && m.configBytes == nil) && (err != nil || !bytes.Equal(actual, m.configBytes)) {
		return fmt.Errorf("配置已变化，请重新运行命令")
	}
	if err = m.checkEnv(); err != nil {
		return err
	}
	return fn()
}

func (m *Manager) emptyRoot() error {
	entries, err := os.ReadDir(m.Root)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			if b, err := readFile(filepath.Join(m.Root, ".lock"), 128); err == nil && string(b) == lockContents {
				continue
			}
		}
		return fmt.Errorf("首次初始化只接受空目录；未修改已有内容，请选择独立 Nido 软件目录")
	}
	return nil
}

func (m *Manager) Init() error {
	if m.configBytes == nil {
		if err := m.emptyRoot(); err != nil {
			return err
		}
	}
	return m.mutate(true, func() error {
		data, err := readFile(m.Executable, 512<<20)
		if err != nil {
			return err
		}
		target := filepath.Join(m.Root, "bin", "nido")
		old, e := readFile(target, 512<<20)
		if e == nil && !bytes.Equal(old, data) {
			info, e := buildinfo.Read(bytes.NewReader(old))
			if e != nil || info.Path != "github.com/MiracleHYH/CDEToolkit/cmd/nido" {
				return fmt.Errorf("bin/nido 已被其他文件占用，未覆盖")
			}
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		if err = m.save(); err != nil {
			return err
		}
		return replaceFile(target, data, 0755, func() error {
			if err := m.checkRoot(); err != nil {
				return err
			}
			return unchangedFile(target, old, 512<<20)
		})
	})
}

func (m *Manager) ConfigureCodex(fn func(codex.Config) (codex.Config, error)) error {
	return m.mutate(false, func() error {
		next, err := fn(m.Config.Codex)
		if err != nil {
			return err
		}
		// Only ConfigureProxy can change the ownership receipt for an external
		// .env edit. Association and upstream installation must preserve it.
		if next.ProxyBlock != m.Config.Codex.ProxyBlock || (m.Config.Codex.ProxyBlock != "" && next.Home != m.Config.Codex.Home) {
			return fmt.Errorf("已有代理配置归属不能由关联或安装操作更改，请使用 codex proxy 或 detach")
		}
		if err := m.Codex.ValidateConfig(next); err != nil {
			return err
		}
		previous := m.Config
		before := append([]byte(nil), m.configBytes...)
		m.Config.Codex = next
		if err := m.save(); err != nil {
			if bytes.Equal(before, m.configBytes) {
				m.Config = previous
			}
			return err
		}
		return nil
	})
}

// ConfigureProxy compensates only this invocation's .env edit if its receipt
// cannot be committed. It never rolls back upstream programs or application
// data, and refuses compensation over concurrent user edits.
func (m *Manager) ConfigureProxy(endpoint string, detach bool) error {
	return m.mutate(false, func() error {
		before := m.Config
		priorBytes := append([]byte(nil), m.configBytes...)
		if detach && m.Config.Codex.ProxyBlock == "" {
			m.Config.Codex = codex.Config{}
			if err := m.save(); err != nil {
				if bytes.Equal(priorBytes, m.configBytes) {
					m.Config = before
				}
				return err
			}
			return nil
		}
		next, undo, err := m.Codex.ChangeProxy(m.Config.Codex, endpoint)
		if err != nil {
			return err
		}
		if detach {
			next = codex.Config{}
		}
		m.Config.Codex = next
		if err = m.save(); err != nil {
			if bytes.Equal(priorBytes, m.configBytes) {
				m.Config = before
				if e := undo(); e != nil {
					return fmt.Errorf("%w；代理配置已变化，无法安全恢复本次修改: %v", err, e)
				}
			}
			return err
		}
		return nil
	})
}

type Status struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	Version    string `json:"version,omitempty"`
	Directory  string `json:"directory,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func (m *Manager) Status() []Status {
	c := m.Codex.Status(m.Config.Codex)
	rows := []Status{{Name: "codex", Kind: "external", Configured: m.Config.Codex.Home != "", State: c.State, Version: c.Version, Directory: c.Home, Detail: c.Detail}}
	for _, name := range builtin.Names() {
		b := m.Builtins.Status(name)
		state := "未安装"
		if b.Installed {
			state = "已停止"
			if !b.Configured {
				state = "待配置"
			}
			if b.Running {
				state = "运行中"
			}
		}
		if b.Problem != "" {
			state = "异常"
		}
		rows = append(rows, Status{Name: name, Kind: "builtin", Configured: b.Configured, State: state, Directory: b.Directory, Detail: b.Problem})
	}
	return rows
}
