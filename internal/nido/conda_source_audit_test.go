package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"go.yaml.in/yaml/v3"
)

const condaSourceAuditSecret = "SOURCE-PROXY-SECRET"

func condaSourceAuditFile(t *testing.T, home, name string) string {
	t.Helper()
	path := filepath.Join(home, name)
	content := "# user source: certificate settings remain live\n" +
		"channels:\n  - defaults\n  - conda-forge\n" +
		"ssl_verify: " + filepath.Join(home, "private-ca.pem") + "\n" +
		"client_ssl_cert: " + filepath.Join(home, "private-client.pem") + "\n" +
		"client_ssl_cert_key: " + filepath.Join(home, "private-client.key") + "\n" +
		"# retain proxy credentials in their native file\n" +
		"proxy_servers:\n  https: https://proxy-user:" + condaSourceAuditSecret + "@proxy.invalid:8443\n" +
		"channel_priority: strict\n" +
		"future_nonmirror_setting:\n  enabled: true\n  values: [first, second]\n" +
		"# user source: trailing comment\n"
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatal(err)
	}
	return path
}

func condaSourceAuditRead(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func condaSourceAuditNonMirrors(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"channels", "default_channels", "custom_channels", "custom_multichannels", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels"} {
		delete(document, key)
	}
	return document
}

func condaSourceAuditSelect(t *testing.T, m *Manager, mirror string) {
	t.Helper()
	// A Manager snapshots the invoking environment. Refresh the test seam to
	// model each later CLI invocation after changing that environment.
	m.Conda.Env = os.Environ()
	next := m.Config.Conda
	next.CondaMirror = mirror
	if err := m.ConfigureConda(next, conda.NativeScope{CondaMirror: true}); err != nil {
		t.Fatal(err)
	}
}

func TestCondaSourceAuditKeepsOriginalPathAndNonMirrorSettings(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "custom settings.yaml")
	t.Setenv("CONDARC", source)
	before := condaSourceAuditRead(t, source)
	beforeInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, mirror := range []string{"tuna", "official", "default"} {
		condaSourceAuditSelect(t, m, mirror)
		if got := m.Config.Conda.SourceCONDARC; got != source {
			t.Fatalf("did not retain the explicit CONDARC path: %q", got)
		}
		if got := m.Conda.Variables(m.Config.Conda)["CONDARC"]; got != source {
			t.Fatalf("replaced the original CONDARC search position with %q", got)
		}
		after := condaSourceAuditRead(t, source)
		if !reflect.DeepEqual(condaSourceAuditNonMirrors(t, before), condaSourceAuditNonMirrors(t, after)) {
			t.Fatal("switching mirrors changed certificate, proxy, or an unknown non-mirror setting")
		}
		for _, comment := range []string{"# user source: certificate settings remain live", "# retain proxy credentials in their native file", "# user source: trailing comment"} {
			if !bytes.Contains(after, []byte(comment)) {
				t.Fatal("switching mirrors removed a non-mirror comment", comment)
			}
		}
		afterInfo, err := os.Stat(source)
		if err != nil || afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
			t.Fatal("switching mirrors changed native permissions", err)
		}
		beforeStat, beforeOK := beforeInfo.Sys().(*syscall.Stat_t)
		afterStat, afterOK := afterInfo.Sys().(*syscall.Stat_t)
		if !beforeOK || !afterOK || beforeStat.Gid != afterStat.Gid {
			t.Fatal("switching mirrors changed native group ownership")
		}
		for name, data := range map[string][]byte{"config.toml": condaSourceAuditRead(t, filepath.Join(m.Root, "config.toml")), "env.sh": []byte(m.Environment())} {
			for _, sensitive := range []string{condaSourceAuditSecret, filepath.Join(home, "private-client.key"), "future_nonmirror_setting"} {
				if bytes.Contains(data, []byte(sensitive)) {
					t.Fatalf("copied native non-mirror settings into %s", name)
				}
			}
		}
	}
}

func TestCondaSourceAuditFreshShellAndAtomicReplacementRemainLive(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "live.yaml")
	t.Setenv("CONDARC", source)
	condaSourceAuditSelect(t, m, "tuna")
	replacement := bytes.ReplaceAll(condaSourceAuditRead(t, source), []byte("channel_priority: strict"), []byte("channel_priority: flexible"))
	replacement = bytes.ReplaceAll(replacement, []byte(condaSourceAuditSecret), []byte("LATER-PROXY-SECRET"))
	staged := filepath.Join(home, "replacement.yaml")
	if err := os.WriteFile(staged, replacement, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, source); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("CONDARC"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Conda.Runner = originalMirrorRunner
	if got := reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"]; got != source {
		t.Fatalf("a fresh shell lost its persisted source: %q", got)
	}
	if got := condaSourceAuditRead(t, reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"]); !bytes.Equal(got, replacement) {
		t.Fatal("activation captured a stale file inode or a non-mirror snapshot")
	}
	next := reloaded.Config.Conda
	next.EnvsDir = filepath.Join(home, "later envs")
	if err := reloaded.ConfigureConda(next, conda.NativeScope{EnvsDir: true}); err != nil {
		t.Fatal(err)
	}
	after := condaSourceAuditRead(t, source)
	beforeNonMirrors := condaSourceAuditNonMirrors(t, replacement)
	afterNonMirrors := condaSourceAuditNonMirrors(t, after)
	delete(afterNonMirrors, "envs_dirs")
	if !reflect.DeepEqual(beforeNonMirrors, afterNonMirrors) || !bytes.Contains(after, []byte(next.EnvsDir)) {
		t.Fatal("a fresh-shell path edit lost later source settings or omitted the source file")
	}
	if _, err := os.Stat(next.EnvsDir); !os.IsNotExist(err) {
		t.Fatal("a source path edit created an environment data directory", err)
	}
}

func TestCondaSourceAuditExplicitAndLegacyMarkerPrecedeSavedSource(t *testing.T) {
	for _, origin := range []string{"current-external", "legacy-marker"} {
		t.Run(origin, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			first := condaSourceAuditFile(t, home, "first.yaml")
			nextSource := condaSourceAuditFile(t, home, "next.yaml")
			t.Setenv("CONDARC", first)
			condaSourceAuditSelect(t, m, "tuna")
			firstBytes := condaSourceAuditRead(t, first)
			defaults := m.Config.Conda.Defaults
			if origin == "current-external" {
				t.Setenv("CONDARC", nextSource)
			} else {
				legacy := filepath.Join(m.Root, "conda", "mirror-legacy.yaml")
				t.Setenv("CONDARC", legacy)
				t.Setenv("_NIDO_CONDA_CONDARC_ACTIVE", "1")
				t.Setenv("_NIDO_CONDA_CONDARC_VALUE", legacy)
				t.Setenv("_NIDO_CONDA_CONDARC_APPLIED_SET", "x")
				t.Setenv("_NIDO_CONDA_CONDARC_ORIGINAL_SET", "x")
				t.Setenv("_NIDO_CONDA_CONDARC_ORIGINAL", nextSource)
			}
			condaSourceAuditSelect(t, m, "official")
			if m.Config.Conda.SourceCONDARC != nextSource || m.Conda.Variables(m.Config.Conda)["CONDARC"] != nextSource {
				t.Fatal("an explicit source was hidden by the persisted association")
			}
			if got := condaSourceAuditRead(t, first); !bytes.Equal(firstBytes, got) {
				t.Fatal("selecting another source rewrote the previously associated file")
			}
			if !reflect.DeepEqual(m.Config.Conda.Defaults, defaults) {
				t.Fatal("selecting another source overwrote the original mirror default")
			}
		})
	}
}

func TestCondaSourceAuditDryRunDoesNotWriteOrCopyNativeSecrets(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "preview.yaml")
	t.Setenv("CONDARC", source)
	paths := []string{source, filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh")}
	before := map[string][]byte{}
	for _, path := range paths {
		before[path] = condaSourceAuditRead(t, path)
	}
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
	if code := c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", "tuna", "--dry-run", "--json"}); code != 0 {
		t.Fatal(stderr.String())
	}
	for _, path := range paths {
		if after := condaSourceAuditRead(t, path); !bytes.Equal(before[path], after) {
			t.Fatal("dry-run wrote a native or Nido file", path)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".condarc")); !os.IsNotExist(err) {
		t.Fatal("dry-run created a native file", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
		t.Fatal("dry-run created an activation resource", err)
	}
	if !strings.Contains(out.String(), source) {
		t.Fatal("preview omitted the original CONDARC path")
	}
	for _, sensitive := range []string{condaSourceAuditSecret, filepath.Join(home, "private-client.key"), "future_nonmirror_setting"} {
		if strings.Contains(out.String()+stderr.String(), sensitive) {
			t.Fatal("preview disclosed a native non-mirror setting")
		}
	}
}

func TestCondaSourceAuditResetRetainsNativeBytesAndSourceAssociation(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "retained.yaml")
	t.Setenv("CONDARC", source)
	condaSourceAuditSelect(t, m, "tuna")
	before := condaSourceAuditRead(t, source)
	defaults := m.Config.Conda.Defaults
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
		t.Fatal("reset queried an upstream while releasing an existing profile")
		return nil, nil
	}}
	if code := c.Run([]string{"--root", m.Root, "conda", "reset", "--yes"}); code != 0 {
		t.Fatal(stderr.String())
	}
	if got := condaSourceAuditRead(t, source); !bytes.Equal(got, before) {
		t.Fatal("reset restored or changed native mirror/settings bytes")
	}
	if err := os.Unsetenv("CONDARC"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Conda.Runner = originalMirrorRunner
	if reloaded.Config.Conda.SourceCONDARC != source || !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
		t.Fatal("reset discarded the source association or original default")
	}
	if _, assigned := reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"]; assigned {
		t.Fatal("reset still actively exports CONDARC")
	}
	condaSourceAuditSelect(t, reloaded, "official")
	if reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"] != source {
		t.Fatal("re-enabling in a fresh shell lost the retained source")
	}
}

func TestCondaSourceAuditPipAndResetIgnoreUnsupportedCurrentCONDARC(t *testing.T) {
	for _, action := range []string{"pip-mirror", "pip-cache", "reset"} {
		t.Run(action, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			unsupported := condaSourceAuditFile(t, home, "ignored-by-conda.conf")
			t.Setenv("CONDARC", unsupported)
			before := condaSourceAuditRead(t, unsupported)
			args := []string{"reset"}
			if action == "pip-mirror" {
				args = []string{"mirror", "pip", "official"}
			} else if action == "pip-cache" {
				args = []string{"cache", "pip", filepath.Join(home, "pip-cache")}
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: func(name string, args, env []string) ([]byte, error) {
				if name == "conda" {
					t.Fatal("an unrelated operation queried Conda")
				}
				return originalMirrorRunner(name, args, env)
			}}
			if code := c.Run(append(append([]string{"--root", m.Root, "conda"}, args...), "--yes")); code != 0 {
				t.Fatal("unrelated operation was blocked by current CONDARC", stderr.String())
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Config.Conda.SourceCONDARC != "" || reloaded.Config.Conda.Defaults.CondaCaptured {
				t.Fatal("an unrelated operation associated or captured Conda")
			}
			if after := condaSourceAuditRead(t, unsupported); !bytes.Equal(before, after) {
				t.Fatal("an unrelated operation modified current CONDARC")
			}
			if _, err := os.Lstat(filepath.Join(home, ".condarc")); !os.IsNotExist(err) {
				t.Fatal("an unrelated operation created a native Conda file", err)
			}
		})
	}
}

func TestCondaSourceAuditResetCanReleaseMissingSource(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "later-removed.yaml")
	t.Setenv("CONDARC", source)
	condaSourceAuditSelect(t, m, "tuna")
	defaults := m.Config.Conda.Defaults
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDARC", "unsupported relative current source")
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
		t.Fatal("reset queried an upstream for a missing source")
		return nil, nil
	}}
	if code := c.Run([]string{"--root", m.Root, "conda", "reset", "--yes"}); code != 0 {
		t.Fatal("reset could not release a missing source", stderr.String())
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.Conda.CondaMirror != "" || reloaded.Config.Conda.SourceCONDARC != source || !reflect.DeepEqual(defaults, reloaded.Config.Conda.Defaults) {
		t.Fatal("reset lost durable default/source information while releasing a missing source")
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatal("reset recreated a removed native source", err)
	}
}

func TestCondaSourceAuditPipOnlyPreservesActiveSourceAssociation(t *testing.T) {
	for _, action := range []string{"pip-mirror", "pip-cache", "pip-setup"} {
		t.Run(action, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			first := condaSourceAuditFile(t, home, "active-source.yaml")
			current := condaSourceAuditFile(t, home, "manually-selected.yaml")
			t.Setenv("CONDARC", first)
			condaSourceAuditSelect(t, m, "tuna")
			beforeFirst := condaSourceAuditRead(t, first)
			beforeCurrent := condaSourceAuditRead(t, current)
			defaults := m.Config.Conda.Defaults
			t.Setenv("CONDARC", current)
			args := []string{"setup", "pip"}
			if action == "pip-mirror" {
				args = []string{"mirror", "pip", "official"}
			} else if action == "pip-cache" {
				args = []string{"cache", "pip", filepath.Join(home, "pip-cache")}
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: func(name string, args, env []string) ([]byte, error) {
				if name == "conda" {
					t.Fatal("a pip-only operation queried Conda")
				}
				return originalMirrorRunner(name, args, env)
			}}
			if code := c.Run(append(append([]string{"--root", m.Root, "conda"}, args...), "--yes")); code != 0 {
				t.Fatal(stderr.String())
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Config.Conda.SourceCONDARC != first || reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"] != first {
				t.Fatal("a pip-only operation reassociated the active Conda source")
			}
			if !bytes.Equal(beforeFirst, condaSourceAuditRead(t, first)) || !bytes.Equal(beforeCurrent, condaSourceAuditRead(t, current)) {
				t.Fatal("a pip-only operation rewrote a native Conda source")
			}
			if defaults.CondaYAML != reloaded.Config.Conda.Defaults.CondaYAML || !reflect.DeepEqual(defaults.CondaVariables, reloaded.Config.Conda.Defaults.CondaVariables) {
				t.Fatal("a pip-only operation changed the Conda default")
			}
		})
	}
}

func TestCondaSourceAuditInitialSetupPersistsInactiveSource(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	source := condaSourceAuditFile(t, home, "setup-source.yaml")
	t.Setenv("CONDARC", source)
	before := condaSourceAuditRead(t, source)
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
	if code := c.Run([]string{"--root", m.Root, "conda", "setup", "conda", "--yes"}); code != 0 {
		t.Fatal(stderr.String())
	}
	if !bytes.Equal(before, condaSourceAuditRead(t, source)) {
		t.Fatal("setup modified the source instead of recording its association")
	}
	if err := os.Unsetenv("CONDARC"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Config.Conda.CondaMirror != "" || !reloaded.Config.Conda.Defaults.CondaCaptured || reloaded.Config.Conda.SourceCONDARC != source {
		t.Fatal("initial setup did not persist the inactive source and original default")
	}
	defaults := reloaded.Config.Conda.Defaults
	reloaded.Conda.Runner = originalMirrorRunner
	condaSourceAuditSelect(t, reloaded, "tuna")
	if reloaded.Conda.Variables(reloaded.Config.Conda)["CONDARC"] != source || !reflect.DeepEqual(defaults, reloaded.Config.Conda.Defaults) {
		t.Fatal("a fresh-shell first switch lost setup's source or replaced its original default")
	}
	if !reflect.DeepEqual(condaSourceAuditNonMirrors(t, before), condaSourceAuditNonMirrors(t, condaSourceAuditRead(t, source))) {
		t.Fatal("a fresh-shell first switch changed setup's non-mirror settings")
	}
}

func TestCondaSourceAuditActiveSetupDoesNotRetargetSource(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	first := condaSourceAuditFile(t, home, "active.yaml")
	current := condaSourceAuditFile(t, home, "other-current.yaml")
	t.Setenv("CONDARC", first)
	condaSourceAuditSelect(t, m, "tuna")
	beforeFirst := condaSourceAuditRead(t, first)
	beforeCurrent := condaSourceAuditRead(t, current)
	defaults := m.Config.Conda.Defaults
	t.Setenv("CONDARC", current)
	for _, flag := range []string{"--dry-run", "--yes"} {
		var out, stderr bytes.Buffer
		c := CLI{Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
			t.Fatal("repeated setup queried a previously captured default")
			return nil, nil
		}}
		if code := c.Run([]string{"--root", m.Root, "conda", "setup", "conda", flag}); code != 0 {
			t.Fatal(stderr.String())
		}
		reloaded, err := New(m.Root)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Config.Conda.SourceCONDARC != first || !reflect.DeepEqual(defaults, reloaded.Config.Conda.Defaults) {
			t.Fatal("repeated active setup retargeted the source or original default")
		}
		if !bytes.Equal(beforeFirst, condaSourceAuditRead(t, first)) || !bytes.Equal(beforeCurrent, condaSourceAuditRead(t, current)) {
			t.Fatal("repeated setup modified a native source")
		}
	}
}
