package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	encodingbinary "encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func hash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func releaseFixture(platform string, archive []byte) (release, map[string][]byte) {
	name := "nido-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz"
	sidecar := []byte(hash(archive) + "  " + name + "\n")
	r := release{Tag: "v0.6.0", URL: releasesAPI + "/123", HTMLURL: "https://github.com/" + repository + "/releases/tag/v0.6.0"}
	files := map[string][]byte{}
	for _, file := range []struct {
		name string
		data []byte
	}{{name, archive}, {name + ".sha256", sidecar}} {
		url := "https://github.com/" + repository + "/releases/download/" + r.Tag + "/" + file.name
		r.Assets = append(r.Assets, asset{Name: file.name, URL: url, Digest: "sha256:" + hash(file.data), Size: int64(len(file.data))})
		files[url] = file.data
	}
	return r, files
}

func fakeClient(t *testing.T, r release, files map[string][]byte, prefix string) (*http.Client, *[]string) {
	t.Helper()
	var calls []string
	return &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		source := req.URL.String()
		if prefix != "" {
			if !strings.HasPrefix(source, prefix+"/") {
				t.Fatalf("request bypassed selected GitHub prefix: %s", source)
			}
			source = strings.TrimPrefix(source, prefix+"/")
		}
		var data []byte
		if source == releasesAPI+"/latest" || source == releasesAPI+"/tags/v0.6.0" {
			data, _ = json.Marshal(r)
		} else {
			var found bool
			data, found = files[source]
			if !found {
				t.Fatalf("unexpected external request: %s", source)
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}, &calls
}

func TestCheckStableVersionsUsesOnlyMetadata(t *testing.T) {
	for _, tc := range []struct {
		current   string
		available bool
	}{{"0.5.0", true}, {"0.6.0", false}, {"0.7.0", false}, {"0.10.0", false}, {"999999999999999999999999.0.0", false}} {
		t.Run(tc.current, func(t *testing.T) {
			r := release{Tag: "v0.6.0"}
			client, calls := fakeClient(t, r, nil, "")
			info, err := (Service{HTTP: client}).Check(tc.current)
			if err != nil || info.Current != tc.current || info.Latest != "0.6.0" || info.Available != tc.available || len(*calls) != 1 {
				t.Fatalf("info=%+v calls=%v err=%v", info, *calls, err)
			}
		})
	}
	for _, version := range []string{"", "dev", "v0.6.0", "01.0.0", "0.6.0-rc1", "0.6.0\n", "0.6"} {
		if ValidVersion(version) {
			t.Fatal("accepted non-stable version", version)
		}
		client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid version made an external request")
			return nil, fmt.Errorf("forbidden")
		})}
		if _, err := (Service{HTTP: client}).Check(version); err == nil {
			t.Fatal("check accepted invalid current version")
		}
		if version != "" {
			if _, err := (Service{HTTP: client}).Prepare(version); err == nil {
				t.Fatal("prepare accepted invalid version")
			}
		}
	}
}

func TestIsNewerRequiresStableVersionsAndNumericOrdering(t *testing.T) {
	for _, tc := range []struct {
		current, candidate string
		want               bool
	}{
		{"0.5.0", "0.6.0", true}, {"1.9.0", "1.10.0", true},
		{"0.6.0", "0.6.0", false}, {"0.6.0", "0.5.0", false},
		{"dev", "1.0.0", false}, {"1.0.0", "dev", false},
		{"v1.0.0", "2.0.0", false}, {"1.0.0", "2.0.0-rc1", false},
		{"999999999999999999999999.0.0", "1000000000000000000000000.0.0", true},
	} {
		if got := IsNewer(tc.current, tc.candidate); got != tc.want {
			t.Fatalf("IsNewer(%q,%q)=%v want %v", tc.current, tc.candidate, got, tc.want)
		}
	}
}

func TestReleaseRejectsUnexpectedRepositoryAndTags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*release)
	}{
		{"draft", func(r *release) { r.Draft = true }},
		{"prerelease", func(r *release) { r.Prerelease = true }},
		{"gds-tag", func(r *release) { r.Tag = "gds-v0.6.0" }},
		{"tag-without-prefix", func(r *release) { r.Tag = "0.6.0" }},
		{"other-tag", func(r *release) { r.Tag = "v0.7.0" }},
		{"foreign-page", func(r *release) { r.HTMLURL = "https://github.com/other/repo/releases/tag/v0.6.0" }},
		{"foreign-api", func(r *release) { r.URL = "https://api.github.com/repos/other/repo/releases/123" }},
		{"api-query", func(r *release) { r.URL += "?private=x" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, files := releaseFixture("linux/amd64", []byte("not downloaded"))
			tc.mutate(&r)
			client, calls := fakeClient(t, r, files, "")
			if _, err := (Service{HTTP: client, Platform: "linux/amd64"}).Prepare("0.6.0"); err == nil || len(*calls) != 1 {
				t.Fatalf("accepted invalid release or fetched an asset: calls=%v err=%v", *calls, err)
			}
		})
	}
}

func nativeFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "nido"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/MiracleHYH/CDEToolkit\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf("package main\nimport \"os\"\nfunc main(){_ = os.WriteFile(%q, []byte(\"executed\"),0600)}\n", marker)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "nido", "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "fixture")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-buildvcs=false", "-o", output, "./cmd/nido")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated Go fixture: %v: %s", err, out)
	}
	b, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return b, marker
}

func archiveFixture(t *testing.T, data []byte, mutate func(*tar.Header), extra bool) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	h := &tar.Header{Name: "nido", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(data)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
	if mutate != nil {
		mutate(h)
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if h.Typeflag == tar.TypeReg {
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if extra {
		if err := tw.WriteHeader(&tar.Header{Name: "extra", Typeflag: tar.TypeReg, Mode: 0600, Size: 0, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestPrepareValidatesNativeReleaseWithoutExecution(t *testing.T) {
	binary, marker := nativeFixture(t)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	archive := archiveFixture(t, binary, nil, false)
	r, files := releaseFixture(platform, archive)
	for _, version := range []string{"", "0.6.0"} {
		client, calls := fakeClient(t, r, files, "")
		prepared, err := (Service{HTTP: client}).Prepare(version)
		if err != nil || prepared.Version != "0.6.0" || !bytes.Equal(prepared.Data, binary) || len(*calls) != 3 {
			t.Fatalf("version=%q result=%s calls=%v err=%v", version, prepared.Version, *calls, err)
		}
		if _, err := os.Lstat(marker); !os.IsNotExist(err) {
			t.Fatal("preparation executed downloaded code")
		}
	}
	for _, prefix := range []string{"https://mirror.example"} {
		client, calls := fakeClient(t, r, files, prefix)
		if _, err := (Service{HTTP: client, GitHubProxy: prefix}).Prepare("0.6.0"); err != nil || len(*calls) != 3 {
			t.Fatalf("proxy calls=%v err=%v", *calls, err)
		}
	}
	other := "linux/amd64"
	if platform == other {
		other = "darwin/arm64"
	}
	if err := checkBinary(binary, other); err == nil {
		t.Fatal("accepted a binary for another platform")
	}
	if err := checkBinary([]byte("#!/bin/sh\nexit 0\n"), platform); err == nil {
		t.Fatal("accepted a non-Go executable")
	}
	for name, modified := range map[string][]byte{
		"cgo-enabled":  bytes.ReplaceAll(binary, []byte("CGO_ENABLED=0"), []byte("CGO_ENABLED=1")),
		"other-main":   bytes.ReplaceAll(binary, []byte("/cmd/nido"), []byte("/cmd/evil")),
		"other-module": bytes.ReplaceAll(binary, []byte("github.com/MiracleHYH/CDEToolkit"), []byte("github.com/MiracleHYY/CDEToolkit")),
	} {
		if bytes.Equal(binary, modified) {
			t.Fatal("fixture build info was not modified", name)
		}
		if err := checkBinary(modified, platform); err == nil {
			t.Fatal("accepted invalid build identity", name)
		}
	}
	wrongHeader := append([]byte(nil), binary...)
	if runtime.GOOS == "linux" {
		otherMachine := elf.EM_X86_64
		if runtime.GOARCH == "amd64" {
			otherMachine = elf.EM_AARCH64
		}
		encodingbinary.LittleEndian.PutUint16(wrongHeader[18:20], uint16(otherMachine))
	} else {
		otherCPU := macho.CpuAmd64
		if runtime.GOARCH == "amd64" {
			otherCPU = macho.CpuArm64
		}
		encodingbinary.LittleEndian.PutUint32(wrongHeader[4:8], uint32(otherCPU))
	}
	if err := checkBinary(wrongHeader, platform); err == nil {
		t.Fatal("accepted wrong native CPU hidden by unchanged Go build info")
	}
}

func TestUnsupportedPlatformDoesNotDownload(t *testing.T) {
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported platform attempted a download")
		return nil, fmt.Errorf("forbidden")
	})}
	if _, err := (Service{HTTP: client, Platform: "linux/riscv64"}).Prepare(""); err == nil {
		t.Fatal("accepted unsupported platform")
	}
}

func TestPrepareRejectsUntrustedAssetMetadataAndChecksums(t *testing.T) {
	platform := "linux/amd64"
	for _, tc := range []struct {
		name   string
		mutate func(*release, map[string][]byte)
	}{
		{"missing-package", func(r *release, _ map[string][]byte) { r.Assets = nil }},
		{"missing-checksum", func(r *release, _ map[string][]byte) { r.Assets = r.Assets[:1] }},
		{"duplicate-package", func(r *release, _ map[string][]byte) { r.Assets = append(r.Assets, r.Assets...) }},
		{"foreign-url", func(r *release, _ map[string][]byte) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, repository, "other/repo", 1)
		}},
		{"other-tag-url", func(r *release, _ map[string][]byte) {
			r.Assets[0].URL = strings.Replace(r.Assets[0].URL, "v0.6.0", "v0.5.0", 1)
		}},
		{"query-url", func(r *release, _ map[string][]byte) { r.Assets[0].URL += "?token=x" }},
		{"missing-api-hash", func(r *release, _ map[string][]byte) { r.Assets[0].Digest = "" }},
		{"wrong-api-hash", func(r *release, _ map[string][]byte) { r.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64) }},
		{"wrong-size", func(r *release, _ map[string][]byte) { r.Assets[0].Size++ }},
		{"wrong-checksum-size", func(r *release, _ map[string][]byte) { r.Assets[1].Size++ }},
		{"wrong-checksum-api-hash", func(r *release, _ map[string][]byte) { r.Assets[1].Digest = "sha256:" + strings.Repeat("f", 64) }},
		{"oversize", func(r *release, _ map[string][]byte) { r.Assets[0].Size = maxArchive + 1 }},
		{"changed-body", func(_ *release, files map[string][]byte) {
			for url := range files {
				if strings.HasSuffix(url, ".tar.gz") {
					files[url] = []byte("changed package")
				}
			}
		}},
		{"wrong-checksum-name", func(r *release, files map[string][]byte) {
			for i, a := range r.Assets {
				if strings.HasSuffix(a.Name, ".sha256") {
					b := bytes.ReplaceAll(files[a.URL], []byte("nido-linux-amd64"), []byte("other-linux-amd64"))
					files[a.URL] = b
					r.Assets[i].Digest = "sha256:" + hash(b)
					r.Assets[i].Size = int64(len(b))
				}
			}
		}},
		{"duplicate-checksum-line", func(r *release, files map[string][]byte) {
			for i, a := range r.Assets {
				if strings.HasSuffix(a.Name, ".sha256") {
					b := append(append([]byte{}, files[a.URL]...), files[a.URL]...)
					files[a.URL] = b
					r.Assets[i].Digest = "sha256:" + hash(b)
					r.Assets[i].Size = int64(len(b))
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, files := releaseFixture(platform, archiveFixture(t, []byte("binary"), nil, false))
			tc.mutate(&r, files)
			client, _ := fakeClient(t, r, files, "")
			if _, err := (Service{HTTP: client, Platform: platform}).Prepare("0.6.0"); err == nil {
				t.Fatal("accepted invalid asset metadata or content")
			}
		})
	}
}

func TestUnpackRejectsMaliciousTarAndCompressedTails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*tar.Header)
		extra  bool
	}{
		{"other-name", func(h *tar.Header) { h.Name = "other" }, false},
		{"traversal", func(h *tar.Header) { h.Name = "../nido" }, false},
		{"wrong-mode", func(h *tar.Header) { h.Mode = 04755 }, false},
		{"foreign-owner", func(h *tar.Header) { h.Uid = 1000 }, false},
		{"symlink", func(h *tar.Header) { h.Typeflag = tar.TypeSymlink; h.Linkname = "/tmp/other"; h.Size = 0 }, false},
		{"hardlink", func(h *tar.Header) { h.Typeflag = tar.TypeLink; h.Linkname = "nido-old"; h.Size = 0 }, false},
		{"pax", func(h *tar.Header) {
			h.Format = tar.FormatPAX
			h.PAXRecords = map[string]string{"comment": "untrusted"}
		}, false},
		{"extra-member", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := unpack(archiveFixture(t, []byte("program"), tc.mutate, tc.extra)); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
	valid := archiveFixture(t, []byte("program"), nil, false)
	if binary, err := unpack(valid); err != nil || string(binary) != "program" {
		t.Fatalf("valid archive failed: %v", err)
	}
	crc := append([]byte{}, valid...)
	crc[len(crc)-8] ^= 1
	for name, data := range map[string][]byte{"crc": crc, "truncated": valid[:len(valid)-4], "compressed-garbage": append(append([]byte{}, valid...), 1), "concatenated-gzip": append(append([]byte{}, valid...), valid...)} {
		t.Run(name, func(t *testing.T) {
			if _, err := unpack(data); err == nil {
				t.Fatal("accepted corrupt compressed data")
			}
		})
	}
	gz, err := gzip.NewReader(bytes.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	tarBytes, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	_ = gz.Close()
	for _, extra := range [][]byte{[]byte("hidden after tar"), make([]byte, 512), make([]byte, 8<<20)} {
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		_, _ = w.Write(tarBytes)
		_, _ = w.Write(extra)
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := unpack(out.Bytes()); err == nil {
			t.Fatal("accepted data after tar terminator")
		}
	}
	for _, removed := range []int{512, 1024} {
		var out bytes.Buffer
		w := gzip.NewWriter(&out)
		_, _ = w.Write(tarBytes[:len(tarBytes)-removed])
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := unpack(out.Bytes()); err == nil {
			t.Fatal("accepted missing tar terminators")
		}
	}
}
