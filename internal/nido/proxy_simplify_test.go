package nido

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type proxyInstallTransport func(*http.Request) (*http.Response, error)

func (f proxyInstallTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProxyCLIOnlineInstallUsesOneConfirmationAndNoMetadataAPI(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{}
	m.Proxy.Runner = runner
	files, apis := 0, 0
	m.Proxy.HTTP = &http.Client{Transport: proxyInstallTransport(func(r *http.Request) (*http.Response, error) {
		files++
		if r.Method != http.MethodGet || r.URL.Host != "github.com" || !strings.HasPrefix(r.URL.Path, "/MetaCubeX/mihomo/releases/download/v1.19.32/mihomo-") || !strings.HasSuffix(r.URL.Path, ".gz") {
			t.Error("installation did not request the fixed official asset", r.Method, r.URL)
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture unavailable")), Request: r}, nil
	})}
	originalAPI := &http.Client{Transport: proxyInstallTransport(func(*http.Request) (*http.Response, error) { apis++; return nil, fmt.Errorf("metadata must not run") })}
	m.Proxy.APIHTTP = originalAPI
	ui := &proxyPageCapture{}
	var output bytes.Buffer
	cli := &CLI{UI: ui, Out: &output, Err: &output}
	if code := cli.proxy(m, cliOptions{githubProxy: "none", proxy: "none"}, []string{"install"}); code == 0 {
		t.Fatal("synthetic failed download was accepted")
	}
	if files != 1 || apis != 0 || !reflect.DeepEqual(ui.selections, []string{"安装 Proxy"}) {
		t.Fatal("installation queried metadata or selected a local program", files, apis, ui.selections, output.String())
	}
	if m.Proxy.APIHTTP != originalAPI || len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
		t.Fatal("failed install changed the API route or executed core")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatal("failed download created a core", err)
	}
}

func TestProxyCLIRemovedActionsAndFlagsCannotReadOrWriteTools(t *testing.T) {
	for _, args := range [][]string{
		{"bind", "ai"}, {"bind", "daily"}, {"bind", "download"}, {"bind", "--tool", "codex"},
		{"logs"}, {"configure", "--file", "/unused.yaml"}, {"quickstart", "--file", "/unused.yaml"}, {"status", "--tool", "git"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			runner := &cliProxyRunner{}
			reader := &apiGateReader{}
			var output bytes.Buffer
			cli := &CLI{In: reader, Out: &output, Err: &output, ProxyRunner: runner}
			if code := cli.Run(append([]string{"--root", m.Root, "proxy"}, args...)); code == 0 {
				t.Fatal("removed operation accepted", args)
			}
			if reader.reads != 0 || runner.starts != 0 || runner.stops != 0 || len(runner.commands) != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("removed operation read input, executed core or changed files", args, reader.reads)
			}
		})
	}
}

func TestProxyCLIInstallAcceptsPrefixAndIndependentProxyTogether(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{}
	m.Proxy.Runner = runner
	files, apis := 0, 0
	m.Proxy.HTTP = &http.Client{Transport: proxyInstallTransport(func(r *http.Request) (*http.Response, error) {
		files++
		if r.URL.Host != "accel.invalid" || !strings.HasPrefix(r.URL.Path, "/https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/") {
			t.Error("file did not use the selected prefix", r.URL)
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("fixture unavailable")), Request: r}, nil
	})}
	m.Proxy.APIHTTP = &http.Client{Transport: proxyInstallTransport(func(*http.Request) (*http.Response, error) {
		apis++
		return nil, fmt.Errorf("metadata must not run")
	})}
	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output}
	code := cli.proxy(m, cliOptions{yes: true, githubProxy: "https://accel.invalid", proxy: "http://127.0.0.1:19999"}, []string{"install"})
	if code == 0 || files != 1 || apis != 0 || len(runner.commands) != 0 {
		t.Fatal("independent prefix/proxy options were rejected or stacked", code, files, apis, output.String())
	}
}

func TestProxyCLIFirstInstallReferencesExplicitProxyWithoutSavedProxy(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if m.Config.Download.ProxyURL != "" {
		t.Fatal("fixture unexpectedly has a saved proxy")
	}
	configBefore, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	externalBefore := proxyBootstrapSnapshot(t, "", home)
	var connects, dials atomic.Int32
	selectedProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		if r.Method != http.MethodConnect || r.Host != "github.com:443" {
			t.Error("first install contacted API or changed file authority", r.Method, r.Host)
		}
		// Fail before opening an origin connection. This verifies route selection
		// without fetching any public file or executing an artifact.
		http.Error(w, "synthetic CONNECT refusal", http.StatusServiceUnavailable)
	}))
	defer selectedProxy.Close()
	proxyAddress := selectedProxy.Listener.Addr().String()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != proxyAddress {
			return nil, fmt.Errorf("fixture forbids direct or external dialing")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", proxyAddress)
	}}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
	t.Setenv("HTTPS_PROXY", "http://ambient.invalid:1")
	t.Setenv("NO_PROXY", "*")
	runner := &cliProxyRunner{}
	var output bytes.Buffer
	cli := &CLI{Out: &output, Err: &output, ProxyRunner: runner}
	code := cli.Run([]string{"--root", m.Root, "proxy", "install", "--github-accelerate", "proxy", "--proxy", selectedProxy.URL, "--yes"})
	if code == 0 || connects.Load() != 1 || dials.Load() != 1 {
		t.Fatal("explicit proxy reference rejected, bypassed or retried", code, connects.Load(), dials.Load(), output.String())
	}
	if strings.Contains(output.String(), "请先配置 Proxy") {
		t.Fatal("explicit Proxy was ignored", output.String())
	}
	if len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
		t.Fatal("failed download executed or changed core")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatal("failed CONNECT created core", err)
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(after, configBefore) {
		t.Fatal("temporary Proxy flags changed saved settings")
	}
	if !reflect.DeepEqual(externalBefore, proxyBootstrapSnapshot(t, "", home)) {
		t.Fatal("temporary Proxy flags changed external data")
	}
}
