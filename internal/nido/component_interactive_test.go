package nido

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func componentInteractiveCLI(t *testing.T, m *Manager, input string, args ...string) (int, string, string, *cliProxyRunner) {
	t.Helper()
	var out, stderr bytes.Buffer
	r := &cliProxyRunner{}
	reader := strings.NewReader(input)
	c := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr, ProxyRunner: r,
		Runner: func(string, []string, []string, []byte) ([]byte, error) {
			t.Fatal("configuration invoked the official installer or Codex")
			return nil, nil
		}}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String(), r
}

func TestComponentInteractiveKeepAndCancelAreReadOnly(t *testing.T) {
	for _, args := range [][]string{{"codex", "configure"}, {"proxy", "configure"}, {"download", "configure"}, {"download", "set"}, {"proxy", "install"}, {"proxy", "update"}} {
		for _, input := range []string{"\n", "q\n", "", "1"} {
			t.Run(strings.Join(args, "-")+"/"+fmt.Sprintf("%q", input), func(t *testing.T) {
				m, codexHome, _ := proxyBindingFixture(t)
				writeBindingListeners(t, m, bindingListeners)
				before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
				code, _, stderr, r := componentInteractiveCLI(t, m, input, args...)
				if code != 0 && code != 1 {
					t.Fatal(code, stderr)
				}
				if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 || r.starts != 0 || r.stops != 0 {
					t.Fatal("keep/cancel changed files or invoked a core", codexHome)
				}
			})
		}
	}
}

func TestComponentInteractiveRejectsInvalidFlagsBeforeReading(t *testing.T) {
	for _, args := range [][]string{
		{"proxy", "install", "--proxy", "bad"},
	} {
		t.Run(strings.Join(args[:2], "-")+args[2], func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			reader := &apiGateReader{}
			var out, stderr bytes.Buffer
			c := CLI{In: reader, Out: &out, Err: &stderr}
			if code := c.Run(append([]string{"--root", m.Root}, args...)); code == 0 || reader.reads != 0 {
				t.Fatal("invalid explicit flag was swallowed by a menu", code, reader.reads, stderr.String())
			}
		})
	}
}

func TestComponentInteractiveEmptyValuesDoNotChooseAnotherAction(t *testing.T) {
	for _, test := range []struct {
		args  []string
		input string
	}{
		{[]string{"proxy", "configure"}, "2\n\n"},
		{[]string{"download", "configure"}, "3\n\n0\n"},
	} {
		t.Run(strings.Join(test.args, "-")+test.input[:1], func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, bindingListeners)
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			code, _, stderr, r := componentInteractiveCLI(t, m, test.input, test.args...)
			if strings.Contains(stderr, "订阅 URL") || strings.Contains(stderr, "是否继续") {
				t.Fatal("empty setting was confirmed or entered another operation", code, stderr)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 {
				t.Fatal("empty setting wrote configuration or executed a program")
			}
		})
	}
}

func TestComponentInteractiveMissingReferencedProxyFailsBeforeConfirmation(t *testing.T) {
	for _, component := range []string{"proxy"} {
		t.Run(component, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			code, _, stderr, r := componentInteractiveCLI(t, m, "y\n", component, "install", "--github-accelerate", "proxy")
			if code == 0 || !strings.Contains(stderr, "先配置 Proxy") || strings.Contains(stderr, "是否继续") {
				t.Fatal("missing referenced Proxy was not rejected before confirmation", code, stderr)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 {
				t.Fatal("invalid route changed data or invoked a core")
			}
		})
	}
}

func TestComponentInteractiveLocalProxyReusesVerifiedInstall(t *testing.T) {
	for _, checksumOK := range []bool{false, true} {
		t.Run(fmt.Sprint(checksumOK), func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, bindingListeners) // Do not enter the independent first-config wizard.
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			artifact := proxyCLIArtifact(t)
			data, err := os.ReadFile(artifact)
			if err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(data))
			if !checksumOK {
				digest = strings.Repeat("0", 64)
			}
			code, _, stderr, r := componentInteractiveCLI(t, m, "y\n", "proxy", "install", "--artifact", artifact, "--sha256", digest)
			if (code == 0) != checksumOK || r.starts != 0 || r.stops != 0 {
				t.Fatal(code, stderr)
			}
			if checksumOK {
				got, err := os.ReadFile(filepath.Join(m.Root, "proxy", "mihomo"))
				if err != nil || !bytes.Equal(got, data) || len(r.commands) != 2 || r.commands[0].Args[0] != "-v" || r.commands[1].Args[0] != "-t" {
					t.Fatal("menu did not use existing verified candidate installation", err, len(r.commands))
				}
			} else if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 {
				t.Fatal("bad local digest changed files or invoked a core")
			}
		})
	}
}

func TestComponentInteractiveLocalSourceRejectsIgnoredGitHubFlag(t *testing.T) {
	for _, component := range []string{"proxy"} {
		t.Run(component, func(t *testing.T) {
			m, _ := fixture(t)
			code, _, stderr, _ := componentInteractiveCLI(t, m, "y\n", component, "install", "--artifact", "/tmp/program", "--sha256", strings.Repeat("0", 64), "--github-proxy", "direct")
			if code == 0 || !strings.Contains(stderr, "--github-proxy 仅用于首次在线安装") {
				t.Fatal(code, stderr)
			}
			if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
				t.Fatal("invalid option initialized a root")
			}
		})
	}
}

func TestComponentInteractiveLocalInstallFinalDeclineIsReadOnly(t *testing.T) {
	m, _, _ := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
	artifact := proxyCLIArtifact(t)
	data, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr, r := componentInteractiveCLI(t, m, "n\n", "proxy", "install", "--artifact", artifact, "--sha256", fmt.Sprintf("%x", sha256.Sum256(data)))
	if code == 0 || !strings.Contains(stderr, "操作已取消") || len(r.commands) != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
		t.Fatal("declined local install changed state", code, stderr)
	}
}

func TestComponentInteractiveNetworkChoiceKeepsExternalToolsIndependent(t *testing.T) {
	m, codexHome, _ := proxyBindingFixture(t)
	before := proxyBootstrapSnapshot(t, "", codexHome)
	code, _, stderr, r := componentInteractiveCLI(t, m, "1\ny\n0\n", "download", "configure")
	if code != 0 || len(r.commands) != 0 {
		t.Fatal(code, stderr)
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Download.GitHubAccelerate != GitHubAccelerateNone || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", codexHome)) {
		t.Fatal("download menu changed unrelated proxy or failed to save", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, apiProxyFile)); !os.IsNotExist(err) {
		t.Fatal("new network configuration wrote a legacy sidecar")
	}
}

func TestComponentInteractiveCodexDirectoryAndProxyKeepExternalData(t *testing.T) {
	m, codexHome, _ := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	before := proxyBootstrapSnapshot(t, "", codexHome)
	code, _, stderr, _ := componentInteractiveCLI(t, m, "\n", "codex", "setup")
	if code != 0 || strings.Contains(stderr, "是否继续") {
		t.Fatal("unchanged linked HOME prompted a write", code, stderr)
	}
	code, _, stderr, r := componentInteractiveCLI(t, m, "3\nhttp://127.0.0.1:17999\ny\n", "codex", "proxy")
	if code != 0 || len(r.commands) != 0 {
		t.Fatal(code, stderr)
	}
	after := proxyBootstrapSnapshot(t, "", codexHome)
	for path, entry := range before {
		if filepath.Base(path) != ".env" && !reflect.DeepEqual(entry, after[path]) {
			t.Fatal("proxy choice changed external data", path)
		}
	}
	if data, err := os.ReadFile(filepath.Join(codexHome, ".env")); err != nil || !bytes.Contains(data, []byte("http://127.0.0.1:17999")) || !bytes.Contains(data, []byte("KEEP=literal")) {
		t.Fatal("existing proxy writer was not reused", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, apiProxyFile)); !os.IsNotExist(err) {
		t.Fatal("Codex proxy choice enabled Nido API")
	}
}

func TestComponentInteractiveCodexInstallCancellationDoesNotRunScript(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "external codex")
	before := proxyBootstrapSnapshot(t, "", m.Root, home)
	code, _, stderr, _ := componentInteractiveCLI(t, m, target+"\nn\n", "codex", "install")
	if code == 0 || !strings.Contains(stderr, target) || !strings.Contains(stderr, "操作已取消") {
		t.Fatal(code, stderr)
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, home)) {
		t.Fatal("cancelled official installer created external directories")
	}
}

func TestComponentInteractiveScriptModesDoNotReadMissingChoices(t *testing.T) {
	for _, args := range [][]string{{"codex", "configure"}, {"codex", "setup"}, {"codex", "install"}, {"proxy", "configure"}, {"download", "configure"}, {"download", "set"}} {
		for _, mode := range []string{"--json", "--yes", "--dry-run", ""} {
			t.Run(strings.Join(args, "-")+mode, func(t *testing.T) {
				m, _ := fixture(t)
				if err := m.Init(); err != nil {
					t.Fatal(err)
				}
				before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
				reader := &apiGateReader{}
				var out, stderr bytes.Buffer
				c := CLI{In: reader, Out: &out, Err: &stderr}
				full := append([]string{"--root", m.Root}, args...)
				if mode != "" {
					full = append(full, mode)
				}
				_ = c.Run(full)
				if reader.reads != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
					t.Fatal("script mode read missing choices or changed files", stderr.String())
				}
			})
		}
	}
}
