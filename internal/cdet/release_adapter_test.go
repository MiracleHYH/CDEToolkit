package cdet

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func adapterNative(label string) []byte {
	if runtime.GOOS == "linux" {
		b := make([]byte, 64)
		copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
		binary.LittleEndian.PutUint16(b[16:], 2)
		machine := uint16(62)
		if runtime.GOARCH == "arm64" {
			machine = 183
		}
		binary.LittleEndian.PutUint16(b[18:], machine)
		binary.LittleEndian.PutUint32(b[20:], 1)
		binary.LittleEndian.PutUint16(b[52:], 64)
		return append(b, label...)
	}
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	cpu := uint32(0x01000007)
	if runtime.GOARCH == "arm64" {
		cpu = 0x0100000c
	}
	binary.LittleEndian.PutUint32(b[4:], cpu)
	binary.LittleEndian.PutUint32(b[12:], 2)
	return append(b, label...)
}

func TestReleaseAdapterLatestVersionNeedsNoPackageAssets(t *testing.T) {
	for _, name := range []string{"codex", "proxy"} {
		t.Run(name, func(t *testing.T) {
			tag, want := "v1.2.3", "v1.2.3"
			if name == "codex" {
				tag, want = "rust-v0.99.0", "0.99.0"
			}
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				fmt.Fprintf(w, `{"tag_name":%q,"draft":false,"prerelease":false}`, tag)
			}))
			defer server.Close()
			c := &ReleaseClient{HTTP: server.Client(), API: server.URL, OS: "unsupported", Arch: "unsupported"}
			version, e := c.latestVersion(name)
			if e != nil || version != want || requests.Load() != 1 {
				t.Fatal("version lookup still requires package metadata or platform", version, e)
			}
		})
	}
}

func TestReleaseAdapterRejectsUnstableOrMalformedVersions(t *testing.T) {
	for _, metadata := range []string{`{"tag_name":"v1.2.3","draft":true}`, `{"tag_name":"v1.2.3","prerelease":true}`, `{"tag_name":"v1.2.3-alpha"}`, `{"tag_name":"../../wrong"}`, `not-json`} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, metadata) }))
		c := &ReleaseClient{HTTP: server.Client(), API: server.URL}
		if _, e := c.latestVersion("proxy"); e == nil {
			t.Fatal("invalid latest version accepted", metadata)
		}
		server.Close()
	}
}

func TestReleaseAdapterMihomoFallbackKeepsOfficialDigestAndConservativeCPU(t *testing.T) {
	var payload map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(payload) }))
	defer server.Close()
	hash := strings.Repeat("a", 64)
	asset := func(name string) map[string]any {
		return map[string]any{"name": name, "browser_download_url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.2.3/" + name, "digest": "sha256:" + hash, "size": 32}
	}
	payload = map[string]any{"tag_name": "v1.2.3", "assets": []any{asset("mihomo-linux-amd64-v1-v1.2.3.gz"), asset("mihomo-linux-amd64-v1.2.3.gz")}}
	c := &ReleaseClient{HTTP: server.Client(), API: server.URL, OS: "linux", Arch: "amd64"}
	a, e := c.resolve("proxy", "latest", hash)
	if e != nil || a.Asset != "mihomo-linux-amd64-v1-v1.2.3.gz" || a.SHA256 != hash {
		t.Fatal("unsafe CPU or unverified fallback selected", a, e)
	}
	payload["assets"] = []any{map[string]any{"name": a.Asset, "browser_download_url": a.Source, "size": 32}}
	if _, e = c.resolve("proxy", "latest", ""); e == nil {
		t.Fatal("official fallback accepted missing digest")
	}
}

func TestReleaseAdapterDoesNotInstallCodexNativePackages(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	c := &ReleaseClient{HTTP: server.Client(), API: server.URL, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if _, e := c.resolve("codex", "latest", ""); e == nil || requests.Load() != 0 {
		t.Fatal("Codex native download remains enabled")
	}
	if _, e := c.artifactBytes(Artifact{Source: "/missing/codex"}, "codex"); e == nil {
		t.Fatal("Codex native unpacker remains enabled")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "mihomo")
	if e = os.WriteFile(path, adapterNative("offline"), 0755); e != nil {
		t.Fatal(e)
	}
	if b, e := c.artifactBytes(Artifact{Source: path}, "proxy"); e != nil || len(b) == 0 {
		t.Fatal("offline Mihomo program no longer supported", e)
	}
}
