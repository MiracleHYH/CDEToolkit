package githubdownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(r *http.Request, status int, data string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data)), Request: r}
}

func TestResolvePriorityAndExplicitEmptyEnvironment(t *testing.T) {
	for _, tc := range []struct {
		explicit string
		env      []string
		saved    string
		want     string
	}{
		{"", nil, "", "auto"},
		{"", nil, "direct", "direct"},
		{"", []string{"NIDO_GITHUB_PROXY="}, "https://saved.invalid", "direct"},
		{"", []string{"NIDO_GITHUB_PROXY=direct", "NIDO_GITHUB_PROXY=https://last.invalid/prefix/"}, "auto", "https://last.invalid/prefix/"},
		{"auto", []string{"NIDO_GITHUB_PROXY=direct"}, "https://saved.invalid", "auto"},
		{"https://explicit.invalid", []string{"NIDO_GITHUB_PROXY=bad"}, "bad", "https://explicit.invalid"},
	} {
		got, err := Resolve(tc.explicit, tc.env, tc.saved)
		if err != nil || got != tc.want {
			t.Fatalf("Resolve(%q,%v,%q) = %q, %v", tc.explicit, tc.env, tc.saved, got, err)
		}
	}
	for _, value := range []string{"https://user:secret@example.com", "http://example.com", "https://example.com?token=secret", "https://example.com?", "https://example.com#", "https://example.com/#section", "https://example.com/\x1b", "https://example.com/back\\slash", "https://example.com:0", "https://example.com:65536", "https://example.com:", "relative", "DIRECT"} {
		if err := Validate(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestGetRoutesPublicAPIAndAssetsWithoutChangingOriginalURLs(t *testing.T) {
	for _, source := range []string{"https://api.github.com/repos/example/tool/releases?per_page=100&page=2", "https://github.com/example/tool/releases/download/v1.2.3/tool.tar.gz"} {
		for _, policy := range []string{"", "direct", "https://accelerator.invalid/prefix/"} {
			var got []string
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				got = append(got, r.URL.String())
				if _, ok := r.Context().Deadline(); !ok {
					t.Fatal("request has no deadline")
				}
				return response(r, 200, "fixture bytes"), nil
			})}
			b, err := Get(client, source, 1024, policy)
			want := source
			if policy != "" && policy != "direct" {
				want = strings.TrimRight(policy, "/") + "/" + source
			}
			if err != nil || string(b) != "fixture bytes" || !reflect.DeepEqual(got, []string{want}) {
				t.Fatalf("source %s policy %q: %v %q %v", source, policy, got, b, err)
			}
		}
	}
}

func TestAutoFallbackIsBoundedAndNeverRetriesSuccessfulOrInvalidContent(t *testing.T) {
	for _, mode := range []string{"network", "403", "429", "503", "success", "404", "oversize", "unbounded", "insecure-redirect"} {
		t.Run(mode, func(t *testing.T) {
			calls := []string{}
			source := "https://api.github.com/repos/example/tool/releases/latest"
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.String())
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 2*time.Minute {
					t.Fatal("unbounded request")
				}
				if r.URL.Host != "api.github.com" {
					return response(r, 200, "fallback"), nil
				}
				// The response body keeps the total transfer budget; the short
				// direct timeout applies only until headers have arrived.
				switch mode {
				case "network":
					return nil, errors.New("https://user:secret@must-not-print")
				case "403":
					return response(r, 403, ""), nil
				case "429":
					return response(r, 429, ""), nil
				case "503":
					return response(r, 503, ""), nil
				case "404":
					return response(r, 404, ""), nil
				case "oversize":
					return response(r, 200, strings.Repeat("x", 33)), nil
				case "unbounded":
					resp := response(r, 200, strings.Repeat("x", 33))
					resp.ContentLength = -1
					return resp, nil
				case "insecure-redirect":
					resp := response(r, 302, "")
					resp.Header.Set("Location", "http://insecure.invalid/asset")
					return resp, nil
				default:
					return response(r, 200, "not JSON; caller must reject"), nil
				}
			})}
			b, err := Get(client, source, 32, "auto")
			fallback := mode == "network" || mode == "403" || mode == "429" || mode == "503" || mode == "404"
			if fallback {
				if err != nil || string(b) != "fallback" || !reflect.DeepEqual(calls, []string{source, defaultPrefix + "/" + source}) {
					t.Fatalf("%v %q %v", calls, b, err)
				}
			} else if len(calls) != 1 || (err == nil) != (mode == "success") {
				t.Fatalf("unexpected route or failure %v %q %v", calls, b, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("leaked transport credentials")
			}
		})
	}
}

func TestRedirectsRejectForeignOriginsAndAuthenticationQueriesBeforeSending(t *testing.T) {
	for _, target := range []string{
		"https://unrelated.invalid/private", "https://127.0.0.1:8443/admin",
		"https://gh-proxy.org.evil.invalid/asset", "https://api.github.com/user?access_token=secret",
		"https://github.com/asset?client_secret=secret", "https://github.com/asset?X-GitHub-Token=secret",
		"https://github.com/asset?auth=%XX", "https://user:secret@github.com/asset",
	} {
		for _, policy := range []string{"direct", "auto", "https://custom.invalid/prefix"} {
			calls := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls != 1 {
					t.Fatal("rejected redirect was sent or failure triggered fallback")
				}
				resp := response(r, 302, "")
				resp.Header.Set("Location", target)
				return resp, nil
			})}
			_, err := Get(client, "https://github.com/example/tool/releases/download/v1.0.0/tool", 32, policy)
			if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatalf("target=%s policy=%s calls=%d err=%v", target, policy, calls, err)
			}
		}
	}
}

func TestRedirectsAllowOfficialAssetCDNAndDocumentedPresetRegions(t *testing.T) {
	for _, tc := range []struct{ policy, target string }{
		{"direct", "https://release-assets.githubusercontent.com/public-asset?jwt=public-asset-signature"},
		{"https://custom.invalid/path", "https://objects.githubusercontent.com/public-asset?sig=public-download-signature"},
		{"https://gh-proxy.org", "https://edge.gh-proxy.com/public-asset"},
		{"https://ghfast.top", "https://edge.ghfast.top/public-asset"},
		{"https://custom.invalid:443/path", "https://custom.invalid/asset"},
	} {
		calls := 0
		client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				resp := response(r, 302, "")
				resp.Header.Set("Location", tc.target)
				return resp, nil
			}
			if r.URL.String() != tc.target {
				t.Fatalf("wrong redirect %s", r.URL)
			}
			return response(r, 200, "fixture"), nil
		})}
		b, err := Get(client, "https://github.com/example/tool/releases/download/v1.0.0/tool", 32, tc.policy)
		if err != nil || string(b) != "fixture" || calls != 2 {
			t.Fatalf("%+v: %q calls=%d err=%v", tc, b, calls, err)
		}
	}
}

func TestDirectHeaderTimeoutDoesNotInterruptAHealthyResponseBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(350 * time.Millisecond):
			fmt.Fprint(w, "complete slow body")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := fetch(ctx, server.Client(), server.URL, 32, 250*time.Millisecond)
	if err != nil || string(b) != "complete slow body" {
		t.Fatalf("header deadline cancelled a healthy body: %q %v", b, err)
	}
}

func TestDirectHeaderTimeoutCancelsAConnectionWithoutHeaders(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := fetch(ctx, server.Client(), server.URL, 32, 30*time.Millisecond); err == nil || time.Since(start) >= time.Second {
		t.Fatalf("header timeout did not cancel promptly: %v", err)
	}
}

func TestGetRejectsNonGitHubAndAuthenticationBeforeRequest(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, nil
	})}
	for _, source := range []string{"https://chatgpt.com/codex/install.sh", "https://untrusted.invalid/api.github.com", "https://github.com.evil.invalid/a", "http://github.com/a", "https://user:secret@github.com/a", "https://api.github.com/repos/a/b?access_token=secret", "https://api.github.com/repos/a/b?API_KEY=secret", "https://api.github.com/repos/a/b?token=%XX", "https://github.com/a#"} {
		if _, err := Get(client, source, 32, "auto"); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestAutoPresetsKeepAPICapabilitySeparateFromAssets(t *testing.T) {
	for _, source := range []string{"https://api.github.com/repos/example/tool/releases/latest", "https://github.com/example/tool/releases/download/v1.0.0/tool"} {
		var calls []string
		client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls = append(calls, r.URL.String())
			if r.URL.Host == "ghfast.top" {
				return response(r, 200, "asset fixture"), nil
			}
			return response(r, 503, "unavailable"), nil
		})}
		b, err := Get(client, source, 32, "auto")
		if strings.Contains(source, "api.github.com") {
			if err == nil || len(calls) != 2 || strings.Contains(strings.Join(calls, " "), "ghfast.top") {
				t.Fatalf("sent API to asset-only preset: %v %v", calls, err)
			}
		} else if err != nil || string(b) != "asset fixture" || len(calls) != 3 || calls[2] != assetPrefix+"/"+source {
			t.Fatalf("asset fallback failed: %v %q %v", calls, b, err)
		}
	}
}

func TestLocalTLSFixtureDoesNotInheritCookieJarOrCredentialRedirectCallback(t *testing.T) {
	var hits int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("credentials reached public fixture")
		}
		if r.URL.Path == "/asset" {
			w.WriteHeader(200)
			fmt.Fprint(w, "verified fixture")
			return
		}
		w.Header().Set("Location", "https://github.com/asset")
		w.WriteHeader(302)
	}))
	defer server.Close()
	client := server.Client()
	jar, _ := cookiejar.New(nil)
	original, _ := url.Parse("https://github.com")
	jar.SetCookies(original, []*http.Cookie{{Name: "secret-cookie", Value: "must-not-forward"}})
	client.Jar = jar
	client.CheckRedirect = func(r *http.Request, _ []*http.Request) error {
		t.Error("inherited callback can inject authentication")
		r.Header.Set("Authorization", "Bearer secret")
		return nil
	}
	base := client.Transport
	local, _ := url.Parse(server.URL)
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Host, copy.URL.Scheme = local.Host, local.Scheme
		copy.Host = local.Host
		return base.RoundTrip(copy)
	})
	b, err := Get(client, "https://github.com/example/tool/releases/download/v1.0.0/tool", 100, "direct")
	if err != nil || string(b) != "verified fixture" || hits != 2 {
		t.Fatalf("%q hits %d %v", b, hits, err)
	}
	if client.Jar != jar {
		t.Fatal("changed caller client")
	}
}

func TestGetRedirectLoopAndSlowResponseRemainBounded(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		resp := response(r, 302, "")
		resp.Header.Set("Location", "https://github.com/loop")
		return resp, nil
	})}
	if _, err := Get(client, "https://github.com/start", 32, "auto"); err == nil || calls != 5 {
		t.Fatalf("redirect loop calls=%d err=%v", calls, err)
	}
	client.Timeout = 10 * time.Millisecond
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	start := time.Now()
	if _, err := Get(client, "https://github.com/start", 32, "direct"); err == nil || time.Since(start) > time.Second {
		t.Fatalf("caller timeout ignored: %v", err)
	}
}
