package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

func TestProxyBindListenerNamesCannotCollideWithMenuControls(t *testing.T) {
	m, _, gitPath := proxyBindingFixture(t)
	writeBindingListeners(t, m, "listeners:\n"+
		"  - {name: off, type: http, listen: 127.0.0.1, port: 17890}\n"+
		"  - {name: custom, type: http, listen: 127.0.0.1, port: 17891}\n"+
		"  - {name: keep, type: http, listen: 127.0.0.1, port: 17892}\n"+
		"  - {name: '1', type: http, listen: 127.0.0.1, port: 17893}\n")
	r := &cliProxyRunner{}
	bind := func(input string, expectedPort int) {
		t.Helper()
		code, out, stderr := proxyCLI(t, m, r, input, "proxy", "bind", "--tool", "git")
		if code != 0 || !strings.Contains(out, "applied") || !strings.Contains(stderr, "listener:名称") {
			t.Fatalf("selection %q: %d %s %s", input, code, out, stderr)
		}
		if got, _ := os.ReadFile(gitPath); !bytes.Contains(got, []byte(fmt.Sprintf("http://127.0.0.1:%d", expectedPort))) {
			t.Fatalf("selection %q did not configure listener port %d: %s", input, expectedPort, got)
		}
	}
	for i, name := range []string{"off", "custom", "keep", "1"} {
		bind("listener:"+name+"\ny\n", 17890+i)
	}
	// Numeric choices always refer to displayed menu positions. A numeric
	// listener name remains available through the explicit listener: form.
	bind("1\ny\n", 17890)
	bind("4\ny\n", 17893)
	// The custom action remains available even when a listener is named custom.
	bind("custom\nhttp://127.0.0.1:17999\ny\n", 17999)
	bind("5\nhttp://127.0.0.1:17998\ny\n", 17998)
	before, _ := os.ReadFile(gitPath)
	for _, input := range []string{"keep\n", "6\n", "\n"} {
		if code, out, stderr := proxyCLI(t, m, r, input, "proxy", "bind", "--tool", "git"); code != 0 || !strings.Contains(out, "unchanged") {
			t.Fatalf("keep selection: %d %s %s", code, out, stderr)
		}
		if got, _ := os.ReadFile(gitPath); !bytes.Equal(got, before) {
			t.Fatal("keep selection changed Git configuration")
		}
	}
	if code, _, stderr := proxyCLI(t, m, r, "off\ny\n", "proxy", "bind", "--tool", "git"); code != 0 {
		t.Fatal(stderr)
	}
	if got, _ := os.ReadFile(gitPath); string(got) != "[user]\n\tname = Fixture User\n" {
		t.Fatal("off control did not remove only the managed block")
	}
	if r.starts != 0 || len(r.commands) != 0 {
		t.Fatal("listener selection invoked a component")
	}
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

func TestProxyBindCategoryAndSingleToolAreIndependent(t *testing.T) {
	m, codexHome, gitPath := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	r := &cliProxyRunner{}
	call := func(args ...string) {
		t.Helper()
		args = append([]string{"proxy", "bind"}, args...)
		args = append(args, "--yes", "--json")
		if code, out, stderr := proxyCLI(t, m, r, "", args...); code != 0 || !strings.Contains(out, "applied") {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	call("ai", "--endpoint", "renamed-ai")
	ai, _ := os.ReadFile(filepath.Join(codexHome, ".env"))
	if !bytes.Contains(ai, []byte("socks5h://127.0.0.1:17890")) {
		t.Fatal("AI did not use selected arbitrary listener")
	}
	if git, _ := os.ReadFile(gitPath); string(git) != "[user]\n\tname = Fixture User\n" {
		t.Fatal("AI modified Git")
	}
	call("daily", "--endpoint", "arbitrary-daily")
	gitBefore, _ := os.ReadFile(gitPath)
	if !bytes.Contains(gitBefore, []byte("http://127.0.0.1:17891")) || bytes.Contains(gitBefore, []byte("https.proxy")) {
		t.Fatal("daily category used incorrect Git setting")
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); !bytes.Equal(got, ai) {
		t.Fatal("daily category changed AI")
	}
	call("--tool", "codex", "--proxy", "http://name:p%40ss%3Aword@127.0.0.1:17892")
	if got, _ := os.ReadFile(gitPath); !bytes.Equal(got, gitBefore) {
		t.Fatal("single Codex operation changed Git")
	}
	call("ai", "--endpoint", "arbitrary-daily")
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); !bytes.Contains(got, []byte("http://127.0.0.1:17891")) || bytes.Contains(got, []byte("p%40ss")) {
		t.Fatal("later explicit category operation did not replace single-tool selection")
	}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	gitBeforeDownload, _ := os.ReadFile(gitPath)
	aiBeforeDownload, _ := os.ReadFile(filepath.Join(codexHome, ".env"))
	call("download", "--endpoint", "arbitrary-daily")
	bindingPath := filepath.Join(m.Root, "download-proxy.toml")
	binding, err := os.ReadFile(bindingPath)
	if err != nil || !bytes.Contains(binding, []byte("nido-api-proxy")) || !bytes.Contains(binding, []byte("arbitrary-daily")) {
		t.Fatalf("download binding did not persist the selected listener: %s %v", binding, err)
	}
	if info, err := os.Stat(bindingPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("download binding is not private: %v %v", info, err)
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(got, before) {
		t.Fatal("download binding changed the main config schema")
	}
	if got, _ := os.ReadFile(gitPath); !bytes.Equal(got, gitBeforeDownload) {
		t.Fatal("Nido API binding changed Git")
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); !bytes.Equal(got, aiBeforeDownload) {
		t.Fatal("Nido API binding changed Codex")
	}
	call("--tool", "git", "--proxy", "off")
	if got, _ := os.ReadFile(gitPath); string(got) != "[user]\n\tname = Fixture User\n" {
		t.Fatal("Git off did not preserve original content")
	}
	for _, name := range []string{"auth.json", "sessions.fixture", "state.sqlite"} {
		if got, _ := os.ReadFile(filepath.Join(codexHome, name)); string(got) != "KEEP=literal\n" {
			t.Fatalf("external data changed: %s", name)
		}
	}
	if r.starts != 0 || len(r.commands) != 0 {
		t.Fatal("binding invoked a real component")
	}
}

func TestProxyBindAlwaysOffersCustomAndIgnoresTraditionalPorts(t *testing.T) {
	for _, config := range []string{"", "mixed-port: 17890\n", bindingListeners} {
		t.Run(config, func(t *testing.T) {
			m, _, gitPath := proxyBindingFixture(t)
			if config != "" {
				writeBindingListeners(t, m, config)
			}
			r := &cliProxyRunner{}
			input := "custom\nsocks5h://u:p%2Dword@[::1]:17893\ny\n"
			code, out, stderr := proxyCLI(t, m, r, input, "proxy", "bind", "--tool", "git")
			if code != 0 || !strings.Contains(stderr, "自定义代理地址") || !strings.Contains(out, "applied") {
				t.Fatalf("custom binding: %d %s %s", code, out, stderr)
			}
			if config != bindingListeners && !strings.Contains(stderr, "没有可用的 listener") {
				t.Fatal("missing listeners did not explain custom fallback")
			}
			if got, _ := os.ReadFile(gitPath); !bytes.Contains(got, []byte("socks5h://u:p%2Dword@[::1]:17893")) {
				t.Fatal("custom URI was decoded, rewritten or omitted")
			}
			if r.starts != 0 || len(r.commands) != 0 {
				t.Fatal("custom binding invoked a component")
			}
		})
	}
}

func TestProxyBindPreviewInvalidOptionsAndChangedListenerPreserveData(t *testing.T) {
	m, codexHome, gitPath := proxyBindingFixture(t)
	writeBindingListeners(t, m, bindingListeners)
	r := &cliProxyRunner{}
	before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	for _, args := range [][]string{
		{"proxy", "bind", "ai", "--endpoint", "renamed-ai", "--dry-run", "--json"},
		{"proxy", "bind", "daily", "--proxy", "http://127.0.0.1:18000", "--dry-run", "--json"},
	} {
		if code, _, stderr := proxyCLI(t, m, r, "", args...); code != 0 {
			t.Fatal(stderr)
		}
	}
	for _, args := range [][]string{
		{"proxy", "bind", "unknown", "--yes"}, {"proxy", "bind", "--tool", "unknown", "--yes"},
		{"proxy", "bind", "ai", "--tool", "codex", "--yes"}, {"proxy", "bind", "ai", "--yes"},
		{"proxy", "bind", "daily", "--proxy", "ftp://127.0.0.1:1", "--yes"},
		{"proxy", "bind", "daily", "--proxy", "http://127.0.0.1", "--yes"},
		{"proxy", "bind", "ai", "--proxy", "http://[not-an-ip]:8080", "--yes"},
		{"proxy", "bind", "ai", "--proxy", "http://127.0.0.1:8080?", "--yes"},
		{"proxy", "bind", "daily", "--proxy", "http://::1:8080", "--yes"},
		{"proxy", "bind", "daily", "--proxy", "http://u:p%0Aq@127.0.0.1:1", "--yes"},
		{"proxy", "bind", "daily", "--proxy", "http://proxy\u200b.example:1080", "--yes"},
		{"proxy", "bind", "--tool", "git", "--proxy", "http://proxy\ufeff.example:1080", "--dry-run"},
		{"proxy", "bind", "ai", "--proxy", "http://proxy\u200b.example:1080", "--yes"},
		{"proxy", "bind", "--tool", "codex", "--proxy", "http://proxy\xff.example:1080", "--yes"},
		{"proxy", "bind", "ai", "--endpoint", "mixed", "--yes"},
	} {
		if code, _, _ := proxyCLI(t, m, r, "", args...); code == 0 {
			t.Fatalf("invalid selection accepted: %v", args)
		}
	}
	var out, stderr bytes.Buffer
	reader := &beforeRead{reader: strings.NewReader("y\n"), before: func() {
		writeBindingListeners(t, m, strings.ReplaceAll(bindingListeners, "17890", "17892"))
	}}
	cli := CLI{In: reader, Out: &out, Err: &stderr, ProxyRunner: r}
	if code := cli.Run([]string{"--root", m.Root, "proxy", "bind", "ai", "--endpoint", "renamed-ai"}); code == 0 || !strings.Contains(stderr.String(), "发生变化") {
		t.Fatalf("stale listener accepted: %d %s %s", code, out.String(), stderr.String())
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(got, before) {
		t.Fatal("previews/rejected bindings changed receipts")
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); string(got) != "KEEP=literal\n" {
		t.Fatal("preview/rejected binding changed Codex")
	}
	if got, _ := os.ReadFile(gitPath); string(got) != "[user]\n\tname = Fixture User\n" {
		t.Fatal("preview/rejected binding changed Git")
	}
}

func TestProxyBindAuthenticatedListenerAndReceiptFailureCompensation(t *testing.T) {
	m, codexHome, gitPath := proxyBindingFixture(t)
	writeBindingListeners(t, m, "listeners: [{name: auth-entry, type: mixed, listen: 127.0.0.1, port: 17890, users: [{username: 'user@name', password: 'p:word'}]}]\n")
	r := &cliProxyRunner{}
	beforePreview, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	for _, extra := range []string{"--yes", "--json", "--dry-run"} {
		var out, stderr bytes.Buffer
		reader := &beforeRead{reader: strings.NewReader(""), before: func() {
			t.Fatal("noninteractive authenticated binding read credentials")
		}}
		cli := CLI{In: reader, Out: &out, Err: &stderr, ProxyRunner: r}
		if code := cli.Run([]string{"--root", m.Root, "proxy", "bind", "ai", "--endpoint", "auth-entry", extra}); code == 0 || !strings.Contains(stderr.String(), "--proxy") {
			t.Fatalf("authenticated listener bound or did not explain custom credentials: %d %s %s", code, out.String(), stderr.String())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(got, beforePreview) {
		t.Fatal("noninteractive authenticated binding changed receipts")
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); string(got) != "KEEP=literal\n" {
		t.Fatal("noninteractive authenticated binding changed Codex")
	}
	if code, out, stderr := proxyCLI(t, m, r, "auth-entry\nuser@name\np:word\ny\n", "proxy", "bind", "ai"); code != 0 {
		t.Fatalf("authenticated interactive binding: %d %s %s", code, out, stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(codexHome, ".env")); !bytes.Contains(got, []byte("http://user%40name:p%3Aword@127.0.0.1:17890")) {
		t.Fatal("listener credentials were not encoded as userinfo")
	}
	// A valid integration edit can still overflow the manager's bounded TOML.
	// The manager must reject that receipt before publication and undo the edit.
	beforeConfig, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	beforeGit, _ := os.ReadFile(gitPath)
	next, err := New(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	address := "http://u:" + strings.Repeat("p", (1<<20)-180) + "@127.0.0.1:17890"
	if err := next.configureToolProxy("git", address, nil); err == nil || !strings.Contains(err.Error(), "序列化后") {
		t.Fatalf("oversized receipt did not exercise save compensation: %v", err)
	}
	if got, _ := os.ReadFile(gitPath); !bytes.Equal(got, beforeGit) {
		t.Fatal("failed receipt save left changed Git data")
	}
	if got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml")); !bytes.Equal(got, beforeConfig) {
		t.Fatal("oversized receipt corrupted Nido configuration")
	}
}
