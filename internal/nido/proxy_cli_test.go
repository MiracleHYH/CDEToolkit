package nido

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

type cliProxyRunner struct {
	process         proxy.Process
	commands        []proxy.Command
	starts, stops   int
	ready           bool
	validationError bool
}

func (r *cliProxyRunner) Run(c proxy.Command) ([]byte, error) {
	r.commands = append(r.commands, c)
	if len(c.Args) == 1 && c.Args[0] == "-v" {
		return []byte("Mihomo Meta v1.2.3 darwin arm64"), nil
	}
	if r.validationError {
		return nil, errors.New("secret provider data")
	}
	return nil, nil
}
func (r *cliProxyRunner) Start(c proxy.Command, commit func(proxy.Process) error) (proxy.Process, <-chan error, error) {
	r.starts++
	r.process = proxy.Process{PID: 4242, UID: uint32(os.Geteuid()), Boot: "test-boot", Started: fmt.Sprintf("birth-%d", r.starts), Executable: c.Launcher}
	if err := commit(r.process); err != nil {
		return r.process, nil, err
	}
	r.process.Executable = c.Program
	return r.process, make(chan error), nil
}
func (r *cliProxyRunner) Inspect(int) (proxy.Process, error) {
	if r.process.PID == 0 {
		return proxy.Process{}, os.ErrNotExist
	}
	return r.process, nil
}
func (r *cliProxyRunner) Stop(p proxy.Process) error {
	if p != r.process {
		return errors.New("wrong identity")
	}
	r.stops++
	r.process = proxy.Process{}
	return nil
}
func (r *cliProxyRunner) OwnsEndpoint(proxy.Process, string) (bool, error) { return r.ready, nil }

func proxyCLI(t *testing.T, m *Manager, r *cliProxyRunner, in string, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	c := CLI{In: strings.NewReader(in), Out: &out, Err: &stderr, ProxyRunner: r}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String()
}

// These executable headers are only read by the native format checker. The
// injected runner is the sole executor in CLI tests.
func proxyCLIArtifact(t *testing.T) string {
	t.Helper()
	var data []byte
	if runtime.GOOS == "linux" {
		data = make([]byte, 64)
		copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
		binary.LittleEndian.PutUint16(data[16:], 2)
		machine := uint16(62)
		if runtime.GOARCH == "arm64" {
			machine = 183
		}
		binary.LittleEndian.PutUint16(data[18:], machine)
		binary.LittleEndian.PutUint32(data[20:], 1)
		binary.LittleEndian.PutUint16(data[52:], 64)
		binary.LittleEndian.PutUint16(data[54:], 56)
		binary.LittleEndian.PutUint16(data[58:], 64)
	} else {
		data = make([]byte, 32)
		binary.LittleEndian.PutUint32(data, 0xfeedfacf)
		cpu := uint32(0x01000007)
		if runtime.GOARCH == "arm64" {
			cpu = 0x0100000c
		}
		binary.LittleEndian.PutUint32(data[4:], cpu)
		binary.LittleEndian.PutUint32(data[12:], 2)
	}
	path := filepath.Join(t.TempDir(), "test-core")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProxyCLIIsolatedLifecycle(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{ready: true}
	artifact := proxyCLIArtifact(t)
	config := filepath.Join(home, "input.yaml")
	want := []byte("mixed-port: 17890\nmode: rule\n")
	if err := os.WriteFile(config, want, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"proxy", "install", "--artifact", artifact, "--yes", "--json"},
		{"proxy", "configure", "--file", config, "--yes", "--json"},
		{"proxy", "validate", "--json"},
	} {
		code, out, stderr := proxyCLI(t, m, r, "", args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatalf("%v: code=%d out=%s err=%s", args, code, out, stderr)
		}
	}
	if r.starts != 0 {
		t.Fatal("install/configure/validate started the component")
	}
	if code, out, stderr := proxyCLI(t, m, r, "", "proxy", "endpoint"); code != 0 || strings.TrimSpace(out) != "http://127.0.0.1:17890" {
		t.Fatalf("endpoint %d %s %s", code, out, stderr)
	}
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "start", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	if code, out, stderr := proxyCLI(t, m, r, "", "proxy", "status", "--json"); code != 0 || !strings.Contains(out, `"state":"running"`) {
		t.Fatalf("status %d %s %s", code, out, stderr)
	}
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "update", "--check", "--dry-run"); code != 0 {
		t.Fatal(stderr)
	}
	if code, _, _ := proxyCLI(t, m, r, "", "proxy", "update", "--artifact", artifact, "--yes"); code == 0 {
		t.Fatal("running update accepted")
	}
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "restart", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	if r.starts != 2 || r.stops != 1 {
		t.Fatalf("restart starts=%d stops=%d", r.starts, r.stops)
	}
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "stop", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	for _, args := range [][]string{{"proxy", "update", "--artifact", artifact, "--yes"}, {"proxy", "remove", "--yes"}} {
		if code, _, stderr := proxyCLI(t, m, r, "", args...); code != 0 {
			t.Fatal(stderr)
		}
	}
	got, err := os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("configuration changed on update/remove")
	}
	if r.starts != 2 {
		t.Fatal("update/remove started a component")
	}
}

func TestProxyCLIReadOnlyAndOptionValidation(t *testing.T) {
	m, _ := fixture(t)
	r := &cliProxyRunner{}
	for _, args := range [][]string{{"proxy", "status", "--json"}, {"proxy", "config-path"}, {"proxy", "install", "--dry-run", "--json"}} {
		if code, _, stderr := proxyCLI(t, m, r, "", args...); code != 0 {
			t.Fatalf("%v %s", args, stderr)
		}
	}
	if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
		t.Fatal("read-only command created root")
	}
	if len(r.commands) != 0 || r.starts != 0 {
		t.Fatal("read-only command invoked a component")
	}
	for _, args := range [][]string{
		{"proxy", "status", "--artifact", "/unused"}, {"proxy", "endpoint", "--file", "/unused"}, {"proxy", "stop", "--check"},
		{"proxy", "validate", "--proxy", "http://127.0.0.1:9"}, {"codex", "--file", "/unused"}, {"proxy", "probe"},
		{"proxy", "logs", "--tail", "invalid"}, {"proxy", "update", "--check", "--artifact", "/unused"},
	} {
		if code, _, _ := proxyCLI(t, m, r, "", args...); code == 0 {
			t.Fatalf("ignored options: %v", args)
		}
	}
}

func TestProxyCLIValidationFailureAndVisibleInput(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{ready: true}
	artifact := proxyCLIArtifact(t)
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "install", "--artifact", artifact, "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	secret := "https://subscription.invalid/provider?token=never-print"
	code, out, stderr := proxyCLI(t, m, r, secret+"\n.*\n\nn\nn\n", "proxy", "configure", "--json")
	if code != 0 || !json.Valid([]byte(out)) || !strings.Contains(stderr, "never-print") {
		t.Fatalf("configure %d %s %s", code, out, stderr)
	}
	t.Setenv("CLASH_CONFIG_STRING", "do-not-validate-this")
	t.Setenv("SAFE_PATHS", home)
	r.validationError = true
	code, _, stderr = proxyCLI(t, m, r, "", "proxy", "validate", "--json")
	if code == 0 || !json.Valid([]byte(stderr)) || strings.Contains(stderr, "secret provider") {
		t.Fatalf("unsafe validation result: %d %s", code, stderr)
	}
	for _, command := range r.commands {
		for _, value := range command.Env {
			if strings.HasPrefix(value, "CLASH_") || strings.HasPrefix(value, "SAFE_PATHS=") {
				t.Fatal("core inherited override")
			}
		}
	}
	if r.starts != 0 {
		t.Fatal("configure or validation started a service")
	}
}

func TestCodexBuiltinUsesNativeListenerWithoutStartingProxy(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, "external-codex")
	if err := os.Mkdir(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, ".env"), []byte("KEEP=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureCodex(func(_ codex.Config) (codex.Config, error) { return m.Codex.Associate(codexHome) }); err != nil {
		t.Fatal(err)
	}
	proxyDir := filepath.Join(m.Root, "proxy")
	if err := os.Mkdir(proxyDir, 0700); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{}
	for _, port := range []int{17890, 17891} {
		config := fmt.Sprintf("listeners:\n  - name: common\n    type: mixed\n    listen: 127.0.0.1\n    port: %d\n", port)
		if err := os.WriteFile(filepath.Join(proxyDir, "config.yaml"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		code, out, stderr := proxyCLI(t, m, runner, "", "codex", "proxy", "builtin", "--yes", "--json")
		if code != 0 || !json.Valid([]byte(out)) || !strings.Contains(stderr, "尚未运行") {
			t.Fatalf("binding=%d %s %s", code, out, stderr)
		}
		data, err := os.ReadFile(filepath.Join(codexHome, ".env"))
		if err != nil || !strings.Contains(string(data), fmt.Sprintf("http://127.0.0.1:%d", port)) || !strings.Contains(string(data), "KEEP=yes\n") {
			t.Fatalf("env=%s err=%v", data, err)
		}
	}
	if runner.starts != 0 || len(runner.commands) != 0 {
		t.Fatal("builtin binding invoked a component")
	}
}

func TestProxyCLIConfigureQueryWhileRunningAndPrivateSourcePolicy(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{ready: true}
	source := filepath.Join(home, "input.yaml")
	want := []byte("mixed-port: 17890\n")
	if err := os.WriteFile(source, want, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"proxy", "install", "--artifact", proxyCLIArtifact(t), "--yes"},
		{"proxy", "configure", "--file", source, "--yes"},
		{"proxy", "start", "--yes"},
	} {
		if code, _, stderr := proxyCLI(t, m, runner, "", args...); code != 0 {
			t.Fatal(stderr)
		}
	}
	commands := len(runner.commands)
	for _, extra := range [][]string{{"--json"}, {"--dry-run", "--json"}} {
		args := append([]string{"proxy", "configure"}, extra...)
		if code, out, stderr := proxyCLI(t, m, runner, "", args...); code != 0 || !strings.Contains(out, "已有配置") {
			t.Fatalf("query=%d %s %s", code, out, stderr)
		}
	}
	if runner.starts != 1 || runner.stops != 0 || len(runner.commands) != commands {
		t.Fatal("configuration query changed the running component")
	}
	if got, err := os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml")); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("query changed config: %s %v", got, err)
	}
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "stop", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	if err := os.Chmod(source, 0644); err != nil {
		t.Fatal(err)
	}
	commands = len(runner.commands)
	for _, extra := range [][]string{{"--yes"}, {"--dry-run"}} {
		args := append([]string{"proxy", "configure", "--file", source}, extra...)
		if code, _, _ := proxyCLI(t, m, runner, "", args...); code == 0 {
			t.Fatal("non-private source accepted")
		}
	}
	if len(runner.commands) != commands {
		t.Fatal("rejected source reached core validation")
	}
	if got, err := os.ReadFile(filepath.Join(m.Root, "proxy", "config.yaml")); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("rejected source changed config: %s %v", got, err)
	}
}

func TestProxyCLIProbeAuthenticationPreviewMatchesExecution(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(m.Root, "proxy")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("mixed-port: 17890\nauthentication: ['user:never-print']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{}
	for _, extra := range [][]string{{"--json"}, {"--dry-run", "--json"}} {
		args := append([]string{"proxy", "probe", "--url", "https://target.invalid/"}, extra...)
		code, _, stderr := proxyCLI(t, m, runner, "", args...)
		if code == 0 || !strings.Contains(stderr, "认证") || strings.Contains(stderr, "never-print") {
			t.Fatalf("probe=%d %s", code, stderr)
		}
	}
	if len(runner.commands) != 0 || runner.starts != 0 {
		t.Fatal("authenticated probe invoked core")
	}
}

type beforeRead struct {
	reader io.Reader
	before func()
}

func (r *beforeRead) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.reader.Read(p)
}

func TestCodexBuiltinRejectsEndpointChangeDuringConfirmation(t *testing.T) {
	for _, replacement := range []string{
		"listeners: [{name: any-name, type: mixed, listen: 127.0.0.1, port: 17891}]\n",
		"listeners: [{name: any-name, type: mixed, listen: 127.0.0.1, port: 17890}]\nauthentication: ['user:never-print']\n",
	} {
		t.Run(fmt.Sprintf("config-%d", len(replacement)), func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			codexHome := filepath.Join(home, "codex")
			if err := os.Mkdir(codexHome, 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("KEEP=yes\n")
			for _, name := range []string{".env", "auth.json", "sessions.fixture"} {
				if err := os.WriteFile(filepath.Join(codexHome, name), original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(codexHome) }); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(m.Root, "config.toml")
			beforeConfig, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(m.Root, "proxy")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("listeners: [{name: any-name, type: mixed, listen: 127.0.0.1, port: 17890}]\n"), 0600); err != nil {
				t.Fatal(err)
			}
			runner := &cliProxyRunner{}
			var out, stderr bytes.Buffer
			reader := &beforeRead{reader: strings.NewReader("any-name\ny\n"), before: func() {
				if err := os.WriteFile(path, []byte(replacement), 0600); err != nil {
					t.Fatal(err)
				}
			}}
			cli := CLI{In: reader, Out: &out, Err: &stderr, ProxyRunner: runner}
			code := cli.Run([]string{"--root", m.Root, "codex", "proxy", "builtin"})
			if code == 0 || !strings.Contains(stderr.String(), "发生变化") {
				t.Fatalf("stale binding=%d %s %s", code, out.String(), stderr.String())
			}
			for _, name := range []string{".env", "auth.json", "sessions.fixture"} {
				data, err := os.ReadFile(filepath.Join(codexHome, name))
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("changed %s: %s %v", name, data, err)
				}
			}
			if data, err := os.ReadFile(configPath); err != nil || !bytes.Equal(data, beforeConfig) {
				t.Fatalf("changed receipt: %s %v", data, err)
			}
			if len(runner.commands) != 0 || runner.starts != 0 {
				t.Fatal("binding invoked core")
			}
		})
	}
}

func TestProxyCLIStopPreviewAllowsChangedDiskProgram(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{ready: true}
	source := filepath.Join(home, "input.yaml")
	if err := os.WriteFile(source, []byte("mixed-port: 17890\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"proxy", "install", "--artifact", proxyCLIArtifact(t), "--yes"},
		{"proxy", "configure", "--file", source, "--yes"},
		{"proxy", "start", "--yes"},
	} {
		if code, _, stderr := proxyCLI(t, m, runner, "", args...); code != 0 {
			t.Fatal(stderr)
		}
	}
	program := filepath.Join(m.Root, "proxy", "mihomo")
	if err := os.WriteFile(program, []byte("changed disk bytes; fake runner retains exact birth\n"), 0700); err != nil {
		t.Fatal(err)
	}
	commands := len(runner.commands)
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "stop", "--dry-run", "--json"); code != 0 {
		t.Fatalf("stop preview rejected recoverable instance: %s", stderr)
	}
	if runner.stops != 0 || len(runner.commands) != commands {
		t.Fatal("stop preview changed the process or invoked core")
	}
	if code, _, stderr := proxyCLI(t, m, runner, "", "proxy", "stop", "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	if runner.stops != 1 || len(runner.commands) != commands {
		t.Fatal("stop did not target only the recorded birth")
	}
}
