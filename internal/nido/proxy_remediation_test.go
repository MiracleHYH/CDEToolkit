package nido

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func TestProxyRemediationRejectsInvalidNetworkOptionsBeforeMenu(t *testing.T) {
	for _, options := range [][]string{
		{"--proxy", "bad"},
		{"--github-proxy", "bad"},
		{"--github-proxy", "auto"},
	} {
		t.Run(strings.Join(options, "_"), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			reader := &apiGateReader{}
			var out, stderr bytes.Buffer
			runner := &cliProxyRunner{}
			cli := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr, ProxyRunner: runner}
			args := append([]string{"--root", m.Root, "proxy", "install"}, options...)
			if code := cli.Run(args); code == 0 || reader.reads != 0 {
				t.Fatal("invalid network option entered installation confirmation", code, reader.reads, stderr.String())
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
				t.Fatal("invalid network option changed files or invoked a core")
			}
		})
	}
}

func TestProxyRemediationMenuNetworkOverridesDoNotLeakBetweenActions(t *testing.T) {
	m, runner, _, _ := proxyRemediationFixture(t, false)
	if err := m.setNetwork(DownloadConfig{Mode: NetworkDirect, GitHubProxy: "direct"}); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	var addresses []string
	transport.DialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		return nil, fmt.Errorf("isolated test: no network connection")
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
	t.Setenv("HTTPS_PROXY", "http://ambient.invalid:99")
	t.Setenv("NO_PROXY", "*")
	originalHTTP := &http.Client{Transport: transport}
	originalAPI := &http.Client{Transport: transport}
	m.Proxy.HTTP, m.Proxy.APIHTTP = originalHTTP, originalAPI
	m.Proxy.GitHubProxy, m.Proxy.Runner = "https://old-prefix.invalid", runner
	for _, address := range []string{"http://127.0.0.1:19991", ""} {
		var out, stderr bytes.Buffer
		cli := CLI{Out: &out, Err: &stderr}
		if code := cli.proxy(m, cliOptions{check: true, json: true, proxy: address}, []string{"update"}); code == 0 {
			t.Fatal("isolated metadata request unexpectedly succeeded")
		}
		if m.Proxy.HTTP != originalHTTP || m.Proxy.APIHTTP != originalAPI || m.Proxy.GitHubProxy != "https://old-prefix.invalid" {
			t.Fatal("one menu action retained a temporary client or file policy")
		}
	}
	want := []string{"127.0.0.1:19991", "api.github.com:443"}
	if strings.Join(addresses, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PROXY to DIRECT retained old routing: got=%v want=%v", addresses, want)
	}
}

func proxyRemediationFixture(t *testing.T, running bool) (*Manager, *cliProxyRunner, string, []byte) {
	t.Helper()
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	r := &cliProxyRunner{ready: true}
	m.Proxy.Runner = r
	if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "install", "--artifact", proxyCLIArtifact(t), "--yes"); code != 0 {
		t.Fatal(stderr)
	}
	path := filepath.Join(m.Root, "proxy", "config.yaml")
	original := []byte("# preserve raw comment and CRLF\r\nmixed-port: 18888\r\nmode: rule\r\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if running {
		if code, _, stderr := proxyCLI(t, m, r, "", "proxy", "start", "--yes"); code != 0 {
			t.Fatal(stderr)
		}
	}
	return m, r, path, original
}

func proxyQuickAnswers(port int, final string) string {
	return fmt.Sprintf("https://provider.invalid/sub?token=private\nDaily\n%d\n1\n1\n\n%s", port, final)
}

func TestProxyRemediationInstallDefaultsToSkip(t *testing.T) {
	for _, input := range []string{"\n", "1\n", ""} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			r := &cliProxyRunner{}
			code, _, stderr := quickStartCLI(t, m, r, "2\n"+input, "proxy", "install", "--artifact", proxyCLIArtifact(t))
			if code != 0 || !strings.Contains(stderr, "Skip（默认）") || strings.Contains(stderr, "Provider URL") {
				t.Fatalf("default did not skip quick start: %d %s", code, stderr)
			}
			if strings.Count(stderr, "安装 Proxy\n") != 1 {
				t.Fatal("installation did not ask exactly one explicit confirmation", stderr)
			}
			for _, name := range []string{"config.yaml", "config.yaml.back", "process.json"} {
				if _, err := os.Lstat(filepath.Join(m.Root, "proxy", name)); !os.IsNotExist(err) {
					t.Fatalf("skip created %s: %v", name, err)
				}
			}
			if r.starts != 0 || r.stops != 0 || len(r.commands) != 1 {
				t.Fatalf("skip executed configuration or runtime operations: %+v", r)
			}
		})
	}
}

func TestProxyRemediationInstallNeedsExplicitConfirmation(t *testing.T) {
	for _, answer := range []string{"", "\n", "1\n", "q\n"} {
		t.Run(fmt.Sprintf("%q", answer), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			r := &cliProxyRunner{}
			code, _, stderr := quickStartCLI(t, m, r, answer, "proxy", "install", "--artifact", proxyCLIArtifact(t))
			if code == 0 || !strings.Contains(stderr, "安装 Proxy") || len(r.commands) != 0 || r.starts != 0 || r.stops != 0 {
				t.Fatalf("installation did not require approval: %d %s", code, stderr)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("cancelled installation changed managed or external data")
			}
		})
	}
}

func TestProxyRemediationRepeatedQuickStartKeepsOneExactBackup(t *testing.T) {
	m, r, path, previous := proxyRemediationFixture(t, false)
	for _, port := range []int{17890, 17891} {
		code, _, stderr := quickStartCLI(t, m, r, proxyQuickAnswers(port, "2\n"), "proxy", "configure")
		if code != 0 || !strings.Contains(stderr, "Provider URL") || strings.Contains(stderr, "Skip") || strings.Contains(stderr, "导入完整") {
			t.Fatalf("repeat quick start: %d %s", code, stderr)
		}
		backup, err := os.ReadFile(path + ".back")
		if err != nil || !bytes.Equal(backup, previous) {
			t.Fatalf("backup did not preserve raw previous bytes: %q %v", backup, err)
		}
		previous, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.Stat(path + ".back")
	if err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(path + ".back")
	if code, _, stderr := quickStartCLI(t, m, r, proxyQuickAnswers(17891, "2\n"), "proxy", "quickstart"); code != 0 {
		t.Fatal(stderr)
	}
	after, err := os.Stat(path + ".back")
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("same content rewrote backup", err)
	}
	if got, _ := os.ReadFile(path + ".back"); !bytes.Equal(got, backup) {
		t.Fatal("same content changed backup bytes")
	}
	if matches, _ := filepath.Glob(path + ".back*"); len(matches) != 1 {
		t.Fatalf("rotating backup files created: %v", matches)
	}
	if r.starts != 0 || r.stops != 0 {
		t.Fatal("quick start implicitly controlled runtime")
	}
}

func TestProxyRemediationRunningQuickStartRequiresExplicitStop(t *testing.T) {
	for _, choice := range []string{"\n", "2\n"} {
		t.Run(strings.TrimSpace(choice), func(t *testing.T) {
			m, r, path, original := proxyRemediationFixture(t, true)
			backup := []byte("original backup sentinel")
			if err := os.WriteFile(path+".back", backup, 0600); err != nil {
				t.Fatal(err)
			}
			commands := len(r.commands)
			input := proxyQuickAnswers(17890, "2\n") + choice
			code, _, stderr := quickStartCLI(t, m, r, input, "proxy", "configure")
			if !strings.Contains(stderr, "明确停止 Proxy") {
				t.Fatalf("no explicit stop choice: %s", stderr)
			}
			if choice == "\n" {
				if code == 0 || r.stops != 0 || r.process.PID == 0 || len(r.commands) != commands {
					t.Fatal("cancelled commit stopped runtime or executed core", code, stderr)
				}
				if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
					t.Fatal("cancelled commit changed main")
				}
				if got, _ := os.ReadFile(path + ".back"); !bytes.Equal(got, backup) {
					t.Fatal("cancelled commit changed backup")
				}
			} else {
				if code != 0 || r.stops != 1 || r.starts != 1 || r.process.PID != 0 {
					t.Fatalf("explicit stop/save failed: %d %s", code, stderr)
				}
				if got, _ := os.ReadFile(path + ".back"); !bytes.Equal(got, original) {
					t.Fatal("explicit save lost original bytes")
				}
			}
		})
	}
}

func TestProxyRemediationRejectsRemovedYAMLImportBeforeInput(t *testing.T) {
	for _, action := range []string{"configure", "quickstart"} {
		t.Run(action, func(t *testing.T) {
			m, r, path, original := proxyRemediationFixture(t, false)
			source := filepath.Join(t.TempDir(), "complete.yaml")
			candidate := []byte("# full user YAML\nmixed-port: 17890\n")
			if err := os.WriteFile(source, candidate, 0600); err != nil {
				t.Fatal(err)
			}
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			commands, starts, stops := len(r.commands), r.starts, r.stops
			reader := &apiGateReader{}
			var out, stderr bytes.Buffer
			c := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), Out: &out, Err: &stderr, ProxyRunner: r}
			code := c.Run([]string{"--root", m.Root, "proxy", action, "--file", source})
			if code == 0 || reader.reads != 0 {
				t.Fatalf("removed YAML import accepted or read input: %d %s", code, stderr.String())
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
				t.Fatal("removed YAML import changed main")
			}
			if got, _ := os.ReadFile(source); !bytes.Equal(got, candidate) || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) || len(r.commands) != commands || r.starts != starts || r.stops != stops {
				t.Fatal("removed YAML import changed data or controlled a component")
			}
		})
	}
}

func TestProxyRemediationLocalUpdateCannotStopOrReplaceAnyInstance(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			m, runner, _, _ := proxyRemediationFixture(t, running)
			artifact := proxyCLIArtifact(t)
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			commands, starts, stops := len(runner.commands), runner.starts, runner.stops
			reader := &apiGateReader{}
			var out, stderr bytes.Buffer
			cli := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), Out: &out, Err: &stderr, ProxyRunner: runner}
			code := cli.Run([]string{"--root", m.Root, "proxy", "update", "--artifact", artifact})
			if code == 0 || reader.reads != 0 || !strings.Contains(stderr.String(), "首次安装") {
				t.Fatal("obsolete local update entered a prompt or succeeded", code, reader.reads, stderr.String())
			}
			if len(runner.commands) != commands || runner.starts != starts || runner.stops != stops ||
				!reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("rejected local update changed files, executed core or changed lifecycle")
			}
		})
	}
}
