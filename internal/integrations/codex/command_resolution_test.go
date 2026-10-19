package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preserveResolutionData(t *testing.T, home string) func() {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range []string{"auth.json", "config.toml", "sessions/one.jsonl", "state.sqlite", ".env"} {
		path := filepath.Join(home, name)
		files[path] = []byte("preserve " + name + "\n")
		write(t, path, files[path], 0600)
	}
	return func() {
		t.Helper()
		for path, before := range files {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !bytes.Equal(read(t, path), before) {
				t.Fatalf("command discovery/preflight changed private data: %s", path)
			}
		}
	}
}

func TestUpdateResolvesLaterPATHEntryAndStatusUsesActualChannel(t *testing.T) {
	s, home := fixture(t)
	preserved := preserveResolutionData(t, home)
	c, err := s.Associate(home)
	if err != nil || c.Command != "" || c.Channel != "" {
		t.Fatalf("unexpected initial association: %+v %v", c, err)
	}
	command := filepath.Join(s.UserHome, "later-bin", "codex")
	mockStandalone(t, home, command, []byte("inert program"))
	s.Env = []string{"PATH=" + filepath.Dir(command)}
	s.Run = func(path string, args, _ []string, _ []byte) ([]byte, error) {
		if path != command || strings.Join(args, " ") != "--version" {
			t.Fatal("status executed more than the selected version probe")
		}
		return []byte("codex-cli 1.2.3\n"), nil
	}
	status := s.Status(c)
	if status.Command != command || status.Channel != "standalone" || status.Version != "1.2.3" {
		t.Fatalf("status failed to resolve the current entry: %+v", status)
	}
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("preflight executed an external command")
		return nil, nil
	}
	next, err := s.CheckInstall(c, true)
	if err != nil || next.Command != status.Command || next.Channel != status.Channel {
		t.Fatalf("status and update preflight disagree: %+v %v", next, err)
	}
	if c.Command != "" || c.Channel != "" {
		t.Fatal("read-only resolution changed the saved association")
	}
	// A stale label never overrides the current physical standalone target.
	stale := next
	stale.Channel = "unknown"
	if resolved, err := s.CheckInstall(stale, true); err != nil || resolved.Channel != "standalone" {
		t.Fatalf("trusted a stale channel instead of the actual link: %+v %v", resolved, err)
	}
	preserved()
}

func TestCommandResolutionPriorityAndExplicitInstallDirectory(t *testing.T) {
	s, home := fixture(t)
	preserved := preserveResolutionData(t, home)
	pathEntry := filepath.Join(s.UserHome, "path-bin", "codex")
	customEntry := filepath.Join(s.UserHome, "custom-bin", "codex")
	defaultEntry := filepath.Join(s.UserHome, ".local", "bin", "codex")
	for _, command := range []string{pathEntry, customEntry, defaultEntry} {
		mockStandalone(t, home, command, []byte("inert program"))
	}
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("discovery/preflight executed a command")
		return nil, nil
	}
	for _, scenario := range []struct {
		name, path, installDir, want string
	}{
		{"PATH-first", filepath.Dir(pathEntry), filepath.Dir(customEntry), pathEntry},
		{"explicit-install-dir", "/unavailable", filepath.Dir(customEntry), customEntry},
		{"default-entry", "/unavailable", "/missing", defaultEntry},
		{"relative-install-dir-ignored", "/unavailable", "relative", defaultEntry},
		{"control-install-dir-ignored", "/unavailable", filepath.Dir(customEntry) + "\n", defaultEntry},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s.Env = []string{"PATH=" + scenario.path, "CODEX_INSTALL_DIR=" + scenario.installDir}
			associated, err := s.Associate(home)
			if err != nil || associated.Command != scenario.want || associated.Channel != "standalone" {
				t.Fatalf("wrong association: %+v %v", associated, err)
			}
			resolved, err := s.CheckInstall(Config{Home: home}, true)
			if err != nil || resolved.Command != scenario.want || resolved.Channel != "standalone" {
				t.Fatalf("wrong preflight resolution: %+v %v", resolved, err)
			}
		})
	}
	preserved()
}

func TestSavedUnavailableEntryNeverFallsBackToAnotherProgram(t *testing.T) {
	for _, kind := range []string{"missing", "broken-link", "not-executable"} {
		t.Run(kind, func(t *testing.T) {
			s, home := fixture(t)
			preserved := preserveResolutionData(t, home)
			command := filepath.Join(s.UserHome, "saved", "codex")
			alternative := filepath.Join(s.UserHome, "on-PATH", "codex")
			mockStandalone(t, home, alternative, []byte("inert program"))
			write(t, filepath.Join(filepath.Dir(command), "keep"), []byte("keep"), 0600)
			switch kind {
			case "broken-link":
				if err := os.Symlink(filepath.Join(home, "absent"), command); err != nil {
					t.Fatal(err)
				}
			case "not-executable":
				write(t, command, []byte("user file"), 0600)
			}
			s.Env = []string{"PATH=" + filepath.Dir(alternative), "CODEX_INSTALL_DIR=" + filepath.Dir(alternative)}
			s.Run = func(string, []string, []string, []byte) ([]byte, error) {
				t.Fatal("unavailable explicit entry executed another command")
				return nil, nil
			}
			c := Config{Home: home, Command: command, Channel: "standalone"}
			if status := s.Status(c); status.Command != command || status.Version != "" {
				t.Fatalf("status replaced the explicit entry: %+v", status)
			}
			if _, err := s.CheckInstall(c, true); err == nil || !strings.Contains(err.Error(), "入口不存在或不可执行") {
				t.Fatalf("wrong explicit-entry failure: %v", err)
			}
			preserved()
		})
	}
}

func TestMissingEntryAndActualOtherChannelsHaveDistinctSafeErrors(t *testing.T) {
	for _, kind := range []string{"missing", "npm", "homebrew", "unknown", "standalone-install"} {
		t.Run(kind, func(t *testing.T) {
			s, home := fixture(t)
			preserved := preserveResolutionData(t, home)
			s.Env = []string{"PATH=/unavailable"}
			c := Config{Home: home}
			want := "未发现 Codex 程序入口"
			if kind == "missing" {
				// Historical package files do not establish a user command entry.
				write(t, filepath.Join(home, "packages", "standalone", "old-version", "bin", "codex"), []byte("unselected old program"), 0700)
			}
			if kind != "missing" {
				entry := filepath.Join(s.UserHome, "bin", "codex")
				target := filepath.Join(s.UserHome, "custom", "codex")
				switch kind {
				case "npm":
					target = filepath.Join(s.UserHome, "node_modules", "@openai", "codex", "bin", "codex")
					want = "npm 渠道"
				case "homebrew":
					target = filepath.Join(s.UserHome, "Cellar", "codex", "1.2.3", "bin", "codex")
					want = "homebrew 渠道"
				case "unknown":
					want = "安装渠道无法确认"
				case "standalone-install":
					target = filepath.Join(home, "packages", "standalone", "existing", "bin", "codex")
					want = "请使用官方更新"
				}
				write(t, target, []byte("inert existing program"), 0700)
				write(t, filepath.Join(filepath.Dir(entry), "keep"), []byte("keep"), 0600)
				if err := os.Symlink(target, entry); err != nil {
					t.Fatal(err)
				}
				// The saved label is deliberately false; physical source wins.
				c.Command, c.Channel = entry, "standalone"
			}
			s.Run = func(string, []string, []string, []byte) ([]byte, error) {
				t.Fatal("unsafe channel started an installer or version command")
				return nil, nil
			}
			_, err := s.CheckInstall(c, kind != "standalone-install")
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("failure did not distinguish %s: %v", kind, err)
			}
			if kind == "missing" && (strings.Contains(err.Error(), "其他渠道") || !strings.Contains(err.Error(), "官方安装")) {
				t.Fatal("missing program was misreported as an unsupported installation")
			}
			preserved()
		})
	}
}
