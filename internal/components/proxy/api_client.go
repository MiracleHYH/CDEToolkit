package proxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIClient binds Nido's GitHub API requests to a named, unauthenticated
// listener on the currently observed managed instance. It neither exports a
// process-wide proxy nor starts the component. Callers must propagate body read
// and Close errors: either can report an instance change during the request.
func (s *Service) APIClient(name string) (*http.Client, error) {
	if name == "" {
		return nil, fmt.Errorf("Nido API 代理必须选择命名 listener")
	}
	endpoints, err := s.ListenerEndpoints()
	if err != nil {
		return nil, err
	}
	var endpoint Endpoint
	found := false
	for _, candidate := range endpoints {
		if candidate.Name == name {
			endpoint, found = candidate, true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("未找到指定的 Nido API 代理 listener")
	}
	if endpoint.Authentication {
		return nil, fmt.Errorf("Nido API 代理 listener 要求认证；不能使用无凭据客户端")
	}
	_, record, err := s.readRecordSnapshot()
	if err != nil {
		return nil, err
	}
	check := func() error {
		if err := record.check(); err != nil {
			return fmt.Errorf("Nido API 请求期间 Proxy 进程记录发生变化")
		}
		current, err := s.Endpoint(name)
		if err != nil || current != endpoint || current.Authentication {
			return fmt.Errorf("Nido API 代理 listener 配置发生变化")
		}
		status, err := s.Runtime()
		if err != nil {
			return err
		}
		if status.State != "running" || !status.Listeners || status.ConfigChanged || status.ConfigHash != endpoint.ConfigDigest {
			return fmt.Errorf("Nido API 代理 listener 尚未就绪或配置已变化")
		}
		current, err = s.Endpoint(name)
		if err != nil || current != endpoint || current.Authentication {
			return fmt.Errorf("Nido API 代理 listener 配置发生变化")
		}
		if err := record.check(); err != nil {
			return fmt.Errorf("Nido API 请求期间 Proxy 进程记录发生变化")
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("无法创建 Nido API 代理传输")
	}
	transport := base.Clone()
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	} else {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = false
	transport.TLSClientConfig.ServerName = "api.github.com"
	proxyURL, err := url.Parse(endpoint.URL())
	if err != nil {
		return nil, fmt.Errorf("Nido API 代理 listener 地址无效")
	}
	// An explicit ProxyURL deliberately bypasses ProxyFromEnvironment: NO_PROXY
	// must not silently turn a selected listener into a direct API request.
	transport.Proxy = http.ProxyURL(proxyURL)
	return &http.Client{
		Transport: &apiTransport{transport: transport, check: check},
		Timeout:   2 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

type apiTransport struct {
	transport *http.Transport
	check     func() error
}

func (t *apiTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// Keep this check in RoundTrip too: download helpers may replace the
	// client's CheckRedirect callback. This client never serves release files
	// or arbitrary user requests, including redirects away from the API host.
	if err := CheckAPIRequest(request); err != nil {
		return nil, err
	}
	if err := t.check(); err != nil {
		return nil, err
	}
	response, err := t.transport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	response.Body = &apiResponseBody{ReadCloser: response.Body, check: t.check}
	return response, nil
}

// CheckAPIRequest retains the same official API boundary for a managed listener
// and an explicit, temporary network proxy. It belongs in RoundTrip because
// callers can replace a client's redirect callback.
func CheckAPIRequest(request *http.Request) error {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" ||
		!strings.EqualFold(request.URL.Hostname(), "api.github.com") ||
		(request.URL.Port() != "" && request.URL.Port() != "443") ||
		strings.HasSuffix(request.URL.Host, ":") || request.URL.User != nil ||
		request.URL.Opaque != "" || request.URL.Fragment != "" ||
		(request.Host != "" && !strings.EqualFold(request.Host, request.URL.Host)) {
		return fmt.Errorf("Nido API 代理仅允许官方 GitHub API HTTPS 地址")
	}
	return nil
}

func (t *apiTransport) CloseIdleConnections() { t.transport.CloseIdleConnections() }

type apiResponseBody struct {
	io.ReadCloser
	check func() error
}

func (b *apiResponseBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if err == io.EOF {
		if checkErr := b.check(); checkErr != nil {
			return n, checkErr
		}
	}
	return n, err
}

func (b *apiResponseBody) Close() error {
	checkErr := b.check()
	closeErr := b.ReadCloser.Close()
	if checkErr != nil {
		return checkErr
	}
	return closeErr
}
