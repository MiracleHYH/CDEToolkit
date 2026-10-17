package cdet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUpstreamCodexDelegatesUntouchedScriptWithAnIsolatedInstallerHome(t *testing.T) {
	m := networkTestManager(t)
	originalHome := t.TempDir()
	write(t, filepath.Join(originalHome, ".profile"), "ORIGINAL-USER-PROFILE", 0600)
	t.Setenv("HOME", originalHome)
	t.Setenv("CODEX_HOME", filepath.Join(originalHome, ".codex"))
	t.Setenv("CODEX_INSTALL_DAEMON_ONLY", "1")
	t.Setenv("CODEX_API_KEY", "PRIVATE-CODEX-SECRET")
	t.Setenv("HTTPS_PROXY", "http://user:PRIVATE-PROXY-SECRET@127.0.0.1:7890")
	t.Setenv("SSL_CERT_FILE", "/isolated-test/ca.pem")
	script := []byte("#!/bin/sh\n# ISOLATED-INSTALLER-SENTINEL\n")
	var calls []CommandSpec
	temporary := ""
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		calls = append(calls, spec)
		temporary = envValue(spec.Env, "HOME")
		if !within(filepath.Join(m.Store, ".cdet"), temporary) || temporary == originalHome || envValue(spec.Env, "TMPDIR") != temporary {
			t.Fatal("upstream installer could write to the user's HOME or temp directory")
		}
		info, err := os.Stat(temporary)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("installer HOME is not private", err)
		}
		if envValue(spec.Env, "CODEX_HOME") != filepath.Join(m.Store, dataPath("codex")) || envValue(spec.Env, "CODEX_INSTALL_DIR") != filepath.Join(m.Store, ".cdet", "bin") || envValue(spec.Env, "CODEX_NON_INTERACTIVE") != "1" || envValue(spec.Env, "CODEX_INSTALL_DAEMON_ONLY") != "" || envValue(spec.Env, "CODEX_API_KEY") != "" {
			t.Fatal("installer inherited stale Codex settings or missing public variables")
		}
		if envValue(spec.Env, "https_proxy") != os.Getenv("HTTPS_PROXY") || envValue(spec.Env, "SSL_CERT_FILE") != os.Getenv("SSL_CERT_FILE") || !strings.HasPrefix(envValue(spec.Env, "PATH"), filepath.Join(m.Store, ".cdet", "bin")+string(os.PathListSeparator)) {
			t.Fatal("installer network, CA, or PATH environment was lost")
		}
		if spec.Path == "curl" {
			if spec.Args[len(spec.Args)-1] != codexInstallerURL || len(spec.Input) != 0 {
				t.Fatal("fetch did not use the official installer URL")
			}
			return script, nil
		}
		if spec.Path != "/bin/sh" || !reflect.DeepEqual(spec.Args, []string{"-s"}) || string(spec.Input) != string(script) {
			t.Fatal("the official script was patched or substituted")
		}
		// Simulate the upstream installer's unconditional profile management.
		write(t, filepath.Join(temporary, ".profile"), "SIMULATED-INSTALLER-PROFILE", 0600)
		return nil, nil
	}
	if err := m.installCodex(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !reflect.DeepEqual(calls[0].Env, calls[1].Env) || !absent(temporary) || os.Getenv("HOME") != originalHome || read(t, filepath.Join(originalHome, ".profile")) != "ORIGINAL-USER-PROFILE" {
		t.Fatal("installer commands used different environments, changed parent HOME, or leaked scratch")
	}
}

func TestUpstreamCodexNeverExecutesAFailedEmptyOrOversizedDownload(t *testing.T) {
	for _, scenario := range []string{"failure", "empty", "oversized", "shell-failure", "dry-run"} {
		t.Run(scenario, func(t *testing.T) {
			m := networkTestManager(t)
			m.Options.DryRun = scenario == "dry-run"
			calls, shellCalls := 0, 0
			temporary := ""
			m.Runner = func(spec CommandSpec) ([]byte, error) {
				calls++
				temporary = envValue(spec.Env, "HOME")
				if spec.Path == "/bin/sh" {
					shellCalls++
					return nil, errors.New("PRIVATE-COMMAND-SECRET")
				}
				switch scenario {
				case "failure":
					return []byte("partial executable script"), errors.New("PRIVATE-COMMAND-SECRET")
				case "empty":
					return []byte(" \n\t"), nil
				case "oversized":
					return make([]byte, maxCommandOutput+1), nil
				}
				return []byte("# FAKE-NONEMPTY-INSTALLER"), nil
			}
			err := m.installCodex()
			if err == nil || strings.Contains(err.Error(), "PRIVATE-COMMAND-SECRET") {
				t.Fatal("installer failure was accepted or credentials leaked", err)
			}
			if scenario == "shell-failure" {
				if shellCalls != 1 {
					t.Fatal("valid downloaded script was not delegated")
				}
			} else if shellCalls != 0 {
				t.Fatal("failed download or preview executed a script")
			}
			if scenario == "dry-run" && calls != 0 || temporary != "" && !absent(temporary) {
				t.Fatal("preview ran a command or installer failure leaked temporary data")
			}
		})
	}
}

func TestCommandRunnerResolvesItsOwnPATHAndBoundsOutputAndTime(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "fixture-command")
	write(t, script, "#!/bin/sh\nprintf 'OPERATION-PATH'\n", 0700)
	out, err := runCommand(CommandSpec{Path: "fixture-command", Env: []string{"PATH=" + root}})
	if err != nil || string(out) != "OPERATION-PATH" {
		t.Fatal("runner resolved the parent PATH rather than operation PATH", err)
	}
	if _, err := runCommandTimeout(CommandSpec{Path: "/bin/sh", Args: []string{"-s"}, Env: os.Environ(), Input: []byte("while :; do :; done\n")}, 30*time.Millisecond); err == nil {
		t.Fatal("runner did not terminate its isolated timed-out process")
	}
	if _, err := runCommand(CommandSpec{Path: "/bin/sh", Args: []string{"-s"}, Env: os.Environ(), Input: []byte("while :; do printf 'abcdefghijklmnopqrstuvwxyz0123456789'; done\n")}); err == nil {
		t.Fatal("runner accepted unbounded output")
	}
}
