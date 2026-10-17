package proxy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestParseConfigEndpoints(t *testing.T) {
	tests := []struct {
		name, data, selected, address string
		auth                          bool
	}{
		{"traditional mixed", "mixed-port: 7890\n", "mixed", "http://127.0.0.1:7890", false},
		{"traditional allow-lan false", "mixed-port: 7890\nbind-address: '::1'\nallow-lan: false\n", "mixed", "http://127.0.0.1:7890", false},
		{"traditional ignores public bind when disabled", "port: 7890\nbind-address: 192.0.2.1\nallow-lan: false\n", "http", "http://127.0.0.1:7890", false},
		{"traditional loopback explicit lan", "socks-port: 7891\nallow-lan: true\nbind-address: 127.0.0.2\n", "socks", "socks5h://127.0.0.2:7891", false},
		{"traditional ipv6", "mixed-port: 7890\nallow-lan: true\nbind-address: '[::1]'\n", "mixed", "http://[::1]:7890", false},
		{"common", "mixed-port: 7890\nlisteners:\n  - {name: common, type: http, listen: '::1', port: 7892}\n", "common", "http://[::1]:7892", false},
		{"listener socks", "listeners:\n  - {name: my-socks, type: socks, listen: 127.0.0.1, port: '1080'}\n", "my-socks", "socks5h://127.0.0.1:1080", false},
		{"listener quoted decimal leading zero", "listeners: [{name: common, type: mixed, port: '01000', listen: 127.0.0.1}]\n", "common", "http://127.0.0.1:1000", false},
		{"top authentication", "mixed-port: 7890\nauthentication: ['someone:secret-value']\n", "mixed", "http://127.0.0.1:7890", true},
		{"listener inherits auth", "authentication: ['someone:secret-value']\nlisteners:\n  - {name: common, type: mixed, listen: 127.0.0.1, port: 7890}\n", "common", "http://127.0.0.1:7890", true},
		{"listener disables inherited auth", "authentication: ['someone:secret-value']\nlisteners:\n  - {name: common, type: mixed, listen: 127.0.0.1, port: 7890, users: []}\n", "common", "http://127.0.0.1:7890", false},
		{"listener users", "listeners:\n  - name: common\n    type: http\n    listen: 127.0.0.1\n    port: 7890\n    users: [{username: someone, password: secret-value}]\n", "common", "http://127.0.0.1:7890", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(tt.data)
			config, err := ParseConfig(data, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := config.Endpoint("")
			if err != nil || endpoint.Name != tt.selected || endpoint.URL() != tt.address || endpoint.Authentication != tt.auth {
				t.Fatalf("endpoint = %+v, err = %v", endpoint, err)
			}
			if config.Digest != hash(data) || endpoint.ConfigDigest != config.Digest || !bytes.Equal(config.Data, data) {
				t.Fatal("configuration snapshot or endpoint digest differs")
			}
			data[0] = 'X'
			if config.Data[0] == 'X' {
				t.Fatal("ParseConfig retained a mutable caller slice")
			}
			encoded, err := json.Marshal(config)
			if err != nil || bytes.Contains(encoded, []byte("secret-value")) {
				t.Fatal("JSON must not contain raw configuration or credentials")
			}
		})
	}
}

func TestParseConfigRejectsInvalidOrUnsupportedPolicy(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "mapping empty": "{}", "null": "null", "sequence": "[mixed-port, 7890]",
		"no ports": "mode: rule\n", "disabled ports": "mixed-port: 0\n",
		"negative": "mixed-port: -1\n", "overflow": "mixed-port: 65536\n", "float": "port: 7890.0\n",
		"bad typed port": "port: true\n", "hex port": "port: 0x1ed2\n",
		"octal traditional port":       "port: 01000\n",
		"octal listener port":          "listeners: [{name: common, type: socks, port: 01000, listen: 127.0.0.1}]\n",
		"quoted traditional port":      "port: '1000'\n",
		"duplicate key":                "port: 7890\nport: 7891\n",
		"nested duplicate":             "port: 7890\nproxies: [{name: foo, name: bar}]\n",
		"multiple documents":           "port: 7890\n---\nport: 7891\n",
		"trailing empty document":      "port: 7890\n---\n",
		"alias":                        "port: &port 7890\nsocks-port: *port\n",
		"merge":                        "defaults: &a {port: 7890}\n<<: *a\n",
		"lan wildcard":                 "port: 7890\nallow-lan: true\n",
		"lan public":                   "port: 7890\nallow-lan: true\nbind-address: 192.0.2.1\n",
		"lan hostname":                 "port: 7890\nallow-lan: true\nbind-address: localhost\n",
		"lan bare ipv6":                "port: 7890\nallow-lan: true\nbind-address: '::1'\n",
		"listeners implicit wildcard":  "listeners: [{name: common, type: mixed, port: 7890}]\n",
		"listeners explicit wildcard":  "listeners: [{name: common, type: mixed, port: 7890, listen: '::'}]\n",
		"listeners public":             "listeners: [{name: common, type: mixed, port: 7890, listen: 192.0.2.1}]\n",
		"listeners hostname":           "listeners: [{name: common, type: mixed, port: 7890, listen: localhost}]\n",
		"listeners wrong type":         "listeners: [{name: common, type: tproxy, port: 7890, listen: 127.0.0.1}]\n",
		"listeners missing name":       "listeners: [{type: http, port: 7890, listen: 127.0.0.1}]\n",
		"listeners zero":               "listeners: [{name: common, type: socks, port: 0, listen: 127.0.0.1}]\n",
		"listeners range":              "listeners: [{name: common, type: http, port: '7890-7891', listen: 127.0.0.1}]\n",
		"listeners TLS":                "listeners: [{name: common, type: http, port: 7890, listen: 127.0.0.1, certificate: cert.pem}]\n",
		"listeners TLS underscore":     "listeners: [{name: common, type: http, port: 7890, listen: 127.0.0.1, private_key: key.pem}]\n",
		"listeners TLS uppercase":      "listeners: [{name: common, type: mixed, port: 7890, listen: 127.0.0.1, CERTIFICATE: cert.pem}]\n",
		"listeners auth uppercase":     "listeners: [{name: common, type: socks, port: 7890, listen: 127.0.0.1, USERS: [{username: user, password: secret}]}]\n",
		"listeners alternate listen":   "listeners: [{name: common, type: mixed, port: 7890, listen: 127.0.0.1, Listen: 0.0.0.0}]\n",
		"duplicate endpoint names":     "port: 7890\nlisteners: [{name: http, type: socks, port: 7891, listen: 127.0.0.1}]\n",
		"duplicate endpoint addresses": "mixed-port: 7890\nport: 7890\n",
		"mapped duplicate address":     "port: 7890\nlisteners: [{name: common, type: socks, port: 7890, listen: '::ffff:127.0.0.1'}]\n",
		"transparent redir":            "port: 7890\nredir-port: 7892\n", "transparent tproxy": "port: 7890\ntproxy-port: 7892\n",
		"tun": "port: 7890\ntun: {enable: true}\n", "iptables": "port: 7890\niptables: {enable: true}\n",
		"tuic": "port: 7890\ntuic-server: {enable: true}\n", "ss": "port: 7890\nss-config: ss://something\n",
		"vmess": "port: 7890\nvmess-config: vmess://something\n", "tunnels": "port: 7890\ntunnels: ['tcp/127.0.0.1:9000/target:443']\n",
		"script": "port: 7890\nscript: {code: something}\n", "external ui": "port: 7890\nexternal-ui: dashboard\n",
		"controller public":         "port: 7890\nexternal-controller: 0.0.0.0:9090\nsecret: x\n",
		"controller empty host":     "port: 7890\nexternal-controller: ':9090'\nsecret: x\n",
		"controller no secret":      "port: 7890\nexternal-controller: 127.0.0.1:9090\n",
		"controller blank secret":   "port: 7890\nexternal-controller: 127.0.0.1:9090\nsecret: ' '\n",
		"controller unix":           "port: 7890\nexternal-controller-unix: /tmp/controller.sock\n",
		"dns public":                "port: 7890\ndns: {enable: true, listen: 0.0.0.0:53}\n",
		"ntp system":                "port: 7890\nntp: {enable: true, write-to-system: true}\n",
		"auth ignored by core":      "port: 7890\nauthentication: [missing-colon]\n",
		"listener invalid users":    "listeners: [{name: common, type: http, port: 7890, listen: 127.0.0.1, users: [{username: x}]}]\n",
		"provider absolute":         "port: 7890\nproxy-providers: {p: {type: http, path: /tmp/data}}\n",
		"provider outside":          "port: 7890\nproxy-providers: {p: {type: http, path: ../data}}\n",
		"provider reserved":         "port: 7890\nproxy-providers: {p: {type: http, path: providers/../mihomo}}\n",
		"provider main config":      "port: 7890\nrule-providers: {p: {type: http, path: config.yaml}}\n",
		"provider uppercase key":    "port: 7890\nproxy-providers: {p: {type: http, PATH: config.yaml}}\n",
		"provider mixed case key":   "port: 7890\nrule-providers: {p: {type: file, Path: output.log}}\n",
		"provider case alias file":  "port: 7890\nproxy-providers: {p: {type: http, path: CONFIG.YAML}}\n",
		"provider core alias file":  "port: 7890\nproxy-providers: {p: {type: http, path: Mihomo}}\n",
		"provider core cache":       "port: 7890\nproxy-providers: {p: {type: http, path: CACHE.DB}}\n",
		"provider core database":    "port: 7890\nproxy-providers: {p: {type: http, path: geosite.DAT}}\n",
		"provider implicit proxy":   "port: 7890\nproxy-providers: {p: {type: http, url: 'https://example.invalid/sub'}}\n",
		"provider implicit rule":    "port: 7890\nrule-providers: {p: {type: http, url: 'https://example.invalid/rule'}}\n",
		"provider empty file":       "port: 7890\nproxy-providers: {p: {type: file, path: ''}}\n",
		"provider alias type":       "port: 7890\nproxy-providers: {p: {TYPE: http}}\n",
		"tls absolute":              "port: 7890\ntls: {private-key: /tmp/key.pem}\n",
		"tls non-string trust cert": "port: 7890\ntls: {custom-certifactes: [false]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig([]byte(data), t.TempDir()); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestParseConfigFixedRuntimeFiles(t *testing.T) {
	// The geodata selection performed by Mihomo is case-insensitive on every
	// supported OS, unlike an ordinary path lookup on case-sensitive Linux.
	for _, name := range []string{"cache.db", ".cache", "GeoSite.dat", "GeoIP.dat", "ASN.mmdb", "BundleMRS.7z", "Country.mmdb", "geoip.db", "geoip.metadb", "gEoSiTe.DaT", "Geoſite.dat"} {
		for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "ordinary"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("preserved"), 0600); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(root, name)
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(outside, target)
				case "hardlink":
					err = os.Link(outside, target)
				case "fifo":
					err = syscall.Mkfifo(target, 0600)
				case "directory":
					err = os.Mkdir(target, 0700)
				case "ordinary":
					err = os.WriteFile(target, []byte("ordinary"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				_, err = ParseConfig([]byte("port: 7890\n"), root)
				if (kind == "ordinary") != (err == nil) {
					t.Fatalf("runtime file policy: %v", err)
				}
				data, err := os.ReadFile(outside)
				if err != nil || string(data) != "preserved" {
					t.Fatal("static checking modified an outside file")
				}
			})
		}
	}
	root := t.TempDir()
	// Unknown user data stays outside the known runtime-file policy.
	if err := os.Mkdir(filepath.Join(root, "user-data"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseConfig([]byte("port: 7890\n"), root); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "uncreated")
	if _, err := ParseConfig([]byte("port: 7890\n"), missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("runtime path checking created a missing root")
	}
}

func TestParseConfigInlineTrustAndProvider(t *testing.T) {
	data := []byte("port: 7890\ntls:\n  custom-certifactes:\n    - |\n      -----BEGIN CERTIFICATE-----\n      core-validates-the-certificate\n      -----END CERTIFICATE-----\nproxy-providers:\n  p: {type: inline, payload: []}\n")
	if _, err := ParseConfig(data, t.TempDir()); err != nil {
		t.Fatalf("inline certificates and providers must not be treated as paths: %v", err)
	}
}

func TestParseConfigResourceBoundaries(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "providers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "providers", "shared")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "providers", "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"linked/data", "providers/shared", "providers/fifo"} {
		data := []byte("port: 7890\nproxy-providers: {p: {type: http, path: " + path + "}}\n")
		if _, err := ParseConfig(data, root); err == nil {
			t.Fatalf("accepted unsafe resource: %s", path)
		}
	}
	data := []byte("port: 7890\nproxy-providers: {p: {type: http, path: providers/p.yaml}}\nrule-providers: {r: {type: file, path: rules/r.yaml}}\n")
	if _, err := ParseConfig(data, root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "rules")); !os.IsNotExist(err) {
		t.Fatal("static parsing created a resource directory")
	}
	current, err := os.ReadFile(outside)
	if err != nil || string(current) != "outside" {
		t.Fatal("static parsing modified an outside file")
	}
}

func TestParseConfigKeepsCoreSemanticsSeparate(t *testing.T) {
	data := []byte("port: 7890\nmode: certainly-invalid\nproxies: [{name: custom, type: a-future-node, extension: {path: an-extension-value}}]\n")
	config, err := ParseConfig(data, t.TempDir())
	if err != nil || !bytes.Equal(config.Data, data) {
		t.Fatalf("static policy should preserve core-owned fields: %v", err)
	}
	data = []byte("port: 7890\ntun: {enable: false}\ntuic-server: {enable: false}\niptables: {enable: false}\nexternal-controller: '[::1]:9090'\nsecret: private-controller-secret\ndns: {enable: false, listen: '127.0.0.1:5353'}\n")
	if _, err := ParseConfig(data, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigReadAndPathAreReadOnly(t *testing.T) {
	root := t.TempDir()
	s := &Service{Root: root}
	path, err := s.ConfigPath()
	canonicalRoot, canonicalErr := filepath.EvalSymlinks(root)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || path != filepath.Join(canonicalRoot, "proxy", "config.yaml") {
		t.Fatalf("config path = %q: %v", path, err)
	}
	if _, err := s.ReadConfig(); err == nil {
		t.Fatal("missing config was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "proxy")); !os.IsNotExist(err) {
		t.Fatal("read-only operation created component directory")
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mixed-port: 7890\n"), 0600); err != nil {
		t.Fatal(err)
	}
	endpoint, err := s.Endpoint("")
	if err != nil || endpoint.URL() != "http://127.0.0.1:7890" {
		t.Fatalf("endpoint = %+v: %v", endpoint, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.ReadConfig(); finished <- err }()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("accepted FIFO configuration")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO configuration blocked a read-only operation")
	}
}

func TestTemplateRoundTripAndSensitiveDiagnostics(t *testing.T) {
	root := t.TempDir()
	subscription := "https://name:private-password@example.com/provider?token=private-token&x=%22quoted%22#fragment"
	data, err := Template(subscription)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(data, root)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := config.Endpoint("")
	if err != nil || endpoint.Name != "common" || endpoint.URL() != "http://127.0.0.1:7890" {
		t.Fatal("template did not create common local mixed endpoint")
	}
	if bytes.Contains(data, []byte("external-controller")) || bytes.Contains(data, []byte("secret:")) {
		t.Fatal("template should not enable controller")
	}
	for _, input := range []string{"private-token", "file:///tmp/private-token", "https://example.com:99999/private-token", "https://example.com/private-token\nport: 22"} {
		_, err := Template(input)
		if err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatal("invalid subscription was accepted or leaked")
		}
	}
	for _, input := range []string{"port: 7890\nprivate-token: [", "port: 7890\nauthentication: [private-token]"} {
		_, err := ParseConfig([]byte(input), root)
		if err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatal("invalid YAML was accepted or leaked")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("template or parsing changed the filesystem")
	}
}

func TestTemplateRoutesThroughSubscriptionAndRejectsEmptyGroup(t *testing.T) {
	data, err := Template("http://127.0.0.1:18080/provider.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Providers map[string]struct {
			Proxy string
		} `yaml:"proxy-providers"`
		Groups []struct {
			Name          string
			Type          string
			Use           []string
			Proxies       []string
			EmptyFallback string `yaml:"empty-fallback"`
		} `yaml:"proxy-groups"`
		Rules []string
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	// Provider downloads otherwise enter the business rule group before it has
	// any nodes. Bootstrap independently while keeping business traffic on the
	// subscription (or REJECT when it is unavailable).
	if config.Providers["subscription"].Proxy != "DIRECT" {
		t.Fatal("subscription download must bootstrap independently of its own nodes")
	}
	if len(config.Groups) != 1 || config.Groups[0].Name != "PROXY" || config.Groups[0].Type != "select" || len(config.Groups[0].Use) != 1 || config.Groups[0].Use[0] != "subscription" {
		t.Fatal("template must select nodes from its subscription provider")
	}
	// Mihomo prepends explicit proxies before provider nodes, and defaults an
	// empty group to COMPATIBLE (direct). Both would silently bypass the user's
	// subscription, so assert the emitted native configuration avoids them.
	if len(config.Groups[0].Proxies) != 0 || config.Groups[0].EmptyFallback != "REJECT" {
		t.Fatal("template must neither prefer direct nor fall back to it")
	}
	if len(config.Rules) != 1 || config.Rules[0] != "MATCH,PROXY" {
		t.Fatal("template traffic must use the subscription group without geodata rules")
	}
}
