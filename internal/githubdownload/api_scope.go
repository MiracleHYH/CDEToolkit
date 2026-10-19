package githubdownload

import (
	"fmt"
	"net/http"
	"strings"
)

// CheckAPIRequest retains the official metadata authority inside a caller's
// RoundTripper, even if its HTTP client's redirect callback is replaced.
func CheckAPIRequest(request *http.Request) error {
	if request == nil || request.URL == nil || request.URL.Scheme != "https" ||
		(!strings.EqualFold(request.URL.Host, "api.github.com") && !strings.EqualFold(request.URL.Host, "api.github.com:443")) ||
		request.URL.User != nil || request.URL.Opaque != "" || request.URL.Fragment != "" ||
		(request.Host != "" && !strings.EqualFold(request.Host, request.URL.Host)) {
		return fmt.Errorf("Nido API 代理仅允许官方 GitHub API HTTPS 地址")
	}
	return nil
}
