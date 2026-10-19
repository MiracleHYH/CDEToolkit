package nido

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

type apiGateReader struct{ reads int }

func (r *apiGateReader) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestNetworkRoutesAreExclusiveAndDirectIgnoresAmbientProxy(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	before := proxyBootstrapSnapshot(t, "", m.Root, home)
	for _, invalid := range []DownloadConfig{
		{GitHubAccelerate: "auto"}, {GitHubAccelerate: "proxy"},
		{GitHubAccelerate: "https://user:secret@invalid"},
		{GitHubAccelerate: "none", ProxyURL: "ftp://127.0.0.1:1"},
		{GitHubAccelerate: "none", Mode: NetworkDirect},
		{Mode: NetworkProxy, Listener: "one"},
	} {
		if err := m.setNetwork(invalid); err == nil {
			t.Fatal("invalid network setting accepted", invalid)
		}
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
		t.Fatal("validation changed files")
	}
	base := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { t.Fatal("ambient proxy was consulted"); return nil, nil }, TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "wrong.invalid"}}
	previous := http.DefaultTransport
	http.DefaultTransport = base
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, accelerate := range []string{"none", "https://gh-proxy.org"} {
		if err := m.setNetwork(DownloadConfig{GitHubAccelerate: accelerate, ProxyURL: "http://127.0.0.1:1"}); err != nil {
			t.Fatal(err)
		}
		client, err := m.networkHTTP(nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		defer client.CloseIdleConnections()
		transport := client.Transport.(apiBindingTransport).base.(*http.Transport)
		if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.ServerName != "" {
			t.Fatal("direct or prefix inherited proxy/unsafe TLS")
		}
	}
	if base.Proxy == nil || !base.TLSClientConfig.InsecureSkipVerify || base.TLSClientConfig.ServerName != "wrong.invalid" {
		t.Fatal("shared transport was modified")
	}
}

// API traffic always uses the official endpoint and the independent general
// Proxy. An invalid file prefix cannot block or rewrite this request.
func TestNetworkGitHubPrefixNeverRoutesAPI(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m, home := fixture(t)
			if err := m.InitWithDownload("https://gh-proxy.org"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Host != "api.github.com" || r.TLS.ServerName != "api.github.com" || r.URL.Path != "/repos/a/b/releases/latest" {
					t.Error("API was accelerated", r.Host, r.URL)
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"tag_name":"v1.2.3"}`)
			}))
			pool := x509.NewCertPool()
			pool.AddCert(certificate)
			transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{RootCAs: pool}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "api.github.com:443" {
					return nil, fmt.Errorf("unexpected API route")
				}
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
			}}
			previous := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			client, err := m.downloadAPIClient("")
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			data, err := githubdownload.Get(client, "https://api.github.com/repos/a/b/releases/latest", 4096, "direct")
			if (err == nil) != (status == 200) || calls != 1 {
				t.Fatal("API failed or fell back", string(data), calls, err)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
				t.Fatal("query changed files")
			}
		})
	}
}

func TestNetworkOldSidecarIsUnownedAndNeverRead(t *testing.T) {
	for _, contents := range []string{"USER DATA", "schema=1\nproduct='nido-api-proxy'\nlistener='old-api'\n"} {
		m, home := fixture(t)
		if err := m.Init(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(m.Root, apiProxyFile)
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(m.Root, "config.toml")
		before := proxyBootstrapSnapshot(t, configPath, m.Root, home)
		if err := m.setNetwork(DownloadConfig{GitHubAccelerate: "none", ProxyURL: "http://127.0.0.1:1"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != contents {
			t.Fatal("unowned sidecar was changed")
		}
		if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, configPath, m.Root, home)) {
			t.Fatal("network settings modified other data")
		}
	}
}

type networkGuardBody struct {
	io.Reader
	close func() error
}

func (b networkGuardBody) Close() error { return b.close() }

func TestNetworkResponseCloseRejectsChangedConfigurationWithoutFallback(t *testing.T) {
	m, home := fixture(t)
	if err := m.InitWithDownload("direct"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Root, "config.toml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte(nil), original...), []byte("# USER EDIT\n")...)
	protected := proxyBootstrapSnapshot(t, path, m.Root, home)
	calls, closes := 0, 0
	fake := &http.Client{Transport: updateCLITransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 8, Body: networkGuardBody{Reader: strings.NewReader("complete"), close: func() error { closes++; return os.WriteFile(path, edited, 0600) }}, Request: r}, nil
	})}
	client, err := m.networkHTTP(fake, "", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := githubdownload.Get(client, "https://github.com/a/b/releases/download/v1/file", 4096, "direct")
	if err == nil || data != nil || calls != 1 || closes != 1 {
		t.Fatal("changed configuration accepted", string(data), calls, closes, err)
	}
	if actual, _ := os.ReadFile(path); !bytes.Equal(actual, edited) {
		t.Fatal("response error overwrote concurrent user edit")
	}
	if !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, path, m.Root, home)) {
		t.Fatal("response error modified unrelated data")
	}
}

func TestNetworkShowDoesNotExposeMalformedCredentials(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	m.Config.Download = DownloadConfig{Mode: NetworkGitHubProxy, GitHubProxy: "https://sentinel-user:sentinel-password@private.invalid"}
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr}
	if code := cli.download(m, cliOptions{json: true}, []string{"show"}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if strings.Contains(out.String()+stderr.String(), "sentinel-") {
		t.Fatal("invalid configuration leaked credentials", out.String(), stderr.String())
	}
}

func TestNetworkLongLivedManagerRebuildsRouteAfterMenuChanges(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := "https://github.com/a/b/releases/download/v1/file"
	var hosts []string
	origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		if r.TLS.ServerName != r.Host {
			t.Error("lost request hostname", r.Host, r.TLS.ServerName)
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	traffic := &bootstrapTraffic{connections: map[net.Conn]bool{}}
	connect := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, false)
	proxyAddress := connect.Listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	var directDials, prefixDials, proxyDials int
	transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: pool}, Proxy: http.ProxyFromEnvironment, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		target := ""
		switch address {
		case proxyAddress:
			proxyDials++
			target = proxyAddress
		case "github.com:443":
			directDials++
			target = origin.Listener.Addr().String()
		case "gh-proxy.org:443":
			prefixDials++
			target = origin.Listener.Addr().String()
		default:
			return nil, fmt.Errorf("unexpected dial target")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", target)
	}}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previous })
	for _, config := range []DownloadConfig{
		{GitHubAccelerate: "proxy", ProxyURL: connect.URL},
		{GitHubAccelerate: "none", ProxyURL: connect.URL},
		{GitHubAccelerate: "https://gh-proxy.org", ProxyURL: connect.URL},
		{GitHubAccelerate: "proxy", ProxyURL: connect.URL},
	} {
		if err := m.setNetwork(config); err != nil {
			t.Fatal(err)
		}
		var err error
		// Match the component's repeated menu actions, including its retained HTTP.
		m.Proxy.HTTP, err = m.networkHTTP(m.Proxy.HTTP, "", "")
		if err != nil {
			t.Fatal(err)
		}
		policy, err := m.networkPolicy("", "")
		if err != nil {
			t.Fatal(err)
		}
		data, err := githubdownload.Get(m.Proxy.HTTP, source, 4096, policy)
		m.Proxy.HTTP.CloseIdleConnections()
		if err != nil || string(data) != "fixture" {
			t.Fatal(config.Mode, string(data), err)
		}
	}
	if directDials != 1 || prefixDials != 1 || proxyDials != 2 || !reflect.DeepEqual(hosts, []string{"github.com", "github.com", "gh-proxy.org", "github.com"}) {
		t.Fatal("old route survived menu selection", directDials, prefixDials, proxyDials, hosts)
	}
}
