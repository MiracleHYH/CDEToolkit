package cdet

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Only these two fields are safe to show in a preview. Credentials stay inside
// a request closure and are never part of JSON, diagnostics or saved state.
type mihomoControllerInfo struct {
	Endpoint  string `json:"endpoint"`
	Restart   bool   `json:"restart"`
	program   string
	config    string
	before    Fingerprint
	configFP  Fingerprint
	client    *http.Client
	authorize func(*http.Request)
	owner     func(string, string) (bool, error)
}

func localMihomoEndpoint(address string) string {
	host, port, e := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 1 || n > 65535 {
		return ""
	}
	ip := net.ParseIP(host)
	if host == "" || host == "0.0.0.0" {
		ip = net.IPv4(127, 0, 0, 1)
	} else if host == "::" {
		ip = net.IPv6loopback
	}
	if ip == nil || !ip.IsLoopback() {
		return ""
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(n))
}

func (m *Manager) mihomoHTTP() (*http.Client, error) {
	// A loopback controller never needs an outbound proxy. In particular, an
	// invalid outbound configuration must not disable the official local API.
	base := m.releases().HTTP
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if tcp, ok := transport.(*http.Transport); ok {
		direct := tcp.Clone()
		direct.Proxy = nil // controller credentials must never traverse a proxy
		client.Transport = direct
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 || client.Timeout > 2*time.Minute {
		client.Timeout = 2 * time.Minute
	}
	return &client, nil
}

func (i *mihomoControllerInfo) request(method, path string) (*http.Response, error) {
	req, e := http.NewRequest(method, i.Endpoint+path, nil)
	if e != nil {
		return nil, fmt.Errorf("Mihomo 控制器请求无效")
	}
	i.authorize(req)
	resp, e := i.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("Mihomo 控制器请求失败；地址和认证未输出")
	}
	return resp, nil
}

// An unavailable, unowned or unsupported controller simply selects the safe
// stopped-program download path. This function never starts a process or POSTs.
func (m *Manager) mihomoController() (*mihomoControllerInfo, error) {
	c, ok := m.State.Components["proxy"]
	if !ok || !c.Installed {
		return nil, nil
	}
	program := filepath.Join(m.Store, programPath("proxy"))
	if noSymlinkParents(program) != nil {
		return nil, nil
	}
	before, e := fingerprint(program)
	if e != nil || before.Kind != "file" || before.Mode&0111 == 0 {
		return nil, nil
	}
	config := filepath.Join(m.Store, dataPath("proxy"), "config.yaml")
	b, e := readRegular(config)
	if e != nil {
		return nil, nil
	}
	configFP, e := fingerprint(config)
	if e != nil || configFP.Kind != "file" || configFP.Digest != digest(b) {
		return nil, nil
	}
	var settings struct {
		Controller string `yaml:"external-controller"`
		Secret     string `yaml:"secret"`
	}
	if yaml.Unmarshal(b, &settings) != nil || strings.ContainsAny(settings.Secret, "\x00\r\n") {
		return nil, nil
	}
	endpoint := localMihomoEndpoint(settings.Controller)
	if endpoint == "" {
		return nil, nil
	}
	owner := m.releases().MihomoOwner
	if owner == nil {
		owner = mihomoSocketOwner
	}
	owned, e := owner(endpoint, program)
	if e != nil || !owned {
		return nil, nil
	}
	client, e := m.mihomoHTTP()
	if e != nil {
		return nil, e
	}
	info := &mihomoControllerInfo{Endpoint: endpoint, Restart: true, program: program, config: config, before: before, configFP: configFP, client: client, owner: owner}
	secret := settings.Secret
	info.authorize = func(req *http.Request) {
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
	}
	preview := *client
	preview.Timeout = 5 * time.Second
	info.client = &preview
	resp, e := info.request(http.MethodGet, "/version")
	info.client = client
	if e != nil {
		return nil, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var version struct {
		Meta    bool   `json:"meta"`
		Version string `json:"version"`
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &version) != nil || !version.Meta || version.Version == "" {
		return nil, nil
	}
	return info, nil
}

// Once POST is attempted the caller must not fall back to replacing the file:
// a timeout or restart can hide a successfully completed official update.
func (m *Manager) updateMihomoOfficial() (handled bool, err error) {
	info, e := m.mihomoController()
	if e != nil || info == nil {
		return false, e
	}
	for path, expected := range map[string]Fingerprint{info.program: info.before, info.config: info.configFP} {
		actual, pe := fingerprint(path)
		if pe != nil || actual != expected || noSymlinkParents(path) != nil {
			return false, fmt.Errorf("Mihomo 程序或控制器配置在预检后发生变化，未提交更新")
		}
	}
	owned, e := info.owner(info.Endpoint, info.program)
	if e != nil || !owned {
		return false, nil
	}
	resp, e := info.request(http.MethodPost, "/upgrade?channel=release")
	if e != nil {
		return true, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("Mihomo 官方更新返回 HTTP %d；请检查组件状态，未改用下载覆盖", resp.StatusCode)
	}
	var result struct {
		Status string `json:"status"`
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &result) != nil || result.Status != "ok" {
		return true, fmt.Errorf("Mihomo 官方更新结果不明确；请检查组件状态，未改用下载覆盖")
	}
	if noSymlinkParents(info.program) != nil || !executable(info.program) {
		return true, fmt.Errorf("Mihomo 官方更新后受管程序路径无效，保留现场")
	}
	program, e := readRegularLarge(info.program)
	if e != nil || verifyPlatform(program, m.releases().OS+"/"+m.releases().Arch) != nil {
		return true, fmt.Errorf("Mihomo 官方更新后程序校验失败，保留现场")
	}
	return true, nil
}

func sameMihomoExecutable(actual, program string) bool {
	// The official updater writes os.Executable(), not an arbitrary inode.
	// A hard link or alternate launch path outside our root is insufficient.
	if !filepath.IsAbs(actual) || filepath.Clean(actual) != program || strings.HasSuffix(actual, " (deleted)") {
		return false
	}
	a, e := os.Stat(actual)
	b, pe := os.Stat(program)
	return e == nil && pe == nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b)
}

func mihomoAddress(endpoint string) (net.IP, int, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" {
		return nil, 0, fmt.Errorf("控制器地址无效")
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.Atoi(port)
	ip := net.ParseIP(host)
	if e != nil || pe != nil || n < 1 || n > 65535 || ip == nil || !ip.IsLoopback() {
		return nil, 0, fmt.Errorf("控制器必须位于本机回环地址")
	}
	return ip, n, nil
}
