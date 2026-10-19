package nido

import (
	"io"
	"net/http"
)

// Kept only as a filename constant so old unowned files can be preserved by
// tests and diagnostics. It is never read, migrated, rewritten or removed.
const apiProxyFile = "download-proxy.toml"

func (m *Manager) downloadAPIClient(explicit string) (*http.Client, error) {
	return m.networkAPIClient("", explicit)
}

type apiBindingTransport struct {
	base         http.RoundTripper
	check        func() error
	networkRoute bool
	template     *http.Client
}

func (t apiBindingTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
func (t apiBindingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if err := t.check(); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	response.Body = &apiBindingBody{ReadCloser: response.Body, check: t.check}
	return response, nil
}

type apiBindingBody struct {
	io.ReadCloser
	check func() error
}

func (b *apiBindingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		if changed := b.check(); changed != nil {
			return n, changed
		}
	}
	return n, err
}
func (b *apiBindingBody) Close() error {
	err := b.ReadCloser.Close()
	if changed := b.check(); changed != nil {
		return changed
	}
	return err
}
