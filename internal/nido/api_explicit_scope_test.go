package nido

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

func explicitAPIScopeFixture(t *testing.T, handler http.Handler, httpsProxy, trustAPI bool) (*Manager, string, *http.Client, *bootstrapTraffic) {
	t.Helper()
	m, home := fixture(t)
	if err := m.InitWithDownload("https://file-only.invalid"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.Root, "proxy"), 0700); err != nil {
		t.Fatal(err)
	}
	writeBindingListeners(t, m, bindingListeners)
	endpoint, err := m.Proxy.Endpoint("arbitrary-daily")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.configureAPIProxy(endpoint.URL(), &endpoint); err != nil {
		t.Fatal(err)
	}
	// This temporary route does not launch or require the bound core. Only the
	// private fixture's HTTP(S) CONNECT proxy can open an origin connection.
	traffic := &bootstrapTraffic{connections: map[net.Conn]bool{}}
	origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traffic.Lock()
		traffic.requests = append(traffic.requests, r.Host+r.URL.RequestURI())
		traffic.Unlock()
		if r.Host != "api.github.com" || r.TLS == nil || r.TLS.ServerName != "api.github.com" {
			t.Errorf("explicit API route lost official Host/SNI: Host=%s TLS=%+v", r.Host, r.TLS)
		}
		handler.ServeHTTP(w, r)
	}))
	plainProxy := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, false)
	proxyURL := plainProxy.URL
	pool := x509.NewCertPool()
	if trustAPI {
		pool.AddCert(certificate)
	}
	if httpsProxy {
		secureProxy := httptest.NewTLSServer(plainProxy.Config.Handler)
		t.Cleanup(secureProxy.Close)
		pool.AddCert(secureProxy.Certificate())
		proxyURL = secureProxy.URL
	}
	address, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	// Both values must be corrected in the private clone. A fixed API SNI
	// applied to the HTTPS proxy's separate TLS hop would reject its valid cert.
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, InsecureSkipVerify: true, ServerName: "inherited.invalid"}
	transport.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		traffic.Lock()
		if destination != address.Host {
			traffic.directDials++
			traffic.Unlock()
			return nil, fmt.Errorf("only the explicitly chosen loopback proxy is allowed")
		}
		traffic.proxyDials++
		traffic.Unlock()
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address.Host)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	client, err := m.downloadAPIClient(proxyURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	if !transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.ServerName != "inherited.invalid" {
		t.Fatal("explicit API client changed the shared transport's TLS configuration")
	}
	if client.Timeout <= 0 || client.Timeout > 2*time.Minute {
		t.Fatalf("explicit API timeout is unbounded: %s", client.Timeout)
	}
	return m, home, client, traffic
}

func TestAPIProxyExplicitRouteEnforcesOfficialAPIBoundary(t *testing.T) {
	for _, httpsProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("https_proxy=%v", httpsProxy), func(t *testing.T) {
			m, home, client, traffic := explicitAPIScopeFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect" {
					w.Header().Set("Location", "https://github.com/owner/repo/releases/download/v1.0.0/file")
					w.WriteHeader(http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, `{"tag_name":"v1.0.0"}`)
			}), httpsProxy, true)
			t.Setenv("NO_PROXY", "*")
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			body, err := githubdownload.Get(client, "https://api.github.com/ok", 4096, "direct")
			if err != nil || string(body) != `{"tag_name":"v1.0.0"}` {
				t.Fatalf("official API through explicit proxy failed: body=%s err=%v", body, err)
			}
			for _, target := range []string{
				"http://api.github.com/", "https://api.github.com:444/", "https://api.github.com:/", "https://api.github.com.invalid/", "https://github.com/file", "https://user:secret@api.github.com/", "https://api.github.com/#fragment",
			} {
				parsed, err := url.Parse(target)
				if err != nil {
					t.Fatal(err)
				}
				// Preserve the empty port too: NewRequest normalizes it before
				// RoundTrip, so a raw request tests the transport's URL boundary.
				request := &http.Request{Method: http.MethodGet, URL: parsed, Host: parsed.Host, Header: make(http.Header)}
				if response, err := client.Transport.RoundTrip(request); err == nil {
					response.Body.Close()
					t.Fatalf("explicit API client accepted a non-API URL: %s", target)
				}
			}
			request, _ := http.NewRequest(http.MethodGet, "https://api.github.com/", nil)
			request.Host = "another.invalid"
			if response, err := client.Do(request); err == nil {
				response.Body.Close()
				t.Fatal("explicit API client accepted a custom Host")
			}
			// Get replaces CheckRedirect and accepts official file hosts. The
			// API-only RoundTripper must retain the narrower boundary regardless.
			if _, err := githubdownload.Get(client, "https://api.github.com/redirect", 4096, "direct"); err == nil {
				t.Fatal("explicit API client followed a redirect to a file host")
			}
			traffic.Lock()
			connects, requests := append([]string(nil), traffic.connects...), append([]string(nil), traffic.requests...)
			direct := traffic.directDials
			traffic.Unlock()
			if !reflect.DeepEqual(connects, []string{"CONNECT api.github.com:443", "CONNECT api.github.com:443"}) || !reflect.DeepEqual(requests, []string{"api.github.com/ok", "api.github.com/redirect"}) || direct != 0 {
				t.Fatalf("explicit API route contacted another host or bypassed proxy: CONNECT=%v requests=%v direct=%d", connects, requests, direct)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
				t.Fatal("explicit API route changed the binding or other configuration/data")
			}
		})
	}
}

func TestAPIProxyExplicitRouteRejectsUntrustedAPI(t *testing.T) {
	for _, httpsProxy := range []bool{false, true} {
		t.Run(fmt.Sprintf("https_proxy=%v", httpsProxy), func(t *testing.T) {
			m, home, client, traffic := explicitAPIScopeFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("explicit API client accepted an untrusted origin certificate")
			}), httpsProxy, false)
			before := proxyBootstrapSnapshot(t, "", m.Root, home)
			if _, err := githubdownload.Get(client, "https://api.github.com/", 4096, "direct"); err == nil || strings.Contains(err.Error(), "inherited.invalid") {
				t.Fatalf("untrusted API certificate did not fail safely: %v", err)
			}
			traffic.Lock()
			connects, requests, direct := len(traffic.connects), len(traffic.requests), traffic.directDials
			traffic.Unlock()
			if connects != 1 || requests != 0 || direct != 0 {
				t.Fatalf("untrusted API route fell back: CONNECT=%d requests=%d direct=%d", connects, requests, direct)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
				t.Fatal("TLS failure changed the binding or other configuration/data")
			}
		})
	}
}
