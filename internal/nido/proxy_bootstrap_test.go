package nido

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The bootstrap core is a format-only fixture. Its existing fake runner is the
// sole executor; this helper never starts a real core or a system service.
func proxyBootstrapLocal(t *testing.T, listener string) (*Manager, string) {
	t.Helper()
	m, home := fixture(t)
	if err := m.InitWithDownload("https://saved-file-only.invalid"); err != nil {
		t.Fatal(err)
	}
	m.Env = append(m.Env, "NIDO_GITHUB_PROXY=https://environment-file-only.invalid")
	runner := &cliProxyRunner{ready: true}
	m.Proxy.Runner = runner
	var requests int
	m.Proxy.HTTP = &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("bootstrap must use only the explicit local artifact")
	})}
	artifact := proxyCLIArtifact(t)
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(listener)
	if err != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		t.Fatal("bootstrap listener must be an explicit loopback fixture")
	}
	configuration := filepath.Join(home, "bootstrap-proxy.yaml")
	if err := os.WriteFile(configuration, []byte("listeners: [{name: bootstrap-api, type: http, listen: 127.0.0.1, port: "+u.Port()+"}]\nmode: rule\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		action  string
		options cliOptions
	}{
		{"install", cliOptions{artifact: artifact, checksum: updateCLIHash(data), yes: true, json: true}},
		{"configure", cliOptions{file: configuration, yes: true, json: true}},
		{"start", cliOptions{yes: true, json: true}},
	} {
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr}
		if code := cli.proxy(m, step.options, []string{step.action}); code != 0 {
			t.Fatalf("local bootstrap %s failed: %s %s", step.action, out.String(), stderr.String())
		}
	}
	endpointForAPI, err := m.Proxy.Endpoint("bootstrap-api")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.configureAPIProxy(endpointForAPI.URL(), &endpointForAPI); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || runner.starts != 1 || runner.stops != 0 {
		t.Fatalf("bootstrap performed HTTP or unexpected lifecycle actions: requests=%d starts=%d stops=%d", requests, runner.starts, runner.stops)
	}
	endpoint, err := m.Proxy.Endpoint("")
	if err != nil || endpoint.URL() != listener {
		t.Fatalf("bootstrap listener mismatch: %v %v", endpoint, err)
	}
	installed, err := os.ReadFile(filepath.Join(m.Root, "proxy", "mihomo"))
	if err != nil || !bytes.Equal(installed, data) {
		t.Fatalf("local core bytes changed: %v", err)
	}
	if m.Config.Download.GitHubProxy != "https://saved-file-only.invalid" {
		t.Fatal("bootstrap changed the saved file-prefix policy")
	}
	return m, home
}

func TestProxyBootstrapLocalArtifactWithoutHTTP(t *testing.T) {
	// Port 1 is configuration data only. The fake runner never binds it.
	proxyBootstrapLocal(t, "http://127.0.0.1:1")
}

func TestProxyBootstrapRejectsLocalChecksumBeforeCoreExecution(t *testing.T) {
	m, _ := fixture(t)
	if err := m.InitWithDownload("https://saved-file-only.invalid"); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{ready: true}
	m.Proxy.Runner = runner
	var requests int
	m.Proxy.HTTP = &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("local checksum rejection must not use HTTP")
	})}
	config, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr}
	options := cliOptions{artifact: proxyCLIArtifact(t), checksum: strings.Repeat("0", 64), yes: true, json: true}
	if code := cli.proxy(m, options, []string{"install"}); code == 0 {
		t.Fatal("bootstrap accepted a mismatching local checksum")
	}
	if requests != 0 || len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
		t.Fatal("mismatching checksum reached HTTP, core validation or process execution")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatal("mismatching core checksum published a program", err)
	}
	if current, err := os.ReadFile(filepath.Join(m.Root, "config.toml")); err != nil || !bytes.Equal(current, config) {
		t.Fatal("mismatching core checksum changed Nido configuration", err)
	}
}

type bootstrapFile struct {
	mode     os.FileMode
	uid, gid uint32
	data     []byte
	link     string
}

func proxyBootstrapSnapshot(t *testing.T, excluded string, roots ...string) map[string]bootstrapFile {
	t.Helper()
	result := map[string]bootstrapFile{}
	for _, root := range roots {
		if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == excluded {
				return err
			}
			entry := bootstrapFile{mode: info.Mode()}
			stat := info.Sys().(*syscall.Stat_t)
			entry.uid, entry.gid = stat.Uid, stat.Gid
			if info.Mode().IsRegular() {
				entry.data, err = os.ReadFile(path)
			} else if info.Mode()&os.ModeSymlink != 0 {
				entry.link, err = os.Readlink(path)
			}
			result[path] = entry
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func proxyBootstrapTLS(t *testing.T, handler http.Handler) (*httptest.Server, *x509.Certificate) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated official-name fixture"},
		DNSNames: []string{"api.github.com", "github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, leaf
}

type bootstrapTraffic struct {
	sync.Mutex
	connects, requests      []string
	proxyDials, directDials int
	connections             map[net.Conn]bool
}

func proxyBootstrapConnect(t *testing.T, origin string, traffic *bootstrapTraffic, reject bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		traffic.Lock()
		traffic.connects = append(traffic.connects, request.Method+" "+request.Host)
		traffic.Unlock()
		if request.Method != http.MethodConnect || request.Host != "api.github.com:443" && request.Host != "github.com:443" {
			http.Error(w, "only explicit official-name fixture targets", http.StatusForbidden)
			return
		}
		if reject {
			http.Error(w, "controlled CONNECT failure", http.StatusBadGateway)
			return
		}
		target, err := net.DialTimeout("tcp4", origin, time.Second)
		if err != nil {
			http.Error(w, "loopback fixture unavailable", http.StatusBadGateway)
			return
		}
		defer target.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		traffic.Lock()
		traffic.connections[client], traffic.connections[target] = true, true
		traffic.Unlock()
		defer func() {
			traffic.Lock()
			delete(traffic.connections, client)
			delete(traffic.connections, target)
			traffic.Unlock()
		}()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if buffered.Flush() != nil {
			return
		}
		go func() { _, _ = io.Copy(target, buffered); _ = target.Close() }()
		_, _ = io.Copy(client, target)
	}))
	t.Cleanup(func() {
		server.Close()
		traffic.Lock()
		for connection := range traffic.connections {
			_ = connection.Close()
		}
		traffic.Unlock()
	})
	return server
}

func TestProxyBootstrapOfficialUpdateThroughConnect(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("candidate execution is restricted to isolated Linux amd64; needs a local Linux Go SDK for updateCLIBinaries")
	}
	// Serial: the CLI deliberately clones DefaultTransport for --proxy. Replace
	// that transport only in this test, retaining the production URL and TLS SNI.
	// The tiny version-command fixtures are built and executed only on Linux.
	binaries := updateCLIBinaries(t)
	archive := updateCLIArchive(t, binaries["0.6.0"])
	name := "nido-linux-amd64.tar.gz"
	assetBase := "https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/"
	sidecar := []byte(updateCLIHash(archive) + "  " + name + "\n")
	metadata, err := json.Marshal(map[string]any{"tag_name": "v0.6.0", "assets": []map[string]any{
		{"name": name, "browser_download_url": assetBase + name, "size": len(archive), "digest": "sha256:" + updateCLIHash(archive)},
		{"name": name + ".sha256", "browser_download_url": assetBase + name + ".sha256", "size": len(sidecar), "digest": "sha256:" + updateCLIHash(sidecar)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	previousVersion := Version
	Version = "0.5.0"
	t.Cleanup(func() { Version = previousVersion })
	for _, scenario := range []string{"success", "bad-certificate", "connect-502", "closed-listener"} {
		t.Run(scenario, func(t *testing.T) {
			traffic := &bootstrapTraffic{connections: map[net.Conn]bool{}}
			origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				traffic.Lock()
				traffic.requests = append(traffic.requests, request.Host+request.URL.RequestURI())
				traffic.Unlock()
				if request.TLS == nil || request.TLS.ServerName != request.Host || request.Header.Get("User-Agent") != "Nido" || request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Error("official Host/SNI/public headers were altered")
					http.Error(w, "invalid fixture request", http.StatusBadRequest)
					return
				}
				var data []byte
				switch request.Host + request.URL.RequestURI() {
				case "api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest", "api.github.com/repos/MiracleHYH/CDEToolkit/releases/tags/v0.6.0":
					data = metadata
				case "github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/" + name + ".sha256":
					data = sidecar
				case "github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/" + name:
					data = archive
				default:
					t.Error("file prefix or unexpected URL reached the official fixture")
					http.NotFound(w, request)
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
				_, _ = w.Write(data)
			}))
			listener := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, scenario == "connect-502")
			m, home := proxyBootstrapLocal(t, listener.URL)
			program := filepath.Join(m.Root, "bin", "nido")
			if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
				t.Fatal(err)
			}
			m.Executable = program
			m.executableInfo, err = os.Stat(program)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf"), filepath.Join(home, "certs", "private.key"), filepath.Join(home, ".cache", "pip", "keep.whl"), filepath.Join(home, ".conda", "envs", "keep", "data"), filepath.Join(m.Root, "user-data.txt"), filepath.Join(m.Root, "gds", "cache", "keep"), filepath.Join(m.Root, "proxy", "data", "keep")} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("protected bootstrap data: "+path), 0640); err != nil {
					t.Fatal(err)
				}
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			protected := proxyBootstrapSnapshot(t, program, m.Root, home)
			pool := x509.NewCertPool()
			if scenario != "bad-certificate" {
				pool.AddCert(certificate)
			}
			proxyAddress := listener.Listener.Addr().String()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				traffic.Lock()
				if address != proxyAddress {
					traffic.directDials++
					traffic.Unlock()
					return nil, fmt.Errorf("direct or file-prefix dialing is forbidden by this fixture")
				}
				traffic.proxyDials++
				traffic.Unlock()
				return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", proxyAddress)
			}
			previousTransport := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })
			if scenario == "closed-listener" {
				listener.Close()
			}
			var out, stderr bytes.Buffer
			cli := CLI{Out: &out, Err: &stderr, UpdateHTTP: &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
				t.Error("explicit --proxy was ignored in favor of the injected client")
				return nil, fmt.Errorf("explicit proxy required")
			})}}
			options := cliOptions{githubProxy: "direct", proxy: listener.URL, yes: true, json: true, check: scenario == "success"}
			if scenario == "success" {
				if code := cli.update(m, options, nil); code != 0 || !strings.Contains(out.String(), `"available":true`) {
					t.Fatalf("API check through CONNECT failed: %s %s", out.String(), stderr.String())
				}
				if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
					t.Fatal("version query changed the old program, configuration or data")
				}
				options.check = false
			}
			options.version = "0.6.0"
			out.Reset()
			stderr.Reset()
			code := cli.update(m, options, nil)
			if (code == 0) != (scenario == "success") {
				t.Fatalf("unexpected %s result: code=%d out=%s err=%s", scenario, code, out.String(), stderr.String())
			}
			if scenario == "success" {
				got, err := os.ReadFile(program)
				if err != nil || !bytes.Equal(got, binaries["0.6.0"]) || !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, program, m.Root, home)) {
					t.Fatal("verified update bytes or protected metadata/data changed", err)
				}
			} else if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
				t.Fatal("failed proxy/TLS operation changed the old program or protected data")
			}
			if stages, _ := filepath.Glob(filepath.Join(m.Root, "bin", ".nido-update-*")); len(stages) != 0 {
				t.Fatal("update candidate was left behind")
			}
			if runner := m.Proxy.Runner.(*cliProxyRunner); runner.starts != 1 || runner.stops != 0 {
				t.Fatalf("Nido update changed the running proxy lifecycle: starts=%d stops=%d", runner.starts, runner.stops)
			}
			traffic.Lock()
			defer traffic.Unlock()
			if traffic.directDials != 0 || traffic.proxyDials == 0 {
				t.Fatalf("explicit general proxy bypassed: proxy=%d direct=%d", traffic.proxyDials, traffic.directDials)
			}
			if scenario == "success" {
				wantRequests := []string{"api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest", "api.github.com/repos/MiracleHYH/CDEToolkit/releases/tags/v0.6.0", "github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/" + name + ".sha256", "github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/" + name}
				wantConnects := []string{"CONNECT api.github.com:443", "CONNECT api.github.com:443", "CONNECT github.com:443", "CONNECT github.com:443"}
				if !reflect.DeepEqual(traffic.requests, wantRequests) || !reflect.DeepEqual(traffic.connects, wantConnects) {
					t.Fatalf("official API/file traffic did not use CONNECT exactly: requests=%v connects=%v", traffic.requests, traffic.connects)
				}
			} else if len(traffic.requests) != 0 || scenario != "closed-listener" && len(traffic.connects) != 1 || scenario == "closed-listener" && len(traffic.connects) != 0 {
				t.Fatalf("failed listener/TLS operation reached an origin or retried: requests=%v connects=%v", traffic.requests, traffic.connects)
			}
		})
	}
}
