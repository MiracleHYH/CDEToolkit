package nido

import (
	"bytes"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func originalMirrorRunner(name string, args, env []string) ([]byte, error) {
	if name == "python" {
		return []byte("global.index-url='https://original.example/simple'\n"), nil
	}
	return []byte(`{"channels":["defaults","conda-forge","https://private.example/team"],"default_channels":["https://original.example/pkgs/main","https://original.example/pkgs/r","https://original.example/pkgs/msys2"],"custom_channels":{"conda-forge":"https://original.example/cloud/conda-forge","bioconda":"https://original.example/cloud/bioconda"},"custom_multichannels":{"defaults":["https://original.example/pkgs/main","https://original.example/pkgs/r","https://original.example/pkgs/msys2"]},"channel_alias":"https://original.example/cloud","migrated_channel_aliases":[],"migrated_custom_channels":{}}`), nil
}
func condaFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	for _, key := range append([]string{"CONDARC", "CONDA_EXE", "CONDA_ENVS_PATH", "CONDA_ENVS_DIRS", "CONDA_PKGS_DIRS", "PIP_CONFIG_FILE", "PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL", "PIP_NO_INDEX", "PIP_CACHE_DIR", "PIP_NO_CACHE_DIR", "XDG_DATA_HOME"}, conda.MirrorVariableNames()...) {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	m, home := fixture(t)
	m.Conda.Runner = originalMirrorRunner
	return m, home
}

func TestCondaNativePathsRemainAfterResetAndPreserveOtherConfiguration(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	if err := os.WriteFile(native, []byte("# keep comment\nchannels:\n  - conda-forge\nssl_verify: true\n"), 0640); err != nil {
		t.Fatal(err)
	}
	pip := filepath.Join(home, ".config", "pip", "pip.conf")
	if err := os.MkdirAll(filepath.Dir(pip), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pip, []byte("# keep certificate\n[global]\ncert = /private/cert.pem\n"), 0640); err != nil {
		t.Fatal(err)
	}
	cfg := conda.Config{CondaMirror: "tuna", PipIndexURL: "https://pypi.org/simple", EnvsDir: filepath.Join(home, "new envs"), PkgsDir: filepath.Join(home, "new pkgs"), PipCacheDir: filepath.Join(home, "new pip")}
	if err := m.ConfigureConda(cfg); err != nil {
		t.Fatal(err)
	}
	if !m.Config.Conda.Defaults.CondaCaptured || !m.Config.Conda.Defaults.PipCaptured {
		t.Fatal("did not capture originals before switch")
	}
	for _, key := range []string{"CONDA_ENVS_PATH", "CONDA_PKGS_DIRS", "PIP_CACHE_DIR"} {
		if strings.Contains(m.Environment(), "export "+key+"=") {
			t.Fatal("storage remains shell-managed", key)
		}
	}
	beforeConda, _ := os.ReadFile(native)
	beforePip, _ := os.ReadFile(pip)
	for _, value := range []string{"# keep comment", "ssl_verify: true", cfg.EnvsDir, cfg.PkgsDir} {
		if !strings.Contains(string(beforeConda), value) {
			t.Fatal("lost Conda field", value)
		}
	}
	for _, value := range []string{"# keep certificate", "cert = /private/cert.pem", cfg.PipCacheDir} {
		if !strings.Contains(string(beforePip), value) {
			t.Fatal("lost pip field", value)
		}
	}
	for _, path := range []string{native, pip} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("lost native permissions", path, err)
		}
	}
	for _, dir := range []string{cfg.EnvsDir, cfg.PkgsDir, cfg.PipCacheDir} {
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatal("created data directory", dir)
		}
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Conda.Runner = originalMirrorRunner
	if !reflect.DeepEqual(reloaded.Config.Conda, m.Config.Conda) {
		t.Fatal("configuration did not survive reload")
	}
	defaults := reloaded.Config.Conda.Defaults
	if err := reloaded.ConfigureConda(conda.Config{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
		t.Fatal("reset lost first-use mirror defaults")
	}
	if got, _ := os.ReadFile(native); !bytes.Equal(got, beforeConda) {
		t.Fatal("reset restored Conda storage")
	}
	if got, _ := os.ReadFile(pip); !bytes.Equal(got, beforePip) {
		t.Fatal("reset restored pip storage")
	}
}

func TestCondaMirrorPublicationRejectsUserEditsAndLinks(t *testing.T) {
	for _, scenario := range []string{"edited", "symlink", "parent-link"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			defaults, err := m.Conda.CaptureDefaults(conda.MirrorDefaults{}, "conda")
			if err != nil {
				t.Fatal(err)
			}
			cfg := conda.Config{CondaMirror: "tuna", Defaults: defaults}
			path, _ := m.Conda.MirrorFile(cfg)
			sentinel := filepath.Join(home, "sentinel")
			if err := os.WriteFile(sentinel, []byte("user content"), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "parent-link" {
				if err := os.Symlink(home, filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "symlink" {
					if err := os.Symlink(sentinel, path); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte("user content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err := m.ConfigureConda(cfg); err == nil {
				t.Fatal("accepted conflicting mirror")
			}
			after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if !bytes.Equal(before, after) || !reflect.DeepEqual(m.Config.Conda, conda.Config{}) {
				t.Fatal("failed change altered intent")
			}
			if got, _ := os.ReadFile(sentinel); string(got) != "user content" {
				t.Fatal("overwrote link target")
			}
		})
	}
}

func TestCondaConcurrentConfigurationAndEnvironmentEditsArePreserved(t *testing.T) {
	for _, name := range []string{"config.toml", "env.sh"} {
		t.Run(name, func(t *testing.T) {
			m, _ := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.Root, name)
			before, _ := os.ReadFile(path)
			changed := append(before, []byte("\n# concurrent edit\n")...)
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna"}); err == nil {
				t.Fatal("accepted concurrent edit")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, changed) {
				t.Fatal("lost edit")
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
				t.Fatal("published after preflight failure")
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
			if name == "env.sh" && c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", "tuna", "--dry-run"}) == 0 {
				t.Fatal("dry-run ignored modified environment")
			}
		})
	}
}

func TestCondaCLIRejectsInvalidOptionsAndDryRunPreservesFiles(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	config, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	for _, args := range [][]string{{"setup", "all", "--dry-run"}, {"mirror", "conda", "tuna", "--dry-run", "--json"}, {"cache", "pip", filepath.Join(home, "new", "cache"), "--dry-run"}, {"envs", filepath.Join(home, "new", "envs"), "--dry-run"}} {
		var out, stderr bytes.Buffer
		c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
		if c.Run(append([]string{"--root", m.Root, "conda"}, args...)) != 0 {
			t.Fatal(stderr.String())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(got, config) {
		t.Fatal("dry-run wrote config")
	}
	for _, path := range []string{filepath.Join(home, "new"), filepath.Join(home, ".condarc"), filepath.Join(m.Root, "conda")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("dry-run created file or directory", path)
		}
	}
	for _, args := range [][]string{{"mirror", "conda", "ustc"}, {"mirror", "pip", "http://bad/simple"}, {"mirror", "wrong", "tuna"}, {"envs", "relative"}, {"envs", "/"}, {"envs", filepath.Join(m.Root, "envs")}, {"cache", "pip", "/tmp/cache", "extra"}, {"status", "--proxy", "http://127.0.0.1:1234"}, {"status", "--check"}, {"status", "--tool", "pip"}, {"status", "--home", home}, {"reset", "extra"}, {"validate", "extra"}, {"setup", "wrong"}, {"unknown"}} {
		var out, stderr bytes.Buffer
		c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
		if c.Run(append(append([]string{"--root", m.Root, "conda"}, args...), "--yes")) == 0 {
			t.Fatal("accepted invalid args", args)
		}
	}
}

func TestCondaDataPathChangesCanBeDiagnosedAndReset(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "cache")
	if err := m.ConfigureConda(conda.Config{PipCacheDir: path}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(m.Root, path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal("changed path blocked startup", err)
	}
	if report := reloaded.CondaStatus(); len(report.Issues) == 0 {
		t.Fatal("no diagnosis of changed path")
	}
	if err := reloaded.ConfigureConda(reloaded.Config.Conda); err == nil {
		t.Fatal("accepted Nido path alias")
	}
	if err := reloaded.ConfigureConda(conda.Config{}); err != nil {
		t.Fatal("could not reset", err)
	}
	if target, err := os.Readlink(path); err != nil || target != m.Root {
		t.Fatal("reset touched user's data link", err)
	}
}

func TestCondaDefaultsSurviveSwitchResetReloadAndCannotBeReplaced(t *testing.T) {
	m, _ := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna", PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple"}); err != nil {
		t.Fatal(err)
	}
	original := m.Config.Conda.Defaults
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Conda.Runner = func(string, []string, []string) ([]byte, error) {
		t.Fatal("re-read first-use defaults")
		return nil, nil
	}
	if err := reloaded.ConfigureConda(conda.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ConfigureConda(conda.Config{CondaMirror: "default", PipMirror: "default"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, reloaded.Config.Conda.Defaults) {
		t.Fatal("default changed across reset or reload")
	}
	vars := reloaded.Conda.Variables(reloaded.Config.Conda)
	if vars["PIP_INDEX_URL"] != "https://original.example/simple" {
		t.Fatal("default is not original pip mirror")
	}
	_, contents := reloaded.Conda.MirrorFile(reloaded.Config.Conda)
	if !bytes.Contains(contents, []byte("https://original.example/pkgs/main")) {
		t.Fatal("lost original Conda mirror")
	}
	report := reloaded.CondaStatus()
	if report.Environment["PIP_INDEX_URL"] == vars["PIP_INDEX_URL"] {
		t.Fatal("default snapshot exposed by ordinary status")
	}
}

func TestCondaSetupRecordsDefaultsWithoutReapplyingNativeChannels(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna"}); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	edited := []byte("# user edited active profile\nchannels:\n  - https://user.example/later\n")
	if err := os.WriteFile(native, edited, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
	if c.Run([]string{"--root", m.Root, "conda", "setup", "all", "--yes"}) != 0 {
		t.Fatal(stderr.String())
	}
	if got, _ := os.ReadFile(native); !bytes.Equal(got, edited) {
		t.Fatal("setup reapplied native channels")
	}
	reloaded, err := New(m.Root)
	if err != nil || !reloaded.Config.Conda.Defaults.PipCaptured {
		t.Fatal("missing new tool snapshot", err)
	}
}
