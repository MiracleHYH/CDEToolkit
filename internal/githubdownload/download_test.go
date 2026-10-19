package githubdownload

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(r *http.Request, status int, data string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data)), Request: r}
}

func TestValidateRejectsUnsafePrefixes(t *testing.T) {
	for _, value := range []string{"https://user:secret@example.com", "http://example.com", "https://example.com?token=secret", "https://example.com?", "https://example.com#", "https://example.com/#section", "https://example.com/\x1b", "https://example.com/back\\slash", "https://example.com:0", "https://example.com:65536", "https://example.com:", "relative", "DIRECT", "auto", "AUTO"} {
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

func TestExplicitRoutesNeverFallbackAndRejectInvalidContent(t *testing.T) {
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
			b, err := Get(client, source, 32, "direct")
			if len(calls) != 1 || (err == nil) != (mode == "success") {
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
		for _, policy := range []string{"direct", "https://custom.invalid/prefix"} {
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
		if _, err := Get(client, source, 32, "direct"); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestAutoIsRejectedBeforeNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) { calls++; return response(r, 200, "unexpected"), nil })}
	for _, source := range []string{"https://api.github.com/repos/a/b/releases/latest", "https://github.com/a/b/releases/download/v1/file"} {
		if _, err := Get(client, source, 32, "auto"); err == nil {
			t.Fatal("Auto was accepted")
		}
	}
	if calls != 0 {
		t.Fatal("Auto reached the network")
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
	if _, err := Get(client, "https://github.com/start", 32, "direct"); err == nil || calls != 5 {
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

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type diagnosticNetError struct{ timeout bool }

func (e diagnosticNetError) Error() string   { return "credential-sentinel" }
func (e diagnosticNetError) Timeout() bool   { return e.timeout }
func (e diagnosticNetError) Temporary() bool { return false }

func TestTransportFailuresClassifyAndRedactWithoutLosingCause(t *testing.T) {
	for _, tc := range []struct {
		name, category string
		cause          error
	}{
		{"dns", "DNS 解析失败", &net.DNSError{Err: "credential-sentinel", Name: "signed-url-sentinel", IsNotFound: true}},
		{"dns-timeout", "DNS 解析超时", &net.DNSError{Err: "credential-sentinel", Name: "signed-url-sentinel", IsTimeout: true}},
		{"deadline", "超时", context.DeadlineExceeded},
		{"network-deadline", "超时", &net.OpError{Op: "credential-sentinel", Net: "tcp", Err: context.DeadlineExceeded}},
		{"network-timeout", "超时", diagnosticNetError{timeout: true}},
		{"wrapped-network-timeout", "超时", &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("credential-sentinel: %w", diagnosticNetError{timeout: true})}},
		{"joined-network-timeout", "超时", errors.Join(errors.New("credential-sentinel"), diagnosticNetError{timeout: true})},
		{"network-non-timeout", "其他传输错误", diagnosticNetError{timeout: false}},
		{"cancelled", "请求已取消", context.Canceled},
		{"tls-verification", "TLS 失败", &tls.CertificateVerificationError{Err: errors.New("credential-sentinel")}},
		{"tls-authority", "TLS 失败", x509.UnknownAuthorityError{Cert: &x509.Certificate{}}},
		{"tls-hostname", "TLS 失败", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "credential-sentinel"}},
		{"tls-record", "TLS 失败", tls.RecordHeaderError{Msg: "credential-sentinel"}},
		{"tls-alert", "TLS 失败", tls.AlertError(40)},
		{"empty-response", "连接中断", io.EOF},
		{"truncated-response", "连接中断", io.ErrUnexpectedEOF},
		{"closed-connection", "连接中断", net.ErrClosed},
		{"reset-connection", "连接中断", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{"refused-connection", "连接失败", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}},
		{"unrecognized", "其他传输错误", errors.New("tls: DNS timeout credential-sentinel")},
	} {
		for _, phase := range []string{"request", "body"} {
			t.Run(tc.name+"/"+phase, func(t *testing.T) {
				// Both the transport message and URL would leak secrets if the
				// cause were formatted, including through net/http's own wrapper.
				sensitive := &url.Error{Op: "Get", URL: "https://credential-sentinel:proxy-password@proxy.invalid/asset?token=token-sentinel&jwt=signed-url-sentinel", Err: fmt.Errorf("credential-sentinel: %w", tc.cause)}
				client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
					if phase == "request" {
						return nil, sensitive
					}
					resp := response(r, 200, "")
					resp.ContentLength = -1
					resp.Body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), failingReader{sensitive}))
					return resp, nil
				})}
				b, err := Get(client, "https://github.com/example/tool/asset?jwt=signed-url-sentinel", 64, "direct")
				prefix := "GitHub 下载请求失败"
				if phase == "body" {
					prefix = "GitHub 下载响应不完整"
				}
				if err == nil || err.Error() != prefix+"（"+tc.category+"）" || b != nil {
					t.Fatalf("unexpected public error or partial bytes: %q %v", b, err)
				}
				var failure *downloadFailure
				var urlFailure *url.Error
				if !errors.As(err, &failure) || !failure.retry || !errors.As(err, &urlFailure) || !errors.Is(err, sensitive) || !errors.Is(err, tc.cause) {
					t.Fatal("lost original retry semantics or inspectable cause")
				}
				for _, formatted := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Errorf("update failed: %w", err).Error()} {
					for _, secret := range []string{"credential-sentinel", "proxy-password", "token-sentinel", "signed-url-sentinel", "proxy.invalid", "https://"} {
						if strings.Contains(formatted, secret) {
							t.Fatal("public transport diagnostic exposed its cause")
						}
					}
				}
			})
		}
	}
}

func TestTransportFailureNeverChangesSelectedRoute(t *testing.T) {
	for _, phase := range []string{"request", "body"} {
		for _, policy := range []string{"direct", "https://custom.invalid/prefix"} {
			t.Run(phase+"/"+policy, func(t *testing.T) {
				var calls []string
				client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
					calls = append(calls, r.URL.String())
					if len(calls) > 1 {
						return response(r, 200, "recovered"), nil
					}
					if phase == "request" {
						return nil, io.EOF
					}
					resp := response(r, 200, "")
					resp.ContentLength = -1
					resp.Body = io.NopCloser(failingReader{io.ErrUnexpectedEOF})
					return resp, nil
				})}
				source := "https://api.github.com/repos/example/tool/releases/latest"
				b, err := Get(client, source, 32, policy)
				want := []string{source}
				switch policy {
				case "direct":
					if err == nil || b != nil {
						t.Fatal("direct retried or returned partial bytes")
					}
				default:
					want[0] = policy + "/" + source
					if err == nil || b != nil {
						t.Fatal("explicit prefix retried or returned partial bytes")
					}
				}
				if !reflect.DeepEqual(calls, want) {
					t.Fatalf("download routes changed: %v", calls)
				}
			})
		}
	}
}

func TestResponseLengthMismatchRemainsRetryableAndInspectable(t *testing.T) {
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		resp := response(r, 200, "short")
		resp.ContentLength = 10
		return resp, nil
	})}
	b, err := Get(client, "https://github.com/example/tool/asset", 32, "direct")
	var failure *downloadFailure
	if b != nil || !errors.As(err, &failure) || !failure.retry || !errors.Is(err, io.ErrUnexpectedEOF) || err.Error() != "GitHub 下载响应不完整（连接中断）" {
		t.Fatalf("length mismatch accepted or changed retry semantics: %q %v", b, err)
	}
}

func TestLocalTLSHTTP1AndHTTP2CompleteAndTruncatedResponses(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, truncated := range []bool{false, true} {
			t.Run(fmt.Sprintf("http2=%t/truncated=%t", http2, truncated), func(t *testing.T) {
				protocol := make(chan int, 1)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					protocol <- r.ProtoMajor
					length := len("fixture")
					if truncated {
						length++
					}
					w.Header().Set("Content-Length", fmt.Sprint(length))
					fmt.Fprint(w, "fixture")
				}))
				server.EnableHTTP2 = http2
				server.StartTLS()
				defer server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				b, err := fetch(ctx, server.Client(), server.URL, 32, 0)
				wantProtocol := 1
				if http2 {
					wantProtocol = 2
				}
				select {
				case got := <-protocol:
					if got != wantProtocol {
						t.Fatalf("TLS fixture did not negotiate HTTP/%d: %d", wantProtocol, got)
					}
				default:
					t.Fatal("TLS fixture was not reached")
				}
				if !truncated {
					if err != nil || string(b) != "fixture" {
						t.Fatalf("complete TLS response failed: %q %v", b, err)
					}
					return
				}
				var failure *downloadFailure
				if b != nil || !errors.As(err, &failure) || !failure.retry || !errors.Is(err, io.ErrUnexpectedEOF) || err.Error() != "GitHub 下载响应不完整（连接中断）" {
					t.Fatalf("truncated TLS response accepted or misclassified: %q %v", b, err)
				}
			})
		}
	}
}

func TestGetContextCancellationStopsOnlyTheForegroundRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, http.StatusOK, "fixture"), nil
	})}
	if _, err := GetContext(ctx, client, "https://api.github.com/repos/example/tool/releases/latest", 1024, "direct"); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancelled operation made a request", err, calls)
	}
	if data, err := GetContext(context.Background(), client, "https://api.github.com/repos/example/tool/releases/latest", 1024, "direct"); err != nil || string(data) != "fixture" || calls != 1 {
		t.Fatal("cancelling one operation affected the next independent request", err, calls)
	}
}
