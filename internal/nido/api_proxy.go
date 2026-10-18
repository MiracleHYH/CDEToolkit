package nido

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/pelletier/go-toml/v2"
)

// A separate, bounded file leaves the main configuration readable by older
// releases. Older programs preserve this file but do not implement its routing.
const apiProxyFile = "download-proxy.toml"

// ErrAPIDisabled is an expected optional-feature state, not a connectivity
// failure. No API request or fallback is permitted before a listener is bound.
var ErrAPIDisabled = errors.New("Nido API 功能未启用；请先运行 nido proxy bind download 选择 listener")

type apiProxyConfig struct {
	Schema   int    `toml:"schema"`
	Product  string `toml:"product"`
	Listener string `toml:"listener"`
}

type apiProxySnapshot struct {
	path string
	info os.FileInfo
	data []byte
}

func privateAPIProxy(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

func (m *Manager) readAPIProxy() (apiProxyConfig, apiProxySnapshot, error) {
	path := filepath.Join(m.Root, apiProxyFile)
	snapshot := apiProxySnapshot{path: path}
	if m.rootInfo == nil {
		if err := safeParents(m.Root); err != nil {
			return apiProxyConfig{}, snapshot, err
		}
		// Standalone local queries can precede initialization. Missing intent
		// is read as disabled, without creating configuration or directories.
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return apiProxyConfig{}, snapshot, nil
		}
	}
	if err := m.checkRoot(); err != nil {
		return apiProxyConfig{}, snapshot, err
	}
	if err := safeParents(m.Root); err != nil {
		return apiProxyConfig{}, snapshot, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return apiProxyConfig{}, snapshot, nil
	}
	if err != nil || !privateAPIProxy(info) || info.Size() > 4096 {
		return apiProxyConfig{}, snapshot, fmt.Errorf("Nido API 绑定需要本用户私有的单链接普通文件；未修改 %s", apiProxyFile)
	}
	data, err := readFile(path, 4096)
	current, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !os.SameFile(info, current) || info.Mode() != current.Mode() || !privateAPIProxy(current) {
		return apiProxyConfig{}, snapshot, fmt.Errorf("Nido API 绑定读取期间发生变化")
	}
	var config apiProxyConfig
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&config); err != nil || config.Schema != 1 || config.Product != "nido-api-proxy" || !utf8.ValidString(config.Listener) || len(config.Listener) > 128 || strings.ContainsFunc(config.Listener, unicode.IsControl) || config.Listener != "" && strings.TrimSpace(config.Listener) == "" {
		return apiProxyConfig{}, snapshot, fmt.Errorf("Nido API 绑定文件无效或不属于当前工具；未修改")
	}
	snapshot.info, snapshot.data = current, data
	return config, snapshot, nil
}

func (s apiProxySnapshot) check() error {
	current, err := os.Lstat(s.path)
	if s.info == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil || s.info == nil || !os.SameFile(s.info, current) || s.info.Mode() != current.Mode() || !privateAPIProxy(current) {
		return fmt.Errorf("Nido API 绑定在操作期间发生变化；未使用过期配置")
	}
	data, err := readFile(s.path, 4096)
	after, statErr := os.Lstat(s.path)
	if err != nil || statErr != nil || !os.SameFile(s.info, after) || s.info.Mode() != after.Mode() || !privateAPIProxy(after) || !bytes.Equal(data, s.data) {
		return fmt.Errorf("Nido API 绑定在操作期间发生变化；未使用过期配置")
	}
	return nil
}

func (m *Manager) configureAPIProxy(address string, expected *proxy.Endpoint) error {
	_, snapshot, err := m.readAPIProxy()
	if err != nil {
		return err
	}
	return m.configureAPIProxySnapshot(address, expected, snapshot)
}

// Keep the pre-confirmation receipt through the commit. Reading a new receipt
// after confirmation would silently adopt and overwrite another user's edit.
func (m *Manager) configureAPIProxySnapshot(address string, expected *proxy.Endpoint, snapshot apiProxySnapshot) error {
	name := ""
	if address != "" {
		if expected == nil || expected.Authentication || address != expected.URL() {
			return fmt.Errorf("Nido API 绑定请选择无认证的命名 listener")
		}
		name = expected.Name
	}
	return m.mutate(false, func() error {
		if err := snapshot.check(); err != nil {
			return err
		}
		if err := m.checkBuiltinEndpoint(expected); err != nil {
			return err
		}
		config, _, err := m.readAPIProxy()
		if err != nil {
			return err
		}
		if config.Listener == name {
			return snapshot.check()
		}
		data, err := toml.Marshal(apiProxyConfig{Schema: 1, Product: "nido-api-proxy", Listener: name})
		if err != nil || len(data) > 4096 {
			return fmt.Errorf("Nido API 绑定无法序列化；未保存")
		}
		return replaceFile(snapshot.path, data, 0600, func() error {
			if err := m.checkRoot(); err != nil {
				return err
			}
			if err := unchanged(filepath.Join(m.Root, "config.toml"), m.configBytes); err != nil {
				return err
			}
			if err := m.checkBuiltinEndpoint(expected); err != nil {
				return err
			}
			return snapshot.check()
		})
	})
}

// The client is passed only to Nido's internal GitHub release API consumers. It
// never changes DefaultTransport, process environment, file-download clients,
// Codex/Git configuration, or a user's independently launched commands.
func (m *Manager) downloadAPIClient(explicit string) (*http.Client, error) {
	config, snapshot, err := m.readAPIProxy()
	if err != nil {
		return nil, err
	}
	if config.Listener == "" {
		return nil, ErrAPIDisabled
	}
	// A one-off URL can override an enabled feature's route, but cannot enable
	// an unconfigured feature, even when a system proxy or HTTP seam exists.
	var client *http.Client
	if explicit != "" {
		if err := codex.ValidateProxyURL(explicit); err != nil {
			return nil, err
		}
		address, _ := url.Parse(explicit)
		client, err = scopedAPIClient(address)
		if err != nil {
			return nil, err
		}
	} else {
		client, err = m.Proxy.APIClient(config.Listener)
		if err != nil {
			return nil, fmt.Errorf("Nido API 代理尚未就绪；请检查所选 listener 或重新运行 proxy bind download：%w", err)
		}
	}
	copy := *client
	copy.Transport = apiBindingTransport{base: client.Transport, check: func() error {
		if err := m.checkRoot(); err != nil {
			return err
		}
		return snapshot.check()
	}}
	return &copy, nil
}

type apiBindingTransport struct {
	base  http.RoundTripper
	check func() error
}

func (t apiBindingTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t apiBindingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if err := t.check(); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	response.Body = &apiBindingBody{ReadCloser: response.Body, check: t.check}
	return response, nil
}

type apiBindingBody struct {
	io.ReadCloser
	check func() error
}

func (b *apiBindingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		if checkErr := b.check(); checkErr != nil {
			return n, checkErr
		}
	}
	return n, err
}

func (b *apiBindingBody) Close() error {
	err := b.ReadCloser.Close()
	if checkErr := b.check(); checkErr != nil {
		return checkErr
	}
	return err
}
