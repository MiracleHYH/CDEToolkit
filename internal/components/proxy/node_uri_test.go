package proxy

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseNodeURLSSCredentials(t *testing.T) {
	for _, tc := range []struct{ name, uri, password string }{
		{"plain-percent", "ss://aes-128-gcm:pass%2Dword%40x%3Ay%252Dz+q@192.0.2.1:8388#ISP%2D1", "pass-word@x:y%2Dz+q"},
		{"userinfo-raw-url", "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:p@ss:word+%2D")) + "@192.0.2.1:8388", "p@ss:word+%2D"},
		{"userinfo-padded-url", "ss://" + base64.URLEncoding.EncodeToString([]byte("aes-128-gcm:pass")) + "@192.0.2.1:8388", "pass"},
		{"userinfo-standard", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:密码")) + "@192.0.2.1:8388", "密码"},
		{"userinfo-standard-slash", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:俿")) + "@192.0.2.1:8388", "俿"},
		{"userinfo-raw-standard", "ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:密码")) + "@192.0.2.1:8388", "密码"},
		{"legacy", "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:p@ss:%2D+word@192.0.2.1:8388")), "p@ss:%2D+word"},
		{"ipv6", "ss://aes-128-gcm:p%40ss@[2001:db8::1]:8388/", "p@ss"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := ParseNodeURL(tc.uri)
			if err != nil {
				t.Fatal(err)
			}
			if node["password"] != tc.password || node["cipher"] != "aes-128-gcm" || node["type"] != "ss" || node["port"] != 8388 {
				t.Fatalf("unexpected node: %#v", node)
			}
			expectedName := "isp-node"
			if tc.name == "plain-percent" {
				expectedName = "ISP-1"
			}
			if node["name"] != expectedName {
				t.Fatalf("name=%q", node["name"])
			}
		})
	}
}

func TestParseNodeURLSSPlugins(t *testing.T) {
	cases := []struct {
		name, query string
		plugin      string
		opts        map[string]any
	}{
		{"obfs", "obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.test", "obfs", map[string]any{"mode": "http", "host": "example.test"}},
		{"simple-obfs", "simple-obfs%3Bobfs%3Dtls", "obfs", map[string]any{"mode": "tls"}},
		{"v2ray", "v2ray-plugin%3Btls%3Bhost%3Dexample.test%3Bpath%3D%2Fws%3Bmux%3Dfalse", "v2ray-plugin", map[string]any{"mode": "websocket", "tls": true, "host": "example.test", "path": "/ws", "mux": false}},
		{"escaping", "v2ray-plugin%3Bpath%3D%2Fa%5C%3Bb%5C%3Dc%5C%5Cd", "v2ray-plugin", map[string]any{"mode": "websocket", "path": "/a;b=c\\d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node, err := ParseNodeURL("ss://aes-128-gcm:pass@example.test:443/?plugin=" + tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if node["plugin"] != tc.plugin || !reflect.DeepEqual(node["plugin-opts"], tc.opts) {
				t.Fatalf("plugin=%#v", node)
			}
		})
	}
}

func TestParseNodeURLSS2022Userinfo(t *testing.T) {
	key := "+/0123456789abcdefghijklmnopqrstuv0123456789="
	encoded := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(key)
	node, err := ParseNodeURL("ss://2022-blake3-aes-256-gcm:" + encoded + ":" + encoded + "@192.0.2.1:443")
	if err != nil || node["password"] != key+":"+key {
		t.Fatalf("SS2022 credential changed: %#v, %v", node, err)
	}
	for _, raw := range []string{
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("2022-blake3-aes-256-gcm:"+key)) + "@192.0.2.1:443",
		"ss://" + base64.RawStdEncoding.EncodeToString([]byte("2022-blake3-aes-256-gcm:"+key+"@192.0.2.1:443")),
	} {
		if _, err := ParseNodeURL(raw); err == nil {
			t.Fatal("SIP002 forbids Base64 userinfo for SS2022")
		}
	}
}

func TestParseNodeURLWSEarlyData(t *testing.T) {
	for _, tc := range []struct {
		path string
		opts map[string]any
	}{
		{"/ws?token=a+b%252D&ed=2048", map[string]any{"path": "/ws?token=a+b%252D", "max-early-data": 2048, "early-data-header-name": "Sec-WebSocket-Protocol"}},
		{"/ws?ed=4096&eh=X-Early-Data", map[string]any{"path": "/ws", "max-early-data": 4096, "early-data-header-name": "X-Early-Data"}},
	} {
		node, err := ParseNodeURL(vmessNodeURI(t, map[string]any{"add": "example.test", "port": 443, "id": "12345678-1234-1234-1234-123456789abc", "net": "ws", "path": tc.path}))
		if err != nil || !reflect.DeepEqual(node["ws-opts"], tc.opts) {
			t.Fatalf("early-data mismatch: %#v, %v", node, err)
		}
	}
	for _, path := range []string{"/ws?ed=bad", "/ws?ed=-1", "/ws?ed=1048577", "/ws?ed=4&ed=5", "/ws?eh=X-Header", "/ws?ed=4&eh=bad%3Aheader"} {
		if _, err := ParseNodeURL(vmessNodeURI(t, map[string]any{"add": "example.test", "port": 443, "id": "12345678-1234-1234-1234-123456789abc", "net": "ws", "path": path})); err == nil {
			t.Fatalf("accepted invalid early-data %q", path)
		}
	}
}

func TestParseNodeURLStandard(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, tc := range []struct {
		name, uri string
		expected  map[string]any
	}{
		{"http", "http://user%40name:p%3Aa+%252D@192.0.2.1:8080#plain", map[string]any{"type": "http", "server": "192.0.2.1", "port": 8080, "username": "user@name", "password": "p:a+%2D", "name": "plain"}},
		{"https", "https://user:pass@example.test?sni=tls.example.test&insecure=0", map[string]any{"type": "http", "port": 443, "tls": true, "sni": "tls.example.test", "skip-cert-verify": false}},
		{"http-anonymous", "http://example.test", map[string]any{"type": "http", "port": 80}},
		{"socks5", "socks5://user:pass@[2001:db8::1]:1080", map[string]any{"type": "socks5", "server": "2001:db8::1", "port": 1080}},
		{"socks5h", "socks5h://192.0.2.1:1080", map[string]any{"type": "socks5"}},
		{"trojan-ws", "trojan://pass%40%3A%2D+@example.test:443?type=ws&security=tls&sni=tls.example.test&host=ws.example.test&path=%2Fv%252D1&alpn=h2%2Chttp%2F1.1&allowInsecure=false", map[string]any{"type": "trojan", "password": "pass@:-+", "sni": "tls.example.test", "network": "ws", "ws-opts": map[string]any{"path": "/v%2D1", "headers": map[string]any{"Host": "ws.example.test"}}, "alpn": []string{"h2", "http/1.1"}, "skip-cert-verify": false}},
		{"vless-tcp", "vless://" + uuid + "@example.test:443?encryption=none&security=none&type=tcp", map[string]any{"type": "vless", "uuid": uuid, "tls": false}},
		{"vless-grpc", "vless://" + uuid + "@example.test:443?security=tls&type=grpc&serviceName=svc%2Dname&mode=gun&sni=tls.example.test&fp=chrome", map[string]any{"type": "vless", "tls": true, "servername": "tls.example.test", "network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "svc-name"}, "client-fingerprint": "chrome"}},
		{"vless-reality", "vless://" + uuid + "@example.test:443?security=reality&pbk=" + publicKey + "&sid=0123abcd&flow=xtls-rprx-vision&packetEncoding=xudp", map[string]any{"tls": true, "reality-opts": map[string]any{"public-key": publicKey, "short-id": "0123abcd"}, "client-fingerprint": "chrome", "flow": "xtls-rprx-vision", "packet-encoding": "xudp"}},
		{"vless-http", "vless://" + uuid + "@example.test:80?type=tcp&headerType=http&host=a.test,b.test&path=%2Fa", map[string]any{"network": "http", "http-opts": map[string]any{"path": []string{"/a"}, "headers": map[string]any{"Host": []string{"a.test", "b.test"}}}}},
		{"vless-http2", "vless://" + uuid + "@example.test:443?security=tls&type=http&host=h2.example.test&path=%2Fh2", map[string]any{"network": "h2", "h2-opts": map[string]any{"path": "/h2", "host": []string{"h2.example.test"}}, "client-fingerprint": "chrome"}},
		{"hy2", "hy2://user:pass%40%3A%2D+@example.test/?obfs=salamander&obfs-password=obfs%2Dpass+&sni=tls.example.test&insecure=1&alpn=h3", map[string]any{"type": "hysteria2", "port": 443, "password": "user:pass@:-+", "obfs": "salamander", "obfs-password": "obfs-pass+", "sni": "tls.example.test", "skip-cert-verify": true, "alpn": []string{"h3"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := ParseNodeURL(tc.uri)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.expected {
				if !reflect.DeepEqual(node[k], v) {
					t.Fatalf("%s=%#v want %#v, node %#v", k, node[k], v, node)
				}
			}
		})
	}
}

func vmessNodeURI(t *testing.T, values map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return "vmess://" + base64.RawStdEncoding.EncodeToString(raw)
}
func TestParseNodeURLVMess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]any
		want   map[string]any
	}{
		{"websocket", map[string]any{"v": "2", "ps": "ISP%2D+", "add": "example.test", "port": "443", "id": "12345678-1234-1234-1234-123456789abc", "aid": "0", "net": "ws", "type": "none", "host": "ws.example.test", "path": "/ws%2D+", "tls": "tls", "sni": "tls.example.test", "alpn": "h2,http/1.1", "fp": "chrome", "allowInsecure": false}, map[string]any{"name": "ISP%2D+", "type": "vmess", "tls": true, "alterId": 0, "cipher": "auto", "servername": "tls.example.test", "ws-opts": map[string]any{"path": "/ws%2D+", "headers": map[string]any{"Host": "ws.example.test"}}}},
		{"http2", map[string]any{"add": "example.test", "port": 443, "id": "12345678-1234-1234-1234-123456789abc", "net": "http", "type": "none", "host": "h2.example.test", "path": "/h2", "tls": "tls"}, map[string]any{"network": "h2", "h2-opts": map[string]any{"path": "/h2", "host": []string{"h2.example.test"}}}},
		{"grpc", map[string]any{"add": "example.test", "port": 443, "id": "12345678-1234-1234-1234-123456789abc", "net": "grpc", "type": "gun", "path": "service", "tls": "tls"}, map[string]any{"network": "grpc", "grpc-opts": map[string]any{"grpc-service-name": "service"}}},
		{"unused-empty-fields", map[string]any{"add": "example.test", "port": 443, "id": "12345678-1234-1234-1234-123456789abc", "sni": "", "alpn": "", "fp": "", "allowInsecure": "", "packetEncoding": ""}, map[string]any{"tls": false, "cipher": "auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := ParseNodeURL(vmessNodeURI(t, tc.values))
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.want {
				if !reflect.DeepEqual(node[k], v) {
					t.Fatalf("%s=%#v want %#v", k, node[k], v)
				}
			}
		})
	}
}

func TestParseNodeURLRejectsUnsupportedOrAmbiguous(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	vmess := func(payload string) string { return "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload)) }
	for _, raw := range []string{
		"", strings.Repeat("x", maxNodeURL+1), "not a uri", "ssr://anything", "SS://aes-128-gcm:pass@example.test:0",
		"ss://aes-128-gcm:pass@example.test:70000", "ss://aes-128-gcm:pass@example.test:443/extra",
		"ss://aes-128-gcm:pass@example.test:443?uot=1", "ss://aes-128-gcm:pass@example.test:443?plugin=unknown",
		"ss://aes-128-gcm:pass@example.test:443?plugin=obfs-local%3Bobfs%3Dhttp%3Bunknown%3Dyes",
		"ss://aes-128-gcm:pass@example.test:443?plugin=v2ray-plugin%3Bmode%3Dquic",
		"ss://aes-128-gcm:pass@example.test:443?plugin=v2ray-plugin%3Bserver",
		"ss://aes-128-gcm:pass@example.test:443?plugin=v2ray-plugin%3Btls%3Btls",
		"ss://aes-128-gcm:pass@example.test:443?plugin=v2ray-plugin%3Bpath%3D%5Cq",
		"ss://aes-128-gcm:pass@example.test:443?plugin=v2ray-plugin%3Bpath%3D%5C",
		"ss://aes-128-gcm:pass@example.test:443?plugin=obfs-local%3Bobfs%3Dhttp&plugin=obfs-local%3Bobfs%3Dtls",
		"http://user@example.test:80", "http://user:@example.test:80", "socks5://user:pass@example.test",
		"http://user%3Aother:pass@example.test:80", "socks5://user:" + strings.Repeat("p", 256) + "@example.test:1080",
		"http://example.test:80?insecure=1", "https://example.test/path", "https://example.test?sni=",
		"https://example.test?insecure=1&skip-cert-verify=0", "https://example.test?insecure=", "https://example.test?insecure=yes",
		"trojan://user:pass@example.test:443", "trojan://pass@example.test:443?security=none",
		"trojan://pass@example.test:443?pbk=foo", "trojan://pass@example.test:443?type=xhttp",
		"vless://bad-id@example.test:443", "vless://" + uuid + "@example.test:443?security=unknown",
		"vless://" + uuid + "@example.test:443?security=", "vless://" + uuid + "@example.test:443?encryption=",
		"vless://" + uuid + "@example.test:443?security=none&sni=example.test",
		"vless://" + uuid + "@example.test:443?security=reality&pbk=bad&sid=ff",
		"vless://" + uuid + "@example.test:443?type=tcp&path=%2Fws",
		"vless://" + uuid + "@example.test:443?type=ws&serviceName=svc",
		"vless://" + uuid + "@example.test:443?type=grpc&mode=multi",
		"vless://" + uuid + "@example.test:443?type=h2",
		"hy2://password@example.test:443?obfs=unknown", "hy2://password@example.test:443?obfs=salamander",
		"hy2://password@example.test:443?obfs-password=pass", "hy2://password@example.test:443?pinSHA256=bad",
		"hy2://password@example.test:443?ech=unsupported", "hy2://password@example.test:443,8443",
		"socks5://192.0.2.1:1080\n", "http://u:p%0A@example.test", "http://u:p@example.test#%7F", "http://u:p@example.test?unknown=%00",
		"ss://aes-128-gcm:p%0A@example.test:443", "ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:pass\n")) + "@example.test:443",
		"http://u:p@example.test#%ZZ", "http://u:p@example.test?a=b&a=c", "http://u:p@example.test?insecure",
		vmess(`{"add":"example.test","port":443,"id":"` + uuid + `","unknown":"x"}`),
		vmess(`{"add":"example.test","add":"other.test","port":443,"id":"` + uuid + `"}`),
		vmess(`{"add":"example.test","port":443,"id":"` + uuid + `","net":[]}`),
		vmess(`{"add":"example.test","port":443,"id":"` + uuid + `"} {}`),
	} {
		t.Run(raw, func(t *testing.T) {
			if node, err := ParseNodeURL(raw); err == nil {
				t.Fatalf("unexpected acceptance: %#v", node)
			}
		})
	}
}

func FuzzParseNodeURL(f *testing.F) {
	for _, s := range []string{"ss://aes-128-gcm:p%2D@192.0.2.1:8388", "http://user:pass@example.test:8080", "hy2://pass@example.test:443", "vless://12345678-1234-1234-1234-123456789abc@example.test:443"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		node, err := ParseNodeURL(raw)
		if err == nil {
			if node == nil || !nodeValuesSafe(node) {
				t.Fatalf("unsafe parsed node")
			}
			if _, err := json.Marshal(node); err != nil {
				t.Fatal(err)
			}
		}
	})
}
