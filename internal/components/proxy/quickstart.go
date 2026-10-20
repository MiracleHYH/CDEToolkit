package proxy

import (
	"bytes"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// DefaultTestURL is the ordinary connectivity target used by generated groups
// and provider health checks. The ISP relay can use a separate test URL.
const DefaultTestURL = "https://www.gstatic.com/generate_204"

const DefaultControllerAddress = "127.0.0.1:9090"

type QuickStartRoute struct {
	Filter string
	Port   int
}

type QuickStartISP struct {
	NodeURL string
	Port    int
	// TestURL measures optional relay candidates, not the complete ISP route.
	TestURL     string
	RelayFilter string
}

type QuickStartOptions struct {
	ProviderURL string
	ProxyFilter string
	ProxyPort   int
	Download    *QuickStartRoute
	ISP         *QuickStartISP
	Controller  *ControllerConfig
}

// QuickStart creates one native Mihomo configuration without reading files,
// fetching subscriptions, or executing the core. Filters retain Mihomo's own
// regular-expression syntax; the core performs their final validation.
func QuickStart(o QuickStartOptions) ([]byte, error) {
	if err := quickStartHTTPURL(o.ProviderURL); err != nil {
		return nil, fmt.Errorf("provider 地址必须是有效的 HTTP 或 HTTPS URL")
	}
	ports := make(map[int]bool)
	checkRoute := func(name, filter string, port int) error {
		if !utf8.ValidString(filter) || strings.TrimSpace(filter) == "" || strings.ContainsFunc(filter, unicode.IsControl) {
			return fmt.Errorf("%s filter 必须是非空的单行表达式；匹配全部节点请填写 .*", name)
		}
		if port < 1 || port > 65535 || ports[port] {
			return fmt.Errorf("%s 端口必须是 1 至 65535 的整数，且不能与其他入口重复", name)
		}
		ports[port] = true
		return nil
	}
	if err := checkRoute("日常代理", o.ProxyFilter, o.ProxyPort); err != nil {
		return nil, err
	}
	groups := []map[string]any{quickStartProviderGroup("Proxy", o.ProxyFilter)}
	listeners := []map[string]any{quickStartListener("endpoint-proxy", "Proxy", o.ProxyPort)}
	updateGroup := "Proxy"
	if o.Download != nil {
		if err := checkRoute("下载代理", o.Download.Filter, o.Download.Port); err != nil {
			return nil, err
		}
		groups = append(groups, quickStartProviderGroup("Download", o.Download.Filter))
		listeners = append(listeners, quickStartListener("endpoint-download", "Download", o.Download.Port))
		updateGroup = "Download"
	}
	config := map[string]any{
		"allow-lan": false,
		"mode":      "rule",
		"log-level": "info",
		"proxy-providers": map[string]any{
			"subscription": map[string]any{
				"type":     "http",
				"url":      o.ProviderURL,
				"path":     "providers/subscription.yaml",
				"proxy":    "DIRECT",
				"interval": 0,
				"health-check": map[string]any{
					"enable":   true,
					"url":      DefaultTestURL,
					"interval": 300,
				},
			},
		},
		// Native updater HTTP uses the initialized inner tunnel. Limit this
		// recommendation to core-internal HTTPS on the official release hosts;
		// ordinary listener traffic retains its explicit outbound group.
		"rules": []string{
			"AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,github.com))," + updateGroup,
			"AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,release-assets.githubusercontent.com))," + updateGroup,
			"MATCH,DIRECT",
		},
	}
	if o.ISP != nil {
		// The ISP listener targets its node directly; only the optional relay
		// needs a provider group and a health-check target.
		if err := checkRoute("ISP 代理", ".*", o.ISP.Port); err != nil {
			return nil, err
		}
		node, err := ParseNodeURL(o.ISP.NodeURL)
		if err != nil {
			return nil, fmt.Errorf("ISP 节点链接无效: %w", err)
		}
		name, _ := node["name"].(string)
		if strings.TrimSpace(name) == "" {
			name = "isp-node"
			node["name"] = name
		}
		// These names select native actions or generated groups rather than
		// the supplied node. Never silently change the listener's destination.
		switch name {
		case "Proxy", "Download", "ISP-Relay", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "PASS-RULE", "GLOBAL", "COMPATIBLE":
			return nil, fmt.Errorf("ISP 节点名称与内置目标或 Quick Start 组冲突，请修改链接名称")
		}
		if o.ISP.RelayFilter != "" {
			if !utf8.ValidString(o.ISP.RelayFilter) || strings.TrimSpace(o.ISP.RelayFilter) == "" || strings.ContainsFunc(o.ISP.RelayFilter, unicode.IsControl) {
				return nil, fmt.Errorf("ISP 前置 filter 必须是非空的单行表达式；匹配全部节点请填写 .*")
			}
			testURL := o.ISP.TestURL
			if testURL == "" {
				testURL = DefaultTestURL
			}
			if err := quickStartHTTPURL(testURL); err != nil {
				return nil, fmt.Errorf("ISP 前置测试地址必须是有效的 HTTP 或 HTTPS URL")
			}
			relay := quickStartProviderGroup("ISP-Relay", o.ISP.RelayFilter)
			relay["url"] = testURL
			groups = append(groups, relay)
			node["dialer-proxy"] = "ISP-Relay"
		}
		config["proxies"] = []map[string]any{node}
		listeners = append(listeners, quickStartListener("endpoint-isp", name, o.ISP.Port))
	}
	config["proxy-groups"] = groups
	config["listeners"] = listeners
	if o.Controller != nil {
		controller, err := validatedController(*o.Controller)
		if err != nil {
			return nil, err
		}
		_, controllerPort, _ := localAddress(controller.Address)
		if strings.HasPrefix(controller.Address, "127.0.0.1:") && ports[controllerPort] {
			return nil, fmt.Errorf("Proxy controller 不能与代理入口监听同一地址和端口")
		}
		config["external-controller"] = controller.Address
		config["secret"] = controller.Secret
	}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return nil, fmt.Errorf("无法生成 Proxy quick start 配置: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("无法生成 Proxy quick start 配置: %w", err)
	}
	if encoded.Len() > maxConfig {
		return nil, fmt.Errorf("Proxy quick start 配置超过大小限制")
	}
	return encoded.Bytes(), nil
}

func quickStartProviderGroup(name, filter string) map[string]any {
	return map[string]any{
		"name":           name,
		"type":           "url-test",
		"use":            []string{"subscription"},
		"filter":         filter,
		"url":            DefaultTestURL,
		"interval":       300,
		"empty-fallback": "REJECT",
	}
}

func quickStartListener(name, group string, port int) map[string]any {
	return map[string]any{
		"name":   name,
		"type":   "mixed",
		"listen": "127.0.0.1",
		"port":   port,
		"proxy":  group,
	}
}

func quickStartHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !utf8.ValidString(raw) || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" || strings.ContainsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return fmt.Errorf("无效的 HTTP 或 HTTPS URL")
	}
	// A URL parser permits an empty trailing port and bracketed non-IP hosts.
	// Neither is a meaningful endpoint for the generated HTTP client.
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("URL 端口为空")
	}
	if strings.Contains(u.Hostname(), ":") || strings.HasPrefix(u.Host, "[") {
		if _, err := netip.ParseAddr(u.Hostname()); err != nil || !strings.HasPrefix(u.Host, "[") {
			return fmt.Errorf("URL IP 地址无效")
		}
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("URL 端口无效")
		}
	}
	return nil
}
