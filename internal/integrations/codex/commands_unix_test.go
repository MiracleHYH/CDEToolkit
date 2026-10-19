//go:build linux || darwin

package codex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHelperCommand(t *testing.T) {
	if os.Getenv("NIDO_TEST_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "streams":
		fmt.Fprint(os.Stdout, "downloaded-script\n")
		fmt.Fprint(os.Stderr, "diagnostic-secret\n")
	case "stdout-full":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), maxCommandOutput+1))
	case "stderr-full":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), maxCommandOutput+1))
	case "child":
		time.Sleep(700 * time.Millisecond)
		_ = os.WriteFile(args[2], []byte("leaked"), 0600)
	case "tree":
		child := exec.Command(os.Args[0], "-test.run=TestHelperCommand", "--", "child", args[2])
		child.Env = os.Environ()
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if child.Start() != nil {
			os.Exit(3)
		}
		if len(args) > 3 {
			_ = os.WriteFile(args[3], []byte("ready"), 0600)
		}
		fmt.Fprintln(os.Stdout, "ready")
		time.Sleep(10 * time.Second)
	case "survivor":
		_ = os.WriteFile(args[2], []byte("ready"), 0600)
		time.Sleep(10 * time.Second)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCommandSeparatesDiagnosticsAndBoundsBothStreams(t *testing.T) {
	env := append(os.Environ(), "NIDO_TEST_HELPER=1")
	args := []string{"-test.run=TestHelperCommand", "--", "streams"}
	out, err := runCommandTimeout(os.Args[0], args, env, nil, 5*time.Second)
	if err != nil || string(out) != "downloaded-script\n" {
		t.Fatalf("%q %v", out, err)
	}
	for _, mode := range []string{"stdout-full", "stderr-full"} {
		out, err = runCommandTimeout(os.Args[0], []string{"-test.run=TestHelperCommand", "--", mode}, env, nil, 5*time.Second)
		if err == nil || len(out) != 0 || strings.Contains(err.Error(), "diagnostic-secret") {
			t.Fatalf("unbounded %s: %d %v", mode, len(out), err)
		}
	}
}

func TestCommandTimeoutStopsItsOwnChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-outlived-timeout")
	env := append(os.Environ(), "NIDO_TEST_HELPER=1")
	start := time.Now()
	_, err := runCommandTimeout(os.Args[0], []string{"-test.run=TestHelperCommand", "--", "tree", marker}, env, nil, 200*time.Millisecond)
	if err == nil || time.Since(start) > 4*time.Second {
		t.Fatalf("timeout not enforced: %v", err)
	}
	time.Sleep(900 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("operation child survived cancellation")
	}
}

func waitCommandFixtureFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fixture did not become ready: %s", path)
}

func TestOperationContextCancelsOnlyItsCurlOrShellProcessTree(t *testing.T) {
	for _, operation := range []string{"curl", "shell"} {
		t.Run(operation, func(t *testing.T) {
			s, _ := fixture(t)
			dir := t.TempDir()
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			env := append(os.Environ(), "NIDO_TEST_HELPER=1")
			// This process already exists before the operation starts. It is
			// intentionally in another process group and must remain alive.
			unrelatedReady := filepath.Join(dir, "existing-process-ready")
			unrelated := exec.Command(program, "-test.run=TestHelperCommand", "--", "survivor", unrelatedReady)
			unrelated.Env = env
			unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := unrelated.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = syscall.Kill(-unrelated.Process.Pid, syscall.SIGKILL); _ = unrelated.Wait() })
			waitCommandFixtureFile(t, unrelatedReady)

			leaked, ready := filepath.Join(dir, "operation-child-leaked"), filepath.Join(dir, "operation-ready")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.Ctx = ctx
			s.Env = env
			path, args, input := "/bin/sh", []string{"-s"}, []byte("\"$NIDO_CONTEXT_HELPER\" -test.run=TestHelperCommand -- tree \"$NIDO_CONTEXT_LEAK\" \"$NIDO_CONTEXT_READY\"\n")
			if operation == "curl" {
				if err := os.Symlink(program, filepath.Join(dir, "curl")); err != nil {
					t.Fatal(err)
				}
				path, args, input = filepath.Join(dir, "curl"), []string{"-test.run=TestHelperCommand", "--", "tree", leaked, ready}, nil
			} else {
				s.Env = append(s.Env, "NIDO_CONTEXT_HELPER="+program, "NIDO_CONTEXT_LEAK="+leaked, "NIDO_CONTEXT_READY="+ready)
			}
			finished := make(chan error, 1)
			go func() { _, err := s.run(path, args, s.Env, input); finished <- err }()
			waitCommandFixtureFile(t, ready)
			start := time.Now()
			cancel()
			select {
			case err := <-finished:
				if err == nil || time.Since(start) > 3*time.Second {
					t.Fatal("context did not cancel current operation promptly", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("context did not stop the operation")
			}
			if err := unrelated.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatal("operation cancellation touched an existing process", err)
			}
			time.Sleep(900 * time.Millisecond)
			if _, err := os.Stat(leaked); !os.IsNotExist(err) {
				t.Fatal("operation descendant survived context cancellation")
			}
		})
	}
}

func TestInstallUsesOperationPATHAndDoesNotExecuteCurlStderr(t *testing.T) {
	s, home := fixture(t)
	fakeBin := filepath.Join(s.UserHome, "test-bin")
	// This is a local mock downloader. It emits shell code only on stdout.
	script := `#!/bin/sh
printf '%s\n' '#!/bin/sh' 'mkdir -p "$CODEX_HOME/packages/standalone/mock/bin"' 'printf mock > "$CODEX_HOME/packages/standalone/mock/bin/codex"' 'chmod 700 "$CODEX_HOME/packages/standalone/mock/bin/codex"' 'ln -s "$CODEX_HOME/packages/standalone/mock/bin/codex" "$CODEX_INSTALL_DIR/codex"'
printf '%s\n' 'touch "$CODEX_HOME/diagnostic-executed"' >&2
`
	write(t, filepath.Join(fakeBin, "curl"), []byte(script), 0700)
	hostileBin := filepath.Join(s.UserHome, "parent-bin")
	write(t, filepath.Join(hostileBin, "curl"), []byte("#!/bin/sh\nexit 93\n"), 0700)
	t.Setenv("PATH", hostileBin)
	s.Env = []string{"PATH=" + fakeBin + ":/usr/bin:/bin"}
	c, err := s.Install(Config{Home: home}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Channel != "standalone" || string(read(t, c.Command)) != "mock" {
		t.Fatal("wrong mock installation result")
	}
	if _, err = os.Stat(filepath.Join(home, "diagnostic-executed")); !os.IsNotExist(err) {
		t.Fatal("curl diagnostics were executed")
	}
}

func TestInstallerSubprocessesKeepProxyTemporaryAndIgnoreRuntimeDotenv(t *testing.T) {
	for _, address := range []string{"", "http://127.0.0.1:17890"} {
		t.Run(map[bool]string{true: "direct", false: "proxy"}[address == ""], func(t *testing.T) {
			s, home := fixture(t)
			fakeBin := filepath.Join(s.UserHome, "test-bin")
			logPath := filepath.Join(s.UserHome, "operation-environment")
			original := []byte("https_proxy=${RUNTIME_PROXY}\nPRIVATE_TOKEN=preserve\n")
			write(t, filepath.Join(home, ".env"), original, 0600)
			// The mock downloader and its inert installer both run as actual
			// local child processes. They never contact any external service.
			script := `#!/bin/sh
printf '%s\n' "curl|${http_proxy-}|${HTTPS_PROXY-}|${all_proxy-}|${NO_PROXY-}" >> "$FIXTURE_LOG"
cat <<'INSTALLER'
#!/bin/sh
printf '%s\n' "sh|${http_proxy-}|${HTTPS_PROXY-}|${all_proxy-}|${NO_PROXY-}" >> "$FIXTURE_LOG"
mkdir -p "$CODEX_HOME/packages/standalone/mock/bin"
printf mock > "$CODEX_HOME/packages/standalone/mock/bin/codex"
chmod 700 "$CODEX_HOME/packages/standalone/mock/bin/codex"
ln -s "$CODEX_HOME/packages/standalone/mock/bin/codex" "$CODEX_INSTALL_DIR/codex"
INSTALLER
`
			write(t, filepath.Join(fakeBin, "curl"), []byte(script), 0700)
			s.Env = []string{"PATH=" + fakeBin + ":/usr/bin:/bin", "FIXTURE_LOG=" + logPath,
				"http_proxy=http://ambient.invalid:8080", "HTTPS_PROXY=http://ambient.invalid:8081",
				"all_proxy=socks5://ambient.invalid:8082", "NO_PROXY=*"}
			before := append([]string(nil), s.Env...)
			proxy := map[string]string{"http_proxy": address, "https_proxy": address, "all_proxy": address, "no_proxy": "localhost,127.0.0.1,::1"}
			if _, err := s.Install(Config{Home: home}, proxy); err != nil {
				t.Fatal(err)
			}
			want := "curl|" + address + "|" + address + "|" + address + "|localhost,127.0.0.1,::1\n" +
				"sh|" + address + "|" + address + "|" + address + "|localhost,127.0.0.1,::1\n"
			if string(read(t, logPath)) != want {
				t.Fatal("the downloader and installer did not share the selected temporary route")
			}
			if !reflect.DeepEqual(s.Env, before) || !bytes.Equal(read(t, filepath.Join(home, ".env")), original) {
				t.Fatal("installer changed the caller environment or Codex runtime dotenv")
			}
		})
	}
}

func TestInjectedRunnerCannotReturnUnboundedOrSensitiveErrors(t *testing.T) {
	s, _ := fixture(t)
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		return bytes.Repeat([]byte("x"), maxCommandOutput+1), nil
	}
	if out, err := s.run("curl", nil, s.Env, nil); err == nil || len(out) > 0 {
		t.Fatal("accepted unbounded mock output")
	}
	s.Run = func(string, []string, []string, []byte) ([]byte, error) {
		return nil, fmt.Errorf("proxy-user:proxy-secret")
	}
	if _, err := s.run("curl", nil, s.Env, nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("exposed runner error")
	}
}
