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

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

func componentInteractiveCLI(t *testing.T, m *Manager, input string, args ...string) (int, string, string, *cliProxyRunner) {
	t.Helper()
	var out, stderr bytes.Buffer
	r := &cliProxyRunner{}
	c := CLI{Interactive: true, In: strings.NewReader(input), Out: &out, Err: &stderr, ProxyRunner: r, GDSRunner: &apiGateGDSRunner{},
		Runner: func(string, []string, []string, []byte) ([]byte, error) {
			t.Fatal("configuration invoked the official installer or Codex")
			return nil, nil
		}}
	code := c.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String(), r
}

func TestComponentInteractiveKeepAndCancelAreReadOnly(t *testing.T) {
	for _, args := range [][]string{{"codex", "configure"}, {"proxy", "configure"}, {"gds", "configure"}, {"download", "configure"}, {"download", "set"}, {"proxy", "install"}, {"proxy", "update"}, {"gds", "install"}, {"gds", "update"}} {
		for _, input := range []string{"\n", "q\n", "", "1"} {
			t.Run(strings.Join(args, "-")+"/"+fmt.Sprintf("%q", input), func(t *testing.T) {
				m, codexHome, _ := proxyBindingFixture(t)
				writeBindingListeners(t, m, bindingListeners)
				before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
				code, _, stderr, r := componentInteractiveCLI(t, m, input, args...)
				if (code == 0) != (input == "\n") {
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
		{"proxy", "install", "--proxy", "bad"}, {"gds", "install", "--proxy", "bad"},
		{"gds", "configure", "--artifact", "/tmp/not-used"}, {"gds", "configure", "--sha256", "bad"},
		{"gds", "configure", "--github-proxy", "direct"}, {"gds", "configure", "--proxy", "http://127.0.0.1:7890"},
	} {
		t.Run(strings.Join(args[:2], "-")+args[2], func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			reader := &apiGateReader{}
			var out, stderr bytes.Buffer
			c := CLI{Interactive: true, In: reader, Out: &out, Err: &stderr}
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
		{[]string{"gds", "configure"}, "2\n\n"},
		{[]string{"gds", "configure"}, "3\n\n"},
		{[]string{"download", "configure"}, "6\n\n"},
		{[]string{"proxy", "install"}, "2\n\n"},
		{[]string{"gds", "install"}, "2\n\n"},
	} {
		t.Run(strings.Join(test.args, "-")+test.input[:1], func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, bindingListeners)
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			code, _, stderr, r := componentInteractiveCLI(t, m, test.input, test.args...)
			if code == 0 || strings.Contains(stderr, "订阅 URL") || strings.Contains(stderr, "是否继续") {
				t.Fatal("empty setting was confirmed or entered another operation", code, stderr)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 {
				t.Fatal("empty setting wrote configuration or executed a program")
			}
		})
	}
}

func TestComponentInteractiveOnlineCannotEnableAPI(t *testing.T) {
	for _, component := range []string{"proxy", "gds"} {
		t.Run(component, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			code, _, stderr, r := componentInteractiveCLI(t, m, "3\n", component, "install")
			if code == 0 || !strings.Contains(stderr, "API 功能未启用") || strings.Contains(stderr, "是否继续") {
				t.Fatal("online selection bypassed API gate", code, stderr)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != 0 {
				t.Fatal("disabled API changed data or invoked a core")
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
			input := "2\n" + artifact + "\n" + digest + "\ny\n"
			code, _, stderr, r := componentInteractiveCLI(t, m, input, "proxy", "install")
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
	for _, component := range []string{"proxy", "gds"} {
		t.Run(component, func(t *testing.T) {
			m, _ := fixture(t)
			code, _, stderr, _ := componentInteractiveCLI(t, m, "2\n/tmp/program\n"+strings.Repeat("0", 64)+"\n", component, "install", "--github-proxy", "direct")
			if code == 0 || !strings.Contains(stderr, "--github-proxy 仅用于在线") {
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
	input := fmt.Sprintf("2\n%s\n%x\nn\n", artifact, sha256.Sum256(data))
	code, _, stderr, r := componentInteractiveCLI(t, m, input, "proxy", "install")
	if code == 0 || !strings.Contains(stderr, "操作已取消") || len(r.commands) != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
		t.Fatal("declined local install changed state", code, stderr)
	}
}

func TestComponentInteractiveDownloadChoiceOnlyChangesFilePolicy(t *testing.T) {
	m, codexHome, _ := proxyBindingFixture(t)
	before := proxyBootstrapSnapshot(t, "", codexHome)
	code, _, stderr, r := componentInteractiveCLI(t, m, "3\ny\n", "download", "configure")
	if code != 0 || len(r.commands) != 0 {
		t.Fatal(code, stderr)
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Download.GitHubProxy != "direct" || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", codexHome)) {
		t.Fatal("download menu changed unrelated proxy or failed to save", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, apiProxyFile)); !os.IsNotExist(err) {
		t.Fatal("file policy enabled API")
	}
}

func TestComponentInteractiveGDSOnlyConfiguresChosenUUID(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	code, _, stderr, r := componentInteractiveCLI(t, m, "3\n"+cliGPUUUID+"\ny\n", "gds", "configure")
	if code != 0 || len(r.commands) != 0 {
		t.Fatal(code, stderr)
	}
	data, err := os.ReadFile(filepath.Join(m.Root, "gds", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := gdsapi.ParseConfig(data)
	if err != nil || cfg.GPUUUID != cliGPUUUID {
		t.Fatal("wrong configured GPU", err, string(data))
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "gds", "gds")); !os.IsNotExist(err) {
		t.Fatal("configuration installed a program")
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
	code, _, stderr, r := componentInteractiveCLI(t, m, "custom\nhttp://127.0.0.1:17999\ny\n", "codex", "proxy")
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
	for _, args := range [][]string{{"codex", "configure"}, {"codex", "setup"}, {"codex", "install"}, {"proxy", "configure"}, {"gds", "configure"}, {"download", "configure"}, {"download", "set"}} {
		for _, mode := range []string{"--json", "--yes", "--dry-run", ""} {
			t.Run(strings.Join(args, "-")+mode, func(t *testing.T) {
				m, _ := fixture(t)
				if err := m.Init(); err != nil {
					t.Fatal(err)
				}
				before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
				reader := &apiGateReader{}
				var out, stderr bytes.Buffer
				c := CLI{Interactive: mode != "", In: reader, Out: &out, Err: &stderr}
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
