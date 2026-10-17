package codex

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const proxyStart = "# >>> nido codex proxy >>>"
const proxyEnd = "# <<< nido codex proxy <<<"
const maxEnvSize = 1 << 20

var proxyKeys = []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"}

// ValidateProxyURL checks an explicit endpoint without inspecting a Codex home.
func ValidateProxyURL(value string) error { return validProxy(value) }

func validProxy(value string) error {
	if strings.ContainsAny(value, "\r\n\x00'\"\\ \t") {
		return fmt.Errorf("代理地址包含不支持的字符")
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return fmt.Errorf("代理地址无效")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("代理协议不支持")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("代理端口无效")
		}
	}
	return nil
}

type envFile struct {
	content []byte
	info    os.FileInfo
	dir     os.FileInfo
}

func readEnv(home string) (envFile, error) {
	dir, err := os.Stat(home)
	if os.IsNotExist(err) {
		return envFile{}, nil
	}
	if err != nil || !dir.IsDir() {
		return envFile{}, fmt.Errorf("无法读取 Codex 目录")
	}
	path := filepath.Join(home, ".env")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return envFile{dir: dir}, nil
	}
	if err != nil {
		return envFile{}, fmt.Errorf("无法读取 Codex .env")
	}
	if !info.Mode().IsRegular() || info.Size() > maxEnvSize {
		return envFile{}, fmt.Errorf("Codex .env 必须是不超过 1 MiB 的普通文件")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return envFile{}, fmt.Errorf("Codex .env 不能是硬链接文件")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return envFile{}, fmt.Errorf("无法读取 Codex .env")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode() != info.Mode() || !opened.ModTime().Equal(info.ModTime()) {
		return envFile{}, fmt.Errorf("Codex .env 在读取期间发生变化")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxEnvSize+1))
	if err != nil || int64(len(b)) != info.Size() || len(b) > maxEnvSize {
		return envFile{}, fmt.Errorf("Codex .env 在读取期间大小发生变化")
	}
	finished, err := f.Stat()
	if err != nil || !info.ModTime().Equal(finished.ModTime()) || info.Mode() != finished.Mode() || finished.Size() != info.Size() {
		return envFile{}, fmt.Errorf("Codex .env 在读取期间发生变化")
	}
	currentDir, err := os.Stat(home)
	if err != nil || !os.SameFile(dir, currentDir) {
		return envFile{}, fmt.Errorf("Codex 目录在读取期间发生变化")
	}
	return envFile{content: b, info: info, dir: dir}, nil
}

// A receipt can authorize removal of precisely one generated block, never an
// arbitrary substring supplied by a modified configuration file.
func validateProxyBlock(block string) error {
	if block == "" {
		return nil
	}
	text := strings.TrimPrefix(block, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) != len(proxyKeys)+3 || lines[0] != proxyStart || lines[len(lines)-2] != proxyEnd || lines[len(lines)-1] != "" {
		return fmt.Errorf("Nido 代理配置收据无效，未修改 .env")
	}
	endpoint := ""
	for i, key := range proxyKeys {
		value, ok := strings.CutPrefix(lines[i+1], key+"='")
		if !ok || !strings.HasSuffix(value, "'") {
			return fmt.Errorf("Nido 代理配置收据无效，未修改 .env")
		}
		value = strings.TrimSuffix(value, "'")
		if i == 0 {
			endpoint = value
		}
		if value != endpoint || validProxy(value) != nil {
			return fmt.Errorf("Nido 代理配置收据无效，未修改 .env")
		}
	}
	return nil
}

func stripOwned(content []byte, owned string) ([]byte, bool, error) {
	if err := validateProxyBlock(owned); err != nil {
		return nil, false, err
	}
	text := string(content)
	if owned == "" {
		if strings.Contains(text, proxyStart) || strings.Contains(text, proxyEnd) {
			return nil, false, fmt.Errorf(".env 存在未被当前配置记录的 Nido 标记，请先明确处理")
		}
		return content, false, nil
	}
	if strings.Count(text, owned) != 1 || strings.Count(text, proxyStart) != 1 || strings.Count(text, proxyEnd) != 1 {
		return nil, false, fmt.Errorf("Nido 代理配置已被修改或删除；为保留用户内容，未修改 .env")
	}
	marker := strings.Index(text, proxyStart)
	if marker > 0 && text[marker-1] != '\n' {
		return nil, false, fmt.Errorf("Nido 代理标记已被移入其他内容，未修改 .env")
	}
	return []byte(strings.Replace(text, owned, "", 1)), true, nil
}

// writeEnv preserves all non-owned bytes and refuses stale writes. It never
// follows an .env symlink and does not evaluate dotenv values as shell code.
func writeEnv(home string, original envFile, next []byte) error {
	if bytes.Equal(original.content, next) {
		return nil
	}
	file, err := os.CreateTemp(home, ".nido-env-*")
	if err != nil {
		return fmt.Errorf("无法写入 Codex .env")
	}
	name := file.Name()
	defer os.Remove(name)
	mode := os.FileMode(0600)
	if original.info != nil {
		mode = original.info.Mode().Perm()
	}
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(next)
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err != nil {
		return fmt.Errorf("无法写入 Codex .env")
	}
	current, err := readEnv(home)
	if err != nil {
		return err
	}
	if original.dir == nil || current.dir == nil || !os.SameFile(original.dir, current.dir) || !bytes.Equal(original.content, current.content) || (original.info == nil) != (current.info == nil) || original.info != nil && (!os.SameFile(original.info, current.info) || original.info.Mode().Perm() != current.info.Mode().Perm() || !original.info.ModTime().Equal(current.info.ModTime())) {
		return fmt.Errorf("Codex .env 同时发生变化，未覆盖")
	}
	if original.info == nil && len(next) == 0 {
		return nil
	}
	if err = os.Rename(name, filepath.Join(home, ".env")); err != nil {
		return fmt.Errorf("无法替换 Codex .env")
	}
	return nil
}

func (s Service) SetProxy(c Config, endpoint string) (Config, error) {
	if err := validProxy(endpoint); err != nil {
		return c, err
	}
	next, _, err := s.ChangeProxy(c, endpoint)
	return next, err
}

func (s Service) ClearProxy(c Config) (Config, error) {
	next, _, err := s.ChangeProxy(c, "")
	return next, err
}

// Detach only removes a proxy block whose exact last-written bytes still match.
// The caller may then discard Config and regenerate its own environment file.
func (s Service) Detach(c Config) error {
	if c.ProxyBlock == "" {
		return nil
	}
	_, err := s.ClearProxy(c)
	return err
}
