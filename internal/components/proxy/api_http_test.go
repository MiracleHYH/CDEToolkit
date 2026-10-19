package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestCoreAPIHTTPIsOnlyUsedForExplicitMetadataQueries(t *testing.T) {
	const prefix = "https://file-only.invalid/public"
	const apiURL = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"
	apiCalls, fileCalls := 0, 0
	s := Service{Platform: "linux/amd64", GitHubProxy: prefix}
	s.APIHTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
		apiCalls++
		if req.URL.String() != apiURL {
			t.Fatalf("metadata was rewritten or sent an asset: %s", req.URL)
		}
		metadata, err := json.Marshal(map[string]any{"tag_name": "v9.8.7"})
		if err != nil {
			t.Fatal(err)
		}
		return coreAPIResponse(req, metadata), nil
	})}
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
		fileCalls++
		asset, err := pinnedInstallAsset("linux/amd64")
		if err != nil || req.URL.String() != prefix+"/"+asset.source() {
			t.Fatalf("file route changed or received API: %s %v", req.URL, err)
		}
		return coreAPIResponse(req, []byte("inert invalid core")), nil
	})}
	if latest, err := s.Latest(); err != nil || latest != "v9.8.7" || apiCalls != 1 || fileCalls != 0 {
		t.Fatalf("explicit metadata query: version=%s api=%d files=%d err=%v", latest, apiCalls, fileCalls, err)
	}
	if _, err := s.artifact("latest", ""); err == nil || apiCalls != 1 || fileCalls != 1 {
		t.Fatalf("first install used latest metadata or accepted invalid bytes: api=%d files=%d err=%v", apiCalls, fileCalls, err)
	}
}

func TestCoreAPIHTTPFailureNeverFallsBack(t *testing.T) {
	for _, policy := range []string{"auto", "https://file-only.invalid"} {
		t.Run(policy, func(t *testing.T) {
			apiCalls, fileCalls := 0, 0
			s := Service{GitHubProxy: policy, APIHTTP: &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				apiCalls++
				if req.URL.Host != "api.github.com" {
					t.Fatalf("API route changed: %s", req.URL)
				}
				return nil, errors.New("isolated selected proxy unavailable")
			})}, HTTP: &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
				fileCalls++
				return nil, errors.New("unexpected file-route request")
			})}}
			if _, err := s.Latest(); err == nil || apiCalls != 1 || fileCalls != 0 {
				t.Fatalf("failure fallback: api=%d files=%d err=%v", apiCalls, fileCalls, err)
			}
		})
	}
}

func coreAPIResponse(req *http.Request, data []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: req}
}
