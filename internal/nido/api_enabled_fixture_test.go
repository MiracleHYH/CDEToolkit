package nido

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// enabledAPIFixture preserves the real CLI's enablement gate and proxy/TLS
// path. Only the final official-name origin is replaced by an existing fixture
// transport. It never starts a core, accesses the public network, or configures
// proxy variables for another process. Callers must pass the returned URL as
// their one-off --proxy after this explicit, private nonempty binding exists.
func enabledAPIFixture(t *testing.T, m *Manager, client *http.Client) string {
	t.Helper()
	if m.configBytes == nil || client == nil || client.Transport == nil {
		t.Fatal("enabled API fixture requires an initialized root and explicit transport")
	}
	if err := os.WriteFile(filepath.Join(m.Root, apiProxyFile), []byte("schema = 1\nproduct = \"nido-api-proxy\"\nlistener = \"unit-api\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureTransport := client.Transport
	origin, certificate := proxyBootstrapTLS(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.TLS == nil || request.TLS.ServerName != request.Host || request.Host != "api.github.com" && request.Host != "github.com" {
			t.Error("enabled API fixture lost the official Host or TLS SNI")
			http.Error(w, "unexpected origin", http.StatusForbidden)
			return
		}
		canonical := request.Clone(request.Context())
		canonical.URL.Scheme = "https"
		canonical.URL.Host = request.Host
		canonical.RequestURI = ""
		response, err := fixtureTransport.RoundTrip(canonical)
		if err != nil {
			http.Error(w, "controlled fixture transport error", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if response.ContentLength > 0 {
			w.Header().Set("Content-Length", fmt.Sprint(response.ContentLength))
		}
		w.WriteHeader(response.StatusCode)
		if _, err := io.Copy(w, response.Body); err != nil {
			t.Errorf("enabled API fixture body transfer failed: %v", err)
		}
	}))
	traffic := &bootstrapTraffic{connections: map[net.Conn]bool{}}
	connect := proxyBootstrapConnect(t, origin.Listener.Addr().String(), traffic, false)
	proxyAddress := connect.Listener.Addr().String()
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		traffic.Lock()
		if address != proxyAddress {
			traffic.directDials++
			traffic.Unlock()
			return nil, fmt.Errorf("enabled API fixture forbids direct or external dialing")
		}
		traffic.proxyDials++
		traffic.Unlock()
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp4", proxyAddress)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		http.DefaultTransport = previous
		traffic.Lock()
		defer traffic.Unlock()
		if traffic.directDials != 0 {
			t.Errorf("enabled API fixture attempted %d direct or external dials", traffic.directDials)
		}
	})
	return connect.URL
}
