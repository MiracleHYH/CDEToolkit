package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type latestHTTPTransport func(*http.Request) (*http.Response, error)

func (f latestHTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func latestHTTPResponse(request *http.Request, data []byte, status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: request}
}

func latestHTTPService(t *testing.T) Service {
	t.Helper()
	return Service{Run: func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("HTTP version lookup invoked an external command or installer")
		return nil, errors.New("unexpected external command")
	}, Env: []string{"NIDO_GITHUB_PROXY=https://file-only.invalid", "HTTPS_PROXY=http://unselected.invalid:1"}}
}

func TestLatestHTTPUsesOfficialMetadataAndRetainsVersionParsing(t *testing.T) {
	for _, tag := range []string{"rust-v0.164.0", "0.164.0", "rust-v1.2.3-rc.1", "rust-v1.2.3+build.1"} {
		t.Run(tag, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: latestHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != LatestURL || request.Method != http.MethodGet || request.Header.Get("User-Agent") != "Nido" || request.Header.Get("Accept") != "application/vnd.github+json" {
					t.Fatalf("unexpected metadata request: %s %s headers=%v", request.Method, request.URL, request.Header)
				}
				data, err := json.Marshal(map[string]string{"tag_name": tag})
				if err != nil {
					t.Fatal(err)
				}
				return latestHTTPResponse(request, data, http.StatusOK), nil
			})}
			version, err := latestHTTPService(t).LatestHTTP(client)
			if err != nil || version != strings.TrimPrefix(tag, "rust-v") || calls != 1 {
				t.Fatalf("version=%q calls=%d err=%v", version, calls, err)
			}
		})
	}
}

func TestLatestHTTPRejectsInvalidMetadataWithoutFallback(t *testing.T) {
	for _, body := range []string{"{", `{}`, `null`, `{"tag_name":1}`, `{"tag_name":"rust-v"}`, `{"tag_name":"v1.2.3"}`, `{"tag_name":"rust-v1.2.3\n"}`, `{"tag_name":"rust-v1.2"}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: latestHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != LatestURL {
					t.Fatalf("invalid metadata triggered another route: %s", request.URL)
				}
				return latestHTTPResponse(request, []byte(body), http.StatusOK), nil
			})}
			if version, err := latestHTTPService(t).LatestHTTP(client); err == nil || version != "" || calls != 1 {
				t.Fatalf("accepted invalid metadata: version=%q calls=%d err=%v", version, calls, err)
			}
		})
	}
}

func TestLatestHTTPFailureIsSafeAndDoesNotFallback(t *testing.T) {
	for _, problem := range []string{"transport", "status", "redirect"} {
		t.Run(problem, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: latestHTTPTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.String() != LatestURL {
					t.Fatalf("failure triggered another network route: %s", request.URL)
				}
				switch problem {
				case "transport":
					return nil, errors.New("http://private-user:private-password@selected.invalid/?token=private-token")
				case "status":
					return latestHTTPResponse(request, []byte("private-token"), http.StatusBadGateway), nil
				default:
					response := latestHTTPResponse(request, nil, http.StatusFound)
					response.Header.Set("Location", "https://other.invalid/?token=private-token")
					return response, nil
				}
			})}
			version, err := latestHTTPService(t).LatestHTTP(client)
			if err == nil || version != "" || calls != 1 {
				t.Fatalf("failure fallback: version=%q calls=%d err=%v", version, calls, err)
			}
			for _, secret := range []string{"private-user", "private-password", "private-token", "selected.invalid", "other.invalid"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("failure exposed transport/response details: %v", err)
				}
			}
		})
	}
}

func TestLatestHTTPRejectsNilClientWithoutUsingDefaultTransport(t *testing.T) {
	previous := http.DefaultTransport
	defer func() { http.DefaultTransport = previous }()
	http.DefaultTransport = latestHTTPTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("nil API client used the default transport")
		return nil, errors.New("unexpected default transport")
	})
	if version, err := latestHTTPService(t).LatestHTTP(nil); err == nil || version != "" {
		t.Fatalf("nil client accepted: version=%q err=%v", version, err)
	}
}

func TestLatestHTTPOperationContextCancelsOnlySelectedRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := latestHTTPService(t)
	s.Ctx = ctx
	started := make(chan struct{})
	client := &http.Client{Transport: latestHTTPTransport(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	finished := make(chan error, 1)
	go func() { _, err := s.LatestHTTP(client); finished <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled metadata request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("metadata lookup ignored operation cancellation")
	}
}
