package nido

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
	"github.com/pelletier/go-toml/v2"
)

type menuReadCounter struct{ reads int }

func (r *menuReadCounter) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

type menuRejectHTTP struct{ calls int }

func (r *menuRejectHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls++
	return nil, fmt.Errorf("read-only page contacted network")
}

// An explicit test driver chooses stable action identities, independently of
// keyboard navigation. Real arrow/resize/restore behavior is exercised in PTYs.
type menuPlanDriver struct {
	plan    map[string][]string
	choices []int
	titles  []string
	states  []string
	labels  []string
}

func (d *menuPlanDriver) Page(p terminalui.Page) (int, error) {
	d.titles = append(d.titles, p.Title)
	if p.ActionsFunc != nil {
		p.Actions = p.ActionsFunc()
	}
	if p.Status != nil {
		d.states = append(d.states, p.Status())
	}
	for _, a := range p.Actions {
		d.labels = append(d.labels, a.ID+":"+a.Label)
	}
	for len(d.plan[p.Title]) > 0 {
		id := d.plan[p.Title][0]
		d.plan[p.Title] = d.plan[p.Title][1:]
		found := false
		for _, a := range p.Actions {
			if a.ID == id {
				found = true
				if a.Run != nil {
					a.Run()
				}
				break
			}
		}
		if !found {
			return 1, fmt.Errorf("missing action %s on %s", id, p.Title)
		}
		if p.Exit != nil && p.Exit() {
			return 0, nil
		}
		if p.Status != nil {
			d.states = append(d.states, p.Status())
		}
		if p.ActionsFunc != nil {
			p.Actions = p.ActionsFunc()
		}
	}
	return 0, nil
}
func (d *menuPlanDriver) Select(_ string, options []string, fallback int) (int, error) {
	if len(d.choices) == 0 {
		return fallback, nil
	}
	n := d.choices[0]
	d.choices = d.choices[1:]
	if n < 0 || n >= len(options) {
		return 0, terminalui.ErrCancelled
	}
	return n, nil
}
func (d *menuPlanDriver) Input(string, string) (string, error) { return "", terminalui.ErrCancelled }

func menuCLI(t *testing.T, driver terminalui.Driver) (*CLI, *bytes.Buffer, *bytes.Buffer, *cliProxyRunner, *menuRejectHTTP) {
	t.Helper()
	var out, stderr bytes.Buffer
	r := &cliProxyRunner{}
	network := &menuRejectHTTP{}
	c := &CLI{UI: driver, Out: &out, Err: &stderr, ProxyRunner: r, UpdateHTTP: &http.Client{Transport: network}, Runner: func(string, []string, []string, []byte) ([]byte, error) {
		t.Fatal("page executed Codex")
		return nil, nil
	}, CondaRunner: func(string, []string, []string) ([]byte, error) {
		t.Fatal("page executed Conda/Python")
		return nil, nil
	}}
	return c, &out, &stderr, r, network
}
func assertMenuReadOnly(t *testing.T, m *Manager, before map[string]bootstrapFile, r *cliProxyRunner, n *menuRejectHTTP) {
	t.Helper()
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))) {
		t.Fatal("page changed user files")
	}
	if len(r.commands) != 0 || r.starts != 0 || r.stops != 0 || n.calls != 0 {
		t.Fatal("page performed execution or network")
	}
}
func TestMainMenuDefaultEntryAndExitAreReadOnly(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			m, _ := fixture(t)
			t.Setenv("NIDO_HOME", m.Root)
			before := proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))
			d := &menuPlanDriver{}
			c, _, errout, r, n := menuCLI(t, d)
			var args []string
			if explicit {
				args = []string{"--root", m.Root}
			}
			if code := c.Run(args); code != 0 {
				t.Fatal(code, errout.String())
			}
			if len(d.titles) != 1 || d.titles[0] != "Nido" || !strings.Contains(strings.Join(d.states, "\n"), "尚未安装") {
				t.Fatal(d.titles, d.states)
			}
			for _, unwanted := range []string{"gds", "查看状态", "进入设置", "shell", "init:", "network:", "version:"} {
				if strings.Contains(strings.ToLower(strings.Join(d.labels, "\n")), unwanted) {
					t.Fatal("unwanted page action", unwanted)
				}
			}
			for _, label := range d.labels {
				if strings.HasPrefix(label, "update:") || strings.HasPrefix(label, "latest:") {
					t.Fatal("standalone version/update action remains", label)
				}
			}
			assertMenuReadOnly(t, m, before, r, n)
		})
	}
}
func TestMainMenuReadOnlyNavigationAcrossComponents(t *testing.T) {
	m, home := fixture(t)
	for name, data := range map[string]string{".bashrc": "shell sentinel\n", ".condarc": "channels: [defaults]\n", ".codex/auth.json": "auth sentinel\n", ".codex/sessions/one.jsonl": "session sentinel\n"} {
		file := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))
	d := &menuPlanDriver{plan: map[string][]string{"Nido": {"proxy", "codex", "conda"}}}
	c, _, stderr, r, n := menuCLI(t, d)
	if code := c.Run([]string{"--root", m.Root}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if len(d.titles) != 4 {
		t.Fatal("unexpected navigation depth", d.titles)
	}
	for _, label := range []string{"Proxy", "Codex", "Conda / pip"} {
		if !strings.Contains(strings.Join(d.titles, "\n"), label) {
			t.Fatal(label, d.titles)
		}
	}
	assertMenuReadOnly(t, m, before, r, n)
}
func TestMainMenuCancelledNetworkSettingsReturnWithoutWriting(t *testing.T) {
	m, _ := fixture(t)
	before := proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))
	d := &menuPlanDriver{plan: map[string][]string{"Nido": {"github-accelerate", "nido-proxy"}}, choices: []int{-1, -1}}
	c, _, _, r, n := menuCLI(t, d)
	if code := c.Run([]string{"--root", m.Root}); code != 0 {
		t.Fatal(code)
	}
	assertMenuReadOnly(t, m, before, r, n)
}
func TestMainMenuWithoutTerminalOrInScriptModeDoesNotRead(t *testing.T) {
	for _, option := range []string{"", "--yes", "--json", "--dry-run"} {
		t.Run(option, func(t *testing.T) {
			m, _ := fixture(t)
			before := proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))
			reader := &menuReadCounter{}
			c, _, _, r, n := menuCLI(t, nil)
			c.In = reader
			args := []string{"--root", m.Root}
			if option != "" {
				args = append(args, option)
			}
			if code := c.Run(args); code == 0 || reader.reads != 0 {
				t.Fatal("nonterminal menu read input", code, reader.reads)
			}
			assertMenuReadOnly(t, m, before, r, n)
		})
	}
}
func TestMainMenuUnavailableCodexDoesNotBlockOtherComponents(t *testing.T) {
	m, home := fixture(t)
	broken := filepath.Join(home, "old-codex")
	if err := os.Symlink(filepath.Join(home, "missing-mount", "codex"), broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.Root, 0700); err != nil {
		t.Fatal(err)
	}
	m.Config.Codex.Home = broken
	data, err := toml.Marshal(m.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Root, "config.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	before := proxyBootstrapSnapshot(t, "", filepath.Dir(m.Root))
	d := &menuPlanDriver{plan: map[string][]string{"Nido": {"codex", "github-accelerate"}}, choices: []int{-1}}
	c, _, stderr, r, n := menuCLI(t, d)
	if code := c.Run([]string{"--root", m.Root}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if !strings.Contains(strings.Join(d.states, "\n"), "目录不可用") {
		t.Fatal(d.states)
	}
	assertMenuReadOnly(t, m, before, r, n)
}
