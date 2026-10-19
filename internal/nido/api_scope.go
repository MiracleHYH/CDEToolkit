package nido

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

func scopedAPIClient(address *url.URL) (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("无法创建 Nido API 代理传输")
	}
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = false
	transport.TLSClientConfig.ServerName = ""
	transport.Proxy = nil
	if address != nil {
		transport.Proxy = http.ProxyURL(address)
	}
	return &http.Client{Transport: scopedAPITransport{base: transport}, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// GitHub API URLs remain official. File acceleration is never applied here.
func (m *Manager) networkAPIClient(_ string, explicit string) (*http.Client, error) {
	address, err := m.generalProxyURL(explicit)
	if err != nil {
		return nil, err
	}
	var parsed *url.URL
	if address != "" {
		parsed, _ = url.Parse(address)
	}
	client, err := scopedAPIClient(parsed)
	if err != nil {
		return nil, err
	}
	client.Transport = apiBindingTransport{base: client.Transport, check: m.networkCheck()}
	return client, nil
}

// Codex's foreground official installer and metadata use Nido's temporary
// general proxy; Codex's persistent runtime .env is an independent setting.
func (m *Manager) codexAPIClient(explicit string) (*http.Client, error) {
	return m.networkAPIClient("", explicit)
}

type scopedAPITransport struct{ base *http.Transport }

func (t scopedAPITransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := githubdownload.CheckAPIRequest(request); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}
func (t scopedAPITransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
