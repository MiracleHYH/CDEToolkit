package gds

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
)

func TestGDSAPIHTTPSeparatesMetadataAndPreservesAssetValidation(t *testing.T) {
	const prefix = "https://file-only.invalid/public"
	const base = "https://api.github.com/repos/" + releaseRepository + "/releases"
	for _, source := range []string{"latest", "gds-v0.1.0"} {
		for _, problem := range []string{"", "source", "digest", "size", "body"} {
			t.Run(source+"/"+problem, func(t *testing.T) {
				data := elfFixture()
				release := releaseFixture("gds-v0.1.0", data)
				switch problem {
				case "source":
					release.Assets[0].URL = "https://other.invalid/" + releaseAsset
				case "digest":
					release.Assets[0].Digest = "sha256:invalid"
				case "size":
					release.Assets[0].Size = maxProgram + 1
				}
				apiCalls, fileCalls := 0, 0
				s := Service{Platform: "linux/amd64", GitHubProxy: prefix}
				s.APIHTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
					apiCalls++
					want := base + "/tags/" + source
					var value any = release
					if source == "latest" {
						want = base + "?per_page=100&page=1"
						value = []releaseInfo{release}
					}
					if req.URL.String() != want {
						t.Fatalf("metadata route changed: got %s want %s", req.URL, want)
					}
					metadata, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					return gdsAPIResponse(req, metadata), nil
				})}
				s.HTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
					fileCalls++
					fileBase := prefix + "/https://github.com/" + releaseRepository + "/releases/download/" + release.Tag + "/"
					var body []byte
					switch req.URL.String() {
					case fileBase + releaseAsset + ".sha256":
						body = []byte(gdsutil.Hash(data) + "  " + releaseAsset + "\n")
					case fileBase + releaseAsset:
						body = data
						if problem == "body" {
							body = bytes.Repeat([]byte("x"), len(data))
						}
					default:
						t.Fatalf("asset route changed or received API: %s", req.URL)
					}
					return gdsAPIResponse(req, body), nil
				})}
				prepared, err := s.downloadRelease(source, "")
				if (err == nil) != (problem == "") || apiCalls != 1 {
					t.Fatalf("release: api=%d files=%d err=%v", apiCalls, fileCalls, err)
				}
				wantFiles := 0
				if problem == "" || problem == "body" {
					wantFiles = 2
				}
				if fileCalls != wantFiles || problem == "" && (!bytes.Equal(prepared.data, data) || prepared.expectedVersion != "0.1.0" || prepared.releaseTag != release.Tag) {
					t.Fatalf("validation changed: prepared=%+v files=%d want=%d", prepared, fileCalls, wantFiles)
				}
			})
		}
	}
}

func TestGDSAPIHTTPIncludesEveryReleasePage(t *testing.T) {
	const base = "https://api.github.com/repos/" + releaseRepository + "/releases"
	apiCalls := 0
	firstPage := make([]releaseInfo, 100)
	for i := range firstPage {
		firstPage[i].Tag = fmt.Sprintf("v1.0.%d", i)
	}
	firstPage[0].Tag = "gds-v1.2.0"
	secondPage := []releaseInfo{{Tag: "gds-v1.10.0"}}
	fileCalls := 0
	service := Service{GitHubProxy: "https://file-only.invalid"}
	service.APIHTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
		apiCalls++
		want := fmt.Sprintf("%s?per_page=100&page=%d", base, apiCalls)
		if req.URL.String() != want || apiCalls > 2 {
			t.Fatalf("pagination route changed: %s", req.URL)
		}
		page := firstPage
		if apiCalls == 2 {
			page = secondPage
		}
		metadata, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		return gdsAPIResponse(req, metadata), nil
	})}
	service.HTTP = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
		fileCalls++
		return nil, errors.New("API page escaped the selected network route")
	})}
	if latest, err := service.Latest(); err != nil || latest != "gds-v1.10.0" || apiCalls != 2 || fileCalls != 0 {
		t.Fatalf("latest=%s api=%d files=%d err=%v", latest, apiCalls, fileCalls, err)
	}
}

func TestGDSAPIHTTPFailureNeverFallsBack(t *testing.T) {
	for _, policy := range []string{"auto", "https://file-only.invalid"} {
		for _, source := range []string{"gds-v0.1.0", "latest", "second-page"} {
			t.Run(policy+"/"+source, func(t *testing.T) {
				apiCalls, fileCalls := 0, 0
				service := Service{GitHubProxy: policy}
				service.APIHTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
					apiCalls++
					if req.URL.Host != "api.github.com" {
						t.Fatalf("API route changed: %s", req.URL)
					}
					if source == "second-page" && apiCalls == 1 {
						metadata, err := json.Marshal(make([]releaseInfo, 100))
						if err != nil {
							t.Fatal(err)
						}
						return gdsAPIResponse(req, metadata), nil
					}
					return nil, errors.New("isolated selected proxy unavailable")
				})}
				service.HTTP = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
					fileCalls++
					return nil, errors.New("unexpected fallback request")
				})}
				requested := source
				wantAPI := 1
				if source == "second-page" {
					requested, wantAPI = "latest", 2
				}
				if _, err := service.release(requested); err == nil || apiCalls != wantAPI || fileCalls != 0 || strings.Contains(err.Error(), "file-only") {
					t.Fatalf("failure fallback: api=%d files=%d err=%v", apiCalls, fileCalls, err)
				}
			})
		}
	}
}

func gdsAPIResponse(req *http.Request, data []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: req}
}
