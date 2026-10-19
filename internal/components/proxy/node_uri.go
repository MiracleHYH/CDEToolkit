package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNodeURL = 64 << 10

// ParseNodeURL translates a supported share link to one native Mihomo outbound.
// It performs no DNS resolution, network requests, filesystem writes or execution.
// Unknown fields fail rather than being silently dropped from the connection.
func ParseNodeURL(raw string) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > maxNodeURL || !nodeText(raw) {
		return nil, fmt.Errorf("节点链接为空、过长或包含控制字符")
	}
	raw = strings.TrimSpace(raw)
	scheme, body, ok := strings.Cut(raw, "://")
	if !ok {
		return nil, fmt.Errorf("节点链接缺少协议")
	}
	var node map[string]any
	var err error
	switch strings.ToLower(scheme) {
	case "ss":
		node, err = parseSSNode(body)
	case "vmess":
		node, err = parseVMessNode(body)
	case "vless", "trojan", "hysteria2", "hy2", "http", "https", "socks5", "socks5h":
		node, err = parseStandardNode(strings.ToLower(scheme), body)
	default:
		return nil, fmt.Errorf("不支持的节点链接协议 %q", scheme)
	}
	if err != nil {
		return nil, err
	}
	if !nodeValuesSafe(node) {
		return nil, fmt.Errorf("节点字段包含控制字符或无效 UTF-8")
	}
	return node, nil
}

func nodeText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func nodeValuesSafe(v any) bool {
	switch value := v.(type) {
	case string:
		return nodeText(value)
	case map[string]any:
		for k, item := range value {
			if !nodeText(k) || !nodeValuesSafe(item) {
				return false
			}
		}
	case []string:
		for _, item := range value {
			if !nodeText(item) {
				return false
			}
		}
	case []any:
		for _, item := range value {
			if !nodeValuesSafe(item) {
				return false
			}
		}
	}
	return true
}

func decodeNode64(value string) (string, error) {
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if decoded, err := enc.Strict().DecodeString(value); err == nil && utf8.Valid(decoded) {
			return string(decoded), nil
		}
	}
	return "", fmt.Errorf("节点 Base64 数据无效")
}

func nodeUnescape(value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil || !nodeText(decoded) {
		return "", fmt.Errorf("节点字段编码无效")
	}
	return decoded, nil
}

func nodeName(fragment string) (string, error) {
	if fragment == "" {
		return "isp-node", nil
	}
	return nodeUnescape(fragment)
}

func nodePort(raw string, fallback int) (int, error) {
	if raw == "" && fallback > 0 {
		return fallback, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("节点端口必须为 1–65535")
	}
	return port, nil
}

func validNodeHost(host string) bool {
	if host == "" || len(host) > 253 || !nodeText(host) || strings.ContainsAny(host, " /\\?#@%") {
		return false
	}
	if strings.Contains(host, ":") {
		return net.ParseIP(host) != nil
	}
	for _, part := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Query decoding follows RFC 3986, preserving literal '+' rather than treating
// credentials as application/x-www-form-urlencoded data. Every field is decoded once.
func parseNodeQuery(raw string) (map[string]string, error) {
	out := map[string]string{}
	if raw == "" {
		return out, nil
	}
	for _, field := range strings.Split(raw, "&") {
		key, value, found := strings.Cut(field, "=")
		if !found || key == "" {
			return nil, fmt.Errorf("节点查询参数必须为 key=value")
		}
		var err error
		key, err = nodeUnescape(key)
		if err != nil {
			return nil, err
		}
		value, err = nodeUnescape(value)
		if err != nil {
			return nil, err
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("节点参数 %q 重复", key)
		}
		out[key] = value
	}
	return out, nil
}

func onlyNodeQuery(q map[string]string, keys ...string) error {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range q {
		if !allowed[key] {
			return fmt.Errorf("不支持的节点参数 %q", key)
		}
	}
	return nil
}

func parseSSNode(body string) (map[string]any, error) {
	body, fragment, _ := strings.Cut(body, "#")
	body, query, _ := strings.Cut(body, "?")
	name, err := nodeName(fragment)
	if err != nil {
		return nil, err
	}
	q, err := parseNodeQuery(query)
	if err != nil {
		return nil, err
	}
	if err := onlyNodeQuery(q, "plugin", "type"); err != nil {
		return nil, err
	}
	// Some exporters append a redundant TCP marker to SIP002 links. It does
	// not select a Mihomo network or change SS UDP support or SIP003 plugins.
	if transport, present := q["type"]; present && transport != "tcp" {
		return nil, fmt.Errorf("SS 节点链接仅支持 type=tcp；插件传输请使用 plugin 参数")
	}
	// Standard Base64 userinfo may contain '/', so split at '@' before looking
	// for the optional terminal slash. Legacy links encode the entire authority.
	body = strings.TrimSuffix(body, "/")
	legacy := false
	if !strings.Contains(body, "@") {
		body, err = nodeUnescape(body)
		if err != nil {
			return nil, err
		}
		body, err = decodeNode64(body)
		if err != nil {
			return nil, err
		}
		legacy = true
	}
	split := strings.LastIndexByte(body, '@')
	if split < 1 {
		return nil, fmt.Errorf("SS 节点缺少认证或地址")
	}
	auth, authority := body[:split], body[split+1:]
	host, portText, err := net.SplitHostPort(authority)
	if err != nil || !validNodeHost(host) {
		return nil, fmt.Errorf("SS 节点地址无效（IPv6 请使用方括号）")
	}
	port, err := nodePort(portText, 0)
	if err != nil {
		return nil, err
	}
	method, password, plain := strings.Cut(auth, ":")
	encodedAuth := !plain || legacy
	if plain && !legacy {
		method, err = nodeUnescape(method)
		if err != nil {
			return nil, err
		}
		password, err = nodeUnescape(password)
		if err != nil {
			return nil, err
		}
	} else if !plain {
		auth, err = nodeUnescape(auth)
		if err != nil {
			return nil, err
		}
		auth, err = decodeNode64(auth)
		if err != nil {
			return nil, err
		}
		method, password, plain = strings.Cut(auth, ":")
		if !plain {
			return nil, fmt.Errorf("SS Base64 认证缺少 cipher:password")
		}
		// Base64 already contains the literal credential, not another URI layer.
	}
	if method == "" || password == "" {
		return nil, fmt.Errorf("SS 节点缺少 cipher 或 password")
	}
	if strings.HasPrefix(method, "2022-") && encodedAuth {
		return nil, fmt.Errorf("SS2022 按 SIP002 必须使用明文、逐字段转义的 userinfo")
	}
	node := map[string]any{"name": name, "type": "ss", "server": host, "port": port, "cipher": method, "password": password, "udp": true}
	if plugin, present := q["plugin"]; present {
		if err := parseSSPlugin(node, plugin); err != nil {
			return nil, err
		}
	}
	return node, nil
}

// SIP003 separates plugin fields with semicolons, with backslash escaping.
func splitSSPlugin(value string) ([]string, error) {
	parts := []string{}
	var part strings.Builder
	escaped := false
	for _, r := range value {
		if escaped {
			if !strings.ContainsRune(":;=\\", r) {
				return nil, fmt.Errorf("SS plugin 转义无效")
			}
			part.WriteRune(r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case ';':
			parts = append(parts, part.String())
			part.Reset()
		default:
			part.WriteRune(r)
		}
	}
	if escaped {
		return nil, fmt.Errorf("SS plugin 转义不完整")
	}
	return append(parts, part.String()), nil
}

func parseSSPlugin(node map[string]any, value string) error {
	parts, err := splitSSPlugin(value)
	if err != nil {
		return err
	}
	opts := map[string]string{}
	for _, field := range parts[1:] {
		k, v, _ := strings.Cut(field, "=")
		if k == "" {
			return fmt.Errorf("SS plugin 参数为空")
		}
		if _, exists := opts[k]; exists {
			return fmt.Errorf("SS plugin 参数 %q 重复", k)
		}
		opts[k] = v
	}
	out := map[string]any{}
	switch parts[0] {
	case "obfs", "obfs-local", "simple-obfs":
		if err := onlyNodeQuery(opts, "obfs", "obfs-host"); err != nil {
			return err
		}
		mode := opts["obfs"]
		if mode != "http" && mode != "tls" {
			return fmt.Errorf("SS obfs 仅支持 http 或 tls")
		}
		node["plugin"] = "obfs"
		out["mode"] = mode
		if host := opts["obfs-host"]; host != "" {
			out["host"] = host
		}
	case "v2ray-plugin":
		if err := onlyNodeQuery(opts, "mode", "tls", "host", "path", "mux"); err != nil {
			return err
		}
		mode := opts["mode"]
		if mode != "" && mode != "websocket" {
			return fmt.Errorf("SS v2ray-plugin 仅支持 websocket")
		}
		node["plugin"] = "v2ray-plugin"
		out["mode"] = "websocket"
		for _, key := range []string{"host", "path"} {
			if v, ok := opts[key]; ok {
				out[key] = v
			}
		}
		for _, key := range []string{"tls", "mux"} {
			if v, ok := opts[key]; ok {
				if v == "" {
					out[key] = true
				} else {
					b, e := nodeBool(v)
					if e != nil {
						return e
					}
					out[key] = b
				}
			}
		}
	default:
		return fmt.Errorf("不支持的 SS plugin %q", parts[0])
	}
	node["plugin-opts"] = out
	return nil
}

func parseStandardNode(scheme, body string) (map[string]any, error) {
	u, err := url.Parse(scheme + "://" + body)
	if err != nil || u.Opaque != "" || !validNodeHost(u.Hostname()) || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("节点地址无效，路径参数请放在查询部分")
	}
	portDefault := 0
	switch scheme {
	case "http":
		portDefault = 80
	case "https", "hysteria2", "hy2":
		portDefault = 443
	}
	port, err := nodePort(u.Port(), portDefault)
	if err != nil {
		return nil, err
	}
	name, err := nodeName(u.EscapedFragment())
	if err != nil {
		return nil, err
	}
	q, err := parseNodeQuery(u.RawQuery)
	if err != nil {
		return nil, err
	}
	node := map[string]any{"name": name, "type": scheme, "server": u.Hostname(), "port": port}
	username, password := "", ""
	hasPassword := false
	if u.User != nil {
		username = u.User.Username()
		password, hasPassword = u.User.Password()
	}
	switch scheme {
	case "http", "https", "socks5", "socks5h":
		if err := onlyNodeQuery(q, "sni", "insecure", "skip-cert-verify"); err != nil {
			return nil, err
		}
		if scheme == "https" {
			node["type"] = "http"
			node["tls"] = true
		}
		if scheme == "socks5h" {
			node["type"] = "socks5"
		}
		if u.User != nil {
			if username == "" || !hasPassword || password == "" {
				return nil, fmt.Errorf("HTTP/SOCKS 认证须为 username:password")
			}
			if (scheme == "http" || scheme == "https") && strings.Contains(username, ":") {
				return nil, fmt.Errorf("HTTP Basic 用户名不能包含冒号")
			}
			if (scheme == "socks5" || scheme == "socks5h") && (len(username) > 255 || len(password) > 255) {
				return nil, fmt.Errorf("SOCKS5 用户名和密码不能超过 255 字节")
			}
			node["username"] = username
			node["password"] = password
		}
		if len(q) > 0 && scheme != "https" {
			return nil, fmt.Errorf("明文 HTTP/SOCKS 节点不支持 TLS 参数")
		}
		if sni, ok := q["sni"]; ok {
			if sni == "" {
				return nil, fmt.Errorf("sni 不能为空")
			}
			node["sni"] = sni
		}
		if err := nodeTLSBool(node, q); err != nil {
			return nil, err
		}
	case "hysteria2", "hy2":
		if err := onlyNodeQuery(q, "sni", "insecure", "obfs", "obfs-password", "pinSHA256", "alpn"); err != nil {
			return nil, err
		}
		node["type"] = "hysteria2"
		if hasPassword {
			username += ":" + password
		}
		node["password"] = username
		for _, key := range []string{"sni", "obfs", "obfs-password"} {
			if v, ok := q[key]; ok {
				if v == "" {
					return nil, fmt.Errorf("%s 不能为空", key)
				}
				node[key] = v
			}
		}
		if obfs, ok := q["obfs"]; ok {
			if obfs != "salamander" {
				return nil, fmt.Errorf("Hysteria2 URI 暂仅支持 salamander 混淆")
			}
			if q["obfs-password"] == "" {
				return nil, fmt.Errorf("Hysteria2 混淆缺少密码")
			}
		} else if _, ok := q["obfs-password"]; ok {
			return nil, fmt.Errorf("obfs-password 缺少 obfs")
		}
		if fingerprint, ok := q["pinSHA256"]; ok {
			v := strings.ReplaceAll(fingerprint, ":", "")
			decoded, e := hex.DecodeString(v)
			if e != nil || len(decoded) != 32 {
				return nil, fmt.Errorf("pinSHA256 必须是 SHA256 证书指纹")
			}
			node["fingerprint"] = fingerprint
		}
		if err := nodeTLSBool(node, q); err != nil {
			return nil, err
		}
		if err := nodeALPN(node, q); err != nil {
			return nil, err
		}
	case "vless", "trojan":
		if err := onlyNodeQuery(q, "security", "sni", "serverName", "alpn", "fp", "allowInsecure", "insecure", "skip-cert-verify", "type", "headerType", "host", "path", "serviceName", "mode", "pbk", "sid", "flow", "encryption", "packetEncoding"); err != nil {
			return nil, err
		}
		if username == "" || hasPassword {
			return nil, fmt.Errorf("VLESS/Trojan 认证必须为单个编码后的凭据")
		}
		node["udp"] = true
		security := q["security"]
		if _, exists := q["security"]; exists && security == "" {
			return nil, fmt.Errorf("security 不能为空")
		}
		if scheme == "trojan" {
			node["password"] = username
			if security != "" && security != "tls" {
				return nil, fmt.Errorf("Trojan 仅支持 TLS")
			}
			if err := onlyEmptyNodeFields(q, "pbk", "sid", "flow", "encryption", "packetEncoding"); err != nil {
				return nil, err
			}
			security = "tls"
		} else {
			if !nodeUUID(username) {
				return nil, fmt.Errorf("VLESS UUID 无效")
			}
			node["uuid"] = username
			if security != "" && security != "none" && security != "tls" && security != "reality" {
				return nil, fmt.Errorf("不支持的 VLESS security")
			}
			if v := q["encryption"]; v != "" && v != "none" {
				return nil, fmt.Errorf("VLESS 节点链接暂仅支持 encryption=none")
			}
			if v, exists := q["encryption"]; exists && v == "" {
				return nil, fmt.Errorf("VLESS encryption 不能为空")
			}
			if flow := q["flow"]; flow != "" {
				if flow != "xtls-rprx-vision" {
					return nil, fmt.Errorf("不支持的 VLESS flow")
				}
				if security != "tls" && security != "reality" {
					return nil, fmt.Errorf("VLESS flow 要求 TLS")
				}
				node["flow"] = flow
			}
			if packet, ok := q["packetEncoding"]; ok {
				if packet != "xudp" && packet != "packetaddr" {
					return nil, fmt.Errorf("不支持的 packetEncoding")
				}
				node["packet-encoding"] = packet
			}
		}
		tlsEnabled := security == "tls" || security == "reality"
		if scheme == "vless" {
			node["tls"] = tlsEnabled
		}
		if !tlsEnabled {
			if err := onlyEmptyNodeFields(q, "sni", "serverName", "alpn", "fp", "allowInsecure", "insecure", "skip-cert-verify", "pbk", "sid"); err != nil {
				return nil, err
			}
		} else {
			sni, err := nodeAlias(q, "sni", "serverName")
			if err != nil {
				return nil, err
			}
			if sni != "" {
				if scheme == "vless" {
					node["servername"] = sni
				} else {
					node["sni"] = sni
				}
			}
			if _, exists := q["sni"]; exists && sni == "" {
				return nil, fmt.Errorf("sni 不能为空")
			}
			if _, exists := q["serverName"]; exists && sni == "" {
				return nil, fmt.Errorf("serverName 不能为空")
			}
			fp := "chrome"
			if value, exists := q["fp"]; exists {
				if value == "" {
					return nil, fmt.Errorf("fp 不能为空")
				}
				fp = value
			}
			node["client-fingerprint"] = fp
			if err := nodeTLSBool(node, q); err != nil {
				return nil, err
			}
			if err := nodeALPN(node, q); err != nil {
				return nil, err
			}
		}
		if security == "reality" {
			pk, err := base64.RawURLEncoding.Strict().DecodeString(q["pbk"])
			if err != nil || len(pk) != 32 {
				return nil, fmt.Errorf("REALITY public key 无效")
			}
			sid := q["sid"]
			decoded, err := hex.DecodeString(sid)
			if err != nil || len(decoded) > 8 {
				return nil, fmt.Errorf("REALITY short id 无效")
			}
			node["reality-opts"] = map[string]any{"public-key": q["pbk"], "short-id": sid}
			if q["fp"] == "" {
				node["client-fingerprint"] = "chrome"
			}
		} else if err := onlyEmptyNodeFields(q, "pbk", "sid"); err != nil {
			return nil, err
		}
		if err := nodeTransport(node, q); err != nil {
			return nil, err
		}
	}
	return node, nil
}

func onlyEmptyNodeFields(q map[string]string, keys ...string) error {
	for _, key := range keys {
		if _, present := q[key]; present {
			return fmt.Errorf("当前协议/传输不支持参数 %q", key)
		}
	}
	return nil
}
func nodeBool(raw string) (bool, error) {
	switch raw {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("布尔节点参数必须为 0/1 或 true/false")
	}
}
func nodeAlias(q map[string]string, keys ...string) (string, error) {
	value := ""
	found := false
	for _, key := range keys {
		if v, ok := q[key]; ok {
			if found {
				return "", fmt.Errorf("同义节点参数重复")
			}
			found = true
			value = v
		}
	}
	return value, nil
}
func nodeTLSBool(node map[string]any, q map[string]string) error {
	raw, err := nodeAlias(q, "allowInsecure", "insecure", "skip-cert-verify")
	if err != nil {
		return err
	}
	if raw != "" {
		b, err := nodeBool(raw)
		if err != nil {
			return err
		}
		node["skip-cert-verify"] = b
	} else {
		for _, key := range []string{"allowInsecure", "insecure", "skip-cert-verify"} {
			if _, ok := q[key]; ok {
				return fmt.Errorf("TLS 布尔参数不能为空")
			}
		}
	}
	return nil
}
func nodeALPN(node map[string]any, q map[string]string) error {
	if raw, ok := q["alpn"]; ok {
		parts := strings.Split(raw, ",")
		for _, p := range parts {
			if p == "" {
				return fmt.Errorf("ALPN 项不能为空")
			}
		}
		node["alpn"] = parts
	}
	return nil
}
func nodeUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}

func nodeTransport(node map[string]any, q map[string]string) error {
	network := q["type"]
	if network == "" {
		if _, exists := q["type"]; exists {
			return fmt.Errorf("节点传输类型不能为空")
		}
		network = "tcp"
	}
	// Xray's share-link type=http denotes HTTP/2. HTTP camouflage over TCP
	// is the separate type=tcp&headerType=http representation.
	if network == "http" {
		network = "h2"
	}
	header := q["headerType"]
	if header != "" && header != "none" && !(network == "tcp" && header == "http") {
		return fmt.Errorf("不支持的 headerType")
	}
	if network == "tcp" && header == "http" {
		network = "http"
	}
	switch network {
	case "tcp":
		if err := onlyEmptyNodeFields(q, "host", "path", "serviceName", "mode"); err != nil {
			return err
		}
	case "ws":
		if err := onlyEmptyNodeFields(q, "serviceName", "mode"); err != nil {
			return err
		}
		opts := map[string]any{}
		if v, ok := q["path"]; ok {
			if v == "" {
				return fmt.Errorf("WebSocket path 不能为空")
			}
			path, earlyData, err := nodeWSPath(v)
			if err != nil {
				return err
			}
			opts["path"] = path
			for key, value := range earlyData {
				opts[key] = value
			}
		}
		if v, ok := q["host"]; ok {
			opts["headers"] = map[string]any{"Host": v}
		}
		node["ws-opts"] = opts
	case "grpc":
		if err := onlyEmptyNodeFields(q, "host", "path"); err != nil {
			return err
		}
		if mode, ok := q["mode"]; ok && mode != "gun" {
			return fmt.Errorf("gRPC 暂仅支持 mode=gun")
		}
		opts := map[string]any{}
		if v, ok := q["serviceName"]; ok {
			opts["grpc-service-name"] = v
		}
		node["grpc-opts"] = opts
	case "http", "h2":
		if node["type"] == "trojan" {
			return fmt.Errorf("Trojan 不支持此链接传输类型")
		}
		if err := onlyEmptyNodeFields(q, "serviceName", "mode"); err != nil {
			return err
		}
		opts := map[string]any{}
		if network == "http" {
			if path, ok := q["path"]; ok {
				opts["path"] = []string{path}
			}
			if host, ok := q["host"]; ok {
				opts["headers"] = map[string]any{"Host": strings.Split(host, ",")}
			}
			node["http-opts"] = opts
		} else {
			if node["tls"] != true {
				return fmt.Errorf("h2 节点需要 TLS")
			}
			if path, ok := q["path"]; ok {
				opts["path"] = path
			}
			if host, ok := q["host"]; ok {
				opts["host"] = strings.Split(host, ",")
			}
			node["h2-opts"] = opts
		}
	default:
		return fmt.Errorf("不支持的节点传输类型 %q", network)
	}
	if network != "tcp" {
		node["network"] = network
	}
	return nil
}

// V2RayN embeds WS early-data options inside its path query. Keep unrelated
// query bytes unchanged so nested escapes and literal '+' retain their meaning.
func nodeWSPath(path string) (string, map[string]any, error) {
	base, query, found := strings.Cut(path, "?")
	opts := map[string]any{}
	if !found {
		return path, opts, nil
	}
	kept := []string{}
	seen := map[string]bool{}
	for _, field := range strings.Split(query, "&") {
		key, value, _ := strings.Cut(field, "=")
		key, err := nodeUnescape(key)
		if err != nil {
			return "", nil, err
		}
		if key != "ed" && key != "eh" {
			kept = append(kept, field)
			continue
		}
		if seen[key] {
			return "", nil, fmt.Errorf("WebSocket early-data 参数重复")
		}
		seen[key] = true
		value, err = nodeUnescape(value)
		if err != nil {
			return "", nil, err
		}
		if key == "ed" {
			size, err := strconv.Atoi(value)
			if err != nil || size < 0 || size > 1<<20 {
				return "", nil, fmt.Errorf("WebSocket early-data 大小必须为 0 至 1048576")
			}
			opts["max-early-data"] = size
		} else {
			if value == "" || strings.ContainsAny(value, " :\t") {
				return "", nil, fmt.Errorf("WebSocket early-data header 无效")
			}
			opts["early-data-header-name"] = value
		}
	}
	if seen["eh"] && !seen["ed"] {
		return "", nil, fmt.Errorf("WebSocket early-data header 缺少 ed")
	}
	if seen["ed"] && !seen["eh"] {
		opts["early-data-header-name"] = "Sec-WebSocket-Protocol"
	}
	if len(kept) > 0 {
		base += "?" + strings.Join(kept, "&")
	}
	return base, opts, nil
}

func parseVMessNode(body string) (map[string]any, error) {
	body, fragment, _ := strings.Cut(body, "#")
	if strings.Contains(body, "?") {
		return nil, fmt.Errorf("VMess JSON 链接不接受额外查询参数")
	}
	payload, err := nodeUnescape(body)
	if err != nil {
		return nil, err
	}
	payload, err = decodeNode64(payload)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewBufferString(payload))
	dec.UseNumber()
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("VMess 内容必须是 JSON 对象")
	}
	values := map[string]string{}
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("VMess JSON 无效")
		}
		key := keyToken.(string)
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("VMess 字段 %q 重复", key)
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("VMess JSON 无效")
		}
		switch value := v.(type) {
		case string:
			values[key] = value
		case json.Number:
			values[key] = string(value)
		case bool:
			values[key] = strconv.FormatBool(value)
		default:
			return nil, fmt.Errorf("VMess 字段 %q 必须是标量", key)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("VMess JSON 无效")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("VMess JSON 包含多余数据")
	}
	if err := onlyNodeQuery(values, "v", "ps", "add", "port", "id", "aid", "scy", "net", "type", "host", "path", "tls", "sni", "alpn", "fp", "allowInsecure", "packetEncoding"); err != nil {
		return nil, err
	}
	// V2RayN routinely includes unused fields as empty strings. They carry no
	// TLS or transport setting; retain strict rejection for nonempty values.
	for _, key := range []string{"sni", "alpn", "fp", "allowInsecure", "packetEncoding"} {
		if value, exists := values[key]; exists && value == "" {
			delete(values, key)
		}
	}
	if version := values["v"]; version != "" && version != "2" {
		return nil, fmt.Errorf("VMess JSON 仅支持版本 2")
	}
	if !validNodeHost(values["add"]) || !nodeUUID(values["id"]) {
		return nil, fmt.Errorf("VMess 地址或 UUID 无效")
	}
	port, err := nodePort(values["port"], 0)
	if err != nil {
		return nil, err
	}
	name := values["ps"]
	if fragment != "" {
		name, err = nodeName(fragment)
		if err != nil {
			return nil, err
		}
	}
	if name == "" {
		name = "isp-node"
	}
	aid := 0
	if values["aid"] != "" {
		aid, err = strconv.Atoi(values["aid"])
		if err != nil || aid < 0 || aid > 65535 {
			return nil, fmt.Errorf("VMess alterId 无效")
		}
	}
	cipher := values["scy"]
	if cipher == "" {
		cipher = "auto"
	}
	switch cipher {
	case "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305":
	default:
		return nil, fmt.Errorf("不支持的 VMess cipher")
	}
	node := map[string]any{"name": name, "type": "vmess", "server": values["add"], "port": port, "uuid": values["id"], "alterId": aid, "cipher": cipher, "udp": true}
	tls := values["tls"]
	if tls != "" && tls != "none" && tls != "tls" {
		return nil, fmt.Errorf("不支持的 VMess TLS 设置")
	}
	node["tls"] = tls == "tls"
	if tls != "tls" {
		if err := onlyEmptyNodeFields(values, "sni", "alpn", "fp", "allowInsecure"); err != nil {
			return nil, err
		}
	} else {
		if v := values["sni"]; v != "" {
			node["servername"] = v
		}
		if v := values["fp"]; v != "" {
			node["client-fingerprint"] = v
		}
		if err := nodeTLSBool(node, values); err != nil {
			return nil, err
		}
		if err := nodeALPN(node, values); err != nil {
			return nil, err
		}
	}
	if packet, ok := values["packetEncoding"]; ok {
		if packet != "xudp" && packet != "packetaddr" {
			return nil, fmt.Errorf("不支持的 packetEncoding")
		}
		node["packet-encoding"] = packet
	}
	q := map[string]string{}
	if values["net"] != "" {
		q["type"] = values["net"]
	}
	// V2RayN's JSON "http" denotes HTTP/2; TCP HTTP camouflage is
	// represented by net=tcp,type=http instead.
	if values["net"] == "http" {
		q["type"] = "h2"
	}
	if header := values["type"]; header != "" {
		q["headerType"] = header
	}
	for _, key := range []string{"host", "path"} {
		if v := values[key]; v != "" {
			q[key] = v
		}
	}
	if values["net"] == "grpc" {
		if values["type"] == "gun" {
			delete(q, "headerType")
			q["mode"] = "gun"
		}
		if path, ok := q["path"]; ok {
			q["serviceName"] = path
			delete(q, "path")
		}
	}
	if err := nodeTransport(node, q); err != nil {
		return nil, err
	}
	return node, nil
}
