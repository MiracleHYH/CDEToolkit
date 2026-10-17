package cdet

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type upstream struct {
	client                                                  *ReleaseClient
	version                                                 string
	requests                                                map[string]int
	corrupt, missingDigest, prerelease, duplicate, wrongURL bool
	status                                                  int
}

// The payload is a minimal, non-running ELF fixture; no downloaded code is
// executed. Both API and asset requests terminate at the TLS test server.
func fakeELF(version string) []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 62)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	return append(b, version...)
}

// A native-format but deliberately non-running program is sufficient for
// migration/update tests. No fixture component is executed by these tests.
func fakeNative(version string) []byte {
	if runtime.GOOS == "linux" {
		b := fakeELF(version)
		if runtime.GOARCH == "arm64" {
			binary.LittleEndian.PutUint16(b[18:], 183)
		}
		return b
	}
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	cpu := uint32(0x01000007)
	if runtime.GOARCH == "arm64" {
		cpu = 0x0100000c
	}
	binary.LittleEndian.PutUint32(b[4:], cpu)
	binary.LittleEndian.PutUint32(b[12:], 2)
	return append(b, version...)
}

func packed(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if name == "" {
		_, _ = gz.Write(data)
	} else {
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0755}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{version: "1.2.3", requests: map[string]int{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.requests[r.URL.Path]++
		if u.status != 0 {
			w.WriteHeader(u.status)
			return
		}
		name, repo := "codex", "openai/codex"
		if strings.Contains(r.URL.Path, "MetaCubeX") {
			name, repo = "proxy", "MetaCubeX/mihomo"
		}
		tag, _ := releaseTag(name, u.version)
		names, _ := mihomoAssetNames(tag, "linux", "amd64")
		member := ""
		if name == "codex" {
			member = strings.TrimSuffix(names[0], ".tar.gz")
		}
		payload := packed(t, member, fakeELF(name+u.version))
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			hash := "sha256:" + digest(payload)
			if u.missingDigest {
				hash = ""
			}
			download := "https://github.com/" + repo + "/releases/download/" + tag + "/" + names[0]
			if u.wrongURL {
				download = "https://github.com/unrelated/project/releases/download/" + tag + "/" + names[0]
			}
			a := map[string]any{"name": names[0], "browser_download_url": download, "digest": hash, "size": len(payload)}
			assets := []any{a}
			if u.duplicate {
				assets = append(assets, a)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "prerelease": u.prerelease, "assets": assets})
			return
		}
		if u.corrupt {
			payload = append(payload, 'x')
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport
	u.client = &ReleaseClient{OS: "linux", Arch: "amd64", API: "https://api.github.com", HTTP: &http.Client{Timeout: time.Second * 10, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		copyURL := *r.URL
		clone.URL = &copyURL
		clone.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return transport.RoundTrip(clone)
	})}}
	return u
}

func TestReleaseValidationAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*upstream)
	}{
		{"digest", func(u *upstream) { u.missingDigest = true }},
		{"prerelease", func(u *upstream) { u.prerelease = true }},
		{"duplicate", func(u *upstream) { u.duplicate = true }},
		{"repository", func(u *upstream) { u.wrongURL = true }},
		{"network", func(u *upstream) { u.status = 503 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream(t)
			tc.change(u)
			if _, err := u.client.resolve("proxy", "latest", ""); err == nil {
				t.Fatal("invalid release accepted")
			}
		})
	}
	u := newUpstream(t)
	if _, err := u.client.resolve("proxy", "1.2.2", ""); err == nil {
		t.Fatal("wrong pinned version accepted")
	}
	if _, err := u.client.resolve("proxy", "latest", strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong supplied digest accepted")
	}
	if _, err := u.client.resolve("proxy", "../../escape", ""); err == nil {
		t.Fatal("bad version accepted")
	}
	names, err := mihomoAssetNames("v1.2.3", "linux", "amd64")
	if err != nil || strings.Join(names, ",") != "mihomo-linux-amd64-v1-v1.2.3.gz,mihomo-linux-amd64-compatible-v1.2.3.gz" {
		t.Fatal(names, err)
	}
	if _, err = mihomoAssetNames("v1.2.3", "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	if _, err = mihomoAssetNames("v1.2.3", "linux", "386"); err == nil {
		t.Fatal("unsupported arch accepted")
	}
	if err = verifyPlatform(fakeELF("x"), "linux/arm64"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	if _, err = secureURL("https://user:secret@example.invalid/a"); err == nil {
		t.Fatal("credential URL accepted")
	}
}

// Reduced, public upstream metadata snapshots verify actual asset names
// without making the test depend on a live release or downloading programs.
func TestPublishedReleaseMetadataSnapshots(t *testing.T) {
	for _, name := range []string{"proxy"} {
		data, err := os.ReadFile(filepath.Join("testdata", "releases", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, goos := range []string{"linux", "darwin"} {
			for _, arch := range []string{"amd64", "arm64"} {
				client := &ReleaseClient{OS: goos, Arch: arch, API: "https://api.github.com", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
				})}}
				a, err := client.resolve(name, "latest", "")
				if err != nil || !validDigest(a.SHA256) {
					t.Fatal(name, goos, arch, err)
				}
				if name == "proxy" && arch == "amd64" && !strings.Contains(a.Asset, "-amd64-v1-") {
					t.Fatal("selected a CPU-specific default", a.Asset)
				}
			}
		}
	}
}
