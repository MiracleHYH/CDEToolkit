package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const maxConfig = 4 << 20

// Config retains the exact bytes checked by the component. Full node and route
// semantics are intentionally left to mihomo -t, rather than copied here.
type Config struct {
	Data      []byte     `json:"-"`
	Digest    string     `json:"digest"`
	Endpoints []Endpoint `json:"endpoints"`
}

func (s *Service) ConfigPath() (string, error) {
	dir, _, err := s.paths()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	if err := safePath(s.Root, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) ReadConfig() (Config, error) {
	path, err := s.ConfigPath()
	if err != nil {
		return Config{}, err
	}
	data, err := readRegular(path, maxConfig)
	if err != nil {
		return Config{}, fmt.Errorf("无法读取 Proxy 普通配置文件: %w", err)
	}
	return ParseConfig(data, filepath.Dir(path))
}

// ParseConfig enforces only the documented local explicit-proxy policy. It does
// not execute the core, resolve hostnames, fetch providers, or change files.
func ParseConfig(data []byte, rootDir string) (Config, error) {
	if len(data) == 0 || len(data) > maxConfig {
		return Config{}, fmt.Errorf("Proxy 配置为空或超过大小限制")
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return Config{}, fmt.Errorf("Proxy 配置必须是有效的单个 YAML mapping")
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return Config{}, fmt.Errorf("Proxy 配置只接受单个 YAML 文档")
	}
	if err := checkYAML(&document, 0); err != nil {
		return Config{}, err
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) == 0 {
		return Config{}, fmt.Errorf("Proxy 配置必须是非空 YAML mapping")
	}
	if !filepath.IsAbs(rootDir) || filepath.Clean(rootDir) == string(filepath.Separator) {
		return Config{}, fmt.Errorf("Proxy 配置根目录必须是非根目录的绝对路径")
	}
	if err := checkRuntimePaths(rootDir); err != nil {
		return Config{}, err
	}
	if err := checkLocalPolicy(root, rootDir); err != nil {
		return Config{}, err
	}
	endpoints, err := configEndpoints(root)
	if err != nil {
		return Config{}, err
	}
	config := Config{Data: bytes.Clone(data), Digest: hash(data), Endpoints: endpoints}
	for i := range config.Endpoints {
		config.Endpoints[i].ConfigDigest = config.Digest
	}
	return config, nil
}

// Explicit keys, rather than a recursive search for arbitrary fields named
// path, define the component's resource policy. Unknown node extensions remain
// core-owned; this validation is not an operating-system sandbox.
func checkLocalPolicy(root *yaml.Node, rootDir string) error {
	for _, name := range []string{"redir-port", "tproxy-port"} {
		port, err := traditionalPortValue(field(root, name))
		if err != nil || port != 0 {
			return fmt.Errorf("Proxy 不支持透明代理入站")
		}
	}
	for _, name := range []string{"ss-config", "vmess-config", "external-controller-tls", "external-controller-unix", "external-controller-pipe", "external-ui", "external-ui-url", "external-ui-name", "script", "scripts", "tunnels"} {
		if n := field(root, name); n != nil && !emptyNode(n) {
			return fmt.Errorf("Proxy 暂不支持 %s 配置", name)
		}
	}
	for _, name := range []string{"tun", "tuic-server", "iptables"} {
		n := field(root, name)
		if n == nil {
			continue
		}
		if n.Kind != yaml.MappingNode {
			return fmt.Errorf("Proxy 的 %s 必须是 mapping", name)
		}
		enabled, err := boolValue(field(n, "enable"), false)
		if err != nil || enabled {
			return fmt.Errorf("Proxy 不支持启用 %s", name)
		}
	}
	if ntp := field(root, "ntp"); ntp != nil {
		if ntp.Kind != yaml.MappingNode {
			return fmt.Errorf("Proxy 的 ntp 必须是 mapping")
		}
		write, err := boolValue(field(ntp, "write-to-system"), false)
		if err != nil || write {
			return fmt.Errorf("Proxy 不支持修改系统时间")
		}
	}
	if dns := field(root, "dns"); dns != nil {
		if dns.Kind != yaml.MappingNode {
			return fmt.Errorf("Proxy 的 dns 必须是 mapping")
		}
		listen, err := stringValue(field(dns, "listen"), "")
		if err != nil {
			return fmt.Errorf("Proxy DNS 监听地址无效")
		}
		if listen != "" {
			if _, _, err := localAddress(listen); err != nil {
				return fmt.Errorf("Proxy DNS 必须使用字面回环地址和有效端口")
			}
		}
	}
	controller, err := stringValue(field(root, "external-controller"), "")
	if err != nil {
		return fmt.Errorf("Proxy controller 地址无效")
	}
	if controller != "" {
		if _, _, err := localAddress(controller); err != nil {
			return fmt.Errorf("Proxy controller 必须使用字面回环地址和有效端口")
		}
		secret, err := stringValue(field(root, "secret"), "")
		if err != nil || strings.TrimSpace(secret) == "" || strings.ContainsFunc(secret, unicode.IsControl) {
			return fmt.Errorf("Proxy controller 必须配置非空 secret")
		}
	}
	for _, name := range []string{"proxy-providers", "rule-providers"} {
		providers := field(root, name)
		if providers == nil {
			continue
		}
		if providers.Kind != yaml.MappingNode {
			return fmt.Errorf("Proxy 的 %s 必须是 mapping", name)
		}
		for i := 1; i < len(providers.Content); i += 2 {
			provider := providers.Content[i]
			if provider.Kind != yaml.MappingNode {
				return fmt.Errorf("Proxy provider 必须是 mapping")
			}
			// Mihomo's provider decoder matches keys without regard to case.
			// Reject aliases instead of letting its interpretation bypass ours.
			if err := canonicalFields(provider, false, "type", "path"); err != nil {
				return err
			}
			kind, err := stringValue(field(provider, "type"), "")
			if err != nil {
				return fmt.Errorf("Proxy provider type 必须是字符串")
			}
			if (kind == "http" || kind == "file") && field(provider, "path") == nil {
				return fmt.Errorf("Proxy http/file provider 必须显式配置组件根内的相对 path")
			}
			if path := field(provider, "path"); path != nil {
				if err := resourcePath(path, rootDir); err != nil {
					return err
				}
			}
		}
	}
	// Certificate fields can be inline PEM or paths in Mihomo. This first version
	// accepts only files inside the component, avoiding ambient file access.
	if tls := field(root, "tls"); tls != nil {
		if tls.Kind != yaml.MappingNode {
			return fmt.Errorf("Proxy 的 tls 必须是 mapping")
		}
		for _, name := range []string{"certificate", "private-key", "client-auth-cert", "ech-key"} {
			if n := field(tls, name); n != nil && !emptyNode(n) {
				if err := resourcePath(n, rootDir); err != nil {
					return err
				}
			}
		}
		// Despite the upstream spelling, this field contains inline trust
		// certificates, not paths. The core validates their PEM/DER contents.
		if certs := field(tls, "custom-certifactes"); certs != nil {
			if certs.Kind != yaml.SequenceNode {
				return fmt.Errorf("Proxy TLS 证书列表无效")
			}
			for _, cert := range certs.Content {
				if _, err := stringValue(cert, ""); err != nil {
					return fmt.Errorf("Proxy TLS 内联证书必须是字符串")
				}
			}
		}
	}
	return nil
}

func configEndpoints(root *yaml.Node) ([]Endpoint, error) {
	allowLAN, err := boolValue(field(root, "allow-lan"), false)
	if err != nil {
		return nil, fmt.Errorf("Proxy allow-lan 必须是布尔值")
	}
	bind, err := stringValue(field(root, "bind-address"), "*")
	if err != nil {
		return nil, fmt.Errorf("Proxy bind-address 必须是字符串")
	}
	auth, err := traditionalAuth(field(root, "authentication"))
	if err != nil {
		return nil, err
	}
	var endpoints []Endpoint
	for _, item := range []struct{ key, name, scheme string }{
		{"mixed-port", "mixed", "http"}, {"port", "http", "http"}, {"socks-port", "socks", "socks5h"},
	} {
		port, err := traditionalPortValue(field(root, item.key))
		if err != nil {
			return nil, fmt.Errorf("Proxy 的 %s 必须是 0 或 1 至 65535 的整数", item.key)
		}
		if port == 0 {
			continue
		}
		host := "127.0.0.1"
		if allowLAN {
			// Upstream joins traditional bind-address with fmt.Sprintf. Preserve
			// its IPv6 bracket requirement instead of inventing a different URL.
			if strings.Contains(bind, ":") && !(strings.HasPrefix(bind, "[") && strings.HasSuffix(bind, "]")) {
				return nil, fmt.Errorf("传统 Proxy IPv6 bind-address 必须使用方括号")
			}
			host, err = loopbackIP(strings.TrimSuffix(strings.TrimPrefix(bind, "["), "]"))
			if err != nil {
				return nil, fmt.Errorf("Proxy 传统端口必须只监听字面回环地址")
			}
		}
		endpoints = append(endpoints, Endpoint{Name: item.name, Scheme: item.scheme, Host: host, Port: port, Authentication: auth})
	}
	if listeners := field(root, "listeners"); listeners != nil {
		if listeners.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("Proxy listeners 必须是列表")
		}
		for _, listener := range listeners.Content {
			if listener.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("Proxy listener 必须是 mapping")
			}
			// The upstream listener decoder accepts case and underscore aliases.
			// Keep policy-controlled fields canonical so exported endpoints and
			// the actual inbound cannot disagree about auth, TLS, or addresses.
			if err := canonicalFields(listener, true, "name", "listen", "port", "users", "certificate", "private-key", "client-auth-type", "client-auth-cert", "ech-key", "reality-config"); err != nil {
				return nil, err
			}
			kind, err := stringValue(field(listener, "type"), "")
			if err != nil || (kind != "mixed" && kind != "http" && kind != "socks") {
				return nil, fmt.Errorf("Proxy 只支持 mixed、http 和 socks listeners")
			}
			name, err := stringValue(field(listener, "name"), "")
			if err != nil || strings.TrimSpace(name) == "" || strings.ContainsFunc(name, unicode.IsControl) || len(name) > 128 {
				return nil, fmt.Errorf("Proxy listener 必须具有有效名称")
			}
			listen, err := stringValue(field(listener, "listen"), "")
			if err != nil {
				return nil, fmt.Errorf("Proxy listener 监听地址无效")
			}
			host, err := loopbackIP(listen)
			if err != nil {
				return nil, fmt.Errorf("Proxy listener 必须显式监听字面回环地址")
			}
			port, err := portValue(field(listener, "port"), false)
			if err != nil {
				return nil, fmt.Errorf("Proxy listener 必须使用 1 至 65535 的单个端口")
			}
			for _, key := range []string{"certificate", "private-key", "client-auth-type", "client-auth-cert", "ech-key", "reality-config"} {
				if n := field(listener, key); n != nil && !emptyNode(n) {
					return nil, fmt.Errorf("Proxy 暂不支持 TLS 或 Reality 代理入站")
				}
			}
			listenerAuth, err := listenerAuth(field(listener, "users"), auth)
			if err != nil {
				return nil, err
			}
			scheme := "http"
			if kind == "socks" {
				scheme = "socks5h"
			}
			endpoints = append(endpoints, Endpoint{Name: name, Scheme: scheme, Host: host, Port: port, Authentication: listenerAuth})
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("Proxy 至少需要一个受支持的回环代理端口")
	}
	names, addresses := map[string]bool{}, map[string]bool{}
	for _, endpoint := range endpoints {
		address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port))
		if names[endpoint.Name] || addresses[address] {
			return nil, fmt.Errorf("Proxy 端点名称或监听地址重复")
		}
		names[endpoint.Name], addresses[address] = true, true
	}
	return endpoints, nil
}

func resourcePath(node *yaml.Node, root string) error {
	path, err := stringValue(node, "")
	if err != nil || path == "" || !filepath.IsLocal(path) || strings.ContainsFunc(path, unicode.IsControl) || strings.Contains(path, "\\") {
		return fmt.Errorf("Proxy 资源必须使用组件根内的相对路径")
	}
	first, _, _ := strings.Cut(filepath.Clean(path), string(filepath.Separator))
	for _, reserved := range []string{"mihomo", "config.yaml", "process.json", "output.log", ".lock"} {
		if strings.EqualFold(first, reserved) {
			return fmt.Errorf("Proxy 资源不能覆盖组件受管文件")
		}
	}
	if runtimeFileName(first) {
		return fmt.Errorf("Proxy 资源不能覆盖核心固定缓存或数据库")
	}
	if err := safePath(root, filepath.Join(root, path)); err != nil {
		return fmt.Errorf("Proxy 资源路径不安全")
	}
	if info, err := os.Lstat(filepath.Join(root, path)); err == nil {
		if !info.Mode().IsRegular() || !singleLink(info) {
			return fmt.Errorf("Proxy 资源必须是无共享硬链接的普通文件")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("无法检查 Proxy 资源路径")
	}
	return nil
}

// These names come from Mihomo's constant/path.go. Geo database discovery is
// case-insensitive even on Linux, so inspect matching directory entries too.
// This bounded policy concerns known files, not every future outbound extension
// or arbitrary user data under the component root.
func runtimeFileName(name string) bool {
	for _, reserved := range []string{"cache.db", ".cache", "GeoSite.dat", "GeoIP.dat", "ASN.mmdb", "BundleMRS.7z", "Country.mmdb", "geoip.db", "geoip.metadb"} {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

func checkRuntimePaths(root string) error {
	if err := safePath(root, root); err != nil {
		return fmt.Errorf("Proxy 核心数据根目录不安全")
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法检查 Proxy 核心固定数据路径")
	}
	for _, entry := range entries {
		if !runtimeFileName(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := safePath(root, path); err != nil {
			return fmt.Errorf("Proxy 核心固定数据路径不安全")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || !singleLink(info) {
			return fmt.Errorf("Proxy 核心固定数据必须是无共享硬链接的普通文件")
		}
	}
	return nil
}

func localAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("监听地址无效")
	}
	host, err = loopbackIP(host)
	port, pe := strconv.Atoi(portText)
	if err != nil || pe != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("监听地址无效")
	}
	return host, port, nil
}

func loopbackIP(host string) (string, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.Unmap().IsLoopback() {
		return "", fmt.Errorf("必须是字面回环 IP")
	}
	return ip.Unmap().String(), nil
}

func traditionalAuth(node *yaml.Node) (bool, error) {
	if node == nil {
		return false, nil
	}
	if node.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("Proxy authentication 必须是列表")
	}
	for _, n := range node.Content {
		value, err := stringValue(n, "")
		if err != nil || !strings.Contains(value, ":") {
			return false, fmt.Errorf("Proxy authentication 项必须包含用户名和密码分隔符")
		}
	}
	return len(node.Content) != 0, nil
}

func listenerAuth(node *yaml.Node, inherited bool) (bool, error) {
	if node == nil {
		return inherited, nil
	}
	if node.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("Proxy listener users 必须是列表")
	}
	for _, user := range node.Content {
		if user.Kind != yaml.MappingNode || field(user, "username") == nil || field(user, "password") == nil {
			return false, fmt.Errorf("Proxy listener users 必须包含用户名和密码")
		}
		if err := canonicalFields(user, true, "username", "password"); err != nil {
			return false, err
		}
		if _, err := stringValue(field(user, "username"), ""); err != nil {
			return false, fmt.Errorf("Proxy listener 用户名必须是字符串")
		}
		if _, err := stringValue(field(user, "password"), ""); err != nil {
			return false, fmt.Errorf("Proxy listener 密码必须是字符串")
		}
	}
	return len(node.Content) != 0, nil
}

func field(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			return node.Content[i+1]
		}
	}
	return nil
}

// canonicalFields applies only to fields whose upstream structure decoder has
// aliases. Top-level YAML struct fields and arbitrary extension keys retain
// their native semantics; user-defined names are never normalized.
func canonicalFields(node *yaml.Node, underscores bool, names ...string) error {
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		candidate := key
		if underscores {
			candidate = strings.ReplaceAll(candidate, "_", "-")
		}
		for _, name := range names {
			if key != name && strings.EqualFold(candidate, name) {
				return fmt.Errorf("Proxy 策略字段必须使用规范的小写连字符名称")
			}
		}
	}
	return nil
}

func stringValue(node *yaml.Node, fallback string) (string, error) {
	if node == nil {
		return fallback, nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("必须是字符串")
	}
	return node.Value, nil
}

func boolValue(node *yaml.Node, fallback bool) (bool, error) {
	if node == nil {
		return fallback, nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, fmt.Errorf("必须是布尔值")
	}
	return strconv.ParseBool(node.Value)
}

func portValue(node *yaml.Node, disabled bool) (int, error) {
	if node == nil && disabled {
		return 0, nil
	}
	if node == nil || node.Kind != yaml.ScalarNode || (node.Tag != "!!int" && node.Tag != "!!str") || node.Value == "" {
		return 0, fmt.Errorf("端口无效")
	}
	// yaml/v3 interprets an unquoted leading-zero integer as octal. The
	// listener decoder sees that decoded number, while quoted ports stay decimal.
	if node.Tag == "!!int" && len(node.Value) > 1 && node.Value[0] == '0' {
		return 0, fmt.Errorf("端口整数不能包含前导零")
	}
	for _, c := range node.Value {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("端口无效")
		}
	}
	port, err := strconv.Atoi(node.Value)
	if err != nil || port < 0 || port > 65535 || (port == 0 && !disabled) {
		return 0, fmt.Errorf("端口无效")
	}
	return port, nil
}

func traditionalPortValue(node *yaml.Node) (int, error) {
	// Unlike listeners, Mihomo decodes traditional ports directly into int
	// fields through yaml/v3, which does not accept quoted numeric strings.
	if node != nil && node.Tag != "!!int" {
		return 0, fmt.Errorf("传统端口必须是 YAML 整数")
	}
	return portValue(node, true)
}

func emptyNode(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "") || (node.Kind == yaml.SequenceNode || node.Kind == yaml.MappingNode) && len(node.Content) == 0
}

func checkYAML(node *yaml.Node, depth int) error {
	if depth > 64 {
		return fmt.Errorf("Proxy YAML 嵌套过深")
	}
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return fmt.Errorf("Proxy 配置暂不接受 YAML 别名或合并键")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return fmt.Errorf("Proxy YAML mapping 键必须是普通字符串")
			}
			if seen[key.Value] {
				return fmt.Errorf("Proxy YAML 含重复键")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := checkYAML(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// Template creates an ordinary Mihomo provider configuration. Sensitive URL
// input is encoded as YAML data and is never copied into diagnostic messages.
func Template(subscription string) ([]byte, error) {
	address, err := url.Parse(subscription)
	if err != nil || (address.Scheme != "https" && address.Scheme != "http") || address.Hostname() == "" || address.Opaque != "" || strings.ContainsFunc(subscription, unicode.IsControl) {
		return nil, fmt.Errorf("订阅地址必须是有效的 HTTP 或 HTTPS URL")
	}
	if address.Port() != "" {
		port, err := strconv.Atoi(address.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("订阅地址端口无效")
		}
	}
	var urlNode yaml.Node
	if err := urlNode.Encode(subscription); err != nil {
		return nil, fmt.Errorf("无法生成订阅配置")
	}
	// A quoted scalar is inserted only after the YAML encoder has escaped it.
	urlNode.Style = yaml.DoubleQuotedStyle
	encoded, err := yaml.Marshal(&urlNode)
	if err != nil {
		return nil, fmt.Errorf("无法生成订阅配置")
	}
	return []byte("# Nido 本地显式代理；订阅需提供 Mihomo provider 格式。\n" +
		"allow-lan: false\nmode: rule\nlog-level: info\n" +
		"listeners:\n  - name: common\n    type: mixed\n    listen: 127.0.0.1\n    port: 7890\n" +
		"proxy-providers:\n  subscription:\n    type: http\n    url: " + strings.TrimSpace(string(encoded)) + "\n" +
		"    path: providers/subscription.yaml\n    proxy: DIRECT\n    interval: 3600\n" +
		"proxy-groups:\n  - name: PROXY\n    type: select\n    use: [subscription]\n    empty-fallback: REJECT\n" +
		"rules:\n  - MATCH,PROXY\n"), nil
}
