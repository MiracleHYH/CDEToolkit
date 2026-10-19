package terminalui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func testModel(p Page) *model {
	ctx, cancel := context.WithCancel(context.Background())
	return newModel(&Session{ctx: ctx, cancel: cancel, done: make(chan struct{})}, p, nil)
}

func TestStatusAndActionsSerializeWithoutPolling(t *testing.T) {
	queries, actions := 0, 0
	m := testModel(Page{Title: "测试", Status: func() string { queries++; return "状态" }, Actions: []Action{{ID: "a", Label: "操作", Run: func() int { actions++; return 0 }}}})
	query := m.Init()
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("action raced status query")
	}
	m.Update(query())
	for i := 0; i < 100; i++ {
		if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}); cmd != nil {
			t.Fatal("navigation triggered work")
		}
	}
	if queries != 1 || actions != 0 {
		t.Fatalf("queries=%d actions=%d", queries, actions)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("action not scheduled")
	}
	if actions != 0 {
		t.Fatal("action executed on UI goroutine")
	}
	result := cmd()
	if actions != 1 {
		t.Fatal("action missing")
	}
	_, refresh := m.Update(result)
	if refresh == nil {
		t.Fatal("status not refreshed after action")
	}
	m.Update(refresh())
	if queries != 2 {
		t.Fatalf("queries=%d", queries)
	}
}

func TestConfirmationDefaultCancelAndEsc(t *testing.T) {
	m := testModel(Page{Title: "test"})
	done := make(chan result, 1)
	m.Update(promptRequest{label: "继续？", options: []string{"取消", "确认"}, index: 0, done: done})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := <-done; got.index != 0 || got.err != nil {
		t.Fatalf("confirmation=%+v", got)
	}
	m.Update(promptRequest{label: "继续？", options: []string{"取消", "确认"}, index: 0, done: done})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if got := <-done; got.err != ErrCancelled {
		t.Fatalf("cancel=%+v", got)
	}
	if m.closing {
		t.Fatal("Esc closed whole application")
	}
}

func TestNarrowViewportAndUntrustedControlText(t *testing.T) {
	m := testModel(Page{Title: "中文标题\x1b[31m", Status: func() string { return strings.Repeat("多行状态测试\n", 20) }, Actions: []Action{{ID: "a", Label: "选择中文操作", Group: "配置"}, {ID: "b", Label: "另一个操作", Group: "维护"}}})
	m.Update(m.Init()())
	m.Update(tea.WindowSizeMsg{Width: 18, Height: 7})
	view := m.View().Content
	if strings.Contains(view, "\x1b[31m") {
		t.Fatal("untrusted escape rendered")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 18 {
			t.Fatalf("wide line %q", line)
		}
	}
	if len(strings.Split(view, "\n")) > 7 {
		t.Fatal("viewport overflow")
	}
	before := view
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.View().Content == before {
		t.Fatal("cannot scroll status")
	}
}

func TestNestedCloseWaitsForStatusBeforeResumingParent(t *testing.T) {
	m := testModel(Page{Title: "root"})
	m.pending = 1
	m.stack[0].running = true
	done := make(chan result, 1)
	_, query := m.Update(pageRequest{page: Page{Title: "child", Status: func() string { return "done" }}, done: done})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	select {
	case <-done:
		t.Fatal("parent resumed beside active child query")
	default:
	}
	m.Update(query())
	select {
	case <-done:
	default:
		t.Fatal("parent was not released after child query finished")
	}
	_, quit := m.Update(actionResult{id: 1})
	if quit == nil {
		t.Fatal("safe exit not completed")
	}
}

func TestReplacementExitDoesNotRunNextAction(t *testing.T) {
	exit := false
	m := testModel(Page{Title: "root", Exit: func() bool { return exit }, Actions: []Action{{ID: "update", Run: func() int { exit = true; return 0 }}}})
	_, run := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, quit := m.Update(run())
	if quit == nil || !m.closing {
		t.Fatal("replacement continued old runtime")
	}
}

func TestNonTTYDoesNotConsumeScript(t *testing.T) {
	input := strings.NewReader("shell source should remain unread")
	if _, err := New(input, nil); err == nil {
		t.Fatal("accepted pipe as terminal")
	}
	if input.Len() != len("shell source should remain unread") {
		t.Fatal("consumed pipe")
	}
}

func TestNoColorAndStickySummary(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	m := testModel(Page{Title: "Nido Proxy", Status: func() string { return "运行中 · Mihomo\n" + strings.Repeat("group -> final: 90ms\n", 30) }, Actions: []Action{{ID: "a", Label: "Quick Start"}}})
	m.Update(m.Init()())
	m.Update(tea.WindowSizeMsg{Width: 42, Height: 12})
	view := m.View().Content
	if strings.Contains(view, "\x1b") {
		t.Fatal("NO_COLOR view contains styled escapes")
	}
	if !strings.Contains(view, "Nido Proxy") || !strings.Contains(view, "运行中 · Mihomo") || !strings.Contains(view, "> Quick Start") {
		t.Fatalf("summary/action disappeared: %q", view)
	}
}

func TestBracketedPasteOnlyEditsActiveTextAndKeepsLimits(t *testing.T) {
	m := testModel(Page{Title: "paste"})
	paste := tea.PasteMsg{Content: "中a\n\t\x00bcdefgh"}
	m.Update(paste)
	if m.input.Value() != "" {
		t.Fatal("paste changed inactive input")
	}
	selection := make(chan result, 1)
	m.Update(promptRequest{label: "确认", options: []string{"取消", "确认"}, done: selection})
	m.Update(tea.PasteMsg{Content: "\r\nconfirm"})
	if m.prompt.index != 0 {
		t.Fatal("paste selected an action")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	answer := make(chan result, 1)
	m.Update(promptRequest{label: "路径", text: true, done: answer})
	m.input.CharLimit = 6
	m.Update(paste)
	if value := m.input.Value(); value != "中a  bc" {
		t.Fatalf("paste sanitizing/limit changed: %q", value)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m.Update(tea.PasteMsg{Content: "must-not-insert"})
	if value := m.input.Value(); value != "中a  bc" {
		t.Fatalf("paste edited cancelled input: %q", value)
	}
	m.Update(promptRequest{label: "关闭前输入", text: true, done: make(chan result, 1)})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m.Update(tea.PasteMsg{Content: "must-not-insert"})
	if m.input.Value() != "" {
		t.Fatal("paste edited closing input")
	}
}

func TestUnicodeAndEditingKeysDelegateToTextInput(t *testing.T) {
	m := testModel(Page{Title: "editing"})
	m.Update(promptRequest{label: "路径", text: true, fallback: "ab中", done: make(chan result, 1)})
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.Update(tea.KeyPressMsg{Code: '文', Text: "文"})
	if m.input.Value() != "ab文中" {
		t.Fatal(m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: '前', Text: "前"})
	m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: '尾', Text: "尾"})
	if m.input.Value() != "前ab中尾" {
		t.Fatal(m.input.Value())
	}
	if m.input.KeyMap.Paste.Enabled() {
		t.Fatal("system clipboard shortcut was enabled")
	}
}

func TestDynamicActionsRefreshAfterLifecycleAndPreserveFocus(t *testing.T) {
	installed := false
	queries, factories, installs, removes := 0, 0, 0, 0
	page := Page{Title: "Dynamic Proxy", Status: func() string { queries++; return "state" }}
	page.ActionsFunc = func() []Action {
		factories++
		if !installed {
			return []Action{{ID: "install", Label: "Install", Run: func() int { installed = true; installs++; return 0 }}}
		}
		return []Action{{ID: "start", Label: "Start"}, {ID: "remove", Label: "Remove", Run: func() int { installed = false; removes++; return 0 }}}
	}
	m := testModel(page)
	first := m.Init()
	if queries != 0 || factories != 0 || first == nil {
		t.Fatal("state read in UI construction")
	}
	m.Update(first())
	if queries != 1 || factories != 1 || len(m.current().page.Actions) != 1 {
		t.Fatal("initial actions missing")
	}
	_, install := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if installed {
		t.Fatal("action ran on UI goroutine")
	}
	_, refresh := m.Update(install())
	if factories != 1 {
		t.Fatal("factory ran in event loop")
	}
	if _, run := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); run != nil {
		t.Fatal("stale install ran during refresh")
	}
	m.Update(refresh())
	if !installed || installs != 1 || m.current().page.Actions[0].ID != "start" {
		t.Fatal("install actions did not refresh")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, remove := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, refresh = m.Update(remove())
	m.Update(refresh())
	if installed || removes != 1 || m.current().selected != 0 || m.current().page.Actions[0].ID != "install" {
		t.Fatal("remove left stale actions or focus")
	}
	for i := 0; i < 100; i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m.View()
	}
	if queries != 3 || factories != 3 {
		t.Fatal("navigation/render polled state", queries, factories)
	}
}

func TestDynamicActionsKeepStableFocusAndSupportEmptyList(t *testing.T) {
	actions := []Action{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}
	m := testModel(Page{Title: "dynamic only", ActionsFunc: func() []Action { return actions }})
	m.Update(m.Init()())
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	actions = []Action{{ID: "b", Label: "B"}, {ID: "a", Label: "A"}}
	m.Update(m.status(m.current())())
	if m.current().selected != 0 || m.current().page.Actions[0].ID != "b" {
		t.Fatal("focus was tied to old index instead of action ID")
	}
	actions = nil
	m.Update(m.status(m.current())())
	if m.current().selected != 0 || len(m.current().page.Actions) != 0 {
		t.Fatal("empty list left stale actions")
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("empty page ran stale action")
	}
}

func TestScriptDriverRefreshesDynamicActionsAfterEachAction(t *testing.T) {
	installed := false
	installs, removes := 0, 0
	driver := NewScriptDriver(strings.NewReader("1\n1\n1\n0\n"), nil)
	_, err := driver.Page(Page{Title: "lifecycle", ActionsFunc: func() []Action {
		if !installed {
			return []Action{{ID: "install", Run: func() int { installs++; installed = true; return 0 }}}
		}
		return []Action{{ID: "remove", Run: func() int { removes++; installed = false; return 0 }}}
	}})
	if err != nil || !installed || installs != 2 || removes != 1 {
		t.Fatal("script driver retained stale actions", installed, installs, removes, err)
	}
}
