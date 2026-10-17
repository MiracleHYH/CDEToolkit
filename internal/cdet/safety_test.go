package cdet

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func unsafeArchive(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, h := range headers {
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg && h.Size < 1024 {
			_, _ = tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
		}
	}
	_ = tw.Close()
	return b.Bytes()
}

func TestArtifactSafetyGuardsSurviveRefactor(t *testing.T) {
	f := newFixture(t)
	c := newReleaseClient()
	for name, headers := range map[string][]*tar.Header{
		"traversal":    {{Name: "../mihomo", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}},
		"symlink":      {{Name: "mihomo", Typeflag: tar.TypeSymlink, Linkname: "/external"}},
		"duplicate":    {{Name: "mihomo", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}, {Name: "bin/mihomo", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}},
		"oversized":    {{Name: "mihomo", Mode: 0755, Size: maxArtifact + 1, Typeflag: tar.TypeReg}},
		"wrong-module": {{Name: "codex", Mode: 0755, Size: 1, Typeflag: tar.TypeReg}},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(f.root, name+".tar")
			write(t, p, string(unsafeArchive(t, headers)), 0600)
			if _, e := c.artifactBytes(Artifact{Source: p}, "proxy"); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
	data := packed(t, "", []byte("#!/bin/sh\nexit 0\n"))
	p := filepath.Join(f.root, "truncated.gz")
	write(t, p, string(data[:len(data)-3]), 0600)
	if _, e := c.artifactBytes(Artifact{Source: p}, "proxy"); e == nil {
		t.Fatal("truncated gzip accepted")
	}
	p = filepath.Join(f.root, "wrong-arch")
	write(t, p, string(fakeELF("x")), 0755)
	c.OS = "linux"
	c.Arch = "arm64"
	if _, e := c.artifactBytes(Artifact{Source: p}, "proxy"); e == nil {
		t.Fatal("wrong native architecture accepted")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/insecure", http.StatusFound)
	}))
	defer server.Close()
	c.HTTP = server.Client()
	if _, e := c.get(server.URL, 100); e == nil {
		t.Fatal("insecure redirect accepted")
	}
}
