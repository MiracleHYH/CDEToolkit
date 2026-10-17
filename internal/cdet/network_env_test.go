package cdet

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type networkRoundTripFunc func(*http.Request) (*http.Response, error)

func (f networkRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func networkTestManager(t *testing.T) *Manager {
	t.Helper()
	for _, key := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY", "no_proxy", "NO_PROXY"} {
		t.Setenv(key, "")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(root, "store")
	if err := os.MkdirAll(filepath.Join(store, ".cdet"), 0700); err != nil {
		t.Fatal(err)
	}
	return &Manager{Store: store, State: State{Components: map[string]Component{}}}
}

func networkProxyFor(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := client.Transport.(*http.Transport).Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if proxy == nil {
		return ""
	}
	return proxy.String()
}

func TestNetworkEnvironmentUsesExplicitProxiesWithoutReadingManagedConfig(t *testing.T) {
	m := networkTestManager(t)
	write(t, filepath.Join(m.Store, dataPath("proxy"), "config.yaml"), "secret: [bad yaml", 0600)
	t.Setenv("HTTP_PROXY", "http://upper.example:8080")
	t.Setenv("http_proxy", "http://lower.example:8081")
	t.Setenv("HTTPS_PROXY", "http://user:PRIVATE-PROXY-SECRET@secure.example:8082")
	t.Setenv("NO_PROXY", ".example.net,127.0.0.0/8")
	env, err := m.networkEnv()
	if err != nil {
		t.Fatal(err)
	}
	if envValue(env, "http_proxy") != "http://lower.example:8081" || envValue(env, "HTTP_PROXY") != envValue(env, "http_proxy") || envValue(env, "https_proxy") != os.Getenv("HTTPS_PROXY") || envValue(env, "no_proxy") != os.Getenv("NO_PROXY") {
		t.Fatal("explicit proxy meaning was not shared across subprocesses and HTTP")
	}
	if os.Getenv("HTTP_PROXY") != "http://upper.example:8080" || os.Getenv("http_proxy") != "http://lower.example:8081" || os.Getenv("https_proxy") != "" {
		t.Fatal("proxy resolution changed the parent environment")
	}
	client, err := m.networkHTTP()
	if err != nil || networkProxyFor(t, client, "https://release.example/latest") != os.Getenv("HTTPS_PROXY") || networkProxyFor(t, client, "http://release.example/latest") != "http://lower.example:8081" || networkProxyFor(t, client, "https://api.example.net/latest") != "" || networkProxyFor(t, client, "https://127.2.3.4/latest") != "" {
		t.Fatal("HTTP did not use this operation's explicit proxy settings", err)
	}
}

func TestNetworkEnvironmentDerivesOnlyPortsAndNeverStartsTheProxy(t *testing.T) {
	for _, scenario := range []struct{ config, http, socks string }{
		{"mixed-port: 7890\nport: 7891\nsocks-port: 7892\nsecret: DO-NOT-PRINT\n", "http://127.0.0.1:7890", "http://127.0.0.1:7890"},
		{"port: 7891\nsocks-port: 7892\n", "http://127.0.0.1:7891", "http://127.0.0.1:7891"},
		{"socks-port: 7892\n", "", "socks5h://127.0.0.1:7892"},
		{"mixed-port: 0\n", "", ""},
	} {
		t.Run(scenario.config, func(t *testing.T) {
			m := networkTestManager(t)
			file := filepath.Join(m.Store, dataPath("proxy"), "config.yaml")
			write(t, file, scenario.config, 0600)
			m.Runner = func(CommandSpec) ([]byte, error) { t.Fatal("network environment started a component"); return nil, nil }
			env, err := m.networkEnv()
			if err != nil || envValue(env, "http_proxy") != scenario.http || envValue(env, "all_proxy") != scenario.socks || read(t, file) != scenario.config {
				t.Fatal("managed proxy port inference failed or changed config", err)
			}
			client, err := m.networkHTTP()
			if err != nil {
				t.Fatal(err)
			}
			if got := networkProxyFor(t, client, "https://release.example/latest"); got != scenario.socks {
				t.Fatal("ALL_PROXY fallback was not used", got)
			}
		})
	}
}

func TestNetworkHTTPHasNoEnvironmentCacheAndKeepsInjectedClientUntouched(t *testing.T) {
	m := networkTestManager(t)
	t.Setenv("ALL_PROXY", "socks5h://127.0.0.1:1080")
	base := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	m.Releases = &ReleaseClient{HTTP: base, API: "https://metadata.example", OS: "linux", Arch: "amd64"}
	first, err := m.networkReleases()
	if err != nil {
		t.Fatal(err)
	}
	if first == m.Releases || first.HTTP == base || first.HTTP.Transport == base.Transport || networkProxyFor(t, first.HTTP, "https://metadata.example/latest") != "socks5h://127.0.0.1:1080" {
		t.Fatal("network client was not independently cloned")
	}
	t.Setenv("ALL_PROXY", "http://127.0.0.1:8088")
	second, err := m.networkHTTP()
	if err != nil || networkProxyFor(t, second, "https://metadata.example/latest") != "http://127.0.0.1:8088" || networkProxyFor(t, first.HTTP, "https://metadata.example/latest") != "socks5h://127.0.0.1:1080" || m.Releases.HTTP != base {
		t.Fatal("network proxy leaked through a global cache or changed original transport", err)
	}
	called := false
	custom := networkRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("STUB"))}, nil
	})
	m.Releases.HTTP = &http.Client{Transport: custom}
	client, err := m.networkHTTP()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("https://isolated.invalid")
	if err != nil || !called {
		t.Fatal("injected HTTP seam was replaced with real networking", err)
	}
	resp.Body.Close()
}

func TestNetworkProxyBypassAndInvalidInputsDoNotExposeCredentials(t *testing.T) {
	m := networkTestManager(t)
	t.Setenv("https_proxy", "http://PRIVATE-PROXY-SECRET@invalid.example:70000")
	if _, err := m.networkEnv(); err == nil || strings.Contains(err.Error(), "PRIVATE-PROXY-SECRET") {
		t.Fatal("invalid proxy accepted or its credentials leaked", err)
	}
	for _, scenario := range []struct {
		target, list string
		match        bool
	}{
		{"https://example.net", "example.net", true},
		{"https://sub.example.net", ".example.net", true},
		{"https://notexample.net", "example.net", false},
		{"https://example.net", "example.net:443", true},
		{"https://example.net", "example.net:80", false},
		{"https://[::1]", "[::1]", true},
		{"https://10.0.0.3", "10.0.0.0/8", true},
		{"https://example.net", "*", true},
	} {
		req, _ := http.NewRequest(http.MethodGet, scenario.target, nil)
		if noProxyMatch(req.URL, scenario.list) != scenario.match {
			t.Fatal("NO_PROXY boundary mismatch", scenario.target, scenario.list)
		}
	}
}
