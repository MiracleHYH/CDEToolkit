package builtin

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
)

type proxyConfig struct {
	Mixed      int      `yaml:"mixed-port"`
	HTTP       int      `yaml:"port"`
	SOCKS      int      `yaml:"socks-port"`
	Bind       string   `yaml:"bind-address"`
	Controller string   `yaml:"external-controller"`
	Secret     string   `yaml:"secret"`
	Auth       []string `yaml:"authentication"`
}

func (m *Manager) proxyConfig() (proxyConfig, error) {
	config, _, err := m.readProxyConfig()
	return config, err
}

func (m *Manager) readProxyConfig() (proxyConfig, []byte, error) {
	dir, _, err := m.paths("proxy")
	if err != nil {
		return proxyConfig{}, nil, err
	}
	path := filepath.Join(dir, "config.yaml")
	if err = safePath(m.Root, path); err != nil {
		return proxyConfig{}, nil, err
	}
	b, err := readRegular(path, 4<<20)
	if err != nil {
		return proxyConfig{}, nil, fmt.Errorf("无法读取内置 Proxy 配置")
	}
	var config proxyConfig
	if yaml.Unmarshal(b, &config) != nil {
		return proxyConfig{}, nil, fmt.Errorf("内置 Proxy 配置格式无效")
	}
	return config, b, nil
}

// ProxyEndpoint only reads configuration. A returned URL does not assert that
// Proxy is running or that this endpoint can reach the network.
func (m *Manager) ProxyEndpoint() (string, error) {
	config, err := m.proxyConfig()
	if err != nil {
		return "", err
	}
	if len(config.Auth) != 0 {
		return "", fmt.Errorf("内置 Proxy 需要认证；请显式配置带认证的代理地址")
	}
	host := "127.0.0.1"
	switch config.Bind {
	case "", "*", "0.0.0.0", "127.0.0.1", "localhost":
	case "::", "::1":
		host = "::1"
	default:
		return "", fmt.Errorf("内置 Proxy 没有配置本机回环监听地址")
	}
	for _, candidate := range []struct {
		port   int
		scheme string
	}{{config.Mixed, "http"}, {config.HTTP, "http"}, {config.SOCKS, "socks5h"}} {
		if candidate.port > 0 && candidate.port <= 65535 {
			return candidate.scheme + "://" + net.JoinHostPort(host, strconv.Itoa(candidate.port)), nil
		}
	}
	return "", fmt.Errorf("内置 Proxy 未配置有效的 mixed-port、port 或 socks-port")
}

func localController(address string) string {
	host, port, err := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	if err != nil || pe != nil || n < 1 || n > 65535 {
		return ""
	}
	switch host {
	case "", "0.0.0.0", "localhost":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return ""
	}
	return "http://" + net.JoinHostPort(ip.String(), port)
}

func (m *Manager) officialUpdate() (handled bool, err error) {
	p, err := m.process("proxy")
	if err != nil || p.PID == 0 {
		return false, err
	}
	config, configBytes, err := m.readProxyConfig()
	if err != nil {
		return false, nil
	}
	programHash, err := m.officialProgram()
	if err != nil {
		return false, err
	}
	endpoint := localController(config.Controller)
	owner, ok := m.runner().(EndpointOwner)
	if endpoint == "" || !ok || !cleanValue(config.Secret) {
		return false, nil
	}
	owned, err := owner.OwnsEndpoint(p, endpoint)
	if err != nil || !owned {
		return false, nil
	}
	base := m.HTTP
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if tcp, ok := transport.(*http.Transport); ok {
		direct := tcp.Clone()
		direct.Proxy = nil
		client.Transport = direct
	} else if m.Runner == nil {
		// Unknown custom transports cannot guarantee controller credentials
		// bypass an outbound proxy.
		return false, nil
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 5 * time.Second
	request := func(method, path string) (*http.Response, error) {
		req, _ := http.NewRequest(method, endpoint+path, nil)
		if config.Secret != "" {
			req.Header.Set("Authorization", "Bearer "+config.Secret)
		}
		return client.Do(req)
	}
	resp, err := request(http.MethodGet, "/version")
	if err != nil {
		return false, nil
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4097))
	resp.Body.Close()
	var version struct {
		Meta    bool   `json:"meta"`
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || readErr != nil || len(raw) > 4096 || json.Unmarshal(raw, &version) != nil || !version.Meta || version.Version == "" {
		return false, nil
	}
	// Revalidate the recorded process and endpoint immediately before POST.
	actual, err := m.process("proxy")
	if err != nil || actual != p {
		return false, fmt.Errorf("Proxy 进程在更新预检后发生变化")
	}
	_, currentConfig, err := m.readProxyConfig()
	if err != nil || hash(currentConfig) != hash(configBytes) {
		return false, fmt.Errorf("Proxy 配置在更新预检后发生变化；未提交更新")
	}
	currentProgram, err := m.officialProgram()
	if err != nil || currentProgram != programHash {
		return false, fmt.Errorf("Proxy 程序在更新预检后发生变化；未提交更新")
	}
	owned, err = owner.OwnsEndpoint(p, endpoint)
	if err != nil || !owned {
		return false, fmt.Errorf("Proxy 控制器归属在更新预检后发生变化")
	}
	client.Timeout = 2 * time.Minute
	resp, err = request(http.MethodPost, "/upgrade?channel=release")
	if err != nil {
		return true, fmt.Errorf("官方更新已提交但结果不明确；请检查 Proxy 状态，未下载覆盖")
	}
	defer resp.Body.Close()
	raw, readErr = io.ReadAll(io.LimitReader(resp.Body, 4097))
	var result struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK || readErr != nil || len(raw) > 4096 || json.Unmarshal(raw, &result) != nil || result.Status != "ok" {
		return true, fmt.Errorf("官方更新返回失败或结果不明确；未下载覆盖")
	}
	if _, err := m.officialProgram(); err != nil {
		return true, fmt.Errorf("官方更新后 Proxy 程序检查失败；保留现场，未下载覆盖")
	}
	return true, nil
}

// The official updater writes its own executable path. Its safety preconditions
// must therefore match downloaded replacement, including link rejection. A
// shared inode is rejected because the upstream may update a file in place.
func (m *Manager) officialProgram() (string, error) {
	_, program, err := m.paths("proxy")
	if err != nil {
		return "", err
	}
	if err = safePath(m.Root, program); err != nil {
		return "", err
	}
	info, err := os.Lstat(program)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("官方更新要求受管 Proxy 程序为普通可执行文件")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return "", fmt.Errorf("官方更新不支持共享硬链接程序；请先停止再使用下载更新")
	}
	b, err := readRegular(program, maxProgram)
	if err != nil || nativeProgram(b, m.platform()) != nil {
		return "", fmt.Errorf("受管 Proxy 程序未通过平台检查")
	}
	return hash(b), nil
}
