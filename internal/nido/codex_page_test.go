package nido

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

type codexPageCapture struct {
	page          terminalui.Page
	status        string
	runIDs        []string
	confirmations []int
	history       [][]string
}

func (u *codexPageCapture) Page(p terminalui.Page) (int, error) {
	for {
		if p.Status != nil {
			u.status = p.Status()
		}
		if p.ActionsFunc != nil {
			p.Actions = p.ActionsFunc()
		}
		u.page = p
		u.history = append(u.history, codexPageIDs(p.Actions))
		if len(u.runIDs) == 0 {
			return 0, nil
		}
		id := u.runIDs[0]
		u.runIDs = u.runIDs[1:]
		var run func() int
		for _, action := range p.Actions {
			if action.ID == id {
				run = action.Run
				break
			}
		}
		if run == nil {
			return 1, fmt.Errorf("requested action %s absent from %v", id, codexPageIDs(p.Actions))
		}
		run()
	}
}
func (u *codexPageCapture) Select(string, []string, int) (int, error) {
	if len(u.confirmations) > 0 {
		answer := u.confirmations[0]
		u.confirmations = u.confirmations[1:]
		return answer, nil
	}
	return 0, fmt.Errorf("status must not prompt")
}
func (*codexPageCapture) Input(string, string) (string, error) {
	return "", fmt.Errorf("status must not prompt")
}

func codexPageIDs(actions []terminalui.Action) []string {
	ids := make([]string, 0, len(actions))
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestCodexPageFlatActionsAndReadOnlyStatus(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	data, bin := filepath.Join(home, ".codex"), filepath.Join(home, "bin")
	for _, dir := range []string{data, bin, filepath.Join(data, "sessions")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	protected := map[string][]byte{
		filepath.Join(data, "auth.json"):                 []byte("AUTH-SENTINEL"),
		filepath.Join(data, "sessions", "example.jsonl"): []byte("SESSION-SENTINEL"),
		filepath.Join(data, ".env"):                      []byte("TOKEN=ORIGINAL-SECRET\n"),
		filepath.Join(bin, "codex"):                      []byte("INERT CODEX FIXTURE"),
	}
	for path, content := range protected {
		mode := os.FileMode(0600)
		if filepath.Base(path) == "codex" {
			mode = 0700
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	m.Codex.Env = []string{"PATH=" + bin}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(data) }); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureProxy("http://SECRET-USER:SECRET-PASSWORD@127.0.0.1:17890", false); err != nil {
		t.Fatal(err)
	}
	protected[filepath.Join(data, ".env")], _ = os.ReadFile(filepath.Join(data, ".env"))
	config, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	env, _ := os.ReadFile(filepath.Join(m.Root, "env.sh"))
	versionCalls := 0
	m.Codex.Run = func(_ string, args, _ []string, _ []byte) ([]byte, error) {
		if !reflect.DeepEqual(args, []string{"--version"}) {
			t.Fatalf("page status executed a non-version operation: %v", args)
		}
		versionCalls++
		return []byte("codex 1.2.3\n"), nil
	}
	u := &codexPageCapture{}
	var out bytes.Buffer
	c := CLI{UI: u, Out: &out, Err: &out}
	if code := c.codexPage(m, cliOptions{}); code != 0 {
		t.Fatal(code, out.String())
	}
	var ids []string
	for _, a := range u.page.Actions {
		ids = append(ids, a.ID)
		if a.Group == "" || a.Run == nil || strings.Contains(a.Label, "状态") || strings.Contains(a.Label, "菜单") {
			t.Fatalf("non-flat action: %+v", a)
		}
	}
	if !reflect.DeepEqual(ids, []string{"associate", "proxy", "check", "detach"}) {
		t.Fatal(ids)
	}
	for _, value := range []string{"已关联", data, filepath.Join(bin, "codex"), "1.2.3", "已保存 Codex .env 代理配置", "官方安装更新使用 Nido Proxy"} {
		if !strings.Contains(u.status, value) {
			t.Fatalf("missing local state %q: %s", value, u.status)
		}
	}
	for _, value := range []string{"SECRET-USER", "SECRET-PASSWORD", "ORIGINAL-SECRET", "AUTH-SENTINEL", "SESSION-SENTINEL", "17890"} {
		if strings.Contains(u.status, value) || strings.Contains(out.String(), value) {
			t.Fatalf("page leaked private data %q", value)
		}
	}
	if versionCalls != 1 {
		t.Fatalf("expected one local version snapshot, got %d", versionCalls)
	}
	for path, expected := range protected {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("page changed external data %s: %v", path, err)
		}
	}
	for path, expected := range map[string][]byte{filepath.Join(m.Root, "config.toml"): config, filepath.Join(m.Root, "env.sh"): env} {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("page changed Nido config %s: %v", path, err)
		}
	}
}

func TestCodexPageActionsFollowLocalCapabilities(t *testing.T) {
	for _, row := range []struct {
		name, channel                                       string
		unlinked, unavailable, versionFailure, discoverOnly bool
		want                                                []string
		hint                                                string
	}{
		{name: "unlinked", unlinked: true, want: []string{"associate", "install"}, hint: "未关联"},
		{name: "linked-without-command", want: []string{"associate", "proxy", "install", "check", "detach"}, hint: "已关联数据目录不代表已安装 Codex 命令"},
		{name: "standalone", channel: "standalone", want: []string{"associate", "proxy", "update", "check", "detach"}, hint: "官方 standalone"},
		{name: "standalone-version-unavailable", channel: "standalone", versionFailure: true, want: []string{"associate", "proxy", "update", "check", "detach"}, hint: "无法查询 Codex 版本"},
		{name: "newly-discovered-standalone", channel: "standalone", discoverOnly: true, want: []string{"associate", "proxy", "update", "check", "detach"}, hint: "官方 standalone"},
		{name: "npm", channel: "npm", want: []string{"associate", "proxy", "check", "detach"}, hint: "使用 npm 等原安装方式更新"},
		{name: "homebrew", channel: "homebrew", want: []string{"associate", "proxy", "check", "detach"}, hint: "使用 Homebrew 更新"},
		{name: "unknown", channel: "unknown", want: []string{"associate", "proxy", "check", "detach"}, hint: "不覆盖未知来源的命令"},
		{name: "unavailable-home", unavailable: true, want: []string{"associate", "detach"}, hint: "不自动新建或迁移目录"},
	} {
		t.Run(row.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			data, bin := filepath.Join(home, "codex"), filepath.Join(home, "bin")
			if err := os.Mkdir(data, 0700); err != nil {
				t.Fatal(err)
			}
			protected := map[string][]byte{}
			for _, name := range []string{"auth.json", "session.jsonl", ".env"} {
				path := filepath.Join(data, name)
				protected[path] = []byte("PRIVATE-SENTINEL " + name)
				if err := os.WriteFile(path, protected[path], 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.Codex.Env = []string{"PATH=" + bin}
			if row.channel != "" {
				var program string
				switch row.channel {
				case "standalone":
					program = filepath.Join(data, "packages", "standalone", "fixture", "bin", "codex")
				case "npm":
					program = filepath.Join(home, "node_modules", "@openai", "codex", "bin", "codex")
				case "homebrew":
					program = filepath.Join(home, "Cellar", "codex", "1.2.3", "bin", "codex")
				default:
					program = filepath.Join(home, "unclassified", "codex")
				}
				for _, dir := range []string{filepath.Dir(program), bin} {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(program, []byte("INERT CODEX"), 0700); err != nil {
					t.Fatal(err)
				}
				protected[program] = []byte("INERT CODEX")
				if err := os.Symlink(program, filepath.Join(bin, "codex")); err != nil {
					t.Fatal(err)
				}
			}
			if !row.unlinked {
				if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(data) }); err != nil {
					t.Fatal(err)
				}
				if row.discoverOnly {
					if err := m.ConfigureCodex(func(c codex.Config) (codex.Config, error) { c.Command, c.Channel = "", ""; return c, nil }); err != nil {
						t.Fatal(err)
					}
				}
			}
			if row.unavailable {
				if err := os.Rename(data, data+"-moved"); err != nil {
					t.Fatal(err)
				}
				moved := map[string][]byte{}
				for path, value := range protected {
					moved[strings.Replace(path, data, data+"-moved", 1)] = value
				}
				protected = moved
			}
			versionCalls := 0
			m.Codex.Run = func(_ string, args, _ []string, _ []byte) ([]byte, error) {
				if !reflect.DeepEqual(args, []string{"--version"}) {
					t.Fatalf("page ran a mutating or network command: %v", args)
				}
				versionCalls++
				if row.versionFailure {
					return nil, fmt.Errorf("synthetic version failure")
				}
				return []byte("codex 1.2.3\n"), nil
			}
			before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			environment, _ := os.ReadFile(filepath.Join(m.Root, "env.sh"))
			u := &codexPageCapture{}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			if code := c.codexPage(m, cliOptions{}); code != 0 {
				t.Fatal(code, output.String())
			}
			if got := codexPageIDs(u.page.Actions); !reflect.DeepEqual(got, row.want) {
				t.Fatalf("actions=%v want=%v status=%s", got, row.want, u.status)
			}
			if !strings.Contains(u.status, row.hint) {
				t.Fatalf("missing hint %q: %s", row.hint, u.status)
			}
			wantVersions := 0
			if row.channel != "" && !row.unlinked && !row.unavailable {
				wantVersions = 1
			}
			if versionCalls != wantVersions {
				t.Fatalf("status/factory repeated version lookup: %d want %d", versionCalls, wantVersions)
			}
			for _, a := range u.page.Actions {
				if a.ID == "associate" && !row.unlinked && !row.unavailable && a.Label != "重新发现 Codex 命令" {
					t.Fatal("linked associate did not explain discovery", a.Label)
				}
				if a.ID == "install" && !row.unlinked && a.Label != "官方安装到当前 CODEX_HOME" {
					t.Fatal("installation destination unclear", a.Label)
				}
				if a.ID == "check" && row.channel == "" && a.Label != "查询最新版本" {
					t.Fatal("missing command advertised current version", a.Label)
				}
			}
			for path, value := range protected {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, value) {
					t.Fatalf("page changed private data %s: %v", path, err)
				}
			}
			for path, value := range map[string][]byte{filepath.Join(m.Root, "config.toml"): before, filepath.Join(m.Root, "env.sh"): environment} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, value) {
					t.Fatalf("capability preflight wrote %s: %v", path, err)
				}
			}
			if strings.Contains(u.status+output.String(), "PRIVATE-SENTINEL") {
				t.Fatal("page read or exposed user data")
			}
		})
	}
}

func TestCodexPageRefreshesAfterOfficialInstallAndDetach(t *testing.T) {
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "existing codex")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, name := range []string{"auth.json", "session.jsonl", ".env"} {
		path := filepath.Join(data, name)
		protected[path] = []byte("PRIVATE-SENTINEL " + name)
		if err := os.WriteFile(path, protected[path], 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.Codex.Env = []string{"PATH=/nonexistent"}
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(data) }); err != nil {
		t.Fatal(err)
	}
	var commands []string
	m.Codex.Run = func(path string, args, env []string, input []byte) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"--version"}) {
			commands = append(commands, "version")
			return []byte("codex 1.2.3\n"), nil
		}
		commands = append(commands, path)
		switch path {
		case "curl":
			if args[len(args)-1] != codex.InstallerURL {
				t.Fatalf("unexpected metadata request: %v", args)
			}
			return []byte("# INERT INSTALLER\n"), nil
		case "/bin/sh":
			if string(input) != "# INERT INSTALLER\n" {
				t.Fatal("incorrect fake installer input")
			}
			values := map[string]string{}
			for _, entry := range env {
				key, value, _ := strings.Cut(entry, "=")
				values[key] = value
			}
			if values["CODEX_HOME"] != data {
				t.Fatal("install moved the selected data directory", values["CODEX_HOME"])
			}
			program := filepath.Join(data, "packages", "standalone", "fixture", "bin", "codex")
			for _, dir := range []string{filepath.Dir(program), values["CODEX_INSTALL_DIR"]} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(program, []byte("INERT INSTALLED CODEX"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(program, filepath.Join(values["CODEX_INSTALL_DIR"], "codex")); err != nil {
				t.Fatal(err)
			}
			return nil, nil
		default:
			t.Fatalf("unexpected process: %s %v", path, args)
			return nil, nil
		}
	}
	u := &codexPageCapture{runIDs: []string{"install", "detach"}, confirmations: []int{1, 1}}
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	if code := c.codexPage(m, cliOptions{}); code != 0 {
		t.Fatal(code, output.String())
	}
	want := [][]string{{"associate", "proxy", "install", "check", "detach"}, {"associate", "proxy", "update", "check", "detach"}, {"associate", "install"}}
	if !reflect.DeepEqual(u.history, want) {
		t.Fatalf("stale page actions after mutations: %v\n%s", u.history, output.String())
	}
	if !reflect.DeepEqual(commands, []string{"curl", "/bin/sh", "version"}) {
		t.Fatal("unexpected or repeated remote/version work", commands)
	}
	if m.Config.Codex.Home != "" {
		t.Fatal("detach did not clear association")
	}
	for path, value := range protected {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, value) {
			t.Fatalf("operation changed external data %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "codex")); err != nil {
		t.Fatal("detach removed installed program", err)
	}
}

func TestCodexPageAssociationSelectionAndCancellation(t *testing.T) {
	for _, scenario := range []struct {
		name, input string
		want        bool
	}{
		{"associate-discovered", "1\n1\ny\n0\n", true},
		{"skip-discovered", "1\n3\n0\n", false},
		{"cancel-default-confirmation", "1\n1\n\n0\n", false},
		{"cancel-discovered-choice", "1\nq\n0\n", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, "existing codex")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			m.Codex.Env = []string{"CODEX_HOME=" + dir, "PATH=/usr/bin:/bin"}
			for _, file := range []string{"auth.json", ".env", "session.jsonl"} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte("UNCHANGED "+file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			var output bytes.Buffer
			c := CLI{UI: terminalui.NewScriptDriver(strings.NewReader(scenario.input), &output), Out: &output, Err: &output}
			if code := c.codexPage(m, cliOptions{}); code != 0 {
				t.Fatal(code, output.String())
			}
			if (m.Config.Codex.Home == dir) != scenario.want {
				t.Fatalf("wrong association: %+v %s", m.Config.Codex, output.String())
			}
			if !scenario.want {
				after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
				if !bytes.Equal(after, before) {
					t.Fatal("cancelled association wrote configuration")
				}
			}
			for _, file := range []string{"auth.json", ".env", "session.jsonl"} {
				got, err := os.ReadFile(filepath.Join(dir, file))
				if err != nil || string(got) != "UNCHANGED "+file {
					t.Fatalf("association changed external file %s", file)
				}
			}
			if strings.Count(output.String(), "发现 CODEX_HOME 环境变量：") != 1 {
				t.Fatal("discovery priority was not displayed exactly once", output.String())
			}
		})
	}
}

func TestCodexConfigureUsesSamePageWithoutNavigation(t *testing.T) {
	for _, args := range [][]string{nil, {"configure"}} {
		m, _ := fixture(t)
		u := &codexPageCapture{}
		var output bytes.Buffer
		c := CLI{UI: u, Out: &output, Err: &output}
		if code := c.codex(m, cliOptions{}, args); code != 0 || u.page.Title != "Codex · 外部辅助集成" {
			t.Fatal(code, output.String(), u.page.Title)
		}
		if _, err := os.Lstat(m.Root); !os.IsNotExist(err) {
			t.Fatal("opening Codex page initialized Nido")
		}
	}
}

func TestCodexPageDetachOnlyRemovesOwnedAssociationAndProxy(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		t.Run(fmt.Sprintf("confirm=%v", confirm), func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, "external codex")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("# User configuration\nUSER_TOKEN=UNCHANGED\n")
			protected := map[string][]byte{
				filepath.Join(dir, ".env"):          original,
				filepath.Join(dir, "auth.json"):     []byte("AUTH-SENTINEL"),
				filepath.Join(dir, "session.jsonl"): []byte("SESSION-SENTINEL"),
				filepath.Join(dir, "codex-program"): []byte("PROGRAM-SENTINEL"),
			}
			for path, content := range protected {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.Codex.Env = []string{"PATH=/nonexistent"}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(dir) }); err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureProxy("http://127.0.0.1:17890", false); err != nil {
				t.Fatal(err)
			}
			configBefore, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if !confirm {
				protected[filepath.Join(dir, ".env")], _ = os.ReadFile(filepath.Join(dir, ".env"))
			}
			var output bytes.Buffer
			answer := 0
			if confirm {
				answer = 1
			}
			u := &codexPageCapture{runIDs: []string{"detach"}, confirmations: []int{answer}}
			c := CLI{UI: u, Out: &output, Err: &output}
			if code := c.codexPage(m, cliOptions{}); code != 0 {
				t.Fatal(code, output.String())
			}
			if (m.Config.Codex.Home == "") != confirm {
				t.Fatalf("wrong detach result: %+v", m.Config.Codex)
			}
			if !confirm {
				current, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
				if !bytes.Equal(current, configBefore) {
					t.Fatal("default-cancel detach wrote Nido configuration")
				}
			}
			for path, expected := range protected {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("detach changed user data %s: %v", path, err)
				}
			}
		})
	}
}

func TestCodexOfficialUpdateSkipsReleaseLookupAndKeepsProxyIndependent(t *testing.T) {
	for _, scenario := range []struct {
		name, savedProxy, explicitProxy, wantProxy string
	}{
		{name: "none-clears-ambient"},
		{name: "saved-nido-proxy", savedProxy: "http://127.0.0.1:17890", wantProxy: "http://127.0.0.1:17890"},
		{name: "explicit-proxy", savedProxy: "http://127.0.0.1:17890", explicitProxy: "socks5://127.0.0.1:17891", wantProxy: "socks5://127.0.0.1:17891"},
		{name: "explicit-none", savedProxy: "http://127.0.0.1:17890", explicitProxy: "none"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer transport.Close()
			data, bin := filepath.Join(home, "external codex"), filepath.Join(home, "bin")
			program := filepath.Join(data, "packages", "standalone", "fixture", "bin", "codex")
			for _, dir := range []string{filepath.Dir(program), bin} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(program, []byte("INERT CODEX FIXTURE"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(program, filepath.Join(bin, "codex")); err != nil {
				t.Fatal(err)
			}
			protected := map[string][]byte{
				// These Codex runtime settings are independent of Nido's operation.
				// Even a shell expression is preserved, never parsed or executed.
				filepath.Join(data, ".env"):          []byte("HTTPS_PROXY=" + transport.URL + "\nhttp_proxy=${RUNTIME_PROXY}\nTOKEN=ORIGINAL\n"),
				filepath.Join(data, "auth.json"):     []byte("AUTH-SENTINEL"),
				filepath.Join(data, "session.jsonl"): []byte("SESSION-SENTINEL"),
			}
			for path, content := range protected {
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.Codex.Env = []string{"PATH=" + bin, "HTTP_PROXY=http://ambient.invalid:18990", "https_proxy=http://ambient.invalid:18991", "all_proxy=socks5://ambient.invalid:18992", "NO_PROXY=*", "Http_Proxy=http://ambient.invalid:18993", "CODEX_CUSTOM=keep"}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(data) }); err != nil {
				t.Fatal(err)
			}
			// This unrelated GitHub prefix must never reach Codex's official command.
			m.Config.Download = DownloadConfig{GitHubAccelerate: "https://github-accelerator.invalid/", ProxyURL: scenario.savedProxy}
			beforeEnvironment := append([]string(nil), m.Codex.Env...)
			var commands []string
			m.Codex.Run = func(path string, args, env []string, input []byte) ([]byte, error) {
				if reflect.DeepEqual(args, []string{"--version"}) {
					return []byte("codex 1.2.3\n"), nil
				}
				commands = append(commands, path)
				values := map[string]string{}
				for _, entry := range env {
					key, value, _ := strings.Cut(entry, "=")
					values[key] = value
					if strings.Contains(value, "github-accelerator.invalid") {
						t.Fatal("Nido GitHub prefix leaked into Codex environment")
					}
				}
				for _, key := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
					if values[key] != scenario.wantProxy {
						t.Fatalf("wrong temporary %s: got %q want %q", key, values[key], scenario.wantProxy)
					}
				}
				if values["no_proxy"] != "localhost,127.0.0.1,::1" || values["NO_PROXY"] != values["no_proxy"] || values["Http_Proxy"] != "" || values["CODEX_CUSTOM"] != "keep" || values["CODEX_HOME"] != data || values["CODEX_INSTALL_DIR"] != bin {
					t.Fatalf("wrong official directory/proxy injection: %+v", values)
				}
				switch path {
				case "curl":
					if args[len(args)-1] != codex.InstallerURL {
						t.Fatalf("unexpected download: %v", args)
					}
					return []byte("# INERT OFFICIAL INSTALLER\n"), nil
				case "/bin/sh":
					if !bytes.Equal(input, []byte("# INERT OFFICIAL INSTALLER\n")) {
						t.Fatal("wrong installer input")
					}
					return nil, nil
				default:
					t.Fatalf("unexpected command: %s %v", path, args)
					return nil, nil
				}
			}
			var output bytes.Buffer
			c := CLI{Out: &output, Err: &output}
			if code := c.codex(m, cliOptions{yes: true, proxy: scenario.explicitProxy}, []string{"update"}); code != 0 {
				t.Fatal(code, output.String())
			}
			if !reflect.DeepEqual(commands, []string{"curl", "/bin/sh"}) || requests.Load() != 0 {
				t.Fatalf("update used an independent metadata fetch: commands=%v requests=%d", commands, requests.Load())
			}
			if !reflect.DeepEqual(m.Codex.Env, beforeEnvironment) {
				t.Fatal("official update changed the parent/runtime environment")
			}
			for path, expected := range protected {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatalf("official update integration changed private data %s: %v", path, err)
				}
			}
		})
	}
}
