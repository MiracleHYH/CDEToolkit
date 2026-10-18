package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The only TCP listeners in these tests are loopback HTTP CONNECT and TLS
// fixtures. The fake runner records readiness without launching a core.
func apiClientFixture(t *testing.T, handler http.Handler, trustCertificate bool) (*Service, *runtimeFakeRunner, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "api.github.com"},
		DNSNames: []string{"api.github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewUnstartedServer(handler)
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	origin.StartTLS()
	t.Cleanup(origin.Close)
	var connects atomic.Int32
	var connectionsMu sync.Mutex
	var connections []net.Conn
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		if r.Method != http.MethodConnect || r.Host != "api.github.com:443" {
			t.Errorf("unexpected proxy request: %s %s", r.Method, r.Host)
			http.Error(w, "unexpected target", http.StatusBadGateway)
			return
		}
		upstream, dialErr := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if dialErr != nil {
			http.Error(w, "private fixture unavailable", http.StatusBadGateway)
			return
		}
		connection, buffered, hijackErr := w.(http.Hijacker).Hijack()
		if hijackErr != nil {
			upstream.Close()
			return
		}
		connectionsMu.Lock()
		connections = append(connections, connection, upstream)
		connectionsMu.Unlock()
		defer connection.Close()
		defer upstream.Close()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		go func() {
			_, _ = io.Copy(upstream, buffered)
			upstream.Close()
		}()
		_, _ = io.Copy(connection, upstream)
	}))
	t.Cleanup(func() {
		connectionsMu.Lock()
		for _, connection := range connections {
			connection.Close()
		}
		connectionsMu.Unlock()
		proxyServer.Close()
	})
	proxyURL, err := url.Parse(proxyServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport)
	transport := base.Clone()
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool()}
	if trustCertificate {
		transport.TLSClientConfig.RootCAs.AddCert(leaf)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = base; transport.CloseIdleConnections() })
	runner := &runtimeFakeRunner{ready: true}
	service := runtimeService(t, runner)
	path, _ := service.ConfigPath()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: %s}]\n", proxyURL.Port())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	return service, runner, &connects
}

func TestAPIClientUsesSelectedListenerDespiteEnvironment(t *testing.T) {
	var requests atomic.Int32
	service, runner, connects := apiClientFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Host != "api.github.com" || r.TLS == nil || r.TLS.ServerName != "api.github.com" || r.URL.Path != "/repos/owner/repository/releases/latest" {
			t.Errorf("official API identity lost: host=%s tls=%+v path=%s", r.Host, r.TLS, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"tag_name":"v1.0.0"}`)
	}), true)
	t.Setenv("HTTPS_PROXY", "http://must-not-be-used.invalid:1")
	t.Setenv("NO_PROXY", "*")
	client, err := service.APIClient("api")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	if client.Timeout <= 0 || client.Timeout > 2*time.Minute {
		t.Fatalf("unbounded client timeout: %v", client.Timeout)
	}
	response, err := client.Get("https://api.github.com/repos/owner/repository/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || string(body) != `{"tag_name":"v1.0.0"}` || connects.Load() != 1 || requests.Load() != 1 {
		t.Fatalf("body=%q read=%v close=%v CONNECT=%d requests=%d", body, readErr, closeErr, connects.Load(), requests.Load())
	}
	if runner.starts != 1 || runner.stops != 0 {
		t.Fatalf("API request changed process lifecycle: starts=%d stops=%d", runner.starts, runner.stops)
	}
}

func TestAPIClientRefusesNonAPIURLsAndRedirects(t *testing.T) {
	service, _, connects := apiClientFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://github.com/owner/repository/releases/download/v1.0.0/file")
		w.WriteHeader(http.StatusFound)
	}), true)
	client, err := service.APIClient("api")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	for _, target := range []string{
		"http://api.github.com/repos/a/b/releases/latest", "https://github.com/file", "https://api.github.com.invalid/", "https://api.github.com:444/", "https://user:secret@api.github.com/", "https://api.github.com/#secret",
	} {
		if response, err := client.Get(target); err == nil {
			response.Body.Close()
			t.Fatalf("unexpected target accepted: %q", target)
		}
	}
	// The standard request constructor normalizes an empty HTTPS port to the
	// equivalent default-port URL. Build a raw request for the transport check;
	// accepting the normalized URL through Client does not widen API routing.
	emptyPort, err := url.Parse("https://api.github.com:/")
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Transport.RoundTrip(&http.Request{Method: http.MethodGet, URL: emptyPort, Host: emptyPort.Host, Header: make(http.Header)}); err == nil {
		response.Body.Close()
		t.Fatal("raw empty port accepted by API transport")
	}
	request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
	request.Host = "another.invalid"
	if _, err := client.Do(request); err == nil {
		t.Fatal("custom Host accepted")
	}
	if connects.Load() != 0 {
		t.Fatalf("invalid targets reached proxy: %d", connects.Load())
	}
	// githubdownload installs its own redirect callback. The transport must
	// retain the API-only restriction independently of that callback.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	if response, err := client.Get("https://api.github.com/repos/a/b/releases/latest"); err == nil {
		response.Body.Close()
		t.Fatal("API redirect reached file host")
	}
	if connects.Load() != 1 {
		t.Fatalf("redirect contacted another host: CONNECT=%d", connects.Load())
	}
}

func TestAPIClientKeepsTLSVerification(t *testing.T) {
	service, _, connects := apiClientFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS request reached origin handler")
	}), false)
	client, err := service.APIClient("api")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	if response, err := client.Get("https://api.github.com/"); err == nil {
		response.Body.Close()
		t.Fatal("untrusted API certificate accepted")
	}
	if connects.Load() != 1 {
		t.Fatalf("selected proxy was not used: CONNECT=%d", connects.Load())
	}
}

func TestAPIClientRequiresNamedUnauthenticatedReadyListener(t *testing.T) {
	for _, test := range []struct {
		name, config, selection string
		start                   bool
	}{
		{"empty name", "listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: 17891}]\n", "", true},
		{"traditional port", "mixed-port: 17891\n", "mixed", true},
		{"missing name", "listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: 17891}]\n", "missing", true},
		{"authentication", "listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: 17891, users: [{username: user, password: secret}]}]\n", "api", true},
		{"not started", "listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: 17891}]\n", "api", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: true}
			service := runtimeService(t, runner)
			path, _ := service.ConfigPath()
			if err := os.WriteFile(path, []byte(test.config), 0600); err != nil {
				t.Fatal(err)
			}
			if test.start {
				if err := service.Start(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := service.APIClient(test.selection); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid selection: %v", err)
			}
			if runner.stops != 0 || runner.starts > 1 {
				t.Fatal("API selection changed process lifecycle")
			}
		})
	}
}

func TestAPIClientRejectsChangeBeforeRequest(t *testing.T) {
	for _, change := range []string{"record", "configuration", "program", "stopped", "unknown", "ownership", "record during ownership", "configuration during ownership"} {
		t.Run(change, func(t *testing.T) {
			service, runner, connects := apiClientFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("changed instance reached API")
			}), true)
			client, err := service.APIClient("api")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.CloseIdleConnections)
			configPath, _ := service.ConfigPath()
			recordPath, _ := service.recordPath()
			record, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatal(err)
			}
			mutateRecord := func() {
				if err := atomicWrite(recordPath, record, 0600); err != nil {
					t.Fatal(err)
				}
			}
			mutateConfig := func() {
				if err := os.WriteFile(configPath, []byte("listeners: [{name: api, type: mixed, listen: 127.0.0.1, port: 17892}]\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "record":
				mutateRecord()
			case "configuration":
				mutateConfig()
			case "program":
				_, program, _ := service.paths()
				if err := os.WriteFile(program, []byte("changed program"), 0700); err != nil {
					t.Fatal(err)
				}
			case "stopped":
				runner.alive = false
			case "unknown":
				runner.inspectErr = fmt.Errorf("unknown process")
			case "ownership":
				runner.ready = false
			case "record during ownership":
				runner.onOwns = mutateRecord
			case "configuration during ownership":
				runner.onOwns = mutateConfig
			}
			if response, err := client.Get("https://api.github.com/"); err == nil {
				response.Body.Close()
				t.Fatal("changed instance was accepted")
			}
			if connects.Load() != 0 || runner.starts != 1 || runner.stops != 0 {
				t.Fatalf("changed instance caused network or lifecycle effects: CONNECT=%d starts=%d stops=%d", connects.Load(), runner.starts, runner.stops)
			}
		})
	}
}

func TestAPIClientChecksInstanceAtEOFAndClose(t *testing.T) {
	for _, change := range []string{"record", "configuration", "program", "stopped", "unknown", "ownership"} {
		for _, closeBeforeEOF := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/close_before_eof=%v", change, closeBeforeEOF), func(t *testing.T) {
				service, runner, connects := apiClientFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.WriteString(w, "private fixture body")
				}), true)
				client, err := service.APIClient("api")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.CloseIdleConnections)
				response, err := client.Get("https://api.github.com/")
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "record":
					path, _ := service.recordPath()
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := atomicWrite(path, data, 0600); err != nil {
						t.Fatal(err)
					}
				case "configuration":
					path, _ := service.ConfigPath()
					if err := os.WriteFile(path, []byte("mixed-port: 17892\n"), 0600); err != nil {
						t.Fatal(err)
					}
				case "program":
					_, program, _ := service.paths()
					if err := os.WriteFile(program, []byte("changed program"), 0700); err != nil {
						t.Fatal(err)
					}
				case "stopped":
					runner.alive = false
				case "unknown":
					runner.inspectErr = fmt.Errorf("unknown process")
				case "ownership":
					runner.ready = false
				}
				if !closeBeforeEOF {
					if _, err := io.ReadAll(response.Body); err == nil {
						t.Fatal("EOF accepted changed instance")
					}
				}
				if err := response.Body.Close(); err == nil {
					t.Fatal("Close accepted changed instance")
				}
				if connects.Load() != 1 || runner.starts != 1 || runner.stops != 0 {
					t.Fatal("body validation changed network or lifecycle")
				}
			})
		}
	}
}
