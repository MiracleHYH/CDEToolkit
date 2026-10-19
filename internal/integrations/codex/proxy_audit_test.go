package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProxyReceiptCannotAuthorizeDeletingUserBytes(t *testing.T) {
	s, home := fixture(t)
	path := filepath.Join(home, ".env")
	original := []byte("TOKEN=keep\r\nUSER=private\n")
	write(t, path, original, 0600)
	c, err := s.SetProxy(Config{Home: home}, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	content := read(t, path)
	for name, receipt := range map[string]string{
		"whole-file":       string(content),
		"prefix":           string(original) + c.ProxyBlock,
		"suffix":           c.ProxyBlock + "USER=private\n",
		"extra-key":        strings.Replace(c.ProxyBlock, proxyEnd, "USER=private\n"+proxyEnd, 1),
		"different-values": strings.Replace(c.ProxyBlock, "https_proxy='http://127.0.0.1:7890'", "https_proxy='http://127.0.0.1:7891'", 1),
	} {
		t.Run(name, func(t *testing.T) {
			bad := c
			bad.ProxyBlock = receipt
			if err := s.Detach(bad); err == nil {
				t.Fatal("untrusted receipt authorized deletion")
			}
			if !bytes.Equal(read(t, path), content) {
				t.Fatal("detach removed user content")
			}
		})
	}
	if err := s.Detach(c); err != nil || !bytes.Equal(read(t, path), original) {
		t.Fatalf("valid receipt no longer works: %v", err)
	}
}

func TestProxyReceiptCannotRemoveInlineMarker(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := s.SetProxy(Config{Home: home}, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".env")
	modified := append([]byte("USER=prefix"), read(t, path)...)
	write(t, path, modified, 0600)
	if err = s.Detach(c); err == nil || !bytes.Equal(read(t, path), modified) {
		t.Fatal("modified block location was deleted")
	}
}

func TestProxyRefusesHardLinksWithoutBreakingRelationship(t *testing.T) {
	s, home := fixture(t)
	path := filepath.Join(home, ".env")
	write(t, path, []byte("USER=original\n"), 0600)
	other := filepath.Join(s.UserHome, "shared-env")
	if err := os.Link(path, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProxy(Config{Home: home}, "http://127.0.0.1:7890"); err == nil {
		t.Fatal("replaced a hard-linked file")
	}
	a, _ := os.Stat(path)
	b, _ := os.Stat(other)
	if !os.SameFile(a, b) || string(read(t, path)) != "USER=original\n" {
		t.Fatal("broke hard-link relationship or changed user bytes")
	}
}

func TestProxySnapshotRefusesReplacedHomeEvenWhenEnvMissing(t *testing.T) {
	_, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := readEnv(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(home, home+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = writeEnv(home, before, []byte("http_proxy='http://127.0.0.1:7890'\n")); err == nil {
		t.Fatal("wrote into a replacement home")
	}
	for _, dir := range []string{home, home + "-old"} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("directory was modified: %v %v", entries, err)
		}
	}
}

func TestProxyUndoRefusesMovedHomeWithSameEnvFile(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	_, undo, err := s.ChangeProxy(Config{Home: home}, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(home, home+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(home+"-old", ".env"), filepath.Join(home, ".env")); err != nil {
		t.Fatal(err)
	}
	if err = undo(); err == nil {
		t.Fatal("compensation removed file in a replacement directory")
	}
	if _, err = os.Stat(filepath.Join(home, ".env")); err != nil {
		t.Fatal("lost user-moved file")
	}
}

func TestDotenvProxyUsesOnlySupportedLiteralAssignments(t *testing.T) {
	content := []byte("# EXAMPLE='not a multiline value\nOTHER=\"multiline\nHTTP_PROXY=http://ignored:1234\n\"\nHttp_Proxy=http://case-sensitive-ignored:1234\nexport\thttp_proxy = 'http://user:$literal@localhost:7890'\nHTTPS_PROXY=http://localhost:7891\t# comment\nNO_PROXY= # deliberately empty\n")
	got, err := dotenvProxy(content)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"http_proxy": "http://user:$literal@localhost:7890", "https_proxy": "http://localhost:7891", "no_proxy": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("not literal or wrong case handling: %#v", got)
	}
	for _, content := range []string{
		"http_proxy=http://${PROXY_HOST}:7890\n",
		"http_proxy=\"http://localhost:$PORT\"\n",
		"NO_PROXY=\"local\\nhost\"\n",
		"HTTP_PROXY='http://localhost:7890\n",
		"OTHER=\"unterminated\nhttp_proxy=http://localhost:7890\n",
		"http_proxy=http://one:1234\nHTTP_PROXY=http://two:1234\n",
	} {
		if _, err := dotenvProxy([]byte(content)); err == nil {
			t.Fatalf("ambiguous dotenv accepted: %q", content)
		}
	}
}

func TestCheckProxyIsReadOnlyAndMatchesActualValidation(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckProxy(Config{Home: home}, "http://127.0.0.1:7890"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatal("preflight created files")
	}
	path := filepath.Join(home, ".env")
	content := []byte("HTTP_PROXY=http://user-proxy:1234\n")
	write(t, path, content, 0600)
	for _, endpoint := range []string{"http://127.0.0.1:7890", "http://host:0", "file:///tmp/x"} {
		if err := s.CheckProxy(Config{Home: home}, endpoint); err == nil {
			t.Fatal("preflight accepted a conflict or invalid endpoint")
		}
		if !bytes.Equal(read(t, path), content) {
			t.Fatal("preflight changed user bytes")
		}
	}
}

func TestDetachWithoutReceiptDoesNotInspectExternalHome(t *testing.T) {
	s, home := fixture(t)
	if err := s.Detach(Config{Home: home}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".env"), []byte(proxyStart+"\nUSER=unowned\n"), 0600)
	before := read(t, filepath.Join(home, ".env"))
	if err := s.Detach(Config{Home: home}); err != nil || !bytes.Equal(before, read(t, filepath.Join(home, ".env"))) {
		t.Fatal("detach without ownership touched foreign content")
	}
}
