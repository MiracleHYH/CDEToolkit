//go:build linux || darwin

package cdet

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This is only a synthetic test process. No Codex/Proxy executable or service
// is installed or started by these tests.
func TestInactiveSyntheticChild(t *testing.T) {
	if os.Getenv("CDET_INACTIVE_HELPER") != "1" {
		return
	}
	root, mode := os.Getenv("CDET_INACTIVE_ROOT"), os.Getenv("CDET_INACTIVE_MODE")
	switch mode {
	case "read-fd", "write-fd", "mapping":
		flags := os.O_RDONLY
		if mode != "read-fd" {
			flags = os.O_RDWR
		}
		f, e := os.OpenFile(filepath.Join(root, "payload"), flags, 0)
		if e != nil {
			t.Fatal(e)
		}
		if mode == "mapping" {
			mapped, e := syscall.Mmap(int(f.Fd()), 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
			if e != nil {
				t.Fatal(e)
			}
			defer syscall.Munmap(mapped)
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
		} else {
			defer f.Close()
		}
	}
	fmt.Fprintln(os.Stdout, "READY")
	for {
		time.Sleep(time.Second)
	}
}

func TestInactiveMatcherKeepsDirectoryBoundariesWhenNormalizingPaths(t *testing.T) {
	m, err := inactivePaths([]string{"/cdet-fixture/codex"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/cdet-fixture/codex", "/cdet-fixture/codex/sessions/one", "/cdet-fixture/codex/sessions/../state.sqlite", "/cdet-fixture/other/../codex/state.sqlite", "/cdet-fixture/codex/state.sqlite (deleted)"} {
		if !m.matches(path) {
			t.Fatal("component path was missed", path)
		}
	}
	for _, path := range []string{"/cdet-fixture/codex-other", "/cdet-fixture/codex-other/state.sqlite", "/cdet-fixture/codex/../other/state.sqlite", "codex/state.sqlite"} {
		if m.matches(path) {
			t.Fatal("unrelated or escaping path matched the component", path)
		}
	}
}

func startInactiveChild(t *testing.T, root, mode string) *exec.Cmd {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestInactiveSyntheticChild$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CODEX_HOME=") && !strings.HasPrefix(entry, "CODEX_SQLITE_HOME=") && !strings.HasPrefix(entry, "CDET_INACTIVE_") && !(mode == "default-codex" && strings.HasPrefix(entry, "HOME=")) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CDET_INACTIVE_HELPER=1", "CDET_INACTIVE_ROOT="+root, "CDET_INACTIVE_MODE="+mode)
	switch mode {
	case "env-daemon":
		cmd.Env = append(cmd.Env, "CODEX_HOME="+root)
		cmd.Args = append(cmd.Args, "daemon")
	case "sqlite-env":
		cmd.Env = append(cmd.Env, "CODEX_SQLITE_HOME="+root)
	case "default-codex":
		cmd.Args[0] = "codex"
		cmd.Args = append(cmd.Args, "app-server")
		cmd.Env = append(cmd.Env, "HOME="+filepath.Dir(root))
	case "cwd":
		cmd.Dir = root
	case "launcher":
		cmd.Args[0] = filepath.Join(root, "codex")
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // only the synthetic child created by this test
		_ = cmd.Wait()
	})
	ready := make(chan error, 1)
	go func() {
		line, e := bufio.NewReader(stdout).ReadString('\n')
		if e == nil && line != "READY\n" {
			e = fmt.Errorf("unexpected readiness message")
		}
		ready <- e
	}()
	select {
	case e = <-ready:
		if e != nil {
			t.Fatal("synthetic child not ready", e, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("synthetic child readiness timed out")
	}
	return cmd
}

func TestInactiveDetectsSyntheticDaemonAndDataUsers(t *testing.T) {
	for _, mode := range []string{"env-daemon", "sqlite-env", "default-codex", "cwd", "launcher", "read-fd", "write-fd", "mapping"} {
		t.Run(mode, func(t *testing.T) {
			base, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			root := filepath.Join(base, ".codex")
			write(t, filepath.Join(root, "payload"), strings.Repeat("x", 4096), 0600)
			cmd := startInactiveChild(t, root, mode)
			e = checkInactive([]string{root})
			if e == nil || !strings.Contains(e.Error(), strconv.Itoa(cmd.Process.Pid)) || !strings.Contains(e.Error(), "daemon/app-server") {
				t.Fatal("active component not identified", mode, e)
			}
			if e = cmd.Process.Signal(syscall.Signal(0)); e != nil {
				t.Fatal("detector stopped the synthetic process", e)
			}
		})
	}
}

func TestInactiveIgnoresCurrentProcessAndUnrelatedComponents(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root, other := filepath.Join(base, "managed"), filepath.Join(base, "unrelated")
	write(t, filepath.Join(root, "payload"), "KEEP", 0600)
	write(t, filepath.Join(other, "payload"), "OTHER", 0600)
	file, e := os.Open(filepath.Join(root, "payload"))
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	t.Setenv("CODEX_HOME", root)
	startInactiveChild(t, other, "env-daemon")
	if e = checkInactive([]string{root}); e != nil {
		t.Fatal("current cdet or unrelated daemon blocked operation", e)
	}
}

func TestInactivePathAndEnvironmentMatchingIsBounded(t *testing.T) {
	m, e := inactivePaths([]string{"/tmp/codex-test"})
	if e != nil {
		t.Fatal(e)
	}
	if m.matches("/tmp/codex-test-other") || m.process([]string{"/bin/zsh"}, []string{"CODEX_HOME=/tmp/codex-test"}, "/tmp") {
		t.Fatal("unrelated prefix or invoking shell was matched")
	}
	if !m.process([]string{"codex", "daemon"}, []string{"HOME=/tmp", "CODEX_HOME=/tmp/codex-test"}, "/tmp") || !m.process([]string{"node", "/opt/codex.js", "app-server"}, []string{"CODEX_HOME=codex-test"}, "/tmp") {
		t.Fatal("daemon environment was not matched")
	}
	if _, e = inactivePaths([]string{"relative/path"}); e == nil {
		t.Fatal("uncheckable relative path accepted")
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root, alias := filepath.Join(base, "real-home"), filepath.Join(base, "home-alias")
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(root, alias); e != nil {
		t.Fatal(e)
	}
	m, e = inactivePaths([]string{root})
	if e != nil || !m.process([]string{"codex", "daemon"}, []string{"CODEX_HOME=" + alias}, base) {
		t.Fatal("daemon data-home alias was missed", e)
	}
}

func TestInactiveAllowsAbsentOptionalSourcesWithoutCreatingPaths(t *testing.T) {
	if e := checkInactive([]string{"", ""}); e != nil {
		t.Fatal("empty optional source blocked fresh installation", e)
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	missing := filepath.Join(base, "unknown-home", "missing-data")
	if e = checkInactive([]string{"", missing}); e != nil {
		t.Fatal("unused missing data root blocked operation", e)
	}
	if !absent(filepath.Join(base, "unknown-home")) {
		t.Fatal("inactive detection created missing HOME or data")
	}
}

func TestInactiveRuntimeUsesFlatHomesWithoutChangingData(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex", "proxy")
	m := f.manager(t, Options{})
	before := tree(t, f.store).RawDigest
	path, _, env, e := m.Command("codex", nil)
	if e != nil || path != filepath.Join(f.store, programPath("codex")) || !contains(env, "CODEX_HOME="+filepath.Join(f.store, "codex")) || !contains(env, "CODEX_SQLITE_HOME="+filepath.Join(f.store, "codex")) {
		t.Fatal("Codex runtime did not use flat managed home", path, e)
	}
	_, args, _, e := m.Command("proxy", nil)
	if e != nil || len(args) != 4 || args[1] != filepath.Join(f.store, "proxy") || args[3] != filepath.Join(f.store, "proxy", "config.yaml") {
		t.Fatal("Proxy runtime did not use flat managed home", args, e)
	}
	if tree(t, f.store).RawDigest != before {
		t.Fatal("runtime construction changed configuration, auth, sessions or databases")
	}
}
