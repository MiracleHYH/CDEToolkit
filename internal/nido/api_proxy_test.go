package nido

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAPIProxyBindingPersistsAndProtectsOtherTools(t *testing.T) {
	m, _, _ := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	before := proxyBootstrapSnapshot(t, filepath.Join(m.Root, apiProxyFile), m.Root, m.UserHome)
	runner := &cliProxyRunner{}
	if code, _, stderr := proxyCLI(t, m, runner, "1\ny\n", "proxy", "bind", "download"); code != 0 {
		t.Fatal(stderr)
	}
	loaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	config, snapshot, err := loaded.readAPIProxy()
	if err != nil || config.Listener != "renamed-ai" || snapshot.info.Mode().Perm() != 0600 {
		t.Fatal("listener selection did not persist privately", config, err)
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, snapshot.path, m.Root, m.UserHome)) {
		t.Fatal("API binding changed main config, environment, or external application data")
	}
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr}
	if code := cli.download(loaded, cliOptions{json: true}, []string{"show"}); code != 0 || !strings.Contains(out.String(), `"api_listener":"renamed-ai"`) {
		t.Fatalf("offline API binding display failed: %d %s %s", code, out.String(), stderr.String())
	}
	if code, _, stderr := proxyCLI(t, loaded, runner, "\n", "proxy", "bind", "download"); code != 0 {
		t.Fatal(stderr)
	}
	if err := snapshot.check(); err != nil {
		t.Fatal("keep rewrote the binding", err)
	}
	if code, _, stderr := proxyCLI(t, loaded, runner, "", "proxy", "bind", "download", "--proxy", "off", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	config, _, err = loaded.readAPIProxy()
	if err != nil || config.Listener != "" {
		t.Fatal("off did not clear only the internal API binding", err)
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, snapshot.path, m.Root, m.UserHome)) || runner.starts != 0 || len(runner.commands) != 0 {
		t.Fatal("binding management invoked a core or changed unrelated state")
	}
}

func TestAPIProxyBindingRefusesUnknownOrUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"unknown-content", "public-permission", "hardlink", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, bindingListeners)
			path := filepath.Join(m.Root, apiProxyFile)
			data := []byte("schema=1\nproduct='nido-api-proxy'\nlistener='renamed-ai'\n")
			if kind == "unknown-content" {
				data = []byte("USER-OWNED-DATA")
			} else if kind == "oversized" {
				data = bytes.Repeat([]byte("#"), 4097)
			}
			mode := os.FileMode(0600)
			if kind == "public-permission" {
				mode = 0644
			}
			if kind == "symlink" {
				target := filepath.Join(m.UserHome, "keep-api-file")
				if err := os.WriteFile(target, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, data, mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
				if kind == "hardlink" {
					if err := os.Link(path, filepath.Join(m.UserHome, "keep-api-file")); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			for _, args := range [][]string{{"--endpoint", "arbitrary-daily"}, {"--proxy", "off"}, {"--endpoint", "arbitrary-daily", "--dry-run"}} {
				command := append([]string{"proxy", "bind", "download"}, args...)
				command = append(command, "--yes")
				if code, _, _ := proxyCLI(t, m, &cliProxyRunner{}, "", command...); code == 0 {
					t.Fatal("unsafe or unknown file was adopted", kind)
				}
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("refused binding changed unknown files or application data")
			}
		})
	}
}

func TestAPIProxyBindingRejectsUnsupportedAndStaleSelections(t *testing.T) {
	m, _, _ := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
	for _, args := range [][]string{{"--proxy", "http://127.0.0.1:17890", "--yes"}, {"--endpoint", "missing", "--yes"}, {"--yes"}} {
		if code, _, _ := proxyCLI(t, m, &cliProxyRunner{}, "", append([]string{"proxy", "bind", "download"}, args...)...); code == 0 {
			t.Fatal("invalid API listener selection accepted", args)
		}
	}
	var out, stderr bytes.Buffer
	cli := CLI{In: &beforeRead{reader: strings.NewReader("y\n"), before: func() {
		writeBindingListeners(t, m, strings.ReplaceAll(bindingListeners, "17890", "17899"))
	}}, Out: &out, Err: &stderr}
	if code := cli.Run([]string{"--root", m.Root, "proxy", "bind", "download", "--endpoint", "renamed-ai"}); code == 0 {
		t.Fatal("listener changed during confirmation was accepted")
	}
	writeBindingListeners(t, m, bindingListeners)
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
		t.Fatal("rejected selection changed unrelated files")
	}
	writeBindingListeners(t, m, "listeners: [{name: auth, type: http, listen: 127.0.0.1, port: 17890, users: [{username: x, password: y}]}]\n")
	if code, _, _ := proxyCLI(t, m, &cliProxyRunner{}, "", "proxy", "bind", "download", "--endpoint", "auth", "--yes"); code == 0 {
		t.Fatal("authenticated listener was persisted without credentials")
	}
}

func TestAPIProxyLocalAndDryRunIgnoreBrokenBinding(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, apiProxyFile), []byte("USER-OWNED-UNKNOWN-DATA"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{ready: true}
	artifact := proxyCLIArtifact(t)
	for _, args := range [][]string{
		{"proxy", "status", "--json"},
		{"proxy", "install", "--artifact", artifact, "--dry-run", "--json"},
		{"proxy", "update", "--check", "--dry-run", "--json"},
		{"gds", "install", "--artifact", artifact, "--dry-run", "--json"},
	} {
		before := proxyBootstrapSnapshot(t, "", m.Root, home)
		if code, _, stderr := proxyCLI(t, m, runner, "", args...); code != 0 {
			t.Fatalf("offline operation consulted broken API binding: %v %s", args, stderr)
		}
		if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
			t.Fatal("read-only operation changed files")
		}
	}
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "install", "--artifact", artifact, "--yes"); code != 0 {
		t.Fatal("local bootstrap consulted API binding", stderr)
	}
	if client, err := m.downloadAPIClient(""); err == nil || client != nil {
		t.Fatal("online operation ignored unknown API binding")
	}
	client, err := m.downloadAPIClient("http://127.0.0.1:1")
	if err == nil || client != nil {
		t.Fatal("explicit temporary override bypassed invalid API binding")
	}
	if runner.starts != 0 {
		t.Fatal("local install automatically started a core")
	}
}

func TestAPIProxyBindingAutomaticUpdate(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("candidate execution requires isolated Linux amd64")
	}
	binaries := updateCLIBinaries(t)
	archive := updateCLIArchive(t, binaries["0.6.0"])
	name := "nido-linux-amd64.tar.gz"
	assetBase := "https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/"
	sidecar := []byte(updateCLIHash(archive) + "  " + name + "\n")
	metadata, _ := json.Marshal(map[string]any{"tag_name": "v0.6.0", "assets": []map[string]any{
		{"name": name, "browser_download_url": assetBase + name, "size": len(archive), "digest": "sha256:" + updateCLIHash(archive)},
		{"name": name + ".sha256", "browser_download_url": assetBase + name + ".sha256", "size": len(sidecar), "digest": "sha256:" + updateCLIHash(sidecar)},
	}})
	traffic := &bootstrapTraffic{connections: map[net.Conn]bool{}}
	origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		traffic.Lock()
		traffic.requests = append(traffic.requests, request.Host+request.URL.RequestURI())
		traffic.Unlock()
		if request.Host != "api.github.com" || request.TLS.ServerName != "api.github.com" {
			t.Error("automatic API binding lost official Host/SNI")
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(metadata)))
		_, _ = w.Write(metadata)
	}))
	listener := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, false)
	m, home := proxyBootstrapLocal(t, listener.URL)
	runner := m.Proxy.Runner.(*cliProxyRunner)
	if code, _, stderr := proxyCLI(t, m, runner, "1\ny\n", "proxy", "bind", "download"); code != 0 {
		t.Fatal(stderr)
	}
	// A fresh Manager proves persistence, rather than in-memory flag reuse.
	m, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	m.Proxy.Runner = runner
	program := filepath.Join(m.Root, "bin", "nido")
	if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
		t.Fatal(err)
	}
	m.Executable, m.executableInfo = program, nil
	m.executableInfo, err = os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	proxyAddress := listener.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != proxyAddress {
			t.Errorf("automatic API proxy bypass attempted: %s", address)
			return nil, fmt.Errorf("only selected loopback proxy is allowed")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", proxyAddress)
	}
	previousTransport, previousVersion := http.DefaultTransport, Version
	http.DefaultTransport, Version = transport, "0.5.0"
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport, Version = previousTransport, previousVersion
	})
	t.Setenv("NO_PROXY", "*")
	var files []string
	client := &http.Client{Transport: updateCLITransport(func(request *http.Request) (*http.Response, error) {
		files = append(files, request.URL.String())
		var data []byte
		switch request.URL.String() {
		case "https://saved-file-only.invalid/" + assetBase + name + ".sha256":
			data = sidecar
		case "https://saved-file-only.invalid/" + assetBase + name:
			data = archive
		default:
			t.Errorf("API or unexpected asset reached file downloader: %s", request.URL)
			return nil, fmt.Errorf("unexpected file request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
	})}
	var out, stderr bytes.Buffer
	cli := CLI{Out: &out, Err: &stderr, UpdateHTTP: client}
	before := proxyBootstrapSnapshot(t, "", m.Root, home)
	if code := cli.update(m, cliOptions{check: true, json: true}, nil); code != 0 {
		t.Fatalf("automatic API check failed: %s %s", out.String(), stderr.String())
	}
	if len(files) != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
		t.Fatal("API check downloaded files or changed configuration/data")
	}
	protected := proxyBootstrapSnapshot(t, program, m.Root, home)
	if code := cli.update(m, cliOptions{version: "0.6.0", yes: true, json: true}, nil); code != 0 {
		t.Fatalf("automatic API update failed: %s %s", out.String(), stderr.String())
	}
	if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, binaries["0.6.0"]) || !reflect.DeepEqual(protected, proxyBootstrapSnapshot(t, program, m.Root, home)) {
		t.Fatal("automatic update changed protected state or installed wrong candidate", err)
	}
	traffic.Lock()
	connectCount, requestCount := len(traffic.connects), len(traffic.requests)
	traffic.Unlock()
	if len(files) != 2 || connectCount != 2 || requestCount != 2 || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("API/files crossed routes or proxy lifecycle changed", files, connectCount, requestCount)
	}
	listener.Close()
	before = proxyBootstrapSnapshot(t, "", m.Root, home)
	if code := cli.update(m, cliOptions{check: true, json: true}, nil); code == 0 {
		t.Fatal("closed API listener unexpectedly succeeded")
	}
	if len(files) != 2 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("API failure used file fallback or changed program/config/data/lifecycle")
	}
}
