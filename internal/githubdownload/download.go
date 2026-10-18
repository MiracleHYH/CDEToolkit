// Package githubdownload routes public Nido component downloads. It is not a
// global HTTP proxy and must never be used by the Codex integration.
package githubdownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultPrefix = "https://gh-proxy.org"
const assetPrefix = "https://ghfast.top"

// Validate accepts auto, direct, or an unauthenticated HTTPS URL prefix.
func Validate(value string) error {
	if value == "" || value == "auto" || value == "direct" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || !validPort(u) || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || unsafeURLText(value) {
		return fmt.Errorf("GitHub 加速地址需要无认证、查询参数或片段的 HTTPS 前缀")
	}
	return nil
}

// Resolve keeps network policy explicit; an empty environment override means
// direct. A Service which does not call Resolve retains its direct behavior.
func Resolve(explicit string, env []string, saved string) (string, error) {
	value := explicit
	if value == "" {
		found := false
		for i := len(env) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(env[i], "NIDO_GITHUB_PROXY="); ok {
				value, found = v, true
				break
			}
		}
		if found && value == "" {
			value = "direct"
		} else if !found {
			value = saved
			if value == "" {
				value = "auto"
			}
		}
	}
	return value, Validate(value)
}

// Get downloads a public GitHub resource with bounded network and response
// sizes. Empty policy is direct for independently constructed components.
func Get(base *http.Client, source string, maximum int64, policy string) ([]byte, error) {
	if err := Validate(policy); err != nil {
		return nil, err
	}
	u, err := secureURL(source)
	if err != nil {
		return nil, err
	}
	switch u.Host {
	case "github.com", "api.github.com", "raw.githubusercontent.com":
	default:
		return nil, fmt.Errorf("GitHub 下载只接受明确支持的官方地址")
	}
	if err := publicQuery(u); err != nil {
		return nil, err
	}
	if maximum <= 0 || maximum >= 1<<62 {
		return nil, fmt.Errorf("GitHub 下载大小上限无效")
	}
	// Auto is a bounded direct attempt followed by capability-scoped presets.
	// The second preset is never used for public REST API requests.
	// No registry, probing, persistent cache, or background health checks.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if policy != "auto" {
		if policy != "" && policy != "direct" {
			source = strings.TrimRight(policy, "/") + "/" + source
		}
		return fetch(ctx, base, source, maximum, 0)
	}
	// Limit connecting and receiving headers, not an already progressing
	// release asset body. A slow but healthy official download need not be
	// discarded in favor of a third party after eight seconds.
	b, err := fetch(ctx, base, source, maximum, 8*time.Second)
	if err == nil {
		return b, nil
	}
	var failure *downloadFailure
	if !errors.As(err, &failure) || !failure.retry || ctx.Err() != nil {
		return nil, err
	}
	prefixes := []string{defaultPrefix}
	if u.Host != "api.github.com" {
		prefixes = append(prefixes, assetPrefix)
	}
	for _, prefix := range prefixes {
		attempt, attemptCancel := context.WithTimeout(ctx, 45*time.Second)
		b, err = fetch(attempt, base, prefix+"/"+source, maximum, 0)
		attemptCancel()
		if err == nil {
			return b, nil
		}
		if !errors.As(err, &failure) || !failure.retry || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, err
}

func unsafeURLText(value string) bool {
	return strings.Contains(value, "\\") || strings.IndexFunc(value, func(c rune) bool { return c <= 32 || c == 127 }) >= 0
}

func secureURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || !validPort(u) || u.User != nil || strings.Contains(value, "#") || unsafeURLText(value) {
		return nil, fmt.Errorf("GitHub 下载需要无认证信息的 HTTPS 地址")
	}
	return u, nil
}

func validPort(u *url.URL) bool {
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		return err == nil && n >= 1 && n <= 65535
	}
	return true
}

func publicQuery(u *url.URL) error {
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return fmt.Errorf("GitHub 下载查询参数无效")
	}
	for key := range query {
		switch strings.ReplaceAll(strings.ToLower(key), "-", "_") {
		case "token", "access_token", "oauth_token", "private_token", "api_key", "apikey", "client_secret", "authorization", "password", "auth", "x_github_token", "x_auth_token":
			return fmt.Errorf("GitHub 下载地址不能包含认证参数")
		}
	}
	return nil
}

func officialRedirect(u *url.URL) bool {
	if httpsPort(u) != 443 {
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com", "api.github.com", "raw.githubusercontent.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "github-releases.githubusercontent.com":
		return true
	}
	return false
}

func allowedRedirect(origin, target *url.URL) bool {
	if strings.EqualFold(origin.Hostname(), target.Hostname()) && httpsPort(origin) == httpsPort(target) || officialRedirect(target) {
		return true
	}
	// Public presets may direct requests to their own regional subdomains.
	// A custom prefix authorizes its own origin, not arbitrary HTTPS services.
	if httpsPort(target) != 443 {
		return false
	}
	domains := []string{}
	switch strings.ToLower(origin.Hostname()) {
	case "gh-proxy.org", "gh-proxy.com":
		domains = []string{"gh-proxy.org", "gh-proxy.com"}
	case "ghfast.top":
		domains = []string{"ghfast.top"}
	}
	for _, domain := range domains {
		if strings.EqualFold(target.Hostname(), domain) || strings.HasSuffix(strings.ToLower(target.Hostname()), "."+domain) {
			return true
		}
	}
	return false
}

func httpsPort(u *url.URL) int {
	if u.Port() == "" {
		return 443
	}
	n, _ := strconv.Atoi(u.Port()) // secureURL has checked the range already.
	return n
}

type downloadFailure struct {
	retry bool
	text  string
}

func (e *downloadFailure) Error() string { return e.text }

func fail(retry bool, message string) error { return &downloadFailure{retry: retry, text: message} }

func fetch(ctx context.Context, base *http.Client, source string, maximum int64, headerTimeout time.Duration) ([]byte, error) {
	origin, err := secureURL(source)
	if err != nil {
		return nil, err
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	if client.Timeout <= 0 || client.Timeout > 2*time.Minute {
		client.Timeout = 2 * time.Minute
	}
	// These requests are public. Do not consult a caller's cookie jar or call
	// a redirect callback which can inject its GitHub authentication headers.
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fail(false, "GitHub 下载重定向过多")
		}
		target, err := secureURL(req.URL.String())
		if err != nil || !allowedRedirect(origin, target) {
			return fail(false, "GitHub 下载拒绝不安全的重定向")
		}
		if err := publicQuery(target); err != nil {
			return fail(false, "GitHub 下载重定向包含无效或认证查询参数")
		}
		req.Header.Del("Authorization")
		req.Header.Del("Proxy-Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Referer")
		return nil
	}
	req, err := http.NewRequestWithContext(operation, http.MethodGet, source, nil)
	if err != nil {
		return nil, fail(false, "GitHub 下载地址无效")
	}
	req.Header.Set("User-Agent", "Nido")
	req.Header.Set("Accept", "application/vnd.github+json")
	var headerTimer *time.Timer
	if headerTimeout > 0 {
		headerTimer = time.AfterFunc(headerTimeout, cancel)
		defer headerTimer.Stop()
	}
	resp, err := client.Do(req)
	if headerTimer != nil {
		headerTimer.Stop()
	}
	if err != nil {
		var failure *downloadFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, fail(true, "GitHub 下载连接失败或超时")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fail(true, fmt.Sprintf("GitHub 下载返回 HTTP %d", resp.StatusCode))
	}
	if resp.ContentLength > maximum {
		return nil, fail(false, "GitHub 下载响应大小越界")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maximum+1))
	if int64(len(b)) > maximum {
		return nil, fail(false, "GitHub 下载响应大小越界")
	}
	if err != nil || resp.ContentLength > 0 && int64(len(b)) != resp.ContentLength {
		return nil, fail(true, "GitHub 下载响应不完整")
	}
	return b, nil
}
