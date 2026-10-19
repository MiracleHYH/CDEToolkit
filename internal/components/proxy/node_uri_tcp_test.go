package proxy

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func syntheticSS2022TCPLink() (string, string) {
	serverKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32))
	userKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32))
	escape := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D")
	return "ss://2022-blake3-aes-256-gcm:" + escape.Replace(serverKey) + ":" + escape.Replace(userKey) + "@192.0.2.9:443?type=tcp#synthetic-isp", serverKey + ":" + userKey
}

func TestQuickStartSS2022ExplicitTCPPreservesKeys(t *testing.T) {
	link, password := syntheticSS2022TCPLink()
	data, err := QuickStart(QuickStartOptions{
		ProviderURL: "https://provider.invalid/sub", ProxyFilter: ".*", ProxyPort: 17890,
		ISP: &QuickStartISP{NodeURL: link, Port: 17892, RelayFilter: "relay"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var document quickStartTestDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Proxies) != 1 {
		t.Fatal("expected one ISP node")
	}
	node := document.Proxies[0]
	if node["name"] != "isp-node" || node["type"] != "ss" || node["cipher"] != "2022-blake3-aes-256-gcm" || node["password"] != password || node["udp"] != true || node["dialer-proxy"] != "isp-relay" {
		t.Fatal("SS2022 TCP marker changed native credentials or routing")
	}
	if _, exists := node["network"]; exists {
		t.Fatal("redundant TCP marker must not introduce a native transport option")
	}
}

func TestParseNodeURLSSTCPMarkerPreservesNativeNode(t *testing.T) {
	for _, original := range []string{
		"ss://aes-128-gcm:p%40ss+%252F@example.test:443#synthetic",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:synthetic")) + "@example.test:443",
		"ss://" + base64.RawStdEncoding.EncodeToString([]byte("aes-128-gcm:synthetic@example.test:443")),
		"ss://aes-128-gcm:synthetic@example.test:443?plugin=obfs-local%3Bobfs%3Dtls",
		"ss://aes-128-gcm:synthetic@example.test:443?plugin=v2ray-plugin%3Bmode%3Dwebsocket%3Btls",
	} {
		body, fragment, hasFragment := strings.Cut(original, "#")
		separator := "?"
		if strings.Contains(body, "?") {
			separator = "&"
		}
		withTCP := body + separator + "type=tcp"
		if hasFragment {
			withTCP += "#" + fragment
		}
		want, err := ParseNodeURL(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseNodeURL(withTCP)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("TCP marker changed native SS node: %v", err)
		}
	}
}

func TestParseNodeURLSSTCPMarkerDoesNotDropUnsupportedOptions(t *testing.T) {
	for _, query := range []string{
		"type=", "type=ws", "type=grpc", "type=udp", "type=tcp&type=tcp",
		"type=tcp&unknown=1", "type=tcp&plugin=obfs-local%3Bobfs%3Dtls%3Btype%3Dtcp",
	} {
		if _, err := ParseNodeURL("ss://aes-128-gcm:synthetic@example.test:443?" + query); err == nil {
			t.Fatalf("accepted unsupported SS options: %s", query)
		}
	}
}
