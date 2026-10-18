package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

func fixture(t *testing.T) (*Manager, string) {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(dir, "user")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("HOME", home)
	t.Setenv("NIDO_HOME", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	m, e := New(filepath.Join(dir, "nido"))
	if e != nil {
		t.Fatal(e)
	}
	return m, home
}

func TestStatusAndDryRunDoNotInitialize(t *testing.T) {
	m, _ := fixture(t)
	if rows := m.Status(); len(rows) != 3 {
		t.Fatalf("rows=%v", rows)
	}
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr}
	if code := c.Run([]string{"--root", m.Root, "init", "--dry-run", "--json"}); code != 0 {
		t.Fatal(stderr.String())
	}
	if _, e := os.Lstat(m.Root); !os.IsNotExist(e) {
		t.Fatalf("read-only command created root: %v", e)
	}
	if !strings.Contains(out.String(), `"operation":"init"`) {
		t.Fatal(out.String())
	}
}

func TestCLIReinstallPreservesCurrentConfigurationBytes(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(m.Root, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config = append([]byte("# user-maintained comment\n\n"), config...)
	if err = os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(filepath.Join(m.Root, "env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err = next.Init(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(configPath); !bytes.Equal(got, config) {
		t.Fatal("CLI reinstall rewrote existing TOML or comments")
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "env.sh")); !bytes.Equal(got, env) {
		t.Fatal("CLI reinstall rewrote the current environment")
	}
}

func TestInitSingleRootAndExternalAssociation(t *testing.T) {
	m, home := fixture(t)
	shell := filepath.Join(home, ".bashrc")
	if e := os.WriteFile(shell, []byte("USER SHELL\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.Init(); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"proxy", "gds", "codex", "state.json", ".instance"} {
		if _, e := os.Lstat(filepath.Join(m.Root, name)); !os.IsNotExist(e) {
			t.Fatalf("unexpected %s", name)
		}
	}
	external := filepath.Join(home, "custom codex")
	if e := os.Mkdir(external, 0700); e != nil {
		t.Fatal(e)
	}
	auth := filepath.Join(external, "auth.json")
	want := []byte("AUTH FIXTURE\n")
	if e := os.WriteFile(auth, want, 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(m.Environment(), "export CODEX_HOME='"+external+"'") {
		t.Fatal(m.Environment())
	}
	if strings.Contains(m.Environment(), "CODEX_SQLITE_HOME") {
		t.Fatal("private data location overridden")
	}
	got, _ := os.ReadFile(auth)
	if !bytes.Equal(got, want) {
		t.Fatal("auth changed")
	}
	got, _ = os.ReadFile(shell)
	if string(got) != "USER SHELL\n" {
		t.Fatal("shell changed")
	}
	if e := m.ConfigureCodex(func(cfg codex.Config) (codex.Config, error) { return codex.Config{}, m.Codex.Detach(cfg) }); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(m.Environment(), "CODEX_HOME") {
		t.Fatal("detached environment still exports home")
	}
	got, _ = os.ReadFile(auth)
	if !bytes.Equal(got, want) {
		t.Fatal("detach changed external data")
	}
}

func TestConfigurationConflictBlocksExternalWrites(t *testing.T) {
	m, _ := fixture(t)
	if e := m.Init(); e != nil {
		t.Fatal(e)
	}
	stale, e := New(m.Root)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.ConfigureCodex(func(cfg codex.Config) (codex.Config, error) { return cfg, nil }); e != nil {
		t.Fatal(e)
	}
	// Explicitly change intent so another pre-existing view is stale.
	if e = os.WriteFile(filepath.Join(m.Root, "config.toml"), append(m.configBytes, []byte("\n# another operation\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	called := false
	e = stale.ConfigureCodex(func(cfg codex.Config) (codex.Config, error) { called = true; return cfg, nil })
	if e == nil || called {
		t.Fatalf("stale operation: called=%v err=%v", called, e)
	}
}

func TestRefuseForeignOrModifiedGeneratedFiles(t *testing.T) {
	m, _ := fixture(t)
	if e := os.MkdirAll(filepath.Join(m.Root, "bin"), 0700); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(m.Root, "bin", "nido")
	if e := os.WriteFile(target, []byte("USER FILE"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := m.Init(); e == nil {
		t.Fatal("foreign binary overwritten")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "USER FILE" {
		t.Fatal("foreign file changed")
	}
	m, _ = fixture(t)
	if e := m.Init(); e != nil {
		t.Fatal(e)
	}
	env := filepath.Join(m.Root, "env.sh")
	if e := os.WriteFile(env, []byte("USER ENV"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.ConfigureCodex(func(codex.Config) (codex.Config, error) {
		t.Fatal("called after env conflict")
		return codex.Config{}, nil
	}); e == nil {
		t.Fatal("modified env overwritten")
	}
}

func TestNoInstallationThroughSymlinkedBin(t *testing.T) {
	m, home := fixture(t)
	if e := os.Mkdir(m.Root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(home, filepath.Join(m.Root, "bin")); e != nil {
		t.Fatal(e)
	}
	if e := m.Init(); e == nil {
		t.Fatal("symlinked bin allowed")
	}
	if _, e := os.Lstat(filepath.Join(home, "nido")); !os.IsNotExist(e) {
		t.Fatal("escaped root")
	}
}

func TestOldConfigurationIsNotConverted(t *testing.T) {
	m, _ := fixture(t)
	if e := os.Mkdir(m.Root, 0700); e != nil {
		t.Fatal(e)
	}
	b := []byte("schema = 4\nstore = '/old/store'\n")
	if e := os.WriteFile(filepath.Join(m.Root, "config.toml"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := New(m.Root); e == nil {
		t.Fatal("old schema accepted")
	}
	got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if !bytes.Equal(got, b) {
		t.Fatal("old config changed")
	}
}

func TestRemovedMigrationCLIAndDryRunInstaller(t *testing.T) {
	m, home := fixture(t)
	for _, args := range [][]string{{"--store", "/old", "init"}, {"repair"}, {"install", "codex"}, {"codex", "setup", "--from", "codex=/old"}} {
		var out, stderr bytes.Buffer
		if code := (CLI{Out: &out, Err: &stderr}).Run(append([]string{"--root", m.Root}, args...)); code == 0 {
			t.Fatalf("obsolete command accepted: %v", args)
		}
	}
	var out, stderr bytes.Buffer
	called := false
	c := CLI{Out: &out, Err: &stderr, Runner: func(string, []string, []string, []byte) ([]byte, error) { called = true; return nil, nil }}
	if code := c.Run([]string{"--root", m.Root, "codex", "install", "--home", filepath.Join(home, "new codex"), "--yes", "--dry-run"}); code != 0 {
		t.Fatal(stderr.String())
	}
	if called {
		t.Fatal("dry-run invoked installer")
	}
	if _, e := os.Lstat(m.Root); !os.IsNotExist(e) {
		t.Fatal("dry-run created root")
	}
}

func TestEditorChangesDuringOperationArePreserved(t *testing.T) {
	for _, file := range []string{"config.toml", "env.sh"} {
		t.Run(file, func(t *testing.T) {
			m, _ := fixture(t)
			if e := m.Init(); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(m.Root, file)
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			changed := append(before, []byte("\n# user edit during operation\n")...)
			e = m.ConfigureCodex(func(cfg codex.Config) (codex.Config, error) {
				if e := os.WriteFile(path, changed, 0600); e != nil {
					t.Fatal(e)
				}
				return cfg, nil
			})
			if e == nil {
				t.Fatal("concurrent edit overwritten")
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, changed) {
				t.Fatal("editor change lost")
			}
		})
	}
}

func TestInitCannotAdoptUnknownNonemptyRoot(t *testing.T) {
	m, _ := fixture(t)
	if e := os.Mkdir(m.Root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(m.Root, "keep"), []byte("USER"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.Init(); e == nil {
		t.Fatal("nonempty unowned root adopted")
	}
	entries, _ := os.ReadDir(m.Root)
	if len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatal("initialization modified unknown root")
	}
}

func TestStatusRejectsIgnoredComponentOptions(t *testing.T) {
	m, _ := fixture(t)
	for _, args := range [][]string{{"proxy", "--artifact", "/tmp/ignored"}, {"proxy", "status", "--artifact", "/tmp/ignored"}, {"gds", "--home", "/tmp/ignored"}, {"codex", "--home", "/tmp/ignored"}} {
		var out, stderr bytes.Buffer
		if code := (CLI{Out: &out, Err: &stderr}).Run(append([]string{"--root", m.Root}, args...)); code == 0 {
			t.Fatalf("ignored incompatible options: %v", args)
		}
	}
}

func TestProxyReceiptSaveFailureRestoresOnlyThisChange(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged user")
	}
	for _, operation := range []string{"set", "clear", "detach"} {
		t.Run(operation, func(t *testing.T) {
			m, home := fixture(t)
			if e := m.Init(); e != nil {
				t.Fatal(e)
			}
			external := filepath.Join(home, "codex")
			if e := os.Mkdir(external, 0700); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(external, ".env")
			if e := os.WriteFile(path, []byte("USER_VALUE=keep\n"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); e != nil {
				t.Fatal(e)
			}
			if operation != "set" {
				if e := m.ConfigureProxy("http://127.0.0.1:7890", false); e != nil {
					t.Fatal(e)
				}
			}
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			configBefore, e := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if e != nil {
				t.Fatal(e)
			}
			if e := os.Chmod(m.Root, 0500); e != nil {
				t.Fatal(e)
			}
			defer os.Chmod(m.Root, 0700)
			endpoint := ""
			if operation == "set" {
				endpoint = "http://127.0.0.1:7890"
			}
			if e := m.ConfigureProxy(endpoint, operation == "detach"); e == nil {
				t.Fatal("expected first config save failure")
			}
			got, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(got, before) {
				t.Fatal("failed receipt save left an external change")
			}
			got, e = os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if e != nil || !bytes.Equal(got, configBefore) {
				t.Fatal("configuration changed on failed first save")
			}
		})
	}
}
