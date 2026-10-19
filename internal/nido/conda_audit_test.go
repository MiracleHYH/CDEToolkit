package nido

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"go.yaml.in/yaml/v3"
)

// Directory and pip operations must not reapply a selected Conda mirror over
// native channel changes made after that mirror was enabled.
func TestCondaAuditUnrelatedCommandsPreserveLaterNativeChannelEdits(t *testing.T) {
	for _, action := range []string{"pip-cache", "conda-cache", "envs", "pip-mirror", "pip-off", "cache-off", "envs-off"} {
		t.Run(action, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			initial := conda.Config{
				CondaMirror: "tuna", PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple",
				EnvsDir: filepath.Join(home, "old-envs"), PkgsDir: filepath.Join(home, "old-pkgs"), PipCacheDir: filepath.Join(home, "old-pip"),
			}
			if err := m.ConfigureConda(initial); err != nil {
				t.Fatal(err)
			}
			defaults := m.Config.Conda.Defaults
			native := filepath.Join(home, ".condarc")
			later := []byte("# user edited channels after selecting a mirror\nchannels:\n  - https://user-added.example/channel\n  - conda-forge\nchannel_priority: strict\nssl_verify: /user/certificate.pem\nenvs_dirs:\n  - " + initial.EnvsDir + "\npkgs_dirs:\n  - " + initial.PkgsDir + "\n")
			if err := os.WriteFile(native, later, 0600); err != nil {
				t.Fatal(err)
			}
			var args []string
			changesCondaPath := false
			switch action {
			case "pip-cache":
				args = []string{"cache", "pip", filepath.Join(home, "new-pip")}
			case "conda-cache":
				args, changesCondaPath = []string{"cache", "conda", filepath.Join(home, "new-pkgs")}, true
			case "envs":
				args, changesCondaPath = []string{"envs", filepath.Join(home, "new-envs")}, true
			case "pip-mirror":
				args = []string{"mirror", "pip", "official"}
			case "pip-off":
				args = []string{"mirror", "pip", "off"}
			case "cache-off":
				args = []string{"cache", "conda", "off"}
			case "envs-off":
				args = []string{"envs", "off"}
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("an unrelated operation reread a captured default")
				return nil, nil
			}}
			if code := c.Run(append(append([]string{"--root", m.Root, "conda"}, args...), "--yes")); code != 0 {
				t.Fatal(stderr.String())
			}
			after, err := os.ReadFile(native)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := yaml.Unmarshal(after, &document); err != nil {
				t.Fatal(err)
			}
			wantChannels := []any{"https://user-added.example/channel", "conda-forge"}
			if !reflect.DeepEqual(document["channels"], wantChannels) {
				t.Fatalf("unrelated command replaced the user's later channels: %s", after)
			}
			if document["channel_priority"] != "strict" || document["ssl_verify"] != "/user/certificate.pem" {
				t.Fatalf("unrelated native fields changed: %s", after)
			}
			if !changesCondaPath && !bytes.Equal(after, later) {
				t.Fatalf("command without a Conda native change rewrote .condarc: %s", after)
			}
			reloaded, err := New(m.Root)
			if err != nil || !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
				t.Fatal("unrelated command changed the durable default", err)
			}
		})
	}
}

func TestCondaAuditSetupDryRunChecksOwnedFilesLikeRealSetup(t *testing.T) {
	for _, captured := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-setup", true: "repeat-setup"}[captured], func(t *testing.T) {
			m, _ := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			if captured {
				defaults, err := m.Conda.CaptureDefaults(conda.MirrorDefaults{}, "all")
				if err != nil {
					t.Fatal(err)
				}
				if err := m.RecordCondaDefaults(defaults); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(m.Root, "env.sh")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := append(before, []byte("\n# user edit\n")...)
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
			if code := c.Run([]string{"--root", m.Root, "conda", "setup", "all", "--dry-run"}); code == 0 {
				t.Fatal("setup dry-run accepted a user-modified env.sh")
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, changed) {
				t.Fatal("setup dry-run modified the owned environment", err)
			}
		})
	}
}

func TestCondaAuditCancelledFirstSwitchDoesNotPersistDefaultOrNativeChange(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	beforeNative := []byte("# retained\nchannels:\n  - https://user.example/channel\n")
	if err := os.WriteFile(native, beforeNative, 0600); err != nil {
		t.Fatal(err)
	}
	beforeConfig, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	c := CLI{UI: newCondaUI(t, condaSelect("取消")), Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
	if c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", "tuna"}) != 0 {
		t.Fatal("cancelled first-use switch was treated as a failure", stderr.String())
	}
	if after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(beforeConfig, after) {
		t.Fatal("cancelled first-use switch persisted a default")
	}
	if after, _ := os.ReadFile(native); !bytes.Equal(beforeNative, after) {
		t.Fatal("cancelled first-use switch modified the native file")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
		t.Fatal("cancelled first-use switch published a mirror")
	}
}

func TestCondaAuditRepeatedPathCommandAppliesOnlyItsRequestedField(t *testing.T) {
	for _, tool := range []string{"conda-cache", "envs", "pip-cache"} {
		t.Run(tool, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			requested := filepath.Join(home, "requested")
			cfg := conda.Config{}
			var args []string
			var native string
			switch tool {
			case "conda-cache":
				cfg.PkgsDir, native = requested, filepath.Join(home, ".condarc")
				args = []string{"cache", "conda", requested}
			case "envs":
				cfg.EnvsDir, native = requested, filepath.Join(home, ".condarc")
				args = []string{"envs", requested}
			case "pip-cache":
				cfg.PipCacheDir = requested
				args = []string{"cache", "pip", requested}
			}
			if err := m.ConfigureConda(cfg); err != nil {
				t.Fatal(err)
			}
			var edited []byte
			if tool == "pip-cache" {
				native = filepath.Join(home, ".config", "pip", "pip.conf")
				edited = []byte("# retain\n[global]\ncache-dir = /user/later-cache\nindex-url = https://user.example/simple\n")
			} else {
				edited = []byte("# retain\nchannels:\n  - https://user.example/channel\nssl_verify: true\nenvs_dirs:\n  - /user/later-envs\npkgs_dirs:\n  - /user/later-pkgs\n")
			}
			if err := os.WriteFile(native, edited, 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, CondaRunner: originalMirrorRunner}
			if c.Run(append(append([]string{"--root", m.Root, "conda"}, args...), "--yes")) != 0 {
				t.Fatal(stderr.String())
			}
			after, err := os.ReadFile(native)
			if err != nil || !bytes.Contains(after, []byte(requested)) {
				t.Fatalf("repeated explicit path did not apply the requested value: %s; %v", after, err)
			}
			for _, kept := range []string{"# retain", "https://user.example/"} {
				if !bytes.Contains(after, []byte(kept)) {
					t.Fatalf("repeated path command lost unrelated settings: %s", after)
				}
			}
			if tool == "envs" && !bytes.Contains(after, []byte("/user/later-pkgs")) || tool == "conda-cache" && !bytes.Contains(after, []byte("/user/later-envs")) {
				t.Fatalf("repeated path command reapplied a different native path: %s", after)
			}
		})
	}
}

func TestCondaAuditFirstDefaultReflectsOriginalAfterConfirmation(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	if err := os.WriteFile(native, []byte("channels:\n  - https://original.example/channel\n"), 0600); err != nil {
		t.Fatal(err)
	}
	afterConfirmation := false
	later := []byte("# edited while confirmation was open\nchannels:\n  - https://later.example/channel\nssl_verify: true\n")
	confirm := condaAccept()
	confirm.before = func() {
		afterConfirmation = true
		if err := os.WriteFile(native, later, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	c := CLI{UI: newCondaUI(t, confirm), Out: &out, Err: &stderr, CondaRunner: func(name string, args, env []string) ([]byte, error) {
		data, err := originalMirrorRunner(name, args, env)
		if afterConfirmation {
			data = bytes.ReplaceAll(data, []byte("original.example"), []byte("later.example"))
		}
		return data, err
	}}
	if code := c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", "tuna"}); code != 0 {
		if after, _ := os.ReadFile(native); !bytes.Equal(after, later) {
			t.Fatal("rejected first switch still changed the concurrent native edit")
		}
		return
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reloaded.Config.Conda.Defaults.CondaYAML, "https://later.example/pkgs/main") {
		t.Fatal("first default retained pre-confirmation mirror state while applying a switch")
	}
}

func TestCondaAuditFirstDefaultIsDurableBeforeNativeWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root process bypasses the native directory write denial")
	}
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	before := []byte("# retain before failed native write\nchannels:\n  - defaults\nssl_verify: true\n")
	if err := os.WriteFile(native, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0700) })
	if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna"}); err == nil {
		t.Fatal("native write unexpectedly succeeded in a nonwritable parent")
	}
	if after, err := os.ReadFile(native); err != nil || !bytes.Equal(after, before) {
		t.Fatal("failed native write changed the user configuration", err)
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Config.Conda.Defaults.CondaCaptured || !strings.Contains(reloaded.Config.Conda.Defaults.CondaYAML, "https://original.example/pkgs/main") {
		t.Fatal("native mutation began before the original default was durably recorded")
	}
	if reloaded.Config.Conda.CondaMirror != "" {
		t.Fatal("failed native write committed an unapplied mirror selection")
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	reloaded.Conda.Runner = func(string, []string, []string) ([]byte, error) {
		return nil, fmt.Errorf("must not reread an already durable default")
	}
	if err := reloaded.ConfigureConda(conda.Config{CondaMirror: "default"}); err != nil {
		t.Fatal("failed write could not reuse its durable original default", err)
	}
}

func TestCondaAuditIntentSaveFailureCompensatesNativePaths(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-native-file", true: "existing-native-file"}[exists], func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			native := filepath.Join(home, ".condarc")
			before := []byte("# retained\nssl_verify: true\n")
			if exists {
				if err := os.WriteFile(native, before, 0640); err != nil {
					t.Fatal(err)
				}
			}
			m.Config.Download.GitHubProxy = strings.Repeat("x", 1<<20)
			if err := m.ConfigureConda(conda.Config{EnvsDir: filepath.Join(home, "new-envs"), PkgsDir: filepath.Join(home, "new-pkgs")}); err == nil {
				t.Fatal("oversized configuration unexpectedly saved")
			}
			if exists {
				after, err := os.ReadFile(native)
				if err != nil || !bytes.Equal(after, before) {
					t.Fatal("failed intent save left changed native settings", err)
				}
				info, err := os.Stat(native)
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatal("failed intent save changed native file permissions", err)
				}
			} else if _, err := os.Lstat(native); !os.IsNotExist(err) {
				t.Fatal("failed intent save left a new native file", err)
			}
			for _, path := range []string{filepath.Join(home, "new-envs"), filepath.Join(home, "new-pkgs")} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("failed intent save created a data directory", err)
				}
			}
		})
	}
}

func TestCondaAuditMirrorExactBytesDoNotBypassPrivateFileChecks(t *testing.T) {
	for _, scenario := range []string{"public-permissions", "hard-link"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureConda(conda.Config{CondaMirror: "default"}); err != nil {
				t.Fatal(err)
			}
			path, expected := m.Conda.MirrorFile(m.Config.Conda)
			linked := filepath.Join(home, "user-mirror-hard-link")
			if scenario == "public-permissions" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Link(path, linked); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(m.Root, "config.toml")
			beforeConfig, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeInfo, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureConda(m.Config.Conda); err == nil {
				t.Fatal("matching mirror bytes bypassed private file ownership checks")
			}
			if after, err := os.ReadFile(configPath); err != nil || !bytes.Equal(after, beforeConfig) {
				t.Fatal("rejected mirror ownership changed the saved intent", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, expected) {
				t.Fatal("rejected mirror ownership overwrote the original file", err)
			}
			afterInfo, err := os.Lstat(path)
			if err != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.Mode() != afterInfo.Mode() {
				t.Fatal("rejected mirror ownership replaced the user-modified inode or mode", err)
			}
			if scenario == "hard-link" {
				linkedInfo, err := os.Lstat(linked)
				if err != nil || !os.SameFile(afterInfo, linkedInfo) {
					t.Fatal("rejected mirror ownership removed or replaced the hard link", err)
				}
			}
			if report := m.CondaStatus(); len(report.Issues) == 0 {
				t.Fatal("status did not diagnose unsafe mirror ownership")
			}
		})
	}
}

func TestCondaAuditExplicitPresetRetainsLaterPrivateChannelsAndOriginalDefault(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna"}); err != nil {
		t.Fatal(err)
	}
	original := m.Config.Conda.Defaults
	private := "https://person:PRESET-PRIVATE-SECRET@later-private.example/team"
	currentChannels := []string{"conda-forge", private, "defaults"}
	native := filepath.Join(home, ".condarc")
	if err := os.WriteFile(native, []byte("# retained later private channel\nchannels:\n  - conda-forge\n  - "+private+"\n  - defaults\nssl_verify: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := func(name string, args, env []string) ([]byte, error) {
		data, err := originalMirrorRunner(name, args, env)
		if name != "conda" || err != nil {
			return data, err
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		result["channels"] = currentChannels
		return json.Marshal(result)
	}
	for _, mirror := range []string{"official", "tuna"} {
		var out, stderr bytes.Buffer
		c := CLI{Out: &out, Err: &stderr, CondaRunner: runner}
		if code := c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", mirror, "--yes"}); code != 0 {
			t.Fatal(stderr.String())
		}
		reloaded, err := New(m.Root)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(reloaded.Config.Conda.Defaults, original) {
			t.Fatal("explicit preset selection replaced the durable original default")
		}
		if !reflect.DeepEqual(conda.DesiredChannels(reloaded.Config.Conda), currentChannels) {
			t.Fatal("explicit preset discarded a later private channel")
		}
		_, overlay := reloaded.Conda.MirrorFile(reloaded.Config.Conda)
		for _, content := range [][]byte{overlay, mustReadCondaAuditFile(t, native)} {
			var parsed struct {
				Channels []string `yaml:"channels"`
			}
			if err := yaml.Unmarshal(content, &parsed); err != nil || !reflect.DeepEqual(parsed.Channels, currentChannels) {
				t.Fatal("native and effective overlay channel selections diverged", err)
			}
		}
		out.Reset()
		stderr.Reset()
		if c.Run([]string{"--root", m.Root, "conda", "status", "--json"}) != 0 || strings.Contains(out.String()+stderr.String(), "PRESET-PRIVATE-SECRET") {
			t.Fatal("status exposed the private preset channel")
		}
	}
	var out, stderr bytes.Buffer
	c := CLI{Out: &out, Err: &stderr, CondaRunner: runner}
	if c.Run([]string{"--root", m.Root, "conda", "mirror", "conda", "default", "--yes"}) != 0 {
		t.Fatal(stderr.String())
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.PresetChannelsSet || len(reloaded.Config.Conda.PresetChannels) != 0 || reloaded.Config.Conda.PresetContext != "" {
		t.Fatal("default retained stale dynamic preset state", err)
	}
}

func mustReadCondaAuditFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
