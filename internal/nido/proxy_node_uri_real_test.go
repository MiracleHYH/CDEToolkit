package nido

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"go.yaml.in/yaml/v3"
)

// realQuickStartNodeMatrix checks configuration acceptance, not successful
// protocol handshakes. It runs the explicitly supplied core only with -v/-t;
// the main quick-start E2E separately proves actual SS/relay traffic.
func realQuickStartNodeMatrix(t *testing.T, artifact, providerURL, healthURL string) {
	t.Helper()
	if !filepath.IsAbs(artifact) {
		t.Fatal("节点配置矩阵需要已核验核心的绝对路径")
	}
	for _, raw := range []string{providerURL, healthURL} {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			t.Fatalf("节点配置矩阵只允许字面回环 HTTP(S) provider/测速地址: %q", raw)
		}
	}
	created := t.TempDir()
	scratch, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	home, tmp := filepath.Join(scratch, "home"), filepath.Join(scratch, "tmp")
	configHome, cacheHome, dataHome := filepath.Join(home, ".config"), filepath.Join(home, ".cache"), filepath.Join(home, ".local", "share")
	for _, dir := range []string{home, tmp, configHome, cacheHome, dataHome} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	service := &proxy.Service{
		Root: filepath.Join(home, "nido"),
		Env: []string{
			"HOME=" + home, "TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp,
			"XDG_CONFIG_HOME=" + configHome, "XDG_CACHE_HOME=" + cacheHome, "XDG_DATA_HOME=" + dataHome,
		},
	}
	core, err := readFile(artifact, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(core)
	if err := service.Install(artifact, hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("节点配置矩阵安装临时核心（仅 -v）: %v", err)
	}
	// Keep these ports reserved: -t must parse rather than bind or start them.
	entryPort, _ := realProxyPort(t)
	ispPort, _ := realProxyPort(t)
	nodePort, _ := realProxyPort(t)
	authority := net.JoinHostPort("127.0.0.1", strconv.Itoa(nodePort))
	uuid := "12345678-1234-1234-1234-123456789abc"
	ss2022Key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32))
	ss2022Escaped := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(ss2022Key)
	publicKeyBytes := make([]byte, 32)
	publicKeyBytes[0] = 9 // X25519 base point; a valid, non-secret public key.
	publicKey := base64.RawURLEncoding.EncodeToString(publicKeyBytes)
	fingerprint := sha256.Sum256([]byte("nido temporary node configuration fixture"))
	vmess := func(values map[string]any) string {
		values["v"], values["add"], values["port"], values["id"], values["aid"] = "2", "127.0.0.1", nodePort, uuid, "0"
		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return "vmess://" + base64.RawStdEncoding.EncodeToString(encoded)
	}
	cases := []struct {
		name, uri string
		want      map[string]any
	}{
		{"ss-plain-percent", "ss://aes-128-gcm:pass%2Dword%40x%3Ay%252Dz+q@" + authority + "#ISP%2Dplain", map[string]any{"type": "ss", "cipher": "aes-128-gcm", "password": "pass-word@x:y%2Dz+q", "name": "ISP-plain"}},
		{"ss-base64-userinfo", "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:base64+%2D:pass")) + "@" + authority, map[string]any{"type": "ss", "cipher": "aes-128-gcm", "password": "base64+%2D:pass"}},
		{"ss-legacy-base64", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:legacy+%2D:pass@"+authority)), map[string]any{"type": "ss", "cipher": "aes-256-gcm", "password": "legacy+%2D:pass"}},
		{"ss-2022", "ss://2022-blake3-aes-256-gcm:" + ss2022Escaped + "@" + authority, map[string]any{"type": "ss", "cipher": "2022-blake3-aes-256-gcm", "password": ss2022Key}},
		{"ss-obfs", "ss://aes-128-gcm:fixture@" + authority + "?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3D127.0.0.1", map[string]any{"type": "ss", "plugin": "obfs", "plugin-opts": map[string]any{"mode": "http", "host": "127.0.0.1"}}},
		{"ss-v2ray-plugin", "ss://aes-128-gcm:fixture@" + authority + "?plugin=v2ray-plugin%3Btls%3Bhost%3D127.0.0.1%3Bpath%3D%2Fmatrix%252Dws%3Bmux%3Dfalse", map[string]any{"type": "ss", "plugin": "v2ray-plugin", "plugin-opts": map[string]any{"mode": "websocket", "tls": true, "host": "127.0.0.1", "path": "/matrix%2Dws", "mux": false}}},
		{"vmess-tcp", vmess(map[string]any{"net": "tcp", "scy": "auto", "ps": "ISP%2D+"}), map[string]any{"type": "vmess", "cipher": "auto", "uuid": uuid, "name": "ISP%2D+", "tls": false}},
		{"vmess-ws-tls-early-data", vmess(map[string]any{"net": "ws", "tls": "tls", "sni": "127.0.0.1", "host": "127.0.0.1", "path": "/matrix%2Dws?token=a+b%252D&ed=2048", "fp": "chrome", "allowInsecure": true}), map[string]any{"type": "vmess", "tls": true, "servername": "127.0.0.1", "network": "ws", "ws-opts": map[string]any{"path": "/matrix%2Dws?token=a+b%252D", "max-early-data": 2048, "early-data-header-name": "Sec-WebSocket-Protocol", "headers": map[string]any{"Host": "127.0.0.1"}}}},
		{"vless-tls-ws", "vless://" + uuid + "@" + authority + "?security=tls&encryption=none&type=ws&sni=127.0.0.1&host=127.0.0.1&path=%2Fmatrix%2Dws%252D+&insecure=1", map[string]any{"type": "vless", "tls": true, "servername": "127.0.0.1", "network": "ws", "ws-opts": map[string]any{"path": "/matrix-ws%2D+", "headers": map[string]any{"Host": "127.0.0.1"}}}},
		{"vless-tls-grpc", "vless://" + uuid + "@" + authority + "?security=tls&type=grpc&serviceName=matrix%2Dgrpc%252D+&mode=gun&sni=127.0.0.1&fp=chrome&packetEncoding=xudp", map[string]any{"type": "vless", "tls": true, "network": "grpc", "packet-encoding": "xudp", "grpc-opts": map[string]any{"grpc-service-name": "matrix-grpc%2D+"}}},
		{"vless-reality-tcp", "vless://" + uuid + "@" + authority + "?security=reality&type=tcp&sni=127.0.0.1&pbk=" + publicKey + "&sid=0123abcd&flow=xtls-rprx-vision&fp=chrome", map[string]any{"type": "vless", "tls": true, "flow": "xtls-rprx-vision", "reality-opts": map[string]any{"public-key": publicKey, "short-id": "0123abcd"}}},
		{"trojan-ws-tls", "trojan://pass%40%3A%2D+%252D@" + authority + "?security=tls&type=ws&sni=127.0.0.1&host=127.0.0.1&path=%2Fmatrix%2Dws&allowInsecure=1", map[string]any{"type": "trojan", "password": "pass@:-+%2D", "sni": "127.0.0.1", "network": "ws", "skip-cert-verify": true}},
		{"hy2-salamander-pin", "hy2://matrix%2Dpass%252D+@" + authority + "?sni=127.0.0.1&obfs=salamander&obfs-password=obfs%2Dpass%252D+&insecure=1&alpn=h3&pinSHA256=" + hex.EncodeToString(fingerprint[:]), map[string]any{"type": "hysteria2", "password": "matrix-pass%2D+", "obfs": "salamander", "obfs-password": "obfs-pass%2D+", "alpn": []string{"h3"}, "fingerprint": hex.EncodeToString(fingerprint[:])}},
		{"http-auth", "http://matrix%2Duser:pass%40%3A%2D+%252D@" + authority, map[string]any{"type": "http", "username": "matrix-user", "password": "pass@:-+%2D"}},
		{"https-auth-tls", "https://matrix%2Duser:pass%2Dword@" + authority + "?sni=127.0.0.1&skip-cert-verify=1", map[string]any{"type": "http", "tls": true, "sni": "127.0.0.1", "username": "matrix-user", "password": "pass-word", "skip-cert-verify": true}},
		{"socks5-auth", "socks5://matrix%2Duser:pass%40%3A%2D+%252D@" + authority, map[string]any{"type": "socks5", "username": "matrix-user", "password": "pass@:-+%2D"}},
		{"socks5h-anonymous", "socks5h://" + authority, map[string]any{"type": "socks5"}},
	}
	for _, tc := range cases {
		t.Run("nodes/"+tc.name, func(t *testing.T) {
			node, err := proxy.ParseNodeURL(tc.uri)
			if err != nil {
				t.Fatalf("分享链接解析失败: %v", err)
			}
			if node["server"] != "127.0.0.1" || node["port"] != nodePort {
				t.Fatalf("节点配置矩阵出现非回环目标: %#v", node)
			}
			for field, want := range tc.want {
				if !reflect.DeepEqual(node[field], want) {
					t.Fatalf("节点字段 %s = %#v，期望 %#v", field, node[field], want)
				}
			}
			generated, err := proxy.QuickStart(proxy.QuickStartOptions{
				ProviderURL: providerURL, ProxyFilter: ".*", ProxyPort: entryPort,
				ISP: &proxy.QuickStartISP{NodeURL: tc.uri, Port: ispPort, TestURL: healthURL},
			})
			if err != nil {
				t.Fatal(err)
			}
			generated = realQuickStartNodeMatrixHealth(t, generated, healthURL)
			if err := service.ConfigureBytes(generated); err != nil {
				t.Fatalf("真实 Mihomo -t 不接受 %s 的 QuickStart 配置: %v", tc.name, err)
			}
			t.Logf("真实 Mihomo -t 配置接受: %s；未启动、未声称该协议转发通过", tc.name)
		})
	}
	if status := service.Status(); status.Runtime.State != "stopped" || status.Runtime.Listeners {
		t.Fatalf("节点配置矩阵不得启动核心: %+v", status)
	}
}

// The matrix deliberately keeps automatic health checking and nonzero URLTest
// intervals intact, replacing only test destinations with the local fixture.
func realQuickStartNodeMatrixHealth(t *testing.T, data []byte, healthURL string) []byte {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	providers := document["proxy-providers"].(map[string]any)
	for _, raw := range providers {
		health := raw.(map[string]any)["health-check"].(map[string]any)
		if health["enable"] != true || health["interval"] != 300 {
			t.Fatalf("节点矩阵生成的 provider 健康检查默认值改变: %#v", health)
		}
		health["url"] = healthURL
	}
	for _, raw := range document["proxy-groups"].([]any) {
		group := raw.(map[string]any)
		if group["type"] != "url-test" || group["interval"] != 300 {
			t.Fatalf("节点矩阵必须保留真实 url-test 配置: %#v", group)
		}
		group["url"] = healthURL
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		t.Fatal(fmt.Errorf("重新编码回环测速配置: %w", err))
	}
	return encoded
}
