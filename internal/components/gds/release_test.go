package gds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseFixture(tag string, data []byte) releaseInfo {
	checksum := []byte(gdsutil.Hash(data) + "  " + releaseAsset + "\n")
	base := "https://github.com/" + releaseRepository + "/releases/download/" + tag + "/"
	return releaseInfo{Tag: tag, Assets: []releaseAssetInfo{
		{Name: releaseAsset, URL: base + releaseAsset, Digest: "sha256:" + gdsutil.Hash(data), Size: int64(len(data))},
		{Name: releaseAsset + ".sha256", URL: base + releaseAsset + ".sha256", Digest: "sha256:" + gdsutil.Hash(checksum), Size: int64(len(checksum))},
	}}
}

func releaseClient(t *testing.T, releases []releaseInfo, data []byte, change func(*http.Request, []byte) []byte) (*http.Client, *[]string) {
	t.Helper()
	requests := []string{}
	return &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		var b []byte
		switch {
		case req.URL.Host == "api.github.com" && req.URL.Path == "/repos/"+releaseRepository+"/releases":
			if req.URL.RawQuery != "per_page=100&page=1" {
				t.Fatalf("unexpected pagination %s", req.URL)
			}
			b, _ = json.Marshal(releases)
		case req.URL.Host == "api.github.com" && strings.HasPrefix(req.URL.Path, "/repos/"+releaseRepository+"/releases/tags/"):
			tag := strings.TrimPrefix(req.URL.Path, "/repos/"+releaseRepository+"/releases/tags/")
			for _, release := range releases {
				if release.Tag == tag {
					b, _ = json.Marshal(release)
				}
			}
		case req.URL.Host == "github.com" && strings.HasSuffix(req.URL.Path, "/"+releaseAsset+".sha256"):
			b = []byte(gdsutil.Hash(data) + "  " + releaseAsset + "\n")
		case req.URL.Host == "github.com" && strings.HasSuffix(req.URL.Path, "/"+releaseAsset):
			b = append([]byte{}, data...)
		default:
			t.Fatalf("unexpected request %s", req.URL)
		}
		if change != nil {
			b = change(req, b)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b)), Request: req}, nil
	})}, &requests
}

func TestOfficialReleaseInstallAndUpdateKeepConfiguration(t *testing.T) {
	s, r, _ := serviceFixture(t)
	release := releaseFixture("gds-v0.1.0", elfFixture())
	s.HTTP, _ = releaseClient(t, []releaseInfo{release}, elfFixture(), nil)
	if err := s.Install("", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(testUUID, ""); err != nil {
		t.Fatal(err)
	}
	config, _ := s.ConfigPath()
	before, _ := os.ReadFile(config)
	_, receipt, err := s.receiptSnapshot(false)
	if err != nil || receipt.ReleaseTag != release.Tag {
		t.Fatalf("receipt %+v: %v", receipt, err)
	}
	newData := append(elfFixture(), []byte("new release")...)
	release = releaseFixture("gds-v0.2.0", newData)
	s.HTTP, _ = releaseClient(t, []releaseInfo{release}, newData, nil)
	r.run = func(Command) ([]byte, error) {
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.2.0", Protocol: 1, Platform: "linux/amd64", Native: true})
	}
	if err = s.Update("gds-v0.2.0", gdsutil.Hash(newData)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(config)
	if !bytes.Equal(before, after) || len(r.stopped) != 0 {
		t.Fatal("official update changed config or stopped a process")
	}
	_, program, _ := s.paths()
	got, _ := os.ReadFile(program)
	if !bytes.Equal(got, newData) {
		t.Fatal("official update did not commit verified binary")
	}
}

func TestLatestGDSIndependentOfNidoAndPublicationTime(t *testing.T) {
	s, _, _ := serviceFixture(t)
	releases := []releaseInfo{
		{Tag: "v99.0.0"}, {Tag: "gds-v1.2.0"}, {Tag: "gds-v1.10.0"},
		{Tag: "gds-v100.0.0", Prerelease: true}, {Tag: "gds-v200.0.0", Draft: true},
		{Tag: "gds-v01.99.0"}, {Tag: "gds-v1.100.0-rc1"},
	}
	s.HTTP, _ = releaseClient(t, releases, nil, nil)
	latest, err := s.Latest()
	if err != nil || latest != "gds-v1.10.0" {
		t.Fatalf("latest %s: %v", latest, err)
	}
	for _, invalid := range [][]releaseInfo{nil, {{Tag: "v0.5.0"}}, {{Tag: "gds-v1.0.0"}, {Tag: "gds-v1.0.0"}}} {
		s.HTTP, _ = releaseClient(t, invalid, nil, nil)
		if _, err = s.Latest(); err == nil {
			t.Fatal("accepted absent/duplicate stable GDS release")
		}
	}
}

func TestReleasePaginationCompleteAndBounded(t *testing.T) {
	s, _, _ := serviceFixture(t)
	for _, complete := range []bool{true, false} {
		calls := 0
		s.HTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.RawQuery != fmt.Sprintf("per_page=100&page=%d", calls) {
				t.Fatal(req.URL)
			}
			releases := make([]releaseInfo, 100)
			for i := range releases {
				releases[i].Tag = fmt.Sprintf("v%d.%d.0", calls, i)
			}
			if calls == 1 {
				releases[0].Tag = "gds-v0.1.0"
			}
			if calls == 2 && complete {
				releases = []releaseInfo{{Tag: "gds-v0.2.0"}}
			}
			b, _ := json.Marshal(releases)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Header: make(http.Header)}, nil
		})}
		latest, err := s.Latest()
		if complete && (err != nil || latest != "gds-v0.2.0" || calls != 2) {
			t.Fatalf("latest %s, calls %d: %v", latest, calls, err)
		}
		if !complete && (err == nil || calls != 10) {
			t.Fatalf("incomplete scan accepted: calls %d, %v", calls, err)
		}
	}
}

func TestOfficialDownloadRejectsUntrustedAssetsWithoutChanges(t *testing.T) {
	tests := map[string]func(*releaseInfo){
		"missing binary":   func(r *releaseInfo) { r.Assets = r.Assets[1:] },
		"duplicate binary": func(r *releaseInfo) { r.Assets = append(r.Assets, r.Assets[0]) },
		"foreign repository": func(r *releaseInfo) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, releaseRepository, "other/repo", 1)
		},
		"other tag URL": func(r *releaseInfo) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "gds-v0.2.0", "gds-v0.1.0", 1)
		},
		"missing digest":   func(r *releaseInfo) { r.Assets[0].Digest = "" },
		"wrong digest":     func(r *releaseInfo) { r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64) },
		"wrong size":       func(r *releaseInfo) { r.Assets[0].Size++ },
		"oversize":         func(r *releaseInfo) { r.Assets[0].Size = maxProgram + 1 },
		"missing checksum": func(r *releaseInfo) { r.Assets = r.Assets[:1] },
		"foreign checksum": func(r *releaseInfo) { r.Assets[1].URL += "?token=private" },
		"prerelease":       func(r *releaseInfo) { r.Prerelease = true },
		"draft":            func(r *releaseInfo) { r.Draft = true },
		"different tag":    func(r *releaseInfo) { r.Tag = "gds-v0.3.0" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s, _, _ := installedFixture(t)
			dir, program, _ := s.paths()
			before, _ := os.ReadFile(program)
			config, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
			receipt, _ := os.ReadFile(filepath.Join(dir, "installed.json"))
			release := releaseFixture("gds-v0.2.0", elfFixture())
			mutate(&release)
			// Force the malformed release through the specified-tag response too.
			s.HTTP, _ = releaseClient(t, []releaseInfo{release}, elfFixture(), func(req *http.Request, b []byte) []byte {
				if strings.Contains(req.URL.Path, "/releases/tags/") {
					b, _ = json.Marshal(release)
				}
				return b
			})
			if err := s.Update("gds-v0.2.0", ""); err == nil {
				t.Fatal("accepted invalid official release")
			}
			for path, expected := range map[string][]byte{program: before, filepath.Join(dir, "config.toml"): config, filepath.Join(dir, "installed.json"): receipt} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("changed %s: %v", path, err)
				}
			}
		})
	}
}

func TestOfficialDownloadChecksSidecarBinaryAndMetadata(t *testing.T) {
	for _, target := range []string{"checksum filename", "checksum duplicate", "checksum digest", "binary bytes", "non-ELF", "metadata version", "supplied digest"} {
		t.Run(target, func(t *testing.T) {
			s, r, _ := serviceFixture(t)
			data := elfFixture()
			if target == "non-ELF" {
				data = []byte("#!/bin/sh\nexit 0\n")
			}
			release := releaseFixture("gds-v0.1.0", data)
			if target == "checksum filename" || target == "checksum duplicate" {
				checksum := []byte(gdsutil.Hash(data) + "  " + releaseAsset + "\n")
				if target == "checksum filename" {
					checksum = bytes.ReplaceAll(checksum, []byte(releaseAsset), []byte("other-binary"))
				} else {
					checksum = append(checksum, checksum...)
				}
				release.Assets[1].Digest = "sha256:" + gdsutil.Hash(checksum)
				release.Assets[1].Size = int64(len(checksum))
			}
			var supplied string
			if target == "supplied digest" {
				supplied = strings.Repeat("f", 64)
			}
			if target == "checksum digest" {
				release.Assets[1].Digest = "sha256:" + strings.Repeat("f", 64)
			}
			if target == "metadata version" {
				r.run = func(Command) ([]byte, error) {
					return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.2.0", Protocol: 1, Platform: "linux/amd64", Native: true})
				}
			}
			s.HTTP, _ = releaseClient(t, []releaseInfo{release}, data, func(req *http.Request, b []byte) []byte {
				if strings.HasSuffix(req.URL.Path, ".sha256") {
					if target == "checksum filename" {
						b = bytes.ReplaceAll(b, []byte(releaseAsset), []byte("other-binary"))
					}
					if target == "checksum duplicate" {
						b = append(b, b...)
					}
				}
				if target == "binary bytes" && strings.HasSuffix(req.URL.Path, "/"+releaseAsset) {
					b = append(b, 0)
				}
				return b
			})
			if err := s.Install("gds-v0.1.0", supplied); err == nil {
				t.Fatal("installed invalid official artifact")
			}
			_, program, _ := s.paths()
			if _, err := os.Stat(program); !os.IsNotExist(err) {
				t.Fatalf("program committed: %v", err)
			}
		})
	}
}

func TestLatestUpdateRefusesImplicitDowngrade(t *testing.T) {
	s, _, _ := installedFixture(t)
	data := append(elfFixture(), []byte("older official release")...)
	release := releaseFixture("gds-v0.0.9", data)
	s.HTTP, _ = releaseClient(t, []releaseInfo{release}, data, nil)
	_, program, _ := s.paths()
	before, _ := os.ReadFile(program)
	if err := s.Update("latest", ""); err == nil {
		t.Fatal("latest implicitly downgraded GDS")
	}
	after, _ := os.ReadFile(program)
	if !bytes.Equal(before, after) {
		t.Fatal("downgrade rejection changed installed program")
	}
}

func TestOfficialDryRunAndUpdateCheckDoNotExecute(t *testing.T) {
	s, r, _ := installedFixture(t)
	r.commands = nil
	s.HTTP = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) { t.Fatal("dry-run connected"); return nil, nil })}
	if err := s.Preflight("update", "latest", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Preflight("check-update", "", ""); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != 0 {
		t.Fatal("dry-run executed installed program")
	}
	s.HTTP, _ = releaseClient(t, []releaseInfo{{Tag: "gds-v0.2.0"}}, nil, nil)
	result, err := s.CheckUpdate()
	if err != nil || result.Current != "0.1.0" || result.Latest != "gds-v0.2.0" || !result.Available || len(r.commands) != 0 {
		t.Fatalf("check %+v: %v", result, err)
	}
	_, program, _ := s.paths()
	s.HTTP, _ = releaseClient(t, []releaseInfo{{Tag: "gds-v0.2.0"}}, nil, func(_ *http.Request, b []byte) []byte { os.WriteFile(program, append(elfFixture(), 0), 0700); return b })
	if _, err = s.CheckUpdate(); err == nil {
		t.Fatal("concurrent program change passed update check")
	}
}

func TestSourceValidationAndPlatformRejectBeforeNetwork(t *testing.T) {
	for _, source := range []string{"gds-v01.2.3", "v0.1.0", "0.1.0", "./gds", "https://example.com/gds", "gds-v0.1.0-rc1"} {
		if ValidateSource(source, "") == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	s, _, _ := serviceFixture(t)
	s.Platform = "darwin/arm64"
	s.HTTP = &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported platform connected")
		return nil, nil
	})}
	if err := s.Preflight("install", "latest", ""); err == nil {
		t.Fatal("dryrun accepted unsupported platform")
	}
	if err := s.Install("latest", ""); err == nil {
		t.Fatal("installed unsupported platform")
	}
	if err := ValidateSource("latest", "short"); err == nil {
		t.Fatal("accepted malformed checksum")
	}
}

func TestDownloadHTTPSRedirectAndBodyBounds(t *testing.T) {
	s, _, _ := serviceFixture(t)
	for _, source := range []string{"http://example.com/asset", "https://user:secret@example.com/asset", "https://example.com/asset#fragment"} {
		if _, err := s.get(source, 16); err == nil {
			t.Fatal("accepted unsafe URL")
		}
	}
	for _, target := range []string{"http://example.com/asset", "https://user:secret@example.com/asset", "https://example.com/asset#fragment"} {
		s.HTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})}
		if _, err := s.get("https://api.github.com/start", 16); err == nil {
			t.Fatal("accepted unsafe redirect")
		}
	}
	for _, length := range []int64{17, -1} {
		s.HTTP = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: length, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 17))), Request: req}, nil
		})}
		if _, err := s.get("https://api.github.com/body", 16); err == nil {
			t.Fatal("accepted oversized body")
		}
	}
}
