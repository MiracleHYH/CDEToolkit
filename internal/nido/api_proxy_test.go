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
	"testing"
	"time"
)

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
		if request.Host != request.TLS.ServerName {
			t.Error("managed route lost Host/SNI")
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
			t.Error("unexpected route", request.Host, request.URL)
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data)

	}))
	listener := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, false)
	m, home := proxyBootstrapLocal(t, listener.URL)
	runner := m.Proxy.Runner.(*cliProxyRunner)
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
	if len(files) != 0 || connectCount != 4 || requestCount != 4 || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("API/files crossed routes or proxy lifecycle changed", files, connectCount, requestCount)
	}
	listener.Close()
	before = proxyBootstrapSnapshot(t, "", m.Root, home)
	if code := cli.update(m, cliOptions{check: true, json: true}, nil); code == 0 {
		t.Fatal("closed API listener unexpectedly succeeded")
	}
	if len(files) != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) || runner.starts != 1 || runner.stops != 0 {
		t.Fatal("API failure used file fallback or changed program/config/data/lifecycle")
	}
}
