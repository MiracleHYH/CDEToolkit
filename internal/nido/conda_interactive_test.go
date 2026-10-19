package nido

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func condaInteractiveFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{
		CondaMirror: "tuna", PipMirror: "tuna", PipIndexURL: "https://pypi.tuna.tsinghua.edu.cn/simple",
		EnvsDir: filepath.Join(home, "envs"), PkgsDir: filepath.Join(home, "pkgs"), PipCacheDir: filepath.Join(home, "pip-cache"),
	}); err != nil {
		t.Fatal(err)
	}
	return m, home
}

func condaInteractiveSnapshot(t *testing.T, m *Manager, home string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, path := range []string{filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh"), filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf")} {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		files[path] = data
	}
	return files
}

// Typed Driver answers semantic prompts and stable action IDs. It does not
// emulate a terminal or retain the retired production numbering protocol.
type condaUIStep struct {
	kind, label, value string
	err                error
	before             func()
	fallback           bool
}
type condaUIAction struct {
	id   string
	code int
}
type condaTestUI struct {
	t                      *testing.T
	steps                  []condaUIStep
	pages                  [][]condaUIAction
	pageCount, actionCount int
	page                   terminalui.Page
	statuses               []string
	inputDefaults          []string
	pageErr                error
}

func newCondaUI(t *testing.T, steps ...condaUIStep) *condaTestUI {
	t.Helper()
	u := &condaTestUI{t: t, steps: steps}
	t.Cleanup(func() {
		if len(u.steps) != 0 {
			t.Errorf("unconsumed UI steps: %+v", u.steps)
		}
	})
	return u
}
func condaSelect(value string) condaUIStep { return condaUIStep{kind: "select", value: value} }
func condaInput(value string) condaUIStep  { return condaUIStep{kind: "input", value: value} }
func condaAccept() condaUIStep             { return condaSelect("确认") }
func (u *condaTestUI) next(kind, label string) condaUIStep {
	u.t.Helper()
	if len(u.steps) == 0 {
		u.t.Fatalf("unexpected %s prompt: %s", kind, label)
	}
	s := u.steps[0]
	u.steps = u.steps[1:]
	if s.kind != kind || s.label != "" && !strings.Contains(label, s.label) {
		u.t.Fatalf("wrong prompt: %s %q, want %+v", kind, label, s)
	}
	if s.before != nil {
		s.before()
	}
	return s
}
func (u *condaTestUI) Select(label string, options []string, fallback int) (int, error) {
	u.t.Helper()
	s := u.next("select", label)
	if reflect.DeepEqual(options, []string{"取消", "确认"}) && fallback != 0 {
		u.t.Fatal("confirmation must default to cancellation")
	}
	if s.err != nil {
		return 0, s.err
	}
	if s.value == "" {
		return fallback, nil
	}
	for i, option := range options {
		if strings.HasPrefix(option, s.value) {
			return i, nil
		}
	}
	u.t.Fatalf("missing semantic option %q in %v", s.value, options)
	return 0, nil
}
func (u *condaTestUI) Input(label, fallback string) (string, error) {
	u.t.Helper()
	s := u.next("input", label)
	u.inputDefaults = append(u.inputDefaults, fallback)
	if s.err != nil {
		return "", s.err
	}
	if s.fallback {
		return fallback, nil
	}
	return s.value, nil
}
func (u *condaTestUI) Page(p terminalui.Page) (int, error) {
	u.t.Helper()
	u.page = p
	refresh := func() {
		if p.Status != nil {
			for i := 0; i < 3; i++ {
				u.statuses = append(u.statuses, p.Status())
			}
		}
	}
	refresh()
	var actions []condaUIAction
	if u.pageCount < len(u.pages) {
		actions = u.pages[u.pageCount]
	}
	u.pageCount++
	for _, request := range actions {
		if p.Exit != nil && p.Exit() {
			break
		}
		var selected *terminalui.Action
		for i := range p.Actions {
			if p.Actions[i].ID == request.id {
				selected = &p.Actions[i]
				break
			}
		}
		if selected == nil || selected.Run == nil {
			u.t.Fatalf("missing page action %q", request.id)
		}
		u.actionCount++
		if code := selected.Run(); code != request.code {
			u.t.Fatalf("%s returned %d, want %d", request.id, code, request.code)
		}
		refresh()
	}
	return 0, u.pageErr
}

func TestCondaPageFlatActionsAndSavedStateOnly(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	before := condaInteractiveSnapshot(t, m, home)
	u := newCondaUI(t)
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	m.Conda.Runner = func(string, []string, []string) ([]byte, error) { t.Fatal("render queried upstream"); return nil, nil }
	if c.condaMenu(m, cliOptions{}) != 0 {
		t.Fatal(output.String())
	}
	var ids []string
	for _, a := range u.page.Actions {
		ids = append(ids, a.ID)
		if a.Group == "" || a.Run == nil || strings.Contains(a.Label, "菜单") {
			t.Fatalf("non-flat action: %+v", a)
		}
	}
	want := []string{"conda-mirror", "pip-mirror", "envs-dir", "conda-cache", "pip-cache", "record-defaults", "validate", "stop-conda-mirror", "stop-pip-mirror", "stop-envs-dir", "stop-conda-cache", "stop-pip-cache", "stop-all"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatal(ids)
	}
	for _, text := range []string{"Nido 保存值", "未核对当前 Shell 或上游实际生效值", "清华镜像", m.Config.Conda.EnvsDir, m.Config.Conda.PkgsDir, m.Config.Conda.PipCacheDir, "不会覆盖"} {
		if !strings.Contains(u.statuses[0], text) {
			t.Fatalf("missing saved state %q: %s", text, u.statuses[0])
		}
	}
	if !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("render changed configuration")
	}
}

func TestCondaInteractiveKeepDoesNotQueryOrWrite(t *testing.T) {
	for _, args := range [][]string{{"configure"}, {"mirror"}, {"mirror", "conda"}, {"mirror", "pip"}, {"cache"}, {"cache", "conda"}, {"cache", "pip"}, {"envs"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := condaInteractiveSnapshot(t, m, home)
			var steps []condaUIStep
			if args[0] != "configure" {
				if len(args) == 1 && args[0] != "envs" {
					steps = append(steps, condaSelect("Conda"))
				}
				if args[0] == "mirror" {
					steps = append(steps, condaSelect("保持当前选择"))
				} else {
					steps = append(steps, condaUIStep{kind: "input", fallback: true})
				}
			}
			u := newCondaUI(t, steps...)
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output, CondaRunner: func(string, []string, []string) ([]byte, error) { t.Fatal("keep queried upstream"); return nil, nil }}
			if c.Run(append([]string{"--root", m.Root, "conda"}, args...)) != 0 || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
				t.Fatal(output.String())
			}
			if args[0] == "envs" && u.inputDefaults[0] != m.Config.Conda.EnvsDir {
				t.Fatal("input lost saved fallback")
			}
		})
	}
}

func TestCondaInteractivePipChoicesReuseSavedDefault(t *testing.T) {
	for _, test := range []struct{ option, mirror, index string }{
		{"恢复首次保存", "default", ""}, {"选择官方源", "official", "https://pypi.org/simple"}, {"选择清华镜像", "tuna", "https://pypi.tuna.tsinghua.edu.cn/simple"}, {"选择中科大镜像", "ustc", "https://mirrors.ustc.edu.cn/pypi/simple"}, {"自定义 HTTPS", "custom", "https://packages.example/simple"},
	} {
		t.Run(test.mirror, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			defaults := m.Config.Conda.Defaults
			before := condaInteractiveSnapshot(t, m, home)
			steps := []condaUIStep{condaSelect(test.option)}
			if test.mirror == "custom" {
				steps = append(steps, condaInput(test.index))
			}
			steps = append(steps, condaAccept())
			u := newCondaUI(t, steps...)
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output, CondaRunner: func(string, []string, []string) ([]byte, error) {
				t.Fatal("saved pip default was recaptured")
				return nil, nil
			}}
			if c.Run([]string{"--root", m.Root, "conda", "mirror", "pip"}) != 0 {
				t.Fatal(output.String())
			}
			reloaded, err := New(m.Root)
			if err != nil || reloaded.Config.Conda.PipMirror != test.mirror || reloaded.Config.Conda.PipIndexURL != test.index || !reflect.DeepEqual(defaults, reloaded.Config.Conda.Defaults) || reloaded.Config.Conda.CondaMirror != "tuna" {
				t.Fatal("pip switch damaged selections/default", err)
			}
			after := condaInteractiveSnapshot(t, m, home)
			for _, p := range []string{filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf")} {
				if !bytes.Equal(before[p], after[p]) {
					t.Fatal("pip mirror changed a native file", p)
				}
			}
		})
	}
}

func TestCondaInteractiveCondaMirrorPreservesOriginalDefault(t *testing.T) {
	for _, test := range []struct{ option, mirror string }{{"选择官方源", "official"}, {"恢复首次保存", "default"}, {"选择清华镜像", "tuna"}} {
		t.Run(test.mirror, func(t *testing.T) {
			m, _ := condaInteractiveFixture(t)
			defaults := m.Config.Conda.Defaults
			u := newCondaUI(t, condaSelect(test.option), condaAccept())
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output, CondaRunner: originalMirrorRunner}
			if c.Run([]string{"--root", m.Root, "conda", "mirror", "conda"}) != 0 {
				t.Fatal(output.String())
			}
			reloaded, err := New(m.Root)
			if err != nil || reloaded.Config.Conda.CondaMirror != test.mirror || !reflect.DeepEqual(reloaded.Config.Conda.Defaults, defaults) || reloaded.Config.Conda.PipMirror != "tuna" {
				t.Fatal("mirror overwrote original or unrelated default", err)
			}
		})
	}
}

func TestCondaInteractivePathsUseExistingMutationWithoutCreatingData(t *testing.T) {
	for _, test := range []struct{ id string }{{"envs-dir"}, {"conda-cache"}, {"pip-cache"}} {
		t.Run(test.id, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := m.Config.Conda
			target := filepath.Join(home, "new-data", test.id)
			u := newCondaUI(t, condaInput(target), condaAccept())
			u.pages = [][]condaUIAction{{{id: test.id}}}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			m.Conda.Runner = func(string, []string, []string) ([]byte, error) {
				t.Fatal("directory queried upstream")
				return nil, nil
			}
			if c.condaMenu(m, cliOptions{}) != 0 {
				t.Fatal(output.String())
			}
			reloaded, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			got := reloaded.Config.Conda
			want := before
			native := filepath.Join(home, ".condarc")
			switch test.id {
			case "envs-dir":
				want.EnvsDir = target
			case "conda-cache":
				want.PkgsDir = target
			case "pip-cache":
				want.PipCacheDir = target
				native = filepath.Join(home, ".config", "pip", "pip.conf")
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("directory changed unrelated selections or default snapshot")
			}
			if data, err := os.ReadFile(native); err != nil || !bytes.Contains(data, []byte(target)) {
				t.Fatal("existing native path writer was not used", err)
			}
			if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
				t.Fatal("created data directory", err)
			}
			if !strings.Contains(output.String(), target) {
				t.Fatal("preview omitted target")
			}
		})
	}
}

func TestCondaInteractiveCancellationAndClosedInput(t *testing.T) {
	for _, stage := range []string{"mirror", "custom-url", "path", "confirmation", "capture-scope"} {
		for _, closed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-closed-%t", stage, closed), func(t *testing.T) {
				m, home := condaInteractiveFixture(t)
				before := condaInteractiveSnapshot(t, m, home)
				err := terminalui.ErrCancelled
				if closed {
					err = terminalui.ErrClosed
				}
				failure := condaUIStep{kind: "select", err: fmt.Errorf("wrapped: %w", err)}
				id := "pip-mirror"
				var steps []condaUIStep
				switch stage {
				case "mirror":
					steps = []condaUIStep{failure}
				case "custom-url":
					failure.kind = "input"
					steps = []condaUIStep{condaSelect("自定义 HTTPS"), failure}
				case "path":
					id = "envs-dir"
					failure.kind = "input"
					steps = []condaUIStep{failure}
				case "confirmation":
					id = "envs-dir"
					steps = []condaUIStep{condaInput(filepath.Join(home, "cancelled")), failure}
				case "capture-scope":
					id = "record-defaults"
					steps = []condaUIStep{failure}
				}
				u := newCondaUI(t, steps...)
				u.pages = [][]condaUIAction{{{id: id}, {id: "stop-pip-mirror"}}}
				if !closed {
					u.steps = append(u.steps, condaSelect("取消"))
				}
				var output bytes.Buffer
				c := CLI{UI: u, Out: &output, Err: &output}
				m.Conda.Runner = func(string, []string, []string) ([]byte, error) { t.Fatal("cancel queried upstream"); return nil, nil }
				if c.condaMenu(m, cliOptions{}) != 0 || strings.Contains(output.String(), "nido:") || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
					t.Fatal("cancel failed or wrote files", output.String())
				}
				if c.promptClosed != closed || closed && u.actionCount != 1 || !closed && u.actionCount != 2 {
					t.Fatal("Esc and closed input were not distinguished", c.promptClosed, u.actionCount)
				}
			})
		}
	}
}

func TestCondaInteractiveDefaultConfirmationCancels(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	before := condaInteractiveSnapshot(t, m, home)
	u := newCondaUI(t, condaInput(filepath.Join(home, "declined")), condaSelect(""))
	u.pages = [][]condaUIAction{{{id: "envs-dir"}}}
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	if c.condaMenu(m, cliOptions{}) != 0 || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) || strings.Contains(output.String(), "nido:") {
		t.Fatal(output.String())
	}
}

func TestCondaInteractiveTextDoesNotInterpretExitWords(t *testing.T) {
	for _, word := range []string{"q", "0", "off"} {
		t.Run(word, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := condaInteractiveSnapshot(t, m, home)
			u := newCondaUI(t, condaInput(word))
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			if c.conda(m, cliOptions{}, []string{"envs"}) == 0 || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
				t.Fatal("text became cancellation or command", output.String())
			}
		})
	}
}

func TestCondaInteractiveErrorsAreNotHiddenByClosedFlag(t *testing.T) {
	var output bytes.Buffer
	c := CLI{Out: &output, Err: &output, promptClosed: true}
	if c.condaInteractionError(errors.New("actual terminal error")) == 0 || !strings.Contains(output.String(), "actual terminal error") {
		t.Fatal("closed flag hid a real error")
	}
}

func TestCondaInteractiveNonTerminalWriteRequiresExplicitYes(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	before := condaInteractiveSnapshot(t, m, home)
	input := strings.NewReader("unused\n")
	var output bytes.Buffer
	c := CLI{In: input, Out: &output, Err: &output, CondaRunner: originalMirrorRunner}
	if c.Run([]string{"--root", m.Root, "conda", "envs", filepath.Join(home, "not-confirmed")}) == 0 || input.Len() != len("unused\n") || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("terminal error swallowed or stdin consumed", output.String())
	}
}

func TestCondaInteractiveMissingArgumentsStayStrictInScripts(t *testing.T) {
	for _, flags := range [][]string{nil, {"--yes"}, {"--json"}, {"--dry-run"}} {
		for _, args := range [][]string{{"configure"}, {"mirror"}, {"mirror", "pip"}, {"cache"}, {"cache", "conda"}, {"envs"}} {
			t.Run(strings.Join(append(append([]string{}, args...), flags...), "-"), func(t *testing.T) {
				m, home := condaInteractiveFixture(t)
				before := condaInteractiveSnapshot(t, m, home)
				input := strings.NewReader("unused\n")
				var output bytes.Buffer
				c := CLI{In: input, Out: &output, Err: &output, CondaRunner: func(string, []string, []string) ([]byte, error) {
					t.Fatal("missing script args queried upstream")
					return nil, nil
				}}
				if len(flags) != 0 {
					c.UI = newCondaUI(t)
				}
				command := append(append([]string{"--root", m.Root, "conda"}, args...), flags...)
				if c.Run(command) == 0 || input.Len() != len("unused\n") || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
					t.Fatal("script supplied missing args from UI/stdin", output.String())
				}
			})
		}
	}
}

func TestCondaInteractiveExplicitArgumentsKeepYesBehavior(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	target := filepath.Join(home, "explicit-pip-cache")
	input := strings.NewReader("unused\n")
	u := newCondaUI(t)
	var output bytes.Buffer
	c := CLI{UI: u, In: input, Out: &output, Err: &output, CondaRunner: originalMirrorRunner}
	if c.Run([]string{"--root", m.Root, "conda", "cache", "pip", target, "--yes"}) != 0 {
		t.Fatal(output.String())
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.PipCacheDir != target || input.Len() != len("unused\n") || u.pageCount != 0 {
		t.Fatal("explicit --yes used UI or changed behavior", err)
	}
}

func TestCondaMenuRepeatedOperationsAndReentryKeepIndependentChanges(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	defaults := m.Config.Conda.Defaults
	pipTarget := filepath.Join(home, "new-pip-data", "cache")
	envsTarget := filepath.Join(home, "new-env-data", "envs")
	u := newCondaUI(t, condaInput(pipTarget), condaAccept(), condaInput(envsTarget), condaAccept(), condaUIStep{kind: "input", err: terminalui.ErrCancelled})
	u.pages = [][]condaUIAction{{{id: "pip-cache"}, {id: "envs-dir"}, {id: "conda-cache"}}, nil}
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	m.Conda.Runner = func(string, []string, []string) ([]byte, error) { t.Fatal("page queried upstream"); return nil, nil }
	if c.condaMenu(m, cliOptions{}) != 0 {
		t.Fatal(output.String())
	}
	beforeReentry := condaInteractiveSnapshot(t, m, home)
	if c.condaMenu(m, cliOptions{}) != 0 || !reflect.DeepEqual(beforeReentry, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("reentry changed state")
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.PipCacheDir != pipTarget || reloaded.Config.Conda.EnvsDir != envsTarget || !reflect.DeepEqual(defaults, reloaded.Config.Conda.Defaults) {
		t.Fatal("later action lost completed operation", err)
	}
	for _, target := range []string{pipTarget, envsTarget} {
		if _, err := os.Lstat(filepath.Dir(target)); !os.IsNotExist(err) {
			t.Fatal("created data directory", err)
		}
		if !strings.Contains(u.statuses[len(u.statuses)-1], target) || !strings.Contains(output.String(), target) {
			t.Fatal("refreshed state or preview omitted target")
		}
	}
}

func TestCondaMenuRecoversFromOneOperationFailure(t *testing.T) {
	for _, failure := range []string{"invalid-directory", "upstream-query"} {
		t.Run(failure, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			defaults := m.Config.Conda.Defaults
			target := filepath.Join(home, "after-failure", "pip-cache")
			first := condaUIAction{id: "envs-dir", code: 1}
			steps := []condaUIStep{condaInput(filepath.Join(m.Root, "invalid-data"))}
			queries := 0
			if failure == "upstream-query" {
				first.id = "validate"
				steps = nil
				// Model a shell that has loaded the saved mirror, so validation
				// reaches the controlled upstream failure rather than activation checks.
				for key, value := range m.Conda.Variables(m.Config.Conda) {
					m.Conda.Env = append(m.Conda.Env, key+"="+value)
				}
			}
			steps = append(steps, condaInput(target), condaAccept())
			u := newCondaUI(t, steps...)
			u.pages = [][]condaUIAction{{first, {id: "pip-cache"}}}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			m.Conda.Runner = func(string, []string, []string) ([]byte, error) {
				queries++
				return nil, fmt.Errorf("isolated query failed")
			}
			if c.condaMenu(m, cliOptions{}) != 0 || !strings.Contains(output.String(), "nido:") {
				t.Fatal(output.String())
			}
			if m.Config.Conda.PipCacheDir != target || m.Config.Conda.EnvsDir != filepath.Join(home, "envs") || !reflect.DeepEqual(defaults, m.Config.Conda.Defaults) {
				t.Fatal("failure damaged unrelated configuration")
			}
			if failure == "upstream-query" && queries == 0 || failure == "invalid-directory" && queries != 0 {
				t.Fatal("unexpected upstream count", queries)
			}
		})
	}
}

func TestCondaMenuPreviewListsActualCustomSourceFilesBeforeConfirmation(t *testing.T) {
	m, home := condaFixture(t)
	source := filepath.Join(home, "custom-source.yaml")
	original := []byte("# retain certificate and proxy\nchannels: [defaults]\nssl_verify: /private/test-ca.pem\nproxy_servers:\n  https: http://127.0.0.1:8000\n")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDARC", source)
	m.Env = append(m.Env, "CONDARC="+source)
	m.Conda.Env = m.Env
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureConda(conda.Config{CondaMirror: "tuna", EnvsDir: filepath.Join(home, "old-envs")}); err != nil {
		t.Fatal(err)
	}
	before := condaInteractiveSnapshot(t, m, home)
	sourceBefore, _ := os.ReadFile(source)
	target := filepath.Join(home, "preview-only-envs")
	var output bytes.Buffer
	decline := condaSelect("取消")
	decline.before = func() {
		for _, expected := range []string{source, filepath.Join(home, ".condarc"), filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh"), target, "不创建、搬迁或清理"} {
			if !strings.Contains(output.String(), expected) {
				t.Fatal("preview missing before confirmation", expected, output.String())
			}
		}
	}
	u := newCondaUI(t, condaInput(target), decline)
	u.pages = [][]condaUIAction{{{id: "envs-dir"}}}
	c := CLI{UI: u, Out: &output, Err: &output}
	m.Conda.Runner = func(string, []string, []string) ([]byte, error) { t.Fatal("preview queried upstream"); return nil, nil }
	if c.condaMenu(m, cliOptions{}) != 0 {
		t.Fatal(output.String())
	}
	sourceAfter, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(sourceBefore, sourceAfter) || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("declined preview changed files", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("preview created data", err)
	}
}

func TestCondaMenuViewAndPreviewHideOriginalMirrorSecrets(t *testing.T) {
	for _, selected := range []string{"tuna", "default"} {
		t.Run(selected, func(t *testing.T) {
			m, _ := condaInteractiveFixture(t)
			secret := "ORIGINAL_MIRROR_SECRET_4f616a"
			m.Config.Conda.Defaults.CondaYAML += "\n# " + secret + "\n"
			m.Config.Conda.Defaults.PipIndexURL = "https://user:" + secret + "@private.example/simple"
			m.Config.Conda.Defaults.CondaVariables = map[string]string{"CONDA_CHANNELS": secret}
			m.Config.Conda.Defaults.PipVariables = map[string]string{"PIP_INDEX_URL": secret}
			m.Config.Conda.CondaMirror = selected
			m.Config.Conda.PipMirror = selected
			if selected == "default" {
				m.Config.Conda.PipIndexURL = ""
			}
			u := newCondaUI(t, condaSelect("保持当前选择"))
			u.pages = [][]condaUIAction{{{id: "pip-mirror"}}}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			c.previewCondaChange(m, "mirror", m.Config.Conda, nil)
			if c.condaMenu(m, cliOptions{}) != 0 || strings.Contains(output.String()+strings.Join(u.statuses, "\n"), secret) {
				t.Fatal("view exposed original mirror")
			}
		})
	}
}

func TestCondaMenuFirstCaptureCancellationCreatesNoSnapshotOrMirrorFiles(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	type entry struct {
		mode os.FileMode
		data string
	}
	snapshot := func() map[string]entry {
		result := map[string]entry{}
		for _, root := range []string{m.Root, home} {
			if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				value := entry{mode: info.Mode()}
				if info.Mode().IsRegular() {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					value.data = string(data)
				}
				result[path] = value
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := snapshot()
	u := newCondaUI(t, condaSelect("选择清华镜像"), condaSelect("取消"))
	u.pages = [][]condaUIAction{{{id: "pip-mirror"}}}
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	if c.condaMenu(m, cliOptions{}) != 0 || !reflect.DeepEqual(before, snapshot()) || m.Config.Conda.Defaults.PipCaptured || m.Config.Conda.Defaults.CondaCaptured {
		t.Fatal("cancel persisted snapshot/overlay", output.String())
	}
}

func TestCondaMenuConcurrentEditDoesNotUndoAnEarlierCompletedOperation(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	pipTarget := filepath.Join(home, "completed-pip-cache")
	envsTarget := filepath.Join(home, "unapplied-envs")
	configFile := filepath.Join(m.Root, "config.toml")
	var edited []byte
	confirm := condaAccept()
	confirm.before = func() {
		data, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(pipTarget)) {
			t.Fatal("first action did not complete before semantic confirm hook")
		}
		edited = append(data, []byte("\n# concurrent menu edit\n")...)
		if err := os.WriteFile(configFile, edited, 0600); err != nil {
			t.Fatal(err)
		}
	}
	u := newCondaUI(t, condaInput(pipTarget), condaAccept(), condaInput(envsTarget), confirm)
	u.pages = [][]condaUIAction{{{id: "pip-cache"}, {id: "envs-dir", code: 1}}}
	var output bytes.Buffer
	c := CLI{UI: u, Out: &output, Err: &output}
	m.Conda.Runner = func(string, []string, []string) ([]byte, error) {
		t.Fatal("directory queried upstream")
		return nil, nil
	}
	if c.condaMenu(m, cliOptions{}) != 0 || !strings.Contains(output.String(), "nido:") {
		t.Fatal(output.String())
	}
	actual, err := os.ReadFile(configFile)
	if err != nil || !bytes.Equal(edited, actual) {
		t.Fatal("overwrote concurrent edit", err)
	}
	reloaded, err := New(m.Root)
	if err != nil || reloaded.Config.Conda.PipCacheDir != pipTarget || reloaded.Config.Conda.EnvsDir == envsTarget {
		t.Fatal("later conflict undid previous success", err)
	}
}

func TestCondaMenuStopActionsKeepNativeValuesAndFirstDefaults(t *testing.T) {
	for _, id := range []string{"stop-conda-mirror", "stop-pip-mirror", "stop-envs-dir", "stop-conda-cache", "stop-pip-cache", "stop-all"} {
		t.Run(id, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			beforeConfig := m.Config.Conda
			before := condaInteractiveSnapshot(t, m, home)
			u := newCondaUI(t, condaAccept())
			u.pages = [][]condaUIAction{{{id: id}}}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			m.Conda.Runner = func(string, []string, []string) ([]byte, error) { t.Fatal("stop queried upstream"); return nil, nil }
			if c.condaMenu(m, cliOptions{}) != 0 {
				t.Fatal(output.String())
			}
			want := beforeConfig
			switch id {
			case "stop-conda-mirror":
				want.CondaMirror = ""
				want.PresetChannels = nil
				want.PresetChannelsSet = false
				want.PresetContext = ""
			case "stop-pip-mirror":
				want.PipMirror = ""
				want.PipIndexURL = ""
			case "stop-envs-dir":
				want.EnvsDir = ""
			case "stop-conda-cache":
				want.PkgsDir = ""
			case "stop-pip-cache":
				want.PipCacheDir = ""
			case "stop-all":
				want = conda.Config{Defaults: beforeConfig.Defaults}
			}
			if !reflect.DeepEqual(m.Config.Conda, want) {
				t.Fatal("stop changed unrelated selections")
			}
			after := condaInteractiveSnapshot(t, m, home)
			for _, path := range []string{filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf")} {
				if !bytes.Equal(before[path], after[path]) {
					t.Fatal("stop restored native configuration", path)
				}
			}
		})
	}
}

func TestCondaInteractiveBareCommandAndScriptStatusUseSeparateEntrypoints(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	before := condaInteractiveSnapshot(t, m, home)
	for _, script := range []bool{false, true} {
		input := strings.NewReader("unused\n")
		u := newCondaUI(t)
		var output bytes.Buffer
		c := CLI{UI: u, In: input, Out: &output, Err: &output, CondaRunner: func(string, []string, []string) ([]byte, error) {
			t.Fatal("bare entry queried upstream")
			return nil, nil
		}}
		args := []string{"--root", m.Root, "conda"}
		if script {
			args = append(args, "--json")
		}
		if c.Run(args) != 0 || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) || input.Len() != len("unused\n") {
			t.Fatal("entry wrote state or read stdin", output.String())
		}
		if script && (u.pageCount != 0 || !strings.Contains(output.String(), `"configured":true`)) || !script && u.pageCount != 1 {
			t.Fatal("wrong UI/script entry", script, u.pageCount, output.String())
		}
	}
}

func TestCondaMenuFirstRecordUsesSetupWithoutSelectingAMirror(t *testing.T) {
	for _, scope := range []struct {
		option     string
		conda, pip bool
	}{{"Conda 和 pip", true, true}, {"仅 Conda", true, false}, {"仅 pip", false, true}} {
		t.Run(scope.option, func(t *testing.T) {
			m, home := condaFixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			before := condaInteractiveSnapshot(t, m, home)
			base := m.Conda.Runner
			m.Conda.Runner = func(name string, args, env []string) ([]byte, error) {
				if name == "python" && !scope.pip || name != "python" && !scope.conda {
					t.Fatal("capture queried unselected tool", name)
				}
				return base(name, args, env)
			}
			u := newCondaUI(t, condaSelect(scope.option), condaAccept())
			u.pages = [][]condaUIAction{{{id: "record-defaults"}}}
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			if c.condaMenu(m, cliOptions{}) != 0 {
				t.Fatal(output.String())
			}
			cfg := m.Config.Conda
			if cfg.Defaults.CondaCaptured != scope.conda || cfg.Defaults.PipCaptured != scope.pip || cfg.CondaMirror != "" || cfg.PipMirror != "" {
				t.Fatal("capture selected a mirror or wrong tool")
			}
			after := condaInteractiveSnapshot(t, m, home)
			for _, path := range []string{filepath.Join(home, ".condarc"), filepath.Join(home, ".config", "pip", "pip.conf")} {
				if !bytes.Equal(before[path], after[path]) {
					t.Fatal("capture changed native file")
				}
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
				t.Fatal("capture published overlay", err)
			}
		})
	}
}
