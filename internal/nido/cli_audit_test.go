package nido

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

func isolatedCLI(t *testing.T, m *Manager, args []string, in string, runner codex.Runner) (int, string, string) {
	t.Helper()
	var out, err bytes.Buffer
	c := CLI{In: strings.NewReader(in), Out: &out, Err: &err, Runner: runner}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), err.String()
}

func TestCLISetupReusesRegisteredHomeWithoutFollowingChangedShell(t *testing.T) {
	m, home := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	registered := filepath.Join(home, "registered codex")
	other := filepath.Join(home, ".codex")
	for _, p := range []string{registered, other} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m.Codex.Env = []string{"PATH=/usr/bin:/bin"}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(registered) }); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", other)
	code, out, stderr := isolatedCLI(t, m, []string{"codex", "setup", "--yes", "--json"}, "", nil)
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.Codex.Home != registered {
		t.Fatal("setup silently changed the registered home")
	}
}

func TestCLIJSONPromptsStayOnStderr(t *testing.T) {
	m, home := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := isolatedCLI(t, m, []string{"codex", "setup", "--home", external, "--json"}, "y\n", nil)
	if code != 0 || !json.Valid([]byte(out)) || !strings.Contains(stderr, "是否继续") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestCLIDoctorJSONReportsErrorsWithoutChangingEnvironmentFile(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Root, "env.sh")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(before, []byte("# user edit\n")...)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := isolatedCLI(t, m, []string{"doctor", "--json"}, "", nil)
	var report struct {
		Components []Status `json:"components"`
		Issues     []string `json:"issues"`
	}
	if code != 1 || json.Unmarshal([]byte(out), &report) != nil || len(report.Components) != 3 || len(report.Issues) == 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, changed) {
		t.Fatal("doctor modified user environment file")
	}
}

func TestCLIRejectsAmbiguousOptionsAndUnsafeInstallPreview(t *testing.T) {
	m, _ := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	called := false
	runner := func(string, []string, []string, []byte) ([]byte, error) {
		called = true
		return nil, fmt.Errorf("must not run")
	}
	for _, args := range [][]string{
		{"init", "--root", m.Root, "--root", m.Root + "-other"},
		{"codex", "install", "--home", "/", "--dry-run", "--yes"},
		{"codex", "install", "--home", filepath.Join(m.Root, "codex"), "--dry-run", "--yes"},
		{"codex", "install", "--home", filepath.Join(m.UserHome, "external"), "--proxy", "file:///tmp/wrong", "--dry-run", "--yes"},
		{"codex", "install", "--home", "--yes"},
	} {
		code, _, _ := isolatedCLI(t, m, args, "", runner)
		if code == 0 {
			t.Fatalf("accepted invalid preview/input: %v", args)
		}
	}
	if called {
		t.Fatal("invalid command ran a downloader or component")
	}
	if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
		t.Fatal("invalid input created Nido root")
	}
}

func TestCLIVersionDoesNotDependOnDamagedInstallation(t *testing.T) {
	m, _ := fixture(t)
	if err := os.Mkdir(m.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "config.toml"), []byte("bad TOML = ["), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := isolatedCLI(t, m, []string{"version", "--json"}, "", nil)
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("version depends on config: %d %q %q", code, out, stderr)
	}
}

func unsetForCLI(t *testing.T, key string) {
	t.Helper()
	old, exists := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestCLIExplicitProxyPreservesNoProxyInOfficialInstall(t *testing.T) {
	m, home := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		unsetForCLI(t, key)
	}
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, ".env"), []byte("NO_PROXY=localhost,127.0.0.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runner := func(path string, args, env []string, input []byte) ([]byte, error) {
		calls++
		values := map[string]string{}
		for _, entry := range env {
			key, value, _ := strings.Cut(entry, "=")
			values[key] = value
		}
		if values["no_proxy"] != "localhost,127.0.0.1" || values["NO_PROXY"] != values["no_proxy"] {
			t.Fatal("explicit endpoint discarded NO_PROXY")
		}
		for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
			if values[key] != "http://127.0.0.1:17890" {
				t.Fatalf("wrong %s", key)
			}
		}
		if path == "curl" {
			return []byte("# fake official installer\n"), nil
		}
		if path != "/bin/sh" {
			t.Fatal("unexpected component execution")
		}
		binary := filepath.Join(external, "packages", "standalone", "fixture", "bin", "codex")
		if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binary, []byte("fixture"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(binary, filepath.Join(values["CODEX_INSTALL_DIR"], "codex")); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	code, out, stderr := isolatedCLI(t, m, []string{"codex", "install", "--home", external, "--proxy", "http://127.0.0.1:17890", "--yes", "--json"}, "", runner)
	if code != 0 || !json.Valid([]byte(out)) || calls != 2 {
		t.Fatalf("code=%d calls=%d out=%q err=%q", code, calls, out, stderr)
	}
	got, err := os.ReadFile(filepath.Join(external, ".env"))
	if err != nil || string(got) != "NO_PROXY=localhost,127.0.0.1\n" {
		t.Fatal("installer integration changed user proxy configuration")
	}
}

func TestCLIProxyAndDetachPreviewsValidateWithoutWriting(t *testing.T) {
	m, home := fixture(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"file:///tmp/wrong", "http://host:0"} {
		if code, _, _ := isolatedCLI(t, m, []string{"codex", "proxy", endpoint, "--dry-run"}, "", nil); code == 0 {
			t.Fatal("invalid proxy preview reported success")
		}
	}
	if _, err := os.Lstat(filepath.Join(external, ".env")); !os.IsNotExist(err) {
		t.Fatal("preview created .env")
	}
	if err := m.ConfigureProxy("http://127.0.0.1:17890", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(external, ".env")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.ReplaceAll(content, []byte("17890"), []byte("17891"))
	if err := os.WriteFile(path, tampered, 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := isolatedCLI(t, m, []string{"codex", "detach", "--dry-run"}, "", nil); code == 0 {
		t.Fatal("tampered detach preview reported success")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, tampered) {
		t.Fatal("detach preview modified user content")
	}
}
