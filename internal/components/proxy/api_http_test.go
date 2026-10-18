package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestCoreAPIHTTPSeparatesMetadataAndPreservesAssetValidation(t *testing.T) {
	const prefix = "https://file-only.invalid/public"
	const apiURL = "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest"
	const name = "mihomo-linux-amd64-v1-v1.2.4.gz"
	const assetURL = "https://github.com/MetaCubeX/mihomo/releases/download/v1.2.4/" + name
	for _, problem := range []string{"", "source", "digest", "size", "body"} {
		t.Run(problem, func(t *testing.T) {
			data := coreNativeFixture("linux", "amd64")
			source, digest, size := assetURL, hash(data), len(data)
			switch problem {
			case "source":
				source = "https://other.invalid/" + name
			case "digest":
				digest = "invalid"
			case "size":
				size = maxProgram + 1
			}
			metadata, err := json.Marshal(map[string]any{"tag_name": "v1.2.4", "assets": []any{map[string]any{"name": name, "browser_download_url": source, "digest": "sha256:" + digest, "size": size}}})
			if err != nil {
				t.Fatal(err)
			}
			apiCalls, fileCalls := 0, 0
			s := Service{Platform: "linux/amd64", GitHubProxy: prefix}
			s.APIHTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				apiCalls++
				if req.URL.String() != apiURL {
					t.Fatalf("metadata was rewritten or sent an asset: %s", req.URL)
				}
				return coreAPIResponse(req, metadata), nil
			})}
			s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				fileCalls++
				if req.URL.String() != prefix+"/"+assetURL {
					t.Fatalf("file route changed or received API: %s", req.URL)
				}
				body := data
				if problem == "body" {
					body = bytes.Repeat([]byte("x"), len(data))
				}
				return coreAPIResponse(req, body), nil
			})}
			prepared, err := s.artifact("latest", "")
			if (err == nil) != (problem == "") || apiCalls != 1 {
				t.Fatalf("artifact: api=%d files=%d err=%v", apiCalls, fileCalls, err)
			}
			wantFiles := 0
			if problem == "" || problem == "body" {
				wantFiles = 1
			}
			if fileCalls != wantFiles || problem == "" && (!bytes.Equal(prepared.data, data) || prepared.expectedVersion != "v1.2.4") {
				t.Fatalf("validation changed: prepared=%+v files=%d want=%d", prepared, fileCalls, wantFiles)
			}
		})
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
