package nido

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"go.yaml.in/yaml/v3"
)

// Appending a newly captured pip default checkpoints Nido's configuration
// before native writes. That checkpoint must not consume the source change
// that the unspecialized ConfigureConda API still needs to apply.
func TestCondaFinalAuditDefaultCheckpointDoesNotConsumeAPISourceDifference(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	first := condaSourceAuditFile(t, home, "first.yaml")
	second := condaSourceAuditFile(t, home, "second.yaml")
	t.Setenv("CONDARC", first)
	condaSourceAuditSelect(t, m, "tuna")
	firstBefore := condaSourceAuditRead(t, first)
	secondBefore := condaSourceAuditRead(t, second)
	if m.Config.Conda.Defaults.PipCaptured {
		t.Fatal("fixture has already captured pip")
	}
	t.Setenv("CONDARC", second)
	m.Conda.Env = os.Environ()
	next := m.Config.Conda
	next.SourceCONDARC = second
	next.PipMirror, next.PipIndexURL = "official", "https://pypi.org/simple"
	if err := m.ConfigureConda(next); err != nil {
		t.Fatal(err)
	}
	if m.Config.Conda.SourceCONDARC != second || !m.Config.Conda.Defaults.PipCaptured {
		t.Fatal("configuration did not retain the new source and appended pip default")
	}
	after := condaSourceAuditRead(t, second)
	if !bytes.Contains(after, []byte("https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main")) {
		t.Fatalf("a default checkpoint consumed the API source difference before native application:\n%s", after)
	}
	if !reflect.DeepEqual(condaSourceAuditNonMirrors(t, secondBefore), condaSourceAuditNonMirrors(t, after)) {
		t.Fatal("source retarget changed unrelated second-source settings")
	}
	if !bytes.Equal(firstBefore, condaSourceAuditRead(t, first)) {
		t.Fatal("retargeting to a different source rewrote the old source")
	}
}

func TestCondaFinalAuditFailedRetargetKeepsActiveSourceDuringDefaultCheckpoint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root process bypasses the native directory write denial")
	}
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	first := condaSourceAuditFile(t, home, "active.yaml")
	secondDir := filepath.Join(home, "denied")
	if err := os.Mkdir(secondDir, 0700); err != nil {
		t.Fatal(err)
	}
	second := condaSourceAuditFile(t, secondDir, "unapplied.yaml")
	t.Setenv("CONDARC", first)
	condaSourceAuditSelect(t, m, "tuna")
	beforeFirst := condaSourceAuditRead(t, first)
	beforeSecond := condaSourceAuditRead(t, second)
	beforeUser := condaSourceAuditRead(t, filepath.Join(home, ".condarc"))
	beforeEnv := condaSourceAuditRead(t, filepath.Join(m.Root, "env.sh"))
	if err := os.Chmod(secondDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secondDir, 0700) })
	t.Setenv("CONDARC", second)
	m.Conda.Env = os.Environ()
	next := m.Config.Conda
	next.SourceCONDARC = second
	next.PipMirror, next.PipIndexURL = "official", "https://pypi.org/simple"
	if err := m.ConfigureConda(next, conda.NativeScope{CondaMirror: true}); err == nil {
		t.Fatal("retargeting unexpectedly wrote a source in a nonwritable directory")
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.Conda.SourceCONDARC != first || reloaded.Config.Conda.CondaMirror != "tuna" {
		t.Fatal("default checkpoint activated a source whose native mirror was not applied")
	}
	if !reloaded.Config.Conda.Defaults.PipCaptured {
		t.Fatal("failed native write lost the safely checkpointed pip default")
	}
	for path, before := range map[string][]byte{
		first: beforeFirst, second: beforeSecond,
		filepath.Join(home, ".condarc"): beforeUser,
		filepath.Join(m.Root, "env.sh"): beforeEnv,
	} {
		if !bytes.Equal(before, condaSourceAuditRead(t, path)) {
			t.Fatal("failed retarget changed a native file or active environment", path)
		}
	}
}

// The upstream query and native read must describe the same configuration.
// Otherwise a private channel added while a slow query is running can be
// deleted by a preset based on the query's stale result.
func TestCondaFinalAuditSourceEditDuringRunnerPreservesNewPrivateChannel(t *testing.T) {
	for _, source := range []string{"standard-user-file", "explicit-source"} {
		t.Run(source, func(t *testing.T) {
			condaFinalAuditRunnerEdit(t, source == "explicit-source")
		})
	}
}

func condaFinalAuditRunnerEdit(t *testing.T, explicitSource bool) {
	t.Helper()
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	name := ".condarc"
	if explicitSource {
		name = "concurrent.yaml"
	}
	source := condaSourceAuditFile(t, home, name)
	if explicitSource {
		t.Setenv("CONDARC", source)
	}
	m.Conda.Env = os.Environ()
	defaults, err := m.Conda.CaptureDefaults(conda.MirrorDefaults{}, "conda")
	if err != nil {
		t.Fatal(err)
	}
	var association []string
	if explicitSource {
		association = []string{source}
	}
	if err := m.RecordCondaDefaults(defaults, association...); err != nil {
		t.Fatal(err)
	}
	original := condaSourceAuditRead(t, source)
	const added = "https://concurrent-private.example/team"
	changed := []byte(strings.Replace(string(original), "  - conda-forge\n", "  - conda-forge\n  - "+added+"\n", 1))
	mutated := false
	m.Conda.Runner = func(name string, args, env []string) ([]byte, error) {
		result, err := originalMirrorRunner(name, args, env)
		if !mutated && name == "conda" {
			mutated = true
			if writeErr := os.WriteFile(source, changed, 0640); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return result, err
	}
	next := m.Config.Conda
	next.CondaMirror = "tuna"
	err = m.ConfigureConda(next, conda.NativeScope{CondaMirror: true})
	if !mutated {
		t.Fatal("test did not exercise a source edit during the upstream query")
	}
	if err != nil {
		if !bytes.Equal(changed, condaSourceAuditRead(t, source)) {
			t.Fatal("rejected stale query overwrote the concurrent source edit")
		}
		return
	}
	var document map[string]any
	if err := yaml.Unmarshal(condaSourceAuditRead(t, source), &document); err != nil {
		t.Fatal(err)
	}
	for _, channel := range document["channels"].([]any) {
		if channel == added {
			return
		}
	}
	t.Fatal("preset based on a stale upstream query deleted a concurrently added private channel")
}

func TestCondaFinalAuditColdCaptureAndPresetUseSavedSourceInRunnerEnvironment(t *testing.T) {
	for _, captured := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-capture", true: "already-captured"}[captured], func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			source := condaSourceAuditFile(t, home, "saved-source.yaml")
			content := condaSourceAuditRead(t, source)
			content = []byte(strings.Replace(string(content), "  - defaults\n", "  - saved-private\n  - defaults\n", 1) + "custom_channels:\n  saved-private: https://saved-private.example/channels\n")
			if err := os.WriteFile(source, content, 0640); err != nil {
				t.Fatal(err)
			}
			defaults := conda.MirrorDefaults{}
			if captured {
				var err error
				defaults, err = m.Conda.CaptureDefaults(defaults, "conda")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := m.RecordCondaDefaults(defaults, source); err != nil {
				t.Fatal(err)
			}
			if err := os.Unsetenv("CONDARC"); err != nil {
				t.Fatal(err)
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			fallback, err := originalMirrorRunner("conda", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var discovered map[string]any
			if err := json.Unmarshal(fallback, &discovered); err != nil {
				t.Fatal(err)
			}
			discovered["channels"] = []string{"saved-private", "defaults", "conda-forge"}
			discovered["custom_channels"].(map[string]any)["saved-private"] = "https://saved-private.example/channels/saved-private"
			sourceResult, err := json.Marshal(discovered)
			if err != nil {
				t.Fatal(err)
			}
			var observedSources []string
			reloaded.Conda.Runner = func(name string, args, env []string) ([]byte, error) {
				if name != "conda" {
					return originalMirrorRunner(name, args, env)
				}
				current := ""
				for _, entry := range env {
					if strings.HasPrefix(entry, "CONDARC=") {
						current = strings.TrimPrefix(entry, "CONDARC=")
					}
				}
				observedSources = append(observedSources, current)
				if current == source {
					return sourceResult, nil
				}
				return fallback, nil
			}
			next := reloaded.Config.Conda
			next.CondaMirror = "tuna"
			if err := reloaded.ConfigureConda(next, conda.NativeScope{CondaMirror: true}); err != nil {
				t.Fatal(err)
			}
			if len(observedSources) == 0 {
				t.Fatal("test did not exercise upstream source discovery")
			}
			for _, current := range observedSources {
				if current != source {
					t.Fatal("cold discovery queried an unrelated source instead of the saved CONDARC", observedSources)
				}
			}
			if !strings.Contains(reloaded.Config.Conda.PresetContext, "saved-private.example") || !bytes.Contains(condaSourceAuditRead(t, source), []byte("saved-private.example")) {
				t.Fatal("cold preset lost the saved source's private channel mapping")
			}
			if captured {
				if !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
					t.Fatal("cold preset replaced the already captured original default")
				}
			} else if !strings.Contains(reloaded.Config.Conda.Defaults.CondaYAML, "saved-private.example") {
				t.Fatal("first cold default capture ignored the saved source")
			}
		})
	}
}

func TestCondaFinalAuditSetupRejectsUnsafeSourceBeforeSavingAssociation(t *testing.T) {
	for _, scenario := range []string{"missing", "directory", "symlink", "hard-link", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(home, "unsafe.yaml")
			switch scenario {
			case "directory":
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hard-link":
				target := condaSourceAuditFile(t, home, "target.yaml")
				link := os.Link
				if scenario == "symlink" {
					link = os.Symlink
				}
				if err := link(target, source); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(source, bytes.Repeat([]byte("#"), 1<<20+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CONDARC", source)
			before := condaSourceAuditRead(t, filepath.Join(m.Root, "config.toml"))
			for _, flag := range []string{"--dry-run", "--yes"} {
				var out, stderr bytes.Buffer
				c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
				if code := c.Run([]string{"--root", m.Root, "conda", "setup", "conda", flag}); code == 0 {
					t.Fatal("setup associated an unsafe source", scenario, flag)
				}
				if !bytes.Equal(before, condaSourceAuditRead(t, filepath.Join(m.Root, "config.toml"))) {
					t.Fatal("failed setup changed the default or source association")
				}
			}
		})
	}
}

func TestCondaFinalAuditActiveSetupDoesNotReadMissingUnrelatedSource(t *testing.T) {
	for _, tool := range []string{"conda", "pip", "all"} {
		t.Run(tool, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			source := condaSourceAuditFile(t, home, "removed.yaml")
			t.Setenv("CONDARC", source)
			condaSourceAuditSelect(t, m, "tuna")
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			defaults := m.Config.Conda.Defaults
			for _, flag := range []string{"--dry-run", "--yes"} {
				var out, stderr bytes.Buffer
				c := CLI{Out: &out, Err: &stderr, CondaRunner: func(name string, args, env []string) ([]byte, error) {
					if name == "conda" {
						t.Fatal("setup re-queried an already captured Conda default")
					}
					return originalMirrorRunner(name, args, env)
				}}
				if code := c.Run([]string{"--root", m.Root, "conda", "setup", tool, flag}); code != 0 {
					t.Fatal("setup was blocked by an unrelated missing active source", tool, flag, stderr.String())
				}
				reloaded, err := New(m.Root)
				if err != nil {
					t.Fatal(err)
				}
				if reloaded.Config.Conda.SourceCONDARC != source || reloaded.Config.Conda.Defaults.CondaYAML != defaults.CondaYAML {
					t.Fatal("setup changed the original source or Conda default")
				}
				if _, err := os.Lstat(source); !os.IsNotExist(err) {
					t.Fatal("setup recreated a missing native source")
				}
			}
		})
	}
}

func TestCondaFinalAuditRecordDefaultsAPICannotRetargetAnActiveSource(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	first := condaSourceAuditFile(t, home, "active-record.yaml")
	second := condaSourceAuditFile(t, home, "other-record.yaml")
	t.Setenv("CONDARC", first)
	condaSourceAuditSelect(t, m, "tuna")
	beforeConfig := condaSourceAuditRead(t, filepath.Join(m.Root, "config.toml"))
	beforeEnv := condaSourceAuditRead(t, filepath.Join(m.Root, "env.sh"))
	beforeFirst, beforeSecond := condaSourceAuditRead(t, first), condaSourceAuditRead(t, second)
	defaults, err := m.Conda.CaptureDefaults(m.Config.Conda.Defaults, "pip")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CheckCondaDefaults(defaults, second); err == nil {
		t.Fatal("setup preview accepted retargeting an active source")
	}
	if err := m.RecordCondaDefaults(defaults, second); err == nil {
		t.Fatal("record-default API retargeted an active source without native application")
	}
	for path, before := range map[string][]byte{
		first: beforeFirst, second: beforeSecond,
		filepath.Join(m.Root, "config.toml"): beforeConfig,
		filepath.Join(m.Root, "env.sh"):      beforeEnv,
	} {
		if !bytes.Equal(before, condaSourceAuditRead(t, path)) {
			t.Fatal("rejected setup retarget changed a native or Nido file", path)
		}
	}
}
