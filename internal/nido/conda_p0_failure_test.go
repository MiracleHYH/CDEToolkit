package nido

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
)

// ConfigureConda captures the tools separately. A later tool's failure must
// not accidentally checkpoint the earlier capture as a successful setup.
func TestCondaP0SecondCaptureFailureDoesNotPersistPartialDefault(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	text := []byte("# original native configuration\nchannels: [defaults]\nssl_verify: /fixture/cert.pem\n")
	if err := os.WriteFile(native, text, 0600); err != nil {
		t.Fatal(err)
	}
	configPath, envPath := filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh")
	beforeConfig := condaSourceAuditRead(t, configPath)
	beforeEnv := condaSourceAuditRead(t, envPath)
	condaQueries, pipQueries := 0, 0
	m.Conda.Runner = func(name string, args, env []string) ([]byte, error) {
		if name == "python" {
			pipQueries++
			return nil, fmt.Errorf("fixture-sensitive-upstream-diagnostic")
		}
		condaQueries++
		return originalMirrorRunner(name, args, env)
	}
	err := m.ConfigureConda(conda.Config{CondaMirror: "tuna", PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple"})
	if err == nil || condaQueries == 0 || pipQueries == 0 {
		t.Fatalf("did not exercise a successful Conda capture followed by failed pip capture: conda=%d pip=%d err=%v", condaQueries, pipQueries, err)
	}
	if strings.Contains(err.Error(), "fixture-sensitive-upstream-diagnostic") {
		t.Fatal("capture failure disclosed an upstream diagnostic")
	}
	for path, want := range map[string][]byte{native: text, configPath: beforeConfig, envPath: beforeEnv} {
		if got := condaSourceAuditRead(t, path); !bytes.Equal(got, want) {
			t.Fatalf("failed multi-tool capture changed %s", path)
		}
	}
	reloaded, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Config.Conda, conda.Config{}) || !reflect.DeepEqual(m.Config.Conda, conda.Config{}) {
		t.Fatal("failed multi-tool capture persisted an incomplete default or a mirror selection")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
		t.Fatal("failed capture published a managed mirror")
	}
}

// The manager must complete all-target preflight before saving even a valid
// first default. In particular, a late pip target must protect earlier Conda
// files and the initial snapshot checkpoint from a partial operation.
func TestCondaP0LateNativePreflightFailureDoesNotCheckpointDefault(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	condarc := filepath.Join(home, ".condarc")
	condaText := []byte("# original native configuration\nchannels: [defaults]\nssl_verify: /fixture/cert.pem\n")
	if err := os.WriteFile(condarc, condaText, 0640); err != nil {
		t.Fatal(err)
	}
	pipPath := filepath.Join(home, ".config", "pip", "pip.conf")
	if err := os.MkdirAll(filepath.Dir(pipPath), 0700); err != nil {
		t.Fatal(err)
	}
	pipText := []byte("#" + strings.Repeat("x", 1<<20) + "\n[global]\ncert = /fixture/pip-cert.pem\n")
	if err := os.WriteFile(pipPath, pipText, 0600); err != nil {
		t.Fatal(err)
	}
	configPath, envPath := filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh")
	beforeConfig := condaSourceAuditRead(t, configPath)
	beforeEnv := condaSourceAuditRead(t, envPath)
	next := conda.Config{CondaMirror: "tuna", EnvsDir: filepath.Join(home, "new-envs"), PipCacheDir: filepath.Join(home, "new-pip-cache")}
	if err := m.ConfigureConda(next); err == nil {
		t.Fatal("late oversized pip target passed all-target preflight")
	}
	for path, want := range map[string][]byte{condarc: condaText, pipPath: pipText, configPath: beforeConfig, envPath: beforeEnv} {
		if got := condaSourceAuditRead(t, path); !bytes.Equal(got, want) {
			t.Fatalf("failed late preflight changed %s", path)
		}
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.Defaults.CondaCaptured {
		t.Fatal("failed all-target preflight checkpointed a first-use default", err)
	}
	for _, path := range []string{next.EnvsDir, next.PipCacheDir, filepath.Join(m.Root, "conda")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("failed preflight created %s", path)
		}
	}
}

// A per-file immutable flag provides a deterministic failure after the intent
// rename but before the environment rename. Directory chmod would reject both
// writes and cannot exercise this boundary. This remains entirely within tmp.
func TestCondaP0CommittedIntentSurvivesEnvironmentRefreshFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("deterministic per-file immutable flag is tested on Darwin")
	}
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	defaults, err := m.Conda.CaptureDefaults(conda.MirrorDefaults{}, "all")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RecordCondaDefaults(defaults); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".condarc")
	nativeText := []byte("# preserve certificate and proxy\nssl_verify: /fixture/cert.pem\nproxy_servers: {https: https://proxy.invalid}\n")
	if err := os.WriteFile(native, nativeText, 0640); err != nil {
		t.Fatal(err)
	}
	configPath, envPath := filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh")
	beforeConfig := condaSourceAuditRead(t, configPath)
	beforeEnv := condaSourceAuditRead(t, envPath)
	chflags := func(flag string) error {
		out, err := exec.Command("/usr/bin/chflags", flag, envPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("chflags %s on isolated env.sh: %w (%s)", flag, err, out)
		}
		return nil
	}
	if err := chflags("uchg"); err != nil {
		t.Skipf("temporary filesystem does not permit immutable-file fault injection: %v", err)
	}
	locked := true
	t.Cleanup(func() {
		if locked {
			if err := chflags("nouchg"); err != nil {
				t.Error(err)
			}
		}
	})
	next := m.Config.Conda
	next.EnvsDir = filepath.Join(home, "new-envs")
	next.PipMirror, next.PipIndexURL = "tuna", "https://pypi.tuna.tsinghua.edu.cn/simple"
	err = m.ConfigureConda(next)
	if err == nil || !strings.Contains(err.Error(), "配置已保存，环境文件写入失败") {
		t.Fatalf("did not reach the post-intent environment failure boundary: %v", err)
	}
	if got := condaSourceAuditRead(t, configPath); bytes.Equal(got, beforeConfig) {
		t.Fatal("fixture did not commit the new intent before environment failure")
	}
	if got := condaSourceAuditRead(t, envPath); !bytes.Equal(got, beforeEnv) {
		t.Fatal("failed environment refresh changed the immutable environment file")
	}
	if got := condaSourceAuditRead(t, native); !bytes.Contains(got, []byte(next.EnvsDir)) || !bytes.Contains(got, []byte("ssl_verify: /fixture/cert.pem")) || !bytes.Contains(got, []byte("proxy_servers:")) {
		t.Fatal("post-commit failure compensated native settings or lost unrelated fields")
	}
	reloaded, err := New(m.Root)
	if err != nil || !reflect.DeepEqual(reloaded.Config.Conda, next) || !reflect.DeepEqual(m.Config.Conda, next) {
		t.Fatal("post-commit failure lost the committed intent or immutable defaults", err)
	}
	if _, err := os.Lstat(next.EnvsDir); !os.IsNotExist(err) {
		t.Fatal("committed intent created an environment data directory")
	}
	if err := chflags("nouchg"); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := reloaded.Init(); err != nil {
		t.Fatal("init could not repair the interrupted environment refresh", err)
	}
	if got := condaSourceAuditRead(t, envPath); !bytes.Equal(got, []byte(reloaded.Environment())) || bytes.Equal(got, beforeEnv) {
		t.Fatal("retry did not regenerate the environment from the committed intent")
	}
	if !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) {
		t.Fatal("environment repair replaced a first-use default")
	}
}
