package nido

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/selfupdate"
)

func TestDownloadConfigurationIsScopedAndPersistent(t *testing.T) {
	m, home := fixture(t)
	t.Setenv("NIDO_GITHUB_PROXY", "")
	if err := m.InitWithDownload("https://mirror.example/gh"); err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, name := range []string{".codex/auth.json", ".codex/.env", ".codex/sessions/one.jsonl", ".gitconfig", ".bashrc"} {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		protected[path] = []byte("EXTERNAL DATA\n")
		if err := os.WriteFile(path, protected[path], 0600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(m.Root)
	if err != nil || m.Config.Download.GitHubProxy != "https://mirror.example/gh" {
		t.Fatalf("saved download configuration: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(after, before) {
		t.Fatal("reinstall changed a saved download preference")
	}
	for _, policy := range []string{"auto", "direct", "https://custom.example/prefix"} {
		code, _, stderr := isolatedCLI(t, m, []string{"download", "set", policy}, "", nil)
		if code != 0 {
			t.Fatal(stderr)
		}
	}
	for _, args := range [][]string{
		{"download", "set", "http://insecure.example"},
		{"codex", "update", "--github-proxy", "https://mirror.example", "--dry-run"},
		{"proxy", "status", "--github-proxy", "auto"},
		{"proxy", "bind", "ai", "--github-proxy", "auto"},
		{"gds", "configure", "--github-proxy", "auto"},
		{"status", "--version", "1.2.3"},
	} {
		if code, _, _ := isolatedCLI(t, m, args, "", nil); code == 0 {
			t.Fatalf("accepted misplaced or invalid download option: %v", args)
		}
	}
	if strings.Contains(m.Environment(), "PROXY") || strings.Contains(m.Environment(), "mirror.example") {
		t.Fatal("download policy leaked into shell or general proxy environment")
	}
	for path, want := range protected {
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			t.Fatalf("download preference modified external file %s", path)
		}
	}
}

func TestDownloadShowPrintsSavedAndEffectivePolicies(t *testing.T) {
	m, _ := fixture(t)
	if err := m.InitWithDownload("https://saved.example/gh"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIDO_GITHUB_PROXY", "direct")
	code, out, stderr := isolatedCLI(t, m, []string{"download", "show"}, "", nil)
	if code != 0 || !strings.Contains(out, "saved: https://saved.example/gh\n") || !strings.Contains(out, "effective: direct\n") {
		t.Fatalf("download show is incomplete: %d %q %q", code, out, stderr)
	}
	code, out, stderr = isolatedCLI(t, m, []string{"download", "set", "auto", "--dry-run"}, "", nil)
	if code != 0 || !strings.Contains(out, "github_proxy: auto\n") {
		t.Fatalf("download preview omitted policy: %d %q %q", code, out, stderr)
	}
}

func TestMalformedSavedDownloadPolicyDoesNotBlockIndependentOperations(t *testing.T) {
	m, home := fixture(t)
	if err := m.InitWithDownload("https://mirror.invalid"); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, ".codex")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(m.Root, "config.toml")
	config, _ := os.ReadFile(configPath)
	config = bytes.Replace(config, []byte("https://mirror.invalid"), []byte("not-a-prefix"), 1)
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIDO_GITHUB_PROXY", "not-an-environment-prefix")
	for _, args := range [][]string{{"status"}, {"env"}, {"path"}, {"proxy", "status"}, {"gds", "status"}} {
		if code, _, stderr := isolatedCLI(t, m, args, "", nil); code != 0 {
			t.Fatalf("download policy blocked independent operation %v: %s", args, stderr)
		}
	}
	called := 0
	runner := func(name string, args, env []string, _ []byte) ([]byte, error) {
		t.Fatalf("Codex metadata query invoked external command: %s %v", name, args)
		return nil, nil
	}
	client := &http.Client{Transport: updateCLITransport(func(request *http.Request) (*http.Response, error) {
		called++
		if request.URL.String() != codex.LatestURL {
			t.Fatalf("Codex API was rewritten: %s", request.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"rust-v1.2.3"}`))}, nil
	})}
	endpoint := enabledAPIFixture(t, m, client)
	code, _, stderr := isolatedCLI(t, m, []string{"codex", "update", "--check", "--proxy", endpoint}, "", runner)
	if code != 0 || called != 1 {
		t.Fatalf("Codex was blocked by download policy: code=%d calls=%d %s", code, called, stderr)
	}
	if after, _ := os.ReadFile(configPath); !bytes.Equal(after, config) {
		t.Fatal("independent operation repaired or rewrote configuration")
	}
	if code, _, _ := isolatedCLI(t, m, []string{"download", "show"}, "", nil); code == 0 {
		t.Fatal("invalid effective policy was silently accepted")
	}
	if code, _, stderr := isolatedCLI(t, m, []string{"download", "set", "direct"}, "", nil); code != 0 {
		t.Fatal("cannot correct malformed preference", stderr)
	}
	loaded, err := New(m.Root)
	if err != nil || loaded.Config.Download.GitHubProxy != "direct" {
		t.Fatal("corrected preference was not saved", err)
	}
}

func TestDownloadPreferenceCanRetryAfterPreservingUserEnvironmentEdit(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		t.Run(fmt.Sprint(reinstall), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.InitWithDownload("direct"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.Root, "env.sh")
			before, _ := os.ReadFile(path)
			modified := append(append([]byte(nil), before...), []byte("# USER EDIT\n")...)
			if err := os.WriteFile(path, modified, 0600); err != nil {
				t.Fatal(err)
			}
			set := func() error {
				if reinstall {
					return m.InitWithDownload("auto")
				}
				return m.setDownload("auto")
			}
			if err := set(); err == nil || m.Config.Download.GitHubProxy != "direct" {
				t.Fatal("failed save lost its previous preference", err)
			}
			if actual, _ := os.ReadFile(path); !bytes.Equal(actual, modified) {
				t.Fatal("failed preference save overwrote user environment")
			}
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			if err := set(); err != nil {
				t.Fatal("retry failed", err)
			}
			loaded, err := New(m.Root)
			if err != nil || loaded.Config.Download.GitHubProxy != "auto" {
				t.Fatal("retry did not persist its preference", err)
			}
		})
	}
}

func TestCodexIgnoresGitHubAccelerationAndUsesGeneralProxy(t *testing.T) {
	m, home := fixture(t)
	if err := m.InitWithDownload("https://github-mirror.invalid"); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(home, ".codex")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	runtimeEnv := []byte("HTTPS_PROXY=${RUNTIME_PROXY}\nKEEP=runtime-data\n")
	if err := os.WriteFile(filepath.Join(external, ".env"), runtimeEnv, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	// Even an invalid acceleration environment must not enter Codex's path.
	t.Setenv("NIDO_GITHUB_PROXY", "not-a-github-prefix")
	called := 0
	runner := func(name string, args, env []string, input []byte) ([]byte, error) {
		t.Fatalf("Codex API query executed curl or installer: %s %v", name, args)
		return nil, nil
	}
	client := &http.Client{Transport: updateCLITransport(func(request *http.Request) (*http.Response, error) {
		called++
		if request.URL.String() != codex.LatestURL {
			t.Fatalf("Codex API was rewritten: %s", request.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"rust-v1.2.3"}`))}, nil
	})}
	endpoint := enabledAPIFixture(t, m, client)
	code, _, stderr := isolatedCLI(t, m, []string{"codex", "update", "--check", "--proxy", endpoint, "--json"}, "", runner)
	if code != 0 || called != 1 {
		t.Fatalf("code=%d calls=%d stderr=%s", code, called, stderr)
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(after, before) {
		t.Fatal("Codex version query changed configuration")
	}
	if after, _ := os.ReadFile(filepath.Join(external, ".env")); !bytes.Equal(after, runtimeEnv) {
		t.Fatal("Nido API query changed Codex runtime proxy settings")
	}
}

func TestSelfUpdateReplacesOnlyProgramAndPreservesFailures(t *testing.T) {
	for _, scenario := range []string{"success", "download-failure", "wrong-version", "program-edited", "config-edited", "candidate-edited"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(m.Root, "bin", "nido")
			before, err := os.ReadFile(program)
			if err != nil {
				t.Fatal(err)
			}
			protected := map[string][]byte{}
			for _, path := range []string{filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh"), filepath.Join(m.Root, "proxy/config.yaml"), filepath.Join(m.Root, "gds/state.json"), filepath.Join(home, ".codex/auth.json"), filepath.Join(home, ".codex/sessions/session.jsonl")} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					data = []byte("USER DATA\n")
					err = os.WriteFile(path, data, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				protected[path] = data
			}
			candidate := []byte("INERT CANDIDATE: NOT EXECUTED\n")
			probe := func(path string) (string, error) {
				if filepath.Dir(path) != filepath.Dir(program) {
					t.Fatal("candidate not staged beside managed entry")
				}
				if scenario == "candidate-edited" {
					_ = os.WriteFile(path, []byte("USER EDIT"), 0755)
				}
				if scenario == "wrong-version" {
					return "nido 8.8.8", nil
				}
				return "nido 1.2.3", nil
			}
			fetch := func() (selfupdate.Prepared, error) {
				if scenario == "download-failure" {
					return selfupdate.Prepared{}, fmt.Errorf("fixture download failure")
				}
				if scenario == "program-edited" {
					_ = os.WriteFile(program, []byte("USER EDIT"), 0755)
				}
				if scenario == "config-edited" {
					path := filepath.Join(m.Root, "config.toml")
					protected[path] = append(append([]byte(nil), protected[path]...), []byte("# USER EDIT\n")...)
					_ = os.WriteFile(path, protected[path], 0600)
				}
				return selfupdate.Prepared{Data: candidate, Version: "1.2.3"}, nil
			}
			err = m.updateSelf(before, fetch, probe)
			if (err == nil) != (scenario == "success") {
				t.Fatalf("unexpected update result: %v", err)
			}
			want := before
			if scenario == "success" {
				want = candidate
			} else if scenario == "program-edited" {
				want = []byte("USER EDIT")
			}
			if got, _ := os.ReadFile(program); !bytes.Equal(got, want) {
				t.Fatal("wrong executable contents after update")
			}
			for path, expected := range protected {
				if got, _ := os.ReadFile(path); !bytes.Equal(got, expected) {
					t.Fatalf("self-update changed protected data: %s", path)
				}
			}
			stages, _ := filepath.Glob(filepath.Join(filepath.Dir(program), ".nido-update-*"))
			if len(stages) != 0 {
				t.Fatal("candidate files leaked")
			}
		})
	}
}
