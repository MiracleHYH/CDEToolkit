package nido

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func proxyBindingCLI(t *testing.T, m *Manager, r *cliProxyRunner, input string, args ...string) (int, string, string) {
	t.Helper()
	var out, stderr bytes.Buffer
	reader := strings.NewReader(input)
	cli := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr, ProxyRunner: r}
	code := cli.Run(append([]string{"--root", m.Root}, args...))
	return code, out.String(), stderr.String()
}

func proxyBindingFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(home, "codex")
	if err := os.Mkdir(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", "auth.json", "sessions.fixture", "state.sqlite"} {
		if err := os.WriteFile(filepath.Join(codexHome, name), []byte("KEEP=literal\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(codexHome) }); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(gitPath, []byte("[user]\n\tname = Fixture User\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.Root, "proxy"), 0700); err != nil {
		t.Fatal(err)
	}
	return m, codexHome, gitPath
}

func writeBindingListeners(t *testing.T, m *Manager, config string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.Root, "proxy", "config.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
}

const bindingListeners = "mixed-port: 17899\nlisteners:\n  - {name: renamed-ai, type: socks, listen: 127.0.0.1, port: 17890}\n  - {name: arbitrary-daily, type: http, listen: 127.0.0.1, port: 17891}\n"

func TestRemovedProxyToolManagementRejectsWithoutSideEffects(t *testing.T) {
	for _, args := range [][]string{
		{"proxy", "bind"},
		{"proxy", "bind", "codex", "--proxy", "http://127.0.0.1:17899"},
		{"proxy", "bind", "git", "--endpoint", "mixed"},
		{"proxy", "bind", "nido", "--tool", "download"},
		{"proxy", "configure", "--file", "/never-read/advanced.yaml"},
		{"proxy", "logs"},
	} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, bindingListeners)
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			r := &cliProxyRunner{}
			input := &menuReadCounter{}
			network := &menuRejectHTTP{}
			var out, stderr bytes.Buffer
			c := CLI{In: input, Out: &out, Err: &stderr, ProxyRunner: r, UpdateHTTP: &http.Client{Transport: network}, Runner: func(string, []string, []string, []byte) ([]byte, error) {
				t.Fatal("removed Proxy command executed Codex")
				return nil, nil
			}}
			if code := c.Run(append([]string{"--root", m.Root}, args...)); code == 0 {
				t.Fatal("removed management entry accepted", out.String(), stderr.String())
			}
			if input.reads != 0 || network.calls != 0 || len(r.commands) != 0 || r.starts != 0 || r.stops != 0 || !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("rejected management entry read input, executed or changed data")
			}
		})
	}
}

func TestExternalProxySelectionTreatsListenerNamesAsData(t *testing.T) {
	for _, name := range []string{"off", "custom", "1"} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := proxyBindingFixture(t)
			writeBindingListeners(t, m, "listeners:\n  - {name: '"+name+"', type: http, listen: 127.0.0.1, port: 17891}\n")
			before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
			c := CLI{UI: &menuPlanDriver{choices: []int{0}}, Err: &bytes.Buffer{}}
			selected, err := c.chooseProxy(m, cliOptions{}, "Codex 运行")
			if err != nil || selected.keep || selected.endpoint == nil || selected.endpoint.Name != name || selected.address != "http://127.0.0.1:17891" {
				t.Fatal("listener name became an instruction", selected, err)
			}
			if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
				t.Fatal("selecting an external endpoint changed files")
			}
		})
	}
}

func TestExternalProxySelectionNeverExportsUnknownCredentials(t *testing.T) {
	m, _, _ := proxyBindingFixture(t)
	writeBindingListeners(t, m, "mixed-port: 17899\nauthentication: [fixture-user:fixture-secret]\n")
	before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
	var stderr bytes.Buffer
	c := CLI{Err: &stderr}
	choice, err := c.chooseProxy(m, cliOptions{endpoint: "mixed", yes: true}, "Codex 运行")
	if err == nil || choice.address != "" || strings.Contains(stderr.String()+err.Error(), "fixture-secret") {
		t.Fatal("authenticated listener silently exported or leaked credentials", choice, err)
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
		t.Fatal("failed authenticated endpoint selection changed files")
	}
}
