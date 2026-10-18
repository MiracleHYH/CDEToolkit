package selfupdate

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSelfUpdateAPIHTTPSeparatesOfficialMetadataFromAcceleratedFiles(t *testing.T) {
	const prefix = "https://file-only.invalid/public"
	for _, version := range []string{"", "0.6.0"} {
		t.Run("version="+version, func(t *testing.T) {
			r, files := releaseFixture("linux/amd64", []byte("isolated release bytes"))
			api, apiCalls := fakeClient(t, r, nil, "")
			assets, assetCalls := fakeClient(t, r, files, prefix)
			s := Service{HTTP: assets, APIHTTP: api, GitHubProxy: prefix, Platform: "linux/amd64"}
			got, err := s.release(version)
			if err != nil || got.Tag != r.Tag {
				t.Fatalf("release=%+v err=%v", got, err)
			}
			wantAPI := releasesAPI + "/latest"
			if version != "" {
				wantAPI = releasesAPI + "/tags/v" + version
			}
			if len(*apiCalls) != 1 || (*apiCalls)[0] != wantAPI || len(*assetCalls) != 0 {
				t.Fatalf("metadata routes: api=%v assets=%v", *apiCalls, *assetCalls)
			}
			for _, a := range got.Assets {
				data, err := s.download(a, maxArchive)
				if err != nil || !bytes.Equal(data, files[a.URL]) {
					t.Fatalf("asset=%s err=%v", a.Name, err)
				}
			}
			if len(*apiCalls) != 1 || len(*assetCalls) != 2 {
				t.Fatalf("file routes: api=%v assets=%v", *apiCalls, *assetCalls)
			}
			for _, route := range *assetCalls {
				if !strings.HasPrefix(route, prefix+"/https://github.com/") {
					t.Fatalf("file policy changed: %s", route)
				}
			}
		})
	}
}

func TestSelfUpdateAPIHTTPFailureNeverFallsBack(t *testing.T) {
	for _, policy := range []string{"auto", "https://file-only.invalid"} {
		for _, version := range []string{"", "0.6.0"} {
			t.Run(policy+"/"+version, func(t *testing.T) {
				apiCalls, fileCalls := 0, 0
				s := Service{GitHubProxy: policy, APIHTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
					apiCalls++
					if req.URL.Host != "api.github.com" {
						t.Fatalf("API route changed: %s", req.URL)
					}
					return nil, errors.New("isolated selected proxy unavailable")
				})}, HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
					fileCalls++
					return nil, errors.New("unexpected file-route request")
				})}}
				if _, err := s.release(version); err == nil || apiCalls != 1 || fileCalls != 0 {
					t.Fatalf("failure fallback: api=%d files=%d err=%v", apiCalls, fileCalls, err)
				}
			})
		}
	}
}

func TestSelfUpdateAPIHTTPDoesNotRelaxAssetValidation(t *testing.T) {
	for _, problem := range []string{"source", "digest", "size", "body"} {
		t.Run(problem, func(t *testing.T) {
			r, files := releaseFixture("linux/amd64", []byte("isolated release bytes"))
			switch problem {
			case "source":
				r.Assets[0].URL = "https://other.invalid/release.tar.gz"
			case "digest":
				r.Assets[0].Digest = "sha256:invalid"
			case "size":
				r.Assets[0].Size = maxArchive + 1
			case "body":
				files[r.Assets[1].URL] = []byte("wrong checksum body")
			}
			api, apiCalls := fakeClient(t, r, nil, "")
			fileCalls := 0
			assets := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
				fileCalls++
				source := strings.TrimPrefix(req.URL.String(), "https://file-only.invalid/")
				data := files[source]
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
			})}
			s := Service{HTTP: assets, APIHTTP: api, GitHubProxy: "https://file-only.invalid", Platform: "linux/amd64"}
			if _, err := s.Prepare("0.6.0"); err == nil || len(*apiCalls) != 1 {
				t.Fatalf("accepted invalid asset: api=%v files=%d err=%v", *apiCalls, fileCalls, err)
			}
			wantFiles := 0
			if problem == "body" {
				wantFiles = 1
			}
			if fileCalls != wantFiles {
				t.Fatalf("invalid metadata/body downloaded extra files: got %d want %d", fileCalls, wantFiles)
			}
		})
	}
}
