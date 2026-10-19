package nido

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

// Retained only to read earlier Nido configurations and automation flags.
const (
	NetworkDirect         = "DIRECT"
	NetworkGitHubProxy    = "GITHUB_PROXY"
	NetworkProxy          = "PROXY"
	GitHubAccelerateNone  = "none"
	GitHubAccelerateProxy = "proxy"
)

var ErrNetworkChoice = errors.New("网络设置无效，请在 Nido 中配置 GitHub Accelerate 和 Proxy")

func normalizeDownload(policy string) (DownloadConfig, error) {
	value := strings.TrimSpace(policy)
	switch strings.ToLower(value) {
	case "", "none", "direct":
		value = GitHubAccelerateNone
	case "proxy":
		value = GitHubAccelerateProxy
	default:
		if err := githubdownload.Validate(value); err != nil {
			return DownloadConfig{}, err
		}
	}
	return DownloadConfig{GitHubAccelerate: value}, nil
}

// canonicalNetwork is deliberately a narrow read conversion, not a second
// routing engine. Every successful write stores only the two current fields.
func canonicalNetwork(config DownloadConfig) (DownloadConfig, error) {
	if config.Listener != "" {
		return DownloadConfig{}, fmt.Errorf("旧 listener 绑定已移除；请在 Nido Proxy 中填写通用代理 URL")
	}
	value := config.GitHubAccelerate
	if value == "" {
		switch strings.ToUpper(config.Mode) {
		case "":
			value = config.GitHubProxy
		case NetworkDirect:
			if config.ProxyURL != "" || config.GitHubProxy != "" && config.GitHubProxy != "direct" {
				return DownloadConfig{}, ErrNetworkChoice
			}
			value = GitHubAccelerateNone
		case NetworkGitHubProxy:
			value = config.GitHubProxy
			if value == "" || value == "direct" || config.ProxyURL != "" {
				return DownloadConfig{}, ErrNetworkChoice
			}
		case NetworkProxy:
			if config.ProxyURL == "" || config.GitHubProxy != "" {
				return DownloadConfig{}, ErrNetworkChoice
			}
			value = GitHubAccelerateProxy
		default:
			return DownloadConfig{}, ErrNetworkChoice
		}
	} else if config.Mode != "" || config.GitHubProxy != "" {
		return DownloadConfig{}, fmt.Errorf("新旧网络字段不能同时配置")
	}
	next, err := normalizeDownload(value)
	if err != nil {
		return DownloadConfig{}, err
	}
	next.ProxyURL = config.ProxyURL
	if strings.EqualFold(next.ProxyURL, "none") {
		next.ProxyURL = ""
	}
	if next.ProxyURL != "" {
		if err := validateBindingProxy(next.ProxyURL); err != nil {
			return DownloadConfig{}, err
		}
	}
	if next.GitHubAccelerate == GitHubAccelerateProxy && next.ProxyURL == "" {
		return DownloadConfig{}, fmt.Errorf("GitHub Accelerate 引用 Proxy 前，请先配置 Proxy URL")
	}
	return next, nil
}

func ValidateNetwork(config DownloadConfig) error { _, err := canonicalNetwork(config); return err }

func (m *Manager) savedNetwork() (DownloadConfig, error) { return canonicalNetwork(m.Config.Download) }

func (m *Manager) resolveNetwork(githubPrefix, proxyURL string) (DownloadConfig, error) {
	config, err := m.savedNetwork()
	if err != nil {
		// Apply explicit overrides to the original fields, then validate. A
		// Proxy override cannot silently turn an invalid file route into direct.
		config = m.Config.Download
	}
	if githubPrefix != "" {
		next, e := normalizeDownload(githubPrefix)
		if e != nil {
			return DownloadConfig{}, e
		}
		config = DownloadConfig{GitHubAccelerate: next.GitHubAccelerate, ProxyURL: config.ProxyURL}
	}
	if proxyURL != "" {
		switch strings.ToLower(proxyURL) {
		case "none", "off", "direct":
			config.ProxyURL = ""
		default:
			config.ProxyURL = proxyURL
		}
	}
	return canonicalNetwork(config)
}

func (m *Manager) networkPolicy(githubPrefix, proxyURL string) (string, error) {
	config, err := m.resolveNetwork(githubPrefix, proxyURL)
	if err != nil {
		return "", err
	}
	if config.GitHubAccelerate != GitHubAccelerateNone && config.GitHubAccelerate != GitHubAccelerateProxy {
		return config.GitHubAccelerate, nil
	}
	return "direct", nil
}

func (m *Manager) setNetwork(config DownloadConfig) error { return m.setNetworkEndpoint(config, nil) }
func (m *Manager) setNetworkEndpoint(config DownloadConfig, endpoint *proxy.Endpoint) error {
	next, err := canonicalNetwork(config)
	if err != nil {
		return err
	}
	return m.mutate(false, func() error {
		if endpoint != nil {
			if err := m.checkBuiltinEndpoint(endpoint); err != nil {
				return err
			}
		}
		if m.Config.Download == next {
			return nil
		}
		previous := m.Config
		before := append([]byte(nil), m.configBytes...)
		m.Config.Download = next
		if err := m.save(); err != nil {
			if bytes.Equal(before, m.configBytes) {
				m.Config = previous
			}
			return err
		}
		return nil
	})
}

func redactedNetworkProxy(address string) string {
	if address == "" {
		return "None"
	}
	if err := validateBindingProxy(address); err != nil {
		return "无效代理地址"
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return "无效代理地址"
	}
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}
	return parsed.String()
}

func (m *Manager) networkCheck() func() error {
	before := append([]byte(nil), m.configBytes...)
	return func() error {
		if m.rootInfo != nil {
			if err := m.checkRoot(); err != nil {
				return err
			}
		}
		return unchanged(filepath.Join(m.Root, "config.toml"), before)
	}
}

// Explicit clients ignore ambient proxies and never mutate the shared transport.
func explicitHTTPClient(base *http.Client, address *url.URL) (*http.Client, error) {
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if address != nil {
		transport = http.DefaultTransport
	}
	if ordinary, ok := transport.(*http.Transport); ok {
		clone := ordinary.Clone()
		clone.Proxy = nil
		if address != nil {
			clone.Proxy = http.ProxyURL(address)
		}
		if clone.TLSClientConfig != nil {
			clone.TLSClientConfig = clone.TLSClientConfig.Clone()
			clone.TLSClientConfig.InsecureSkipVerify = false
			clone.TLSClientConfig.ServerName = ""
		}
		client.Transport = clone
	} else {
		if address != nil {
			return nil, fmt.Errorf("自定义 HTTP 传输不能叠加通用代理")
		}
		client.Transport = transport
	}
	client.Jar = nil
	return &client, nil
}

func (m *Manager) networkHTTP(base *http.Client, githubPrefix, proxyURL string) (*http.Client, error) {
	for base != nil {
		previous, ok := base.Transport.(apiBindingTransport)
		if !ok || !previous.networkRoute {
			break
		}
		base = previous.template
	}
	config, err := m.resolveNetwork(githubPrefix, proxyURL)
	if err != nil {
		return nil, err
	}
	var address *url.URL
	if config.GitHubAccelerate == GitHubAccelerateProxy {
		address, _ = url.Parse(config.ProxyURL)
	}
	client, err := explicitHTTPClient(base, address)
	if err != nil {
		return nil, err
	}
	client.Transport = apiBindingTransport{base: client.Transport, check: m.networkCheck(), networkRoute: true, template: base}
	return client, nil
}
func (m *Manager) networkClient(base *http.Client, proxyURL string) (*http.Client, error) {
	return m.networkHTTP(base, "", proxyURL)
}

// General API/command routing is independent of the GitHub file route. A bad
// file prefix must not block unrelated API or official-installer operations.
func (m *Manager) generalProxyURL(explicit string) (string, error) {
	address := explicit
	if address == "" {
		if m.Config.Download.Listener != "" {
			return "", fmt.Errorf("旧 listener 绑定已移除；请在 Nido 中配置 Proxy URL")
		}
		address = m.Config.Download.ProxyURL
	}
	switch strings.ToLower(address) {
	case "", "none", "off", "direct":
		return "", nil
	}
	if err := validateBindingProxy(address); err != nil {
		return "", err
	}
	return address, nil
}

func (m *Manager) commandProxyEnv(base []string, explicit string) ([]string, error) {
	address, err := m.generalProxyURL(explicit)
	if err != nil {
		return nil, err
	}
	env := make([]string, 0, len(base)+8)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToLower(key) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			continue
		}
		env = append(env, entry)
	}
	if address != "" {
		for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
			env = append(env, key+"="+address)
		}
	}
	env = append(env, "no_proxy=localhost,127.0.0.1,::1", "NO_PROXY=localhost,127.0.0.1,::1")
	return env, nil
}

func networkSummary(config DownloadConfig) string {
	next, err := canonicalNetwork(config)
	if err != nil {
		return "网络设置需要重新配置"
	}
	accelerate := next.GitHubAccelerate
	if accelerate == GitHubAccelerateNone {
		accelerate = "None"
	} else if accelerate == GitHubAccelerateProxy {
		accelerate = "Proxy"
	}
	return "GitHub Accelerate：" + accelerate + "\nProxy：" + redactedNetworkProxy(next.ProxyURL)
}

func (c *CLI) saveNetworkChoice(m *Manager, o cliOptions, next DownloadConfig, endpoint *proxy.Endpoint) int {
	if err := ValidateNetwork(next); err != nil {
		return c.fail(err)
	}
	fmt.Fprintf(c.Out, "%s\n保存到：%s\n仅影响 Nido 本次及后续操作，不改变 Codex、Git 或系统代理。\n", networkSummary(next), filepath.Join(m.Root, "config.toml"))
	if err := c.confirm(o, "保存网络设置"); err != nil {
		return c.fail(err)
	}
	if err := m.setNetworkEndpoint(next, endpoint); err != nil {
		return c.fail(err)
	}
	return c.output(o, map[string]string{"detail": "网络设置已保存，未启动服务。"})
}

func (c *CLI) networkConfigure(m *Manager, o cliOptions) int {
	if !c.interactive(o) {
		return c.fail(fmt.Errorf("网络配置需要交互终端；脚本请用 download set none|proxy|HTTPS前缀 和 download proxy none|URL"))
	}
	return c.githubAccelerateConfigure(m, o)
}
