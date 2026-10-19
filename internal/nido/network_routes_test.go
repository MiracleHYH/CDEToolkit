package nido

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func TestNetworkFileRouteMatrixAndIndependentAPIRoute(t *testing.T) {
	for _, tc := range []struct{ name, accelerate, proxy, fileProxy, policy string }{
		{"none", "none", "http://127.0.0.1:7890", "", "direct"},
		{"prefix", "https://gh-proxy.org", "http://127.0.0.1:7890", "", "https://gh-proxy.org"},
		{"reference", "proxy", "http://127.0.0.1:7890", "http://127.0.0.1:7890", "direct"},
		{"fully-direct", "none", "", "", "direct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.InitWithNetwork(DownloadConfig{GitHubAccelerate: tc.accelerate, ProxyURL: tc.proxy}); err != nil {
				t.Fatal(err)
			}
			m.Env = append(m.Env, "NIDO_GITHUB_PROXY=https://must-not-override.invalid", "HTTPS_PROXY=http://ambient.invalid:1")
			file, err := m.networkHTTP(nil, "", "")
			if err != nil {
				t.Fatal(err)
			}
			defer file.CloseIdleConnections()
			request, _ := http.NewRequest(http.MethodGet, "https://github.com/a/b/releases/download/v1/file", nil)
			fileTransport := file.Transport.(apiBindingTransport).base.(*http.Transport)
			var got *url.URL
			if fileTransport.Proxy != nil {
				got, err = fileTransport.Proxy(request)
				if err != nil {
					t.Fatal(err)
				}
			}
			fileProxy := ""
			if got != nil {
				fileProxy = got.String()
			}
			if fileProxy != tc.fileProxy {
				t.Fatalf("file proxy %q want %q", fileProxy, tc.fileProxy)
			}
			if policy, err := m.networkPolicy("", ""); err != nil || policy != tc.policy {
				t.Fatal("file policy", policy, err)
			}
			api, err := m.networkAPIClient("", "")
			if err != nil {
				t.Fatal(err)
			}
			defer api.CloseIdleConnections()
			apiTransport := api.Transport.(apiBindingTransport).base.(scopedAPITransport).base
			got = nil
			if apiTransport.Proxy != nil {
				got, err = apiTransport.Proxy(request)
				if err != nil {
					t.Fatal(err)
				}
			}
			apiProxy := ""
			if got != nil {
				apiProxy = got.String()
			}
			if apiProxy != tc.proxy {
				t.Fatalf("API proxy %q want %q", apiProxy, tc.proxy)
			}
		})
	}
}

func TestNetworkTemporaryCommandEnvironmentAndProxyDisable(t *testing.T) {
	m, _ := fixture(t)
	if err := m.InitWithNetwork(DownloadConfig{GitHubAccelerate: "https://gh-proxy.org", ProxyURL: "socks5://127.0.0.1:7890"}); err != nil {
		t.Fatal(err)
	}
	base := []string{"KEEP=original", "http_proxy=http://ambient.invalid:1", "HTTPS_PROXY=http://ambient.invalid:2", "ALL_PROXY=http://ambient.invalid:3", "NO_PROXY=*", "no_proxy=remote.invalid"}
	original := append([]string(nil), base...)
	for _, explicit := range []string{"", "none", "http://127.0.0.1:17990"} {
		env, err := m.commandProxyEnv(base, explicit)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(base, original) {
			t.Fatal("command scope mutated parent env")
		}
		text := strings.Join(env, "\n")
		if strings.Contains(text, "ambient.invalid") || strings.Contains(text, "remote.invalid") || strings.Contains(text, "NO_PROXY=*") {
			t.Fatal("ambient route survived", text)
		}
		want := "socks5://127.0.0.1:7890"
		if explicit == "none" {
			want = ""
		} else if explicit != "" {
			want = explicit
		}
		for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
			found := false
			for _, entry := range env {
				if entry == key+"="+want {
					found = true
				}
			}
			if found != (want != "") {
				t.Fatal("wrong temporary proxy key", key, env)
			}
		}
		if !strings.Contains(text, "no_proxy=localhost,127.0.0.1,::1") || !strings.Contains(text, "KEEP=original") {
			t.Fatal("lost loopback/parent env", env)
		}
	}
}

func TestNetworkSettingsSaveOnlyCurrentFieldsAndPreserveIndependentChoices(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	protected := proxyBootstrapSnapshot(t, filepath.Join(m.Root, "config.toml"), m.Root, home)
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr}
	for _, args := range [][]string{{"proxy", "http://127.0.0.1:7890"}, {"set", "https://gh-proxy.org"}, {"set", "proxy"}, {"set", "none"}} {
		if code := c.download(m, cliOptions{yes: true}, args); code != 0 {
			t.Fatal(code, stderr.String())
		}
		if m.Config.Download.ProxyURL != "http://127.0.0.1:7890" {
			t.Fatal("accelerate changed independent Proxy")
		}
	}
	data, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"github_proxy =", "listener =", "mode ="} {
		if strings.Contains(string(data), old) {
			t.Fatal("new write retained legacy route", string(data))
		}
	}
	if !strings.Contains(string(data), "github_accelerate = 'none'") {
		t.Fatal("new route not saved", string(data))
	}
	if !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, filepath.Join(m.Root, "config.toml"), m.Root, home)) {
		t.Fatal("network configuration changed external files")
	}
	if code := c.download(m, cliOptions{yes: true}, []string{"proxy", "none"}); code != 0 {
		t.Fatal(stderr.String())
	}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if code := c.download(m, cliOptions{yes: true}, []string{"set", "proxy"}); code == 0 {
		t.Fatal("missing Proxy reference accepted")
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(before, after) {
		t.Fatal("failed reference changed config")
	}
}

func TestNetworkInteractiveSettingsPreserveIndependentChoicesAndSecrets(t *testing.T) {
	m, home := fixture(t)
	if err := m.InitWithNetwork(DownloadConfig{GitHubAccelerate: "https://gh-proxy.org", ProxyURL: "http://127.0.0.1:7890"}); err != nil {
		t.Fatal(err)
	}
	protected := proxyBootstrapSnapshot(t, filepath.Join(m.Root, "config.toml"), m.Root, home)
	var out, stderr bytes.Buffer
	input := strings.NewReader("1\ny\n")
	c := CLI{UI: terminalui.NewScriptDriver(input, &stderr), In: input, Out: &out, Err: &stderr}
	if code := c.githubAccelerateConfigure(m, cliOptions{}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if m.Config.Download.GitHubAccelerate != "none" || m.Config.Download.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatal("file route overwrote Proxy", m.Config.Download)
	}
	secret := "http://private-user:private-password@127.0.0.1:17990"
	out.Reset()
	stderr.Reset()
	input = strings.NewReader("2\n" + secret + "\ny\n")
	c.UI = terminalui.NewScriptDriver(input, &stderr)
	c.In = input
	if code := c.nidoProxyConfigure(m, cliOptions{}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if m.Config.Download.GitHubAccelerate != "none" || m.Config.Download.ProxyURL != secret {
		t.Fatal("Proxy save changed file route")
	}
	if strings.Contains(out.String()+stderr.String(), "private-password") || strings.Contains(out.String()+stderr.String(), "private-user") {
		t.Fatal("proxy credentials printed", out.String(), stderr.String())
	}
	if !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, filepath.Join(m.Root, "config.toml"), m.Root, home)) {
		t.Fatal("UI configured unrelated application data")
	}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	input = strings.NewReader("3\nn\n")
	c.UI = terminalui.NewScriptDriver(input, &stderr)
	c.In = input
	if code := c.githubAccelerateConfigure(m, cliOptions{}); code == 0 {
		t.Fatal("declined setting was reported saved")
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(before, after) {
		t.Fatal("cancel changed settings")
	}
}

func TestNetworkExplicitProxyDoesNotSilentlyReplaceInvalidFileRoute(t *testing.T) {
	m, _ := fixture(t)
	m.Config.Download = DownloadConfig{GitHubAccelerate: "invalid-file-prefix", ProxyURL: "http://127.0.0.1:7890"}
	if _, err := m.resolveNetwork("", "http://127.0.0.1:17990"); err == nil {
		t.Fatal("Proxy override silently selected direct file route")
	}
	route, err := m.resolveNetwork("none", "http://127.0.0.1:17990")
	if err != nil || route.GitHubAccelerate != "none" || route.ProxyURL != "http://127.0.0.1:17990" {
		t.Fatal("explicit route correction failed", route, err)
	}
	m.Config.Download = DownloadConfig{GitHubAccelerate: "proxy"}
	route, err = m.resolveNetwork("", "http://127.0.0.1:17990")
	if err != nil || route.GitHubAccelerate != "proxy" {
		t.Fatal("explicit proxy failed to complete reference", route, err)
	}
	m.Config.Download = DownloadConfig{Mode: NetworkProxy, Listener: "old-listener"}
	if _, err := m.generalProxyURL(""); err == nil {
		t.Fatal("old listener silently became direct API route")
	}
	if address, err := m.generalProxyURL("none"); err != nil || address != "" {
		t.Fatal("explicit direct API override failed", address, err)
	}
}
