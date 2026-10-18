package nido

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
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
	// HTTPS proxies have a separate TLS hop. Let Transport derive the name for
	// each hop instead of applying an inherited ServerName to both connections.
	// The request boundary below fixes the final hop to api.github.com.
	transport.TLSClientConfig.ServerName = ""
	transport.Proxy = http.ProxyURL(address)
	return &http.Client{
		Transport: scopedAPITransport{base: transport},
		Timeout:   2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

type scopedAPITransport struct{ base *http.Transport }

func (t scopedAPITransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := proxy.CheckAPIRequest(request); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(request)
}

func (t scopedAPITransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
