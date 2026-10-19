package githubdownload

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var officialFileTLSHosts = []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com"}

// This exercises the current downloader rather than the removed component-
// bound clients. All DNS is replaced with a private TLS origin; no core runs.
func githubFileTLSFixture(t *testing.T, names []string, trust bool) (*http.Client, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.TLS == nil || r.TLS.ServerName != r.Host {
			t.Error("redirect lost Host/SNI identity", r.Host, r.TLS)
		}
		for i, host := range officialFileTLSHosts {
			if r.Host != host {
				continue
			}
			if i+1 < len(officialFileTLSHosts) {
				w.Header().Set("Location", "https://"+officialFileTLSHosts[i+1]+"/asset?jwt=public-signature")
				w.WriteHeader(http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, "verified public asset")
			return
		}
		t.Error("unapproved official host reached origin", r.Host)
		http.NotFound(w, r)
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	origin.Config.ErrorLog = log.New(io.Discard, "", 0)
	origin.StartTLS()
	t.Cleanup(origin.Close)
	pool := x509.NewCertPool()
	if trust {
		pool.AddCert(certificate)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		allowed := false
		for _, host := range officialFileTLSHosts {
			allowed = allowed || address == host+":443"
		}
		if !allowed {
			return nil, fmt.Errorf("private TLS fixture rejects unexpected dial")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}, &requests
}

func TestCurrentFileDownloaderVerifiesEveryOfficialRedirectTLSIdentity(t *testing.T) {
	client, requests := githubFileTLSFixture(t, officialFileTLSHosts, true)
	data, err := Get(client, "https://github.com/a/b/releases/download/v1/asset", 4096, "direct")
	if err != nil || string(data) != "verified public asset" || requests.Load() != int32(len(officialFileTLSHosts)) {
		t.Fatal("official redirect failed", string(data), requests.Load(), err)
	}
}

func TestCurrentFileDownloaderRejectsUntrustedAndWrongHostnameTLS(t *testing.T) {
	for _, trust := range []bool{false, true} {
		t.Run(fmt.Sprint(trust), func(t *testing.T) {
			client, requests := githubFileTLSFixture(t, []string{"api.github.com"}, trust)
			data, err := Get(client, "https://github.com/a/b/releases/download/v1/asset", 4096, "direct")
			if err == nil || len(data) != 0 || requests.Load() != 0 {
				t.Fatal("invalid TLS accepted", string(data), requests.Load(), err)
			}
			if strings.Contains(err.Error(), "api.github.com") {
				t.Fatal("TLS diagnostic leaked origin name", err)
			}
		})
	}
}
