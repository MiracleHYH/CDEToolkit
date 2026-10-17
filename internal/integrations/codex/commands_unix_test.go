//go:build linux || darwin

package codex

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		fmt.Fprintln(os.Stdout, "ready")
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
