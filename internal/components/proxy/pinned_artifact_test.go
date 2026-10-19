package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corePinnedFixture(t *testing.T) (pinnedAsset, []byte, []byte) {
	t.Helper()
	data := coreNativeFixture("linux", "amd64")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	payload := compressed.Bytes()
	return pinnedAsset{"mihomo-linux-amd64-compatible-" + pinnedInstallVersion + ".gz", int64(len(payload)), hash(payload)}, payload, data
}

func TestPinnedInstallAssetMatchesReviewedOfficialMetadata(t *testing.T) {
	want := map[string]pinnedAsset{
		"linux/amd64":  {"mihomo-linux-amd64-compatible-v1.19.32.gz", 22877876, "ba3ce607747a07f948fc35780e108a4a7c7f552a38b9bd4d115f313ebcb89c20"},
		"linux/arm64":  {"mihomo-linux-arm64-v1.19.32.gz", 20809592, "9dd862e28b46ff7d775f169cceebc28deccaa0a9e804237d421cd2571e0caba0"},
		"darwin/amd64": {"mihomo-darwin-amd64-compatible-v1.19.32.gz", 22302953, "18b382df77bded2ad0fb3db27db5636cb15b20729d5ba995a357eeb9b46bf507"},
		"darwin/arm64": {"mihomo-darwin-arm64-v1.19.32.gz", 20756622, "3312a6780652c622890fd4357c6a853bbf865464fd047ac7b7f52dab8de18652"},
	}
	for platform, expected := range want {
		asset, err := pinnedInstallAsset(platform)
		if err != nil || asset != expected {
			t.Fatalf("reviewed asset changed for %s: %+v %v", platform, asset, err)
		}
		if asset.source() != "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/"+expected.name {
			t.Fatalf("noncanonical source for %s: %s", platform, asset.source())
		}
	}
}

func TestPinnedArtifactFetchUsesOnlySelectedFileRouteAndLocalInstallChecks(t *testing.T) {
	for _, policy := range []string{"", "direct", "https://accelerator.invalid/prefix"} {
		t.Run(policy, func(t *testing.T) {
			asset, payload, data := corePinnedFixture(t)
			s, runner, _ := coreService(t)
			s.Platform, s.GitHubProxy = "linux/amd64", policy
			fileCalls, apiCalls := 0, 0
			s.APIHTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
				apiCalls++
				return nil, errors.New("metadata must not participate in first install")
			})}
			s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				fileCalls++
				want := asset.source()
				if policy != "" && policy != "direct" {
					want = policy + "/" + want
				}
				if req.Method != http.MethodGet || req.URL.String() != want {
					t.Fatalf("file route changed: %s %s", req.Method, req.URL)
				}
				return coreAPIResponse(req, payload), nil
			})}
			prepared, err := s.fetchPinnedArtifact(context.Background(), asset, strings.ToUpper(asset.sha256))
			if err != nil || !bytes.Equal(prepared.data, payload) || prepared.expectedVersion != "v1.19.32" || fileCalls != 1 || apiCalls != 0 {
				t.Fatalf("fixed fetch: version=%s files=%d api=%d err=%v", prepared.expectedVersion, fileCalls, apiCalls, err)
			}
			// The synthetic descriptor never enters Service. The existing local
			// artifact path exercises gzip, native-format and candidate checks.
			local := filepath.Join(t.TempDir(), "inert-core.gz")
			if err := os.WriteFile(local, prepared.data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.Install(local, asset.sha256); err != nil {
				t.Fatal(err)
			}
			_, program, _ := s.paths()
			installed, err := os.ReadFile(program)
			if err != nil || !bytes.Equal(installed, data) || len(runner.commands) != 1 || runner.commands[0].Args[0] != "-v" || fileCalls != 1 || apiCalls != 0 || runner.starts != 0 || runner.stops != 0 {
				t.Fatalf("local integration changed route/runtime: files=%d api=%d commands=%d err=%v", fileCalls, apiCalls, len(runner.commands), err)
			}
		})
	}
}

func TestPinnedArtifactFetchRejectsInvalidDescriptorAndBodyWithoutFallback(t *testing.T) {
	for _, problem := range []string{"name", "digest", "size", "oversize", "checksum", "short", "long", "body", "download"} {
		t.Run(problem, func(t *testing.T) {
			asset, payload, _ := corePinnedFixture(t)
			supplied := ""
			switch problem {
			case "name":
				asset.name = "../" + asset.name
			case "digest":
				asset.sha256 = "invalid"
			case "size":
				asset.size = 0
			case "oversize":
				asset.size = maxProgram + 1
			case "checksum":
				supplied = strings.Repeat("0", 64)
			case "short":
				payload = payload[:len(payload)-1]
			case "long":
				payload = append(payload, 0)
			case "body":
				payload = bytes.Repeat([]byte("x"), len(payload))
			}
			fileCalls := 0
			s := Service{GitHubProxy: "https://accelerator.invalid/prefix", HTTP: &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
				fileCalls++
				if problem == "download" {
					return nil, errors.New("isolated download failure")
				}
				return coreAPIResponse(req, payload), nil
			})}}
			if _, err := s.fetchPinnedArtifact(context.Background(), asset, supplied); err == nil {
				t.Fatal("invalid fixed asset accepted")
			}
			wantCalls := 0
			if problem == "short" || problem == "long" || problem == "body" || problem == "download" {
				wantCalls = 1
			}
			if fileCalls != wantCalls {
				t.Fatalf("invalid asset retried or reached network: %d want %d", fileCalls, wantCalls)
			}
		})
	}
}

func TestPinnedInstallRejectsChangedAssetWithoutAPIExecutionOrDataChanges(t *testing.T) {
	asset, err := pinnedInstallAsset("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	// Exact advertised length isolates SHA verification from length rejection.
	payload := make([]byte, int(asset.size))
	for _, source := range []string{"", "latest"} {
		for _, policy := range []string{"direct", "https://accelerator.invalid/prefix"} {
			t.Run(source+"/"+policy, func(t *testing.T) {
				s, runner, _ := coreService(t)
				s.Platform, s.GitHubProxy, s.API = "linux/amd64", policy, "https://metadata-must-not-be-used.invalid"
				dir, program, _ := s.paths()
				preserved := []fileSnapshot{}
				for _, name := range []string{"config.yaml", "config.yaml.back", "unknown-cache"} {
					path := filepath.Join(dir, name)
					data := []byte("preserved " + name)
					if name == "config.yaml" {
						data = []byte(coreConfig)
					}
					coreWrite(t, path, data)
					before, err := capture(path, maxConfig, false)
					if err != nil {
						t.Fatal(err)
					}
					preserved = append(preserved, before)
				}
				fileCalls, apiCalls := 0, 0
				s.APIHTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
					apiCalls++
					return nil, errors.New("unavailable API route")
				})}
				s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
					fileCalls++
					want := asset.source()
					if policy != "direct" {
						want = policy + "/" + want
					}
					if req.Method != http.MethodGet || req.URL.String() != want {
						t.Fatalf("installation did not request the pinned asset: %s %s", req.Method, req.URL)
					}
					return coreAPIResponse(req, payload), nil
				})}
				if err := s.Install(source, ""); err == nil || !strings.Contains(err.Error(), "SHA-256") {
					t.Fatalf("changed pinned bytes accepted: %v", err)
				}
				if fileCalls != 1 || apiCalls != 0 || len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
					t.Fatalf("first install used metadata/retry/execution: files=%d api=%d commands=%d", fileCalls, apiCalls, len(runner.commands))
				}
				if _, err := os.Lstat(program); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("invalid pinned bytes created core", err)
				}
				if files, _ := filepath.Glob(filepath.Join(dir, ".nido-core-*")); len(files) != 0 {
					t.Fatal("invalid pinned bytes left candidate", files)
				}
				for _, before := range preserved {
					if err := before.check(); err != nil {
						t.Fatal("invalid pinned bytes changed existing data", err)
					}
				}
			})
		}
	}
}

func TestPinnedInstallUnsupportedPlatformAndChecksumRejectBeforeNetwork(t *testing.T) {
	for _, problem := range []string{"platform", "checksum", "malformed-checksum"} {
		t.Run(problem, func(t *testing.T) {
			s, runner, _ := coreService(t)
			s.Platform = "linux/amd64"
			checksum := strings.Repeat("0", 64)
			if problem == "platform" {
				s.Platform, checksum = "windows/amd64", ""
			} else if problem == "malformed-checksum" {
				checksum = "invalid"
			}
			s.HTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("rejected first install used network")
				return nil, nil
			})}
			if err := s.Install("latest", checksum); err == nil || len(runner.commands) != 0 {
				t.Fatalf("invalid platform/checksum reached candidate: %v", err)
			}
		})
	}
}
