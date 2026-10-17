package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mockStandalone(t *testing.T, home, entry string, content []byte) {
	t.Helper()
	target := filepath.Join(home, "packages", "standalone", "mock", "bin", "codex")
	write(t, target, content, 0700)
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, entry); err != nil {
		t.Fatal(err)
	}
}

func TestValidateConfigAllowsMissingExternalPathsWithoutCreatingThem(t *testing.T) {
	s, home := fixture(t)
	command := filepath.Join(s.UserHome, "missing-bin", "codex")
	if err := s.ValidateConfig(Config{Home: home, Command: command, Channel: "standalone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("validation created Codex home")
	}
	if _, err := os.Stat(filepath.Dir(command)); !os.IsNotExist(err) {
		t.Fatal("validation created a command directory")
	}
	for _, c := range []Config{
		{Command: command},
		{Home: home, Channel: "standalone"},
		{Home: home, Command: "relative/codex"},
		{Home: home, Command: command, Channel: "custom"},
		{Home: home, Command: filepath.Join(s.Root, "bin", "codex")},
		{Home: home, Command: filepath.Join(s.UserHome, "with:colon", "codex")},
	} {
		if err := s.ValidateConfig(c); err == nil {
			t.Fatalf("accepted invalid configuration %+v", c)
		}
	}
}

func TestCheckInstallIsReadOnlyAndChecksActualUpdateChannel(t *testing.T) {
	s, home := fixture(t)
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("preflight ran an external command")
		return nil, nil
	}
	c, err := s.CheckInstall(Config{Home: home}, false)
	if err != nil || c.Home != home {
		t.Fatalf("%+v %v", c, err)
	}
	for _, path := range []string{home, filepath.Join(s.UserHome, ".local")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("preflight created directories")
		}
	}
	command := filepath.Join(s.UserHome, "custom-bin", "codex")
	mockStandalone(t, home, command, []byte("original"))
	c = Config{Home: home, Command: command, Channel: "standalone"}
	if _, err = s.CheckInstall(c, true); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(command); err != nil {
		t.Fatal(err)
	}
	write(t, command, []byte("unrelated replacement"), 0700)
	if _, err = s.CheckInstall(c, true); err == nil {
		t.Fatal("trusted stale standalone channel after entry replacement")
	}
	if _, err = s.Update(c, nil); err == nil {
		t.Fatal("updated an unrelated replacement")
	}
	if string(read(t, command)) != "unrelated replacement" {
		t.Fatal("changed replacement command")
	}
}

func TestInstallRejectsColonInProgramDirectoryButAllowsItInCodexHome(t *testing.T) {
	s, home := fixture(t)
	if _, err := s.CheckInstall(Config{Home: home + ":data"}, false); err != nil {
		t.Fatal("Codex data home does not enter PATH:", err)
	}
	s.UserHome += ":invalid-PATH-entry"
	if _, err := s.CheckInstall(Config{Home: home}, false); err == nil {
		t.Fatal("allowed default program directory containing PATH separator")
	}
}

func TestUpdateUsesAssociatedEntryAndHomeWithExplicitEmptyProxy(t *testing.T) {
	s, home := fixture(t)
	command := filepath.Join(s.UserHome, "custom program bin", "codex")
	mockStandalone(t, home, command, []byte("original"))
	for _, name := range []string{"auth.json", "config.toml", "sessions/one", "state.db"} {
		write(t, filepath.Join(home, name), []byte("preserve "+name), 0600)
	}
	s.Env = append(s.Env, "HTTP_PROXY=http://old:8080", "http_proxy=http://old:8080", "CODEX_HOME=/stale", "CODEX_INSTALL_DIR=/stale")
	calls := 0
	s.Run = func(path string, _ []string, env []string, _ []byte) ([]byte, error) {
		calls++
		if envValue(env, "CODEX_HOME") != home || envValue(env, "CODEX_INSTALL_DIR") != filepath.Dir(command) {
			t.Fatal("update selected a second home or entry")
		}
		if envValue(env, "http_proxy") != "" || envValue(env, "HTTP_PROXY") != "" || !strings.Contains(strings.Join(env, "\n"), "http_proxy=\n") {
			t.Fatal("explicit empty proxy failed to clear inherited values")
		}
		if path == "curl" {
			return []byte("mock script"), nil
		}
		return nil, nil
	}
	c, err := s.Update(Config{Home: home, Command: command, Channel: "standalone"}, map[string]string{"http_proxy": ""})
	if err != nil || calls != 2 || c.Command != command {
		t.Fatalf("%+v %d %v", c, calls, err)
	}
	for _, name := range []string{"auth.json", "config.toml", "sessions/one", "state.db"} {
		if string(read(t, filepath.Join(home, name))) != "preserve "+name {
			t.Fatal("changed external Codex data")
		}
	}
}

func TestUpdateRejectsEntryForAnotherHome(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(s.UserHome, ".local", "bin", "codex")
	mockStandalone(t, home+"-other", command, []byte("other installation"))
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("foreign home ran installer")
		return nil, nil
	}
	if _, err := s.Update(Config{Home: home, Command: command, Channel: "standalone"}, nil); err == nil {
		t.Fatal("updated command belonging to another Codex home")
	}
}

func TestUpdateRejectsUsingReleaseBinaryAsInstallEntry(t *testing.T) {
	s, home := fixture(t)
	command := filepath.Join(home, "packages", "standalone", "mock", "bin", "codex")
	write(t, command, []byte("package binary"), 0700)
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("package-internal command ran installer")
		return nil, nil
	}
	if _, err := s.Update(Config{Home: home, Command: command, Channel: "standalone"}, nil); err == nil {
		t.Fatal("allowed installer to replace its own release binary")
	}
	if string(read(t, command)) != "package binary" {
		t.Fatal("changed package binary")
	}
}

func TestInstallerSuccessMustProduceSelectedStandaloneEntry(t *testing.T) {
	s, home := fixture(t)
	s.Run = func(path string, _ []string, _ []string, _ []byte) ([]byte, error) {
		if path == "curl" {
			return []byte("mock script"), nil
		}
		write(t, filepath.Join(s.UserHome, ".local", "bin", "codex"), []byte("unexpected program"), 0700)
		return nil, nil
	}
	if _, err := s.Install(Config{Home: home}, nil); err == nil {
		t.Fatal("registered unexpected installer result")
	}
}

func TestIntegrationJSONUsesLowercaseFields(t *testing.T) {
	b, err := json.Marshal(Status{State: "linked", Home: "/external", ProxyConfigured: true})
	if err != nil || !bytes.Contains(b, []byte(`"proxy_configured":true`)) || bytes.Contains(b, []byte(`"State"`)) {
		t.Fatalf("%s %v", b, err)
	}
	b, err = json.Marshal(Candidate{Home: "/external", Source: "default"})
	if err != nil || string(b) != `{"home":"/external","source":"default"}` {
		t.Fatalf("%s %v", b, err)
	}
}
