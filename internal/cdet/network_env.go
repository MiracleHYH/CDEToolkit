package cdet

import (
	"fmt"
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

func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], key+"="); ok {
			return value
		}
	}
	return ""
}

func setEnvValue(env []string, key, value string) []string {
	next := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, key+"=") {
			next = append(next, entry)
		}
	}
	return append(next, key+"="+value)
}

// Package-manager provenance inherited from a parent Codex process is not the
// installation context of this managed standalone command or its version probe.
func codexRuntimeEnv(env []string, data, bin string) []string {
	var clean []string
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "CODEX_MANAGED_PACKAGE_ROOT", "CODEX_MANAGED_BY_NPM", "CODEX_MANAGED_BY_BUN", "CODEX_MANAGED_BY_PNPM", "CODEX_MANAGED_BY_VITE_PLUS":
			continue
		}
		clean = append(clean, entry)
	}
	clean = setEnvValue(clean, "CODEX_HOME", data)
	clean = setEnvValue(clean, "CODEX_SQLITE_HOME", data)
	return setEnvValue(clean, "CODEX_INSTALL_DIR", bin)
}

func proxyURL(value string) (*url.URL, error) {
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(value, "\r\n\x00") {
		return nil, fmt.Errorf("代理地址无效（地址未输出）")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("代理协议不支持")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("代理端口无效")
		}
	}
	return u, nil
}

func (m *Manager) networkEnv() ([]string, error) {
	env := append([]string(nil), os.Environ()...)
	explicit := false
	// Use curl's lowercase-first preference consistently for subprocesses and
	// Go HTTP. Filling both forms also makes uppercase HTTP_PROXY work in curl.
	for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		value := envValue(env, key)
		if value == "" {
			value = envValue(env, strings.ToUpper(key))
		}
		if value == "" {
			continue
		}
		if key != "no_proxy" {
			if _, err := proxyURL(value); err != nil {
				return nil, err
			}
			explicit = true
		}
		env = setEnvValue(env, key, value)
		env = setEnvValue(env, strings.ToUpper(key), value)
	}
	if explicit {
		return env, nil
	}
	file := filepath.Join(m.Store, dataPath("proxy"), "config.yaml")
	if absent(file) {
		return env, nil
	}
	b, err := readRegular(file)
	if err != nil {
		return nil, fmt.Errorf("无法读取受管代理端口配置")
	}
	var ports struct {
		Mixed int `yaml:"mixed-port"`
		HTTP  int `yaml:"port"`
		SOCKS int `yaml:"socks-port"`
	}
	if yaml.Unmarshal(b, &ports) != nil {
		return nil, fmt.Errorf("受管代理端口配置无效")
	}
	for _, port := range []int{ports.Mixed, ports.HTTP, ports.SOCKS} {
		if port < 0 || port > 65535 {
			return nil, fmt.Errorf("受管代理端口无效")
		}
	}
	port := ports.Mixed
	if port == 0 {
		port = ports.HTTP
	}
	if port != 0 {
		value := "http://127.0.0.1:" + strconv.Itoa(port)
		for _, key := range []string{"http_proxy", "https_proxy", "all_proxy"} {
			env = setEnvValue(env, key, value)
			env = setEnvValue(env, strings.ToUpper(key), value)
		}
	} else if ports.SOCKS != 0 {
		value := "socks5h://127.0.0.1:" + strconv.Itoa(ports.SOCKS)
		env = setEnvValue(env, "all_proxy", value)
		env = setEnvValue(env, "ALL_PROXY", value)
	}
	return env, nil
}

func noProxyMatch(u *url.URL, list string) bool {
	host, port := strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	ip := net.ParseIP(host)
	for _, item := range strings.Split(list, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "*" {
			return true
		}
		if _, subnet, err := net.ParseCIDR(item); err == nil {
			if ip != nil && subnet.Contains(ip) {
				return true
			}
			continue
		}
		pattern, onlyPort, err := net.SplitHostPort(item)
		if err == nil {
			if onlyPort != port {
				continue
			}
			item = pattern
		}
		item = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(item, "."), "*"), ".")
		item = strings.Trim(item, "[]")
		if item == "" {
			continue
		}
		if candidate := net.ParseIP(item); candidate != nil {
			if ip != nil && candidate.Equal(ip) {
				return true
			}
		} else if host == item || strings.HasSuffix(host, "."+item) {
			return true
		}
	}
	return false
}

func networkProxy(env []string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if noProxyMatch(req.URL, envValue(env, "no_proxy")) {
			return nil, nil
		}
		value := envValue(env, req.URL.Scheme+"_proxy")
		if value == "" {
			value = envValue(env, "all_proxy")
		}
		if value == "" {
			return nil, nil
		}
		return proxyURL(value)
	}
}

func (m *Manager) networkHTTP() (*http.Client, error) {
	env, err := m.networkEnv()
	if err != nil {
		return nil, err
	}
	base := m.releases().HTTP
	if base == nil {
		base = &http.Client{Timeout: 2 * time.Minute}
	}
	client := *base
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		copy := standard.Clone()
		copy.Proxy = networkProxy(env)
		client.Transport = copy
	} else {
		// A deliberately injected RoundTripper is an isolated-test seam.
		client.Transport = transport
	}
	return &client, nil
}

func (m *Manager) networkReleases() (*ReleaseClient, error) {
	client, err := m.networkHTTP()
	if err != nil {
		return nil, err
	}
	copy := *m.releases()
	copy.HTTP = client
	return &copy, nil
}
