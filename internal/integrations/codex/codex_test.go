package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Service, string) {
	t.Helper()
	dir := t.TempDir()
	var err error
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "user")
	root := filepath.Join(dir, "nido")
	for _, p := range []string{home, root} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return Service{UserHome: home, Root: root, Env: []string{"PATH=/usr/bin:/bin"}}, filepath.Join(dir, "external codex")
}

func write(t *testing.T, path string, b []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestAssociateDiscoverDoesNotModifyHome(t *testing.T) {
	s, home := fixture(t)
	write(t, filepath.Join(home, "auth.json"), []byte("private auth"), 0600)
	write(t, filepath.Join(home, "sessions", "one"), []byte("session"), 0600)
	write(t, filepath.Join(home, ".env"), []byte("A='$(touch never)'"), 0600)
	s.Env = append(s.Env, "CODEX_HOME="+home)
	c, err := s.Associate(home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Home != home || c.Command != "" || c.ProxyBlock != "" {
		t.Fatalf("unexpected config: %+v", c)
	}
	candidates, err := s.Discover()
	if err != nil || len(candidates) != 1 || candidates[0].Home != home {
		t.Fatalf("%+v %v", candidates, err)
	}
	if string(read(t, filepath.Join(home, ".env"))) != "A='$(touch never)'" {
		t.Fatal("changed dotenv")
	}
	if _, err = s.Associate(filepath.Join(home, "absent")); err == nil {
		t.Fatal("associated nonexistent home")
	}
	if _, err = s.Associate(filepath.Join(s.Root, "codex")); err == nil {
		t.Fatal("accepted internal home")
	}
	link := filepath.Join(filepath.Dir(home), "internal-link")
	if err = os.Symlink(s.Root, link); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Associate(link); err == nil {
		t.Fatal("accepted aliased internal home")
	}
}

func TestProxyPreservesOtherBytesAndDetachesOnlyOwnedContent(t *testing.T) {
	s, home := fixture(t)
	original := []byte("# keep\r\nTOKEN='x $(do-not-run)'\r\nNO_PROXY=localhost")
	path := filepath.Join(home, ".env")
	write(t, path, original, 0640)
	c, err := s.Associate(home)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.SetProxy(c, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(read(t, path), original) {
		t.Fatal("changed unrelated bytes")
	}
	first := read(t, path)
	c, err = s.SetProxy(c, "http://127.0.0.1:7890")
	if err != nil || !bytes.Equal(first, read(t, path)) {
		t.Fatalf("not idempotent: %v", err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
		t.Fatal("permissions changed")
	}
	write(t, path, append(read(t, path), []byte("ANOTHER=user\n")...), 0640)
	if err = s.Detach(c); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(read(t, path), append(append([]byte(nil), original...), []byte("ANOTHER=user\n")...)) {
		t.Fatal("detach changed user bytes")
	}
}

func TestProxyRefusesConflictTamperAndSymlink(t *testing.T) {
	for _, content := range []string{"http_proxy=http://private:1234\n", " export HTTPS_PROXY = 'http://private:1234'\n", proxyStart + "\n"} {
		t.Run(content, func(t *testing.T) {
			s, home := fixture(t)
			path := filepath.Join(home, ".env")
			write(t, path, []byte(content), 0600)
			c, _ := s.Associate(home)
			if _, err := s.SetProxy(c, "http://127.0.0.1:7890"); err == nil {
				t.Fatal("accepted conflict")
			}
			if string(read(t, path)) != content {
				t.Fatal("changed conflict")
			}
		})
	}
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Associate(home)
	c, err := s.SetProxy(c, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".env")
	tampered := bytes.ReplaceAll(read(t, path), []byte("7890"), []byte("7891"))
	write(t, path, tampered, 0600)
	if err = s.Detach(c); err == nil {
		t.Fatal("removed modified block")
	}
	if !bytes.Equal(tampered, read(t, path)) {
		t.Fatal("modified tampered file")
	}
	outside := filepath.Join(s.UserHome, "secret")
	write(t, outside, []byte("TOKEN=secret"), 0600)
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProxy(Config{Home: home}, "http://127.0.0.1:7890"); err == nil {
		t.Fatal("followed dotenv symlink")
	}
	if string(read(t, outside)) != "TOKEN=secret" {
		t.Fatal("changed symlink target")
	}
}

func TestDotenvProxyIsLiteralAndNeverEvaluatesShell(t *testing.T) {
	s, home := fixture(t)
	content := []byte("OTHER=$(touch /must-not-run)\nhttp_proxy='http://127.0.0.1:7890'\nHTTPS_PROXY=\"http://127.0.0.1:7891\" # literal\nNO_PROXY=localhost\n")
	write(t, filepath.Join(home, ".env"), content, 0600)
	got, err := dotenvProxy(content)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"http_proxy": "http://127.0.0.1:7890", "https_proxy": "http://127.0.0.1:7891", "no_proxy": "localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for _, value := range []string{"http://host:0", "http://host:65536", "file:///tmp/a", "http://host\nINJECT=x", "http://host/'"} {
		if _, err = s.SetProxy(Config{Home: home}, value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestInstallDelegatesAndLimitsOwnedEnvironment(t *testing.T) {
	s, home := fixture(t)
	s.Env = append(s.Env, "CODEX_SQLITE_HOME=/leave/unchanged", "CODEX_CUSTOM=keep", "CODEX_INSTALL_DIR=/stale", "HOME="+s.UserHome)
	var calls int
	var temporary string
	s.Run = func(path string, args []string, env []string, input []byte) ([]byte, error) {
		calls++
		temporary = envValue(env, "HOME")
		if temporary == s.UserHome || temporary == "" {
			t.Fatal("installer HOME not isolated")
		}
		if envValue(env, "CODEX_HOME") != home || envValue(env, "CODEX_INSTALL_DIR") != filepath.Join(s.UserHome, ".local", "bin") {
			t.Fatal("wrong upstream directories")
		}
		if envValue(env, "CODEX_SQLITE_HOME") != "/leave/unchanged" || envValue(env, "CODEX_CUSTOM") != "keep" {
			t.Fatal("changed unrelated Codex environment")
		}
		if envValue(env, "http_proxy") != "http://127.0.0.1:7890" || envValue(env, "HTTPS_PROXY") != "http://127.0.0.1:7890" {
			t.Fatal("proxy not injected into both phases")
		}
		if calls == 1 {
			if path != "curl" || args[len(args)-1] != InstallerURL {
				t.Fatal("wrong official download")
			}
			return []byte("# mocked official script\n"), nil
		}
		if path != "/bin/sh" || string(input) != "# mocked official script\n" {
			t.Fatal("wrong script handoff")
		}
		mockStandalone(t, home, filepath.Join(s.UserHome, ".local", "bin", "codex"), []byte("mock binary"))
		return nil, nil
	}
	c, err := s.Install(Config{Home: home}, map[string]string{"http_proxy": "http://127.0.0.1:7890", "https_proxy": "http://127.0.0.1:7890"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || c.Channel != "standalone" || c.Command == "" {
		t.Fatalf("%+v calls %d", c, calls)
	}
	if _, err = os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("temporary HOME leaked")
	}
	if _, err = os.Stat(filepath.Join(s.UserHome, ".profile")); !os.IsNotExist(err) {
		t.Fatal("real shell profile created")
	}
}

func TestUpdateRefusesOtherChannelsAndPreservesDataOnFailure(t *testing.T) {
	s, home := fixture(t)
	write(t, filepath.Join(home, "auth.json"), []byte("private"), 0600)
	command := filepath.Join(s.UserHome, ".local", "bin", "codex")
	write(t, command, []byte("mock"), 0700)
	for _, ch := range []string{"npm", "homebrew", "unknown", ""} {
		s.Run = func(string, []string, []string, []byte) ([]byte, error) {
			t.Fatal("unsupported channel ran installer")
			return nil, nil
		}
		if _, err := s.Update(Config{Home: home, Command: command, Channel: ch}, nil); err == nil {
			t.Fatal("accepted other update channel")
		}
	}
	count := 0
	if err := os.Remove(command); err != nil {
		t.Fatal(err)
	}
	mockStandalone(t, home, command, []byte("mock"))
	s.Run = func(path string, _ []string, _ []string, _ []byte) ([]byte, error) {
		count++
		if path == "curl" {
			return []byte("mock script"), nil
		}
		return nil, fmt.Errorf("mock failure")
	}
	if _, err := s.Update(Config{Home: home, Command: command, Channel: "standalone"}, nil); err == nil {
		t.Fatal("ignored failure")
	}
	if count != 2 || string(read(t, filepath.Join(home, "auth.json"))) != "private" || string(read(t, command)) != "mock" {
		t.Fatal("update failure changed data")
	}
}

func TestStatusLocalAndLatestExplicit(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(s.UserHome, "codex")
	write(t, command, []byte("mock"), 0700)
	s.Run = func(path string, args []string, env []string, _ []byte) ([]byte, error) {
		if path != command || strings.Join(args, " ") != "--version" || envValue(env, "CODEX_HOME") != home {
			t.Fatal("status did more than local version probe")
		}
		return []byte("codex-cli 0.163.0\n"), nil
	}
	st := s.Status(Config{Home: home, Command: command})
	if st.Version != "0.163.0" || st.State != "linked" {
		t.Fatalf("%+v", st)
	}
	s.Run = func(path string, args []string, _ []string, _ []byte) ([]byte, error) {
		if path != "curl" || args[len(args)-1] != LatestURL {
			t.Fatal("wrong version URL")
		}
		return []byte(`{"tag_name":"rust-v0.164.0"}`), nil
	}
	latest, err := s.Latest(nil)
	if err != nil || latest != "0.164.0" {
		t.Fatalf("%q %v", latest, err)
	}
}
