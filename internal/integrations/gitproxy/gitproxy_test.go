package gitproxy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedService(t *testing.T) Service {
	t.Helper()
	home := t.TempDir()
	return Service{UserHome: home, Env: []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1"}}
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func contents(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProxyPreservesOtherBytesAndUsesOnlyHTTPProxy(t *testing.T) {
	s := isolatedService(t)
	path := filepath.Join(s.UserHome, ".gitconfig")
	original := "# untouched configuration\r\n[user]\r\n\tname = Existing Name\r\n[credential]\r\n\thelper = existing-helper"
	put(t, path, original)
	endpoint := "socks5h://user:p%40ss%3Aword@127.0.0.1:17890"
	if err := s.CheckProxy(Config{}, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := contents(t, path); got != original {
		t.Fatal("preflight modified config")
	}
	c, _, err := s.ChangeProxy(Config{}, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	written := contents(t, path)
	if !strings.HasPrefix(written, original) || !strings.Contains(written, "[http]\n\tproxy = ") || strings.Contains(written, "https.proxy") || strings.Contains(written, "[https]") {
		t.Fatalf("unexpected config: %s", written)
	}
	// Only the isolated explicit config is read; this never discovers user HOME,
	// repositories, credentials or hooks.
	if git, err := exec.LookPath("git"); err == nil {
		cmd := exec.Command(git, "config", "--file", path, "--get", "http.proxy")
		cmd.Dir = s.UserHome
		cmd.Env = s.Env
		got, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(got)) != endpoint {
			t.Fatalf("Git rejected managed config: %q, %v", got, err)
		}
	}
	next, undo, err := s.ChangeProxy(c, "off")
	if err != nil || next != (Config{}) || contents(t, path) != original {
		t.Fatalf("remove: %#v, %v", next, err)
	}
	if err := undo(); err != nil || contents(t, path) != written {
		t.Fatalf("undo remove: %v", err)
	}
	if err := undo(); err != nil {
		t.Fatal("undo must be idempotent", err)
	}
}

func TestNewConfigUndoAndUpdate(t *testing.T) {
	s := isolatedService(t)
	path := filepath.Join(s.UserHome, ".gitconfig")
	c, undo, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("undo did not remove only newly created file")
	}
	c, _, err = s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err = s.ChangeProxy(c, "http://127.0.0.1:17891")
	if err != nil || strings.Count(contents(t, path), blockStart) != 1 || !strings.Contains(c.ProxyBlock, "17891") {
		t.Fatalf("update failed: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("new config is not private")
	}
}

func TestUndoRefusesConcurrentChanges(t *testing.T) {
	s := isolatedService(t)
	path := filepath.Join(s.UserHome, ".gitconfig")
	_, undo, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	changed := contents(t, path) + "# user changed after apply\n"
	put(t, path, changed)
	if err := undo(); err == nil || contents(t, path) != changed {
		t.Fatal("compensation overwrote concurrent user edit")
	}
}

func TestConflictConfigurationsNeverChange(t *testing.T) {
	for _, content := range []string{
		"[http]\nproxy = http://external:8888\n",
		"[HTTP \"https://example.com\"]\nProxy = http://external:8888\n",
		"[http.https://example.com]\nproxy = http://external:8888\n",
		"[remote \"origin\"]\nproxy = socks5://external:8888\n",
		"[include]\npath = elsewhere\n",
		"[includeIf \"gitdir:~/work/\"]\npath = elsewhere\n",
		"[user]\nname = Unclear \\\n[http]\nproxy = hidden\n",
		"[malformed\n",
		"= bad\n",
		blockStart + "\n",
	} {
		t.Run(content, func(t *testing.T) {
			s := isolatedService(t)
			path := filepath.Join(s.UserHome, ".gitconfig")
			put(t, path, content)
			if err := s.CheckProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
				t.Fatal("preflight accepted conflict")
			}
			if _, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
				t.Fatal("write accepted conflict")
			}
			if contents(t, path) != content {
				t.Fatal("conflict modified external file")
			}
		})
	}
}

func TestXDGGlobalConfigConflictAndOverride(t *testing.T) {
	s := isolatedService(t)
	xdg := filepath.Join(s.UserHome, ".config", "git")
	if err := os.MkdirAll(xdg, 0700); err != nil {
		t.Fatal(err)
	}
	xdgPath := filepath.Join(xdg, "config")
	put(t, xdgPath, "[http]\nproxy = external\n")
	if err := s.CheckProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
		t.Fatal("missed XDG conflict")
	}
	global := filepath.Join(s.UserHome, "explicit.config")
	s.Env = append(s.Env, "GIT_CONFIG_GLOBAL="+global)
	c, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	resolvedGlobal, resolveErr := resolvePath(global)
	if err != nil || resolveErr != nil || c.ConfigFile != resolvedGlobal {
		t.Fatalf("explicit global selection: %v", err)
	}
	if contents(t, xdgPath) != "[http]\nproxy = external\n" {
		t.Fatal("explicit selection touched unrelated XDG file")
	}
	s.Env = append(s.Env, "GIT_CONFIG_GLOBAL="+filepath.Join(s.UserHome, "moved.config"))
	if _, _, err := s.ChangeProxy(c, "off"); err == nil {
		t.Fatal("receipt followed changed global location")
	}
}

func TestUnsafeFilePathsAndReceipts(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "writable", "directory", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedService(t)
			path := filepath.Join(s.UserHome, ".gitconfig")
			target := filepath.Join(s.UserHome, "unrelated")
			put(t, target, "[user]\nname = sentinel\n")
			switch kind {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "writable":
				put(t, path, "[user]\nname = other\n")
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				put(t, path, strings.Repeat("#", maxConfigSize+1))
			}
			if _, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
				t.Fatal("accepted unsafe file")
			}
			if contents(t, target) != "[user]\nname = sentinel\n" {
				t.Fatal("modified unrelated inode")
			}
		})
	}
	s := isolatedService(t)
	for _, c := range []Config{
		{ConfigFile: "/arbitrary", ProxyBlock: "sentinel"},
		{ConfigFile: "relative", ProxyBlock: generatedBlock("http://localhost:1234", false)},
		{ConfigFile: "/arbitrary", ProxyBlock: generatedBlock("http://localhost:1234", false) + "extra"},
		{ConfigFile: "/arbitrary"},
	} {
		if err := s.ValidateConfig(c); err == nil {
			t.Fatalf("accepted forged receipt %#v", c)
		}
	}
}

func TestManagedBlockModificationAndConcurrentSnapshot(t *testing.T) {
	s := isolatedService(t)
	c, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	path := c.ConfigFile
	changed := contents(t, path) + "[user]\nname = after\n"
	put(t, path, changed)
	if _, _, err := s.ChangeProxy(c, "off"); err == nil || contents(t, path) != changed {
		t.Fatal("modified ambiguous user tail")
	}
	before, err := readFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	put(t, path, changed+"# concurrent\n")
	if err := replaceFile(before, []byte("replacement")); err == nil {
		t.Fatal("stale write accepted")
	}
}

func TestURLAndEnvironmentValidation(t *testing.T) {
	for _, endpoint := range []string{"http://localhost:1234", "https://u%3Aser:p%40ss%2B%252D@proxy.example:8443", "socks5://[::1]:1080", "socks5h://127.0.0.1:1080"} {
		if err := ValidateProxyURL(endpoint); err != nil {
			t.Fatalf("valid %q: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"", "off", "ftp://localhost:1234", "http://localhost:0", "http://localhost:65536", "http://localhost:", "http://[::1]:", "http://::1:1080", "http://[example.com]:1080", "http://localhost:12/path", "http://localhost:12?", "http://localhost:12#fragment", "http://u:p%0A@localhost:12", "http://localhost:12\n"} {
		if err := ValidateProxyURL(endpoint); err == nil {
			t.Fatalf("invalid %q accepted", endpoint)
		}
	}
	for _, env := range []string{"GIT_CONFIG_GLOBAL=relative", "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_PARAMETERS=anything"} {
		s := isolatedService(t)
		s.Env = append(s.Env, env)
		if err := s.CheckProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
			t.Fatalf("ambiguous environment accepted %q", env)
		}
		files, _ := os.ReadDir(s.UserHome)
		if len(files) != 0 {
			t.Fatal("preflight wrote files")
		}
	}
}

func TestUnprintableProxyURLsNeverChangeConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, endpoint string
	}{
		{"zero-width-space", "http://proxy\u200b.example:1080"},
		{"byte-order-mark", "http://proxy\ufeff.example:1080"},
		{"invalid-utf8", "http://proxy\xff.example:1080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := isolatedService(t)
			path := filepath.Join(s.UserHome, ".gitconfig")
			put(t, path, "# preserve every existing byte\n[user]\n\tname = Fixture User\n")
			if err := ValidateProxyURL(test.endpoint); err == nil {
				t.Fatal("accepted a proxy URL that cannot be encoded as a Git string")
			}
			for _, managed := range []bool{false, true} {
				config := Config{}
				if managed {
					var err error
					config, _, err = s.ChangeProxy(config, "http://127.0.0.1:17890")
					if err != nil {
						t.Fatal(err)
					}
				}
				before := contents(t, path)
				if err := s.CheckProxy(config, test.endpoint); err == nil {
					t.Fatal("preflight accepted unprintable proxy URL")
				}
				if _, _, err := s.ChangeProxy(config, test.endpoint); err == nil {
					t.Fatal("write accepted unprintable proxy URL")
				}
				if contents(t, path) != before {
					t.Fatal("rejected proxy URL changed existing configuration")
				}
			}
		})
	}
}

func TestPrintableUnicodeProxyURLsRemainGitCompatible(t *testing.T) {
	git, haveGit := exec.LookPath("git")
	for _, endpoint := range []string{
		"http://代理.example:17890",
		"https://u:p%40ss@παράδειγμα.example:17891",
	} {
		t.Run(endpoint, func(t *testing.T) {
			s := isolatedService(t)
			config, _, err := s.ChangeProxy(Config{}, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateConfig(config); err != nil {
				t.Fatal("valid Unicode URL changed receipt compatibility", err)
			}
			if haveGit == nil {
				cmd := exec.Command(git, "config", "--file", config.ConfigFile, "--get", "http.proxy")
				cmd.Dir, cmd.Env = s.UserHome, s.Env
				got, err := cmd.CombinedOutput()
				if err != nil || string(got) != endpoint+"\n" {
					t.Fatalf("Git rejected printable Unicode proxy URL: %q, %v", got, err)
				}
			}
		})
	}
}

func TestClearKeepsUnrelatedConfigBytes(t *testing.T) {
	s := isolatedService(t)
	path := filepath.Join(s.UserHome, ".gitconfig")
	original := []byte("# leading\n[user]\nname = untouched\n")
	put(t, path, string(original))
	c, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	// Editing pre-existing unrelated bytes is supported; only the owned suffix
	// must remain identical. Compensation still requires the whole snapshot.
	put(t, path, "# user addition\n"+contents(t, path))
	if _, _, err := s.ChangeProxy(c, ""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(contents(t, path)), append([]byte("# user addition\n"), original...)) {
		t.Fatal("clear changed unrelated bytes")
	}
}

func TestChangedPermissionsAndSymlinkUndoAreRejected(t *testing.T) {
	for _, kind := range []string{"mode", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedService(t)
			c, undo, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890")
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(s.UserHome, "sentinel")
			put(t, target, "untouched")
			switch kind {
			case "mode":
				err = os.Chmod(c.ConfigFile, 0644)
			case "symlink":
				if err = os.Remove(c.ConfigFile); err == nil {
					err = os.Symlink(target, c.ConfigFile)
				}
			case "hardlink":
				err = os.Link(c.ConfigFile, filepath.Join(s.UserHome, "hardlink"))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := undo(); err == nil {
				t.Fatal("undo accepted changed file identity or sharing")
			}
			if contents(t, target) != "untouched" {
				t.Fatal("undo touched sentinel")
			}
		})
	}
}

func TestMarkerMustStartAnIndependentLine(t *testing.T) {
	s := isolatedService(t)
	path, _, err := s.paths()
	if err != nil {
		t.Fatal(err)
	}
	c := Config{ConfigFile: path, ProxyBlock: generatedBlock("http://127.0.0.1:17890", false)}
	text := "#comment-with-no-newline" + c.ProxyBlock
	put(t, path, text)
	if _, _, err := s.ChangeProxy(c, "off"); err == nil || contents(t, path) != text {
		t.Fatal("accepted embedded ownership marker")
	}
}

func TestWritableDirectoryRejected(t *testing.T) {
	s := isolatedService(t)
	if err := os.Chmod(s.UserHome, 0777); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:17890"); err == nil {
		t.Fatal("accepted shared-writable config directory")
	}
}

func TestNidoRootIsNeverGitConfiguration(t *testing.T) {
	for _, kind := range []string{"main-config", "other-file", "root-alias", "parent-alias"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedService(t)
			root := filepath.Join(s.UserHome, "nido")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			s.Root = root
			path := filepath.Join(root, "config.toml")
			if kind == "other-file" {
				path = filepath.Join(root, "git-config")
			}
			put(t, path, "# Nido sentinel\n")
			if kind == "root-alias" {
				alias := filepath.Join(s.UserHome, "root-alias")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				s.Root = alias
			}
			global := path
			if kind == "parent-alias" {
				alias := filepath.Join(s.UserHome, "parent-alias")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				global = filepath.Join(alias, filepath.Base(path))
			}
			s.Env = append(s.Env, "GIT_CONFIG_GLOBAL="+global)
			if err := s.CheckProxy(Config{}, "http://127.0.0.1:1080"); err == nil {
				t.Fatal("preflight accepted Nido-owned target")
			}
			if _, _, err := s.ChangeProxy(Config{}, "http://127.0.0.1:1080"); err == nil || contents(t, path) != "# Nido sentinel\n" {
				t.Fatal("modified Nido-owned target")
			}
		})
	}
}

func TestXDGSnapshotRecheckedBeforePublish(t *testing.T) {
	for _, kind := range []string{"changed", "created-file", "created-parent", "replaced-file", "replaced-parent"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedService(t)
			path := filepath.Join(s.UserHome, ".gitconfig")
			put(t, path, "# untouched global\n")
			xdgDir := filepath.Join(s.UserHome, ".config", "git")
			xdgFile := filepath.Join(xdgDir, "config")
			if kind != "created-parent" {
				if err := os.MkdirAll(xdgDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "created-parent" && kind != "created-file" {
				put(t, xdgFile, "# original xdg\n")
			}
			_, before, next, err := s.prepare(Config{}, "http://127.0.0.1:1080")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "changed", "created-file":
				put(t, xdgFile, "[http]\nproxy = newly-added\n")
			case "created-parent":
				if err := os.MkdirAll(xdgDir, 0700); err != nil {
					t.Fatal(err)
				}
				put(t, xdgFile, "[http]\nproxy = newly-added\n")
			case "replaced-file":
				if err := os.Rename(xdgFile, xdgFile+".old"); err != nil {
					t.Fatal(err)
				}
				put(t, xdgFile, "# original xdg\n")
			case "replaced-parent":
				if err := os.Rename(xdgDir, xdgDir+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(xdgDir, 0700); err != nil {
					t.Fatal(err)
				}
				put(t, xdgFile, "# original xdg\n")
			}
			if err := replaceFile(before, next); err == nil || contents(t, path) != "# untouched global\n" {
				t.Fatal("published after auxiliary XDG source changed")
			}
		})
	}
}

func TestTargetFileAndParentIdentityRechecked(t *testing.T) {
	for _, kind := range []string{"file", "parent"} {
		t.Run(kind, func(t *testing.T) {
			s := isolatedService(t)
			configDir := filepath.Join(s.UserHome, "global")
			if err := os.Mkdir(configDir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(configDir, "config")
			put(t, path, "# same bytes\n")
			before, err := readFile(path, false)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(configDir, configDir+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(configDir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			put(t, path, "# same bytes\n")
			if err := replaceFile(before, []byte("replacement")); err == nil || contents(t, path) != "# same bytes\n" {
				t.Fatal("published after target file/parent identity changed")
			}
		})
	}
}
