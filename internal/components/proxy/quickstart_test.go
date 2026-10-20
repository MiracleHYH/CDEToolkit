package proxy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestQuickStartControllerAndManualOnlyProviderRefresh(t *testing.T) {
	o := QuickStartOptions{
		ProviderURL: "https://provider.invalid/sub", ProxyFilter: ".*", ProxyPort: 17890,
		Download:   &QuickStartRoute{Filter: "download", Port: 17891},
		ISP:        &QuickStartISP{NodeURL: quickStartTestNode, Port: 17892, RelayFilter: "relay"},
		Controller: &ControllerConfig{Address: DefaultControllerAddress, Secret: "synthetic-controller-secret"},
	}
	data, err := QuickStart(o)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ParseConfig(data, t.TempDir())
	if err != nil || config.Controller == nil || *config.Controller != *o.Controller {
		t.Fatalf("quick start controller failed round trip: %v", err)
	}
	var document quickStartTestDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if provider := document.Provider["subscription"]; provider.Interval != 0 || !provider.HealthCheck.Enable || provider.HealthCheck.Interval != 300 {
		t.Fatal("manual provider refresh disabled health checks or retained polling")
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	provider := raw["proxy-providers"].(map[string]any)["subscription"].(map[string]any)
	if interval, explicit := provider["interval"]; !explicit || interval != 0 {
		t.Fatal("manual provider refresh must emit an explicit zero interval")
	}
	for _, group := range document.Groups {
		if group.Type != "url-test" || group.Interval != 300 {
			t.Fatal("manual provider refresh disabled group measurements")
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil || strings.Contains(string(encoded), o.Controller.Secret) {
		t.Fatal("controller secret leaked through configuration JSON")
	}
	for _, controller := range []ControllerConfig{
		{Address: "127.0.0.1:17890", Secret: "token"},
		{Address: "127.0.0.1:17891", Secret: "token"},
		{Address: "127.0.0.1:17892", Secret: "token"},
		{Address: "0.0.0.0:9090", Secret: "token"},
		{Address: DefaultControllerAddress},
		{Address: DefaultControllerAddress, Secret: "bad\nsecret"},
	} {
		o.Controller = &controller
		if _, err := QuickStart(o); err == nil {
			t.Fatal("quick start accepted unsafe controller or listener conflict")
		}
	}
}

const quickStartTestNode = "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@192.0.2.9:8388#ISP%2Dname"

type quickStartTestGroup struct {
	Name          string   `yaml:"name"`
	Type          string   `yaml:"type"`
	Use           []string `yaml:"use"`
	Proxies       []string `yaml:"proxies"`
	Filter        string   `yaml:"filter"`
	URL           string   `yaml:"url"`
	Interval      int      `yaml:"interval"`
	EmptyFallback string   `yaml:"empty-fallback"`
}

type quickStartTestListener struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Listen string `yaml:"listen"`
	Port   int    `yaml:"port"`
	Proxy  string `yaml:"proxy"`
}

type quickStartTestDocument struct {
	AllowLAN bool   `yaml:"allow-lan"`
	Mode     string `yaml:"mode"`
	LogLevel string `yaml:"log-level"`
	Provider map[string]struct {
		Type        string `yaml:"type"`
		URL         string `yaml:"url"`
		Path        string `yaml:"path"`
		Proxy       string `yaml:"proxy"`
		Interval    int    `yaml:"interval"`
		HealthCheck struct {
			Enable   bool   `yaml:"enable"`
			URL      string `yaml:"url"`
			Interval int    `yaml:"interval"`
		} `yaml:"health-check"`
	} `yaml:"proxy-providers"`
	Proxies   []map[string]any         `yaml:"proxies"`
	Groups    []quickStartTestGroup    `yaml:"proxy-groups"`
	Listeners []quickStartTestListener `yaml:"listeners"`
	Rules     []string                 `yaml:"rules"`
}

func TestQuickStartOptionalRoutes(t *testing.T) {
	for _, download := range []bool{false, true} {
		for _, isp := range []string{"disabled", "direct", "relay"} {
			name := isp
			if download {
				name += "/download"
			}
			t.Run(name, func(t *testing.T) {
				o := QuickStartOptions{ProviderURL: "https://provider.invalid/sub", ProxyFilter: "(?i)daily", ProxyPort: 17890}
				expectedGroups := map[string]string{"Proxy": o.ProxyFilter}
				expectedListeners := []quickStartTestListener{{Name: "endpoint-proxy", Type: "mixed", Listen: "127.0.0.1", Port: 17890, Proxy: "Proxy"}}
				if download {
					o.Download = &QuickStartRoute{Filter: "download.*", Port: 17891}
					expectedGroups["Download"] = o.Download.Filter
					expectedListeners = append(expectedListeners, quickStartTestListener{Name: "endpoint-download", Type: "mixed", Listen: "127.0.0.1", Port: 17891, Proxy: "Download"})
				}
				if isp != "disabled" {
					o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 17892}
					expectedListeners = append(expectedListeners, quickStartTestListener{Name: "endpoint-isp", Type: "mixed", Listen: "127.0.0.1", Port: 17892, Proxy: "ISP-name"})
					if isp == "relay" {
						o.ISP.RelayFilter = "relay.*"
						expectedGroups["ISP-Relay"] = o.ISP.RelayFilter
					}
				}
				data, err := QuickStart(o)
				if err != nil {
					t.Fatal(err)
				}
				var config quickStartTestDocument
				if err := yaml.Unmarshal(data, &config); err != nil {
					t.Fatal(err)
				}
				outbound := "Proxy"
				if download {
					outbound = "Download"
				}
				expectedRules := []string{
					"AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,github.com))," + outbound,
					"AND,((IN-TYPE,INNER),(DST-PORT,443),(DOMAIN,release-assets.githubusercontent.com))," + outbound,
					"MATCH,DIRECT",
				}
				if config.AllowLAN || config.Mode != "rule" || config.LogLevel != "info" || !reflect.DeepEqual(config.Rules, expectedRules) {
					t.Fatalf("unexpected defaults: %+v", config)
				}
				provider, ok := config.Provider["subscription"]
				if !ok || len(config.Provider) != 1 || provider.Type != "http" || provider.URL != o.ProviderURL || provider.Path != "providers/subscription.yaml" || provider.Proxy != "DIRECT" || provider.Interval != 0 || !provider.HealthCheck.Enable || provider.HealthCheck.URL != DefaultTestURL || provider.HealthCheck.Interval != 300 {
					t.Fatalf("unexpected provider: %+v", config.Provider)
				}
				if !reflect.DeepEqual(config.Listeners, expectedListeners) {
					t.Fatalf("listeners = %+v, want %+v", config.Listeners, expectedListeners)
				}
				groups := make(map[string]quickStartTestGroup)
				for _, group := range config.Groups {
					if _, duplicate := groups[group.Name]; duplicate {
						t.Fatalf("duplicate group %s", group.Name)
					}
					groups[group.Name] = group
				}
				wantGroupCount := len(expectedGroups)
				if len(groups) != wantGroupCount {
					t.Fatalf("unexpected group count: %+v", groups)
				}
				for groupName, filter := range expectedGroups {
					group := groups[groupName]
					if group.Type != "url-test" || group.Filter != filter || group.URL != DefaultTestURL || group.Interval != 300 || group.EmptyFallback != "REJECT" || !reflect.DeepEqual(group.Use, []string{"subscription"}) || len(group.Proxies) != 0 {
						t.Fatalf("unexpected provider group: %+v", group)
					}
				}
				if o.ISP == nil {
					if len(config.Proxies) != 0 {
						t.Fatalf("unrequested ISP node: %+v", config.Proxies)
					}
				} else {
					if len(config.Proxies) != 1 {
						t.Fatalf("unexpected ISP nodes: %+v", config.Proxies)
					}
					node := config.Proxies[0]
					if node["name"] != "ISP-name" || node["type"] != "ss" || node["server"] != "192.0.2.9" || node["port"] != 8388 || node["cipher"] != "aes-128-gcm" || node["password"] != "password" {
						t.Fatalf("ISP node parsing lost fields: %+v", node)
					}
					if isp == "relay" {
						if node["dialer-proxy"] != "ISP-Relay" {
							t.Fatalf("ISP relay chain is missing: %+v", node)
						}
					} else if _, exists := node["dialer-proxy"]; exists {
						t.Fatalf("unrequested ISP relay: %+v", node)
					}
					if _, exists := groups["isp"]; exists {
						t.Fatal("single-node ISP wrapper group must not be generated")
					}
				}
				var keys map[string]any
				if err := yaml.Unmarshal(data, &keys); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"port", "socks-port", "mixed-port", "external-controller", "tun", "dns", "authentication"} {
					if _, exists := keys[key]; exists {
						t.Fatalf("generated unexpected global setting %s", key)
					}
				}
				parsed, err := ParseConfig(data, t.TempDir())
				if err != nil || len(parsed.Endpoints) != len(expectedListeners) {
					t.Fatalf("generated config fails local policy: %v", err)
				}
			})
		}
	}
}

func TestQuickStartPreservesScalarInput(t *testing.T) {
	o := QuickStartOptions{
		ProviderURL: "https://user:p%40ss@provider.invalid:443/sub%2Dpath?token=a%2Bb%252Dc&literal=%23frag#name",
		ProxyFilter: `(?i)^(?!.*download).*['":#].*$`,
		ProxyPort:   7890,
		Download:    &QuickStartRoute{Filter: `.*`, Port: 7891},
		ISP: &QuickStartISP{
			NodeURL:     quickStartTestNode,
			Port:        7892,
			TestURL:     "https://isp-check.invalid:8443/path%2Dpart?q=%252D&label=%23test",
			RelayFilter: `^(?=.*relay)(?!.*isp).*$`,
		},
	}
	data, err := QuickStart(o)
	if err != nil {
		t.Fatal(err)
	}
	var config quickStartTestDocument
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Provider["subscription"].URL != o.ProviderURL {
		t.Fatal("provider URL was decoded, altered, or interpreted as YAML")
	}
	for _, group := range config.Groups {
		switch group.Name {
		case "Proxy":
			if group.Filter != o.ProxyFilter {
				t.Fatal("Mihomo filter was rewritten or interpreted as YAML")
			}
		case "ISP-Relay":
			if group.Filter != o.ISP.RelayFilter {
				t.Fatal("relay filter was not preserved")
			}
			if group.URL != o.ISP.TestURL {
				t.Fatal("ISP relay test URL was replaced by the ordinary health-check URL")
			}
		}
	}
	if _, err := ParseConfig(data, t.TempDir()); err != nil {
		t.Fatalf("escaped values damaged the generated document: %v", err)
	}
}

func TestQuickStartISPUsesNamedNodeWithoutWrapperGroup(t *testing.T) {
	base, _, _ := strings.Cut(quickStartTestNode, "#")
	for _, tc := range []struct {
		fragment, want string
	}{
		{"ISP-US", "ISP-US"},
		{"ISP%3A%20%23%20quoted", "ISP: # quoted"},
		{"", "isp-node"},
		{"%20%20", "isp-node"},
	} {
		t.Run(tc.fragment, func(t *testing.T) {
			o := QuickStartOptions{
				ProviderURL: "https://provider.invalid/sub", ProxyFilter: ".*", ProxyPort: 17890,
				ISP: &QuickStartISP{NodeURL: base + "#" + tc.fragment, Port: 17892},
			}
			data, err := QuickStart(o)
			if err != nil {
				t.Fatal(err)
			}
			var config quickStartTestDocument
			if err := yaml.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			if len(config.Groups) != 1 || config.Groups[0].Name != "Proxy" || len(config.Proxies) != 1 || config.Proxies[0]["name"] != tc.want || len(config.Listeners) != 2 || config.Listeners[1].Proxy != tc.want {
				t.Fatal("ISP must directly select its named node without another group")
			}
			if _, found := config.Proxies[0]["dialer-proxy"]; found {
				t.Fatal("unrequested ISP relay was generated")
			}
			if _, err := ParseConfig(data, t.TempDir()); err != nil {
				t.Fatalf("quoted node name damaged YAML or listener references: %v", err)
			}
		})
	}
	for _, name := range []string{"Proxy", "Download", "ISP-Relay", "DIRECT", "REJECT", "REJECT-DROP", "PASS", "PASS-RULE", "GLOBAL", "COMPATIBLE"} {
		o := QuickStartOptions{
			ProviderURL: "https://provider.invalid/sub", ProxyFilter: ".*", ProxyPort: 17890,
			ISP: &QuickStartISP{NodeURL: base + "#" + name, Port: 17892, RelayFilter: ".*"},
		}
		if _, err := QuickStart(o); err == nil {
			t.Fatalf("node name %s can redirect the listener or create a relay cycle", name)
		}
	}
}

func TestQuickStartRejectsInvalidInput(t *testing.T) {
	tests := map[string]func(*QuickStartOptions){
		"provider missing":          func(o *QuickStartOptions) { o.ProviderURL = "" },
		"provider relative":         func(o *QuickStartOptions) { o.ProviderURL = "/provider" },
		"provider opaque":           func(o *QuickStartOptions) { o.ProviderURL = "https:provider.invalid" },
		"provider file":             func(o *QuickStartOptions) { o.ProviderURL = "file:///etc/passwd" },
		"provider absent host":      func(o *QuickStartOptions) { o.ProviderURL = "https:///provider" },
		"provider control":          func(o *QuickStartOptions) { o.ProviderURL += "\nport: 1" },
		"provider invalid UTF8":     func(o *QuickStartOptions) { o.ProviderURL += "\xff" },
		"provider space":            func(o *QuickStartOptions) { o.ProviderURL += " a" },
		"provider port empty":       func(o *QuickStartOptions) { o.ProviderURL = "https://provider.invalid:/sub" },
		"provider port invalid":     func(o *QuickStartOptions) { o.ProviderURL = "https://provider.invalid:abc/sub" },
		"provider port zero":        func(o *QuickStartOptions) { o.ProviderURL = "https://provider.invalid:0/sub" },
		"provider port overflow":    func(o *QuickStartOptions) { o.ProviderURL = "https://provider.invalid:65536/sub" },
		"provider bracket hostname": func(o *QuickStartOptions) { o.ProviderURL = "https://[provider.invalid]/sub" },
		"provider unbracketed IPv6": func(o *QuickStartOptions) { o.ProviderURL = "http://::1:8000/sub" },
		"filter missing":            func(o *QuickStartOptions) { o.ProxyFilter = "" },
		"filter whitespace":         func(o *QuickStartOptions) { o.ProxyFilter = " \t " },
		"filter control":            func(o *QuickStartOptions) { o.ProxyFilter += "\x00" },
		"filter invalid UTF8":       func(o *QuickStartOptions) { o.ProxyFilter += "\xff" },
		"proxy port zero":           func(o *QuickStartOptions) { o.ProxyPort = 0 },
		"proxy port negative":       func(o *QuickStartOptions) { o.ProxyPort = -1 },
		"proxy port overflow":       func(o *QuickStartOptions) { o.ProxyPort = 65536 },
		"download filter empty":     func(o *QuickStartOptions) { o.Download = &QuickStartRoute{Port: 7891} },
		"download filter control":   func(o *QuickStartOptions) { o.Download = &QuickStartRoute{Filter: "a\nb", Port: 7891} },
		"download port invalid":     func(o *QuickStartOptions) { o.Download = &QuickStartRoute{Filter: ".*", Port: 0} },
		"duplicate proxy download":  func(o *QuickStartOptions) { o.Download = &QuickStartRoute{Filter: ".*", Port: o.ProxyPort} },
		"ISP port invalid":          func(o *QuickStartOptions) { o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 65536} },
		"duplicate proxy ISP":       func(o *QuickStartOptions) { o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: o.ProxyPort} },
		"duplicate download ISP": func(o *QuickStartOptions) {
			o.Download = &QuickStartRoute{Filter: ".*", Port: 7891}
			o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 7891}
		},
		"ISP node absent":      func(o *QuickStartOptions) { o.ISP = &QuickStartISP{Port: 7892} },
		"ISP node unsupported": func(o *QuickStartOptions) { o.ISP = &QuickStartISP{NodeURL: "file:///etc/passwd", Port: 7892} },
		"ISP test URL invalid": func(o *QuickStartOptions) {
			o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 7892, RelayFilter: ".*", TestURL: "file:///etc/passwd"}
		},
		"ISP test URL whitespace": func(o *QuickStartOptions) {
			o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 7892, RelayFilter: ".*", TestURL: " "}
		},
		"ISP relay whitespace": func(o *QuickStartOptions) {
			o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 7892, RelayFilter: " "}
		},
		"ISP relay control": func(o *QuickStartOptions) {
			o.ISP = &QuickStartISP{NodeURL: quickStartTestNode, Port: 7892, RelayFilter: "relay\r"}
		},
		"oversize generated input": func(o *QuickStartOptions) { o.ProxyFilter = strings.Repeat("x", maxConfig) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			o := QuickStartOptions{ProviderURL: "https://provider.invalid/sub", ProxyFilter: ".*", ProxyPort: 7890}
			mutate(&o)
			if data, err := QuickStart(o); err == nil || len(data) != 0 {
				t.Fatal("accepted invalid quick start input")
			}
		})
	}
}

func TestQuickStartAllowsIPv6ProviderAndTestURL(t *testing.T) {
	o := QuickStartOptions{
		ProviderURL: "http://[::1]:8000/provider",
		ProxyFilter: ".*",
		ProxyPort:   1,
		ISP: &QuickStartISP{
			NodeURL: quickStartTestNode,
			Port:    65535,
			TestURL: "http://[2001:db8::1]:8080/check",
		},
	}
	if _, err := QuickStart(o); err != nil {
		t.Fatal(err)
	}
}
