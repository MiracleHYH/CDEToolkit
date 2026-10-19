package terminalui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type result struct {
	index int
	text  string
	err   error
}
type pageRequest struct {
	page Page
	done chan result
}
type promptRequest struct {
	label, fallback string
	options         []string
	index           int
	text            bool
	secret          bool
	done            chan result
}
type outputMessage string
type exitRequest struct{}
type statusResult struct {
	id      int
	text    string
	actions []Action
	dynamic bool
}
type actionResult struct {
	id, code int
	task     bool
}
type frame struct {
	id               int
	page             Page
	selected         int
	status           string
	running, loading bool
	returnAfterLoad  bool
	done             chan result
}
type model struct {
	session                                           *Session
	stack                                             []*frame
	prompt                                            *promptRequest
	input                                             textinput.Model
	palette                                           palette
	width, height, nextID, pending, queries, exitCode int
	log                                               string
	closing                                           bool
	scroll                                            int
	task                                              func() int
}

func newModel(s *Session, p Page, task func() int) *model {
	t := textinput.New()
	t.CharLimit = 8192
	t.KeyMap.Paste.SetEnabled(false)
	inputStyle := t.Styles()
	inputStyle.Cursor.Blink = false
	t.SetStyles(inputStyle)
	return &model{session: s, stack: []*frame{{id: 1, page: p, loading: p.Status != nil || p.ActionsFunc != nil}}, input: t, palette: newPalette(), width: 80, height: 24, nextID: 1, task: task}
}

func (m *model) Init() tea.Cmd {
	if m.task != nil {
		m.pending++
		return m.run(1, m.task, true)
	}
	return m.status(m.stack[0])
}
func (m *model) current() *frame {
	if len(m.stack) == 0 {
		return nil
	}
	return m.stack[len(m.stack)-1]
}
func (m *model) status(f *frame) tea.Cmd {
	if f == nil || f.page.Status == nil && f.page.ActionsFunc == nil {
		return nil
	}
	f.loading = true
	m.queries++
	id, query, actions := f.id, f.page.Status, f.page.ActionsFunc
	return func() tea.Msg {
		result := statusResult{id: id, dynamic: actions != nil}
		if query != nil {
			result.text = query()
		}
		if actions != nil {
			result.actions = append([]Action(nil), actions()...)
		}
		return result
	}
}
func (m *model) run(id int, run func() int, task bool) tea.Cmd {
	return func() tea.Msg {
		code := 0
		if run != nil {
			code = run()
		}
		return actionResult{id: id, code: code, task: task}
	}
}
func (m *model) answer(r result) {
	if m.prompt != nil {
		if m.prompt.secret {
			m.input.SetValue("")
		}
		m.prompt.done <- r
		m.prompt = nil
		m.input.Blur()
	}
}
func (m *model) pop() {
	if len(m.stack) <= 1 {
		return
	}
	f := m.current()
	m.stack = m.stack[:len(m.stack)-1]
	if f.done != nil {
		f.done <- result{}
	}
}
func (m *model) stop() tea.Cmd {
	m.closing = true
	m.session.cancel()
	m.answer(result{err: ErrCancelled})
	return m.finishClose()
}

func (m *model) finishClose() tea.Cmd {
	// Unwind a nested page only after its own query/action has finished. A
	// parent action waiting in Page must not resume beside a child query.
	for len(m.stack) > 1 {
		f := m.current()
		if f.running || f.loading {
			return nil
		}
		m.pop()
	}
	if m.pending == 0 && m.queries == 0 {
		return tea.Quit
	}
	return nil
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case exitRequest:
		return m, m.stop()
	case tea.WindowSizeMsg:
		m.width = max(12, v.Width)
		m.height = max(4, v.Height)
		m.input.SetWidth(max(4, m.width-6))
		return m, nil
	case outputMessage:
		m.log += safe(string(v))
		if len(m.log) > 16384 {
			m.log = m.log[len(m.log)-16384:]
		}
		return m, nil
	case statusResult:
		m.queries--
		for _, f := range m.stack {
			if f.id == v.id {
				f.status = m.session.protectOutput(v.text)
				if v.dynamic {
					selectedID := ""
					if f.selected >= 0 && f.selected < len(f.page.Actions) {
						selectedID = f.page.Actions[f.selected].ID
					}
					f.page.Actions = v.actions
					f.selected = min(f.selected, max(0, len(v.actions)-1))
					if selectedID != "" {
						for i, a := range v.actions {
							if a.ID == selectedID {
								f.selected = i
								break
							}
						}
					}
				}
				f.loading = false
			}
		}
		if m.closing {
			return m, m.finishClose()
		}
		if f := m.current(); f != nil && f.returnAfterLoad && !f.loading {
			if len(m.stack) == 1 {
				return m, tea.Quit
			}
			m.pop()
		}
		return m, nil
	case pageRequest:
		if m.closing {
			v.done <- result{err: ErrCancelled}
			return m, nil
		}
		m.nextID++
		f := &frame{id: m.nextID, page: v.page, done: v.done}
		m.stack = append(m.stack, f)
		m.scroll = 0
		return m, m.status(f)
	case promptRequest:
		if m.closing {
			v.done <- result{err: ErrCancelled}
			return m, nil
		}
		if m.prompt != nil {
			v.done <- result{err: fmt.Errorf("不能并发提问")}
			return m, nil
		}
		m.prompt = &v
		if v.text {
			m.input.EchoMode = textinput.EchoNormal
			if v.secret {
				m.input.EchoMode = textinput.EchoPassword
			}
			m.input.SetValue(v.fallback)
			m.input.CursorEnd()
			m.input.Focus()
		}
		return m, nil
	case actionResult:
		m.pending--
		for _, f := range m.stack {
			if f.id == v.id {
				f.running = false
			}
		}
		if v.task {
			m.exitCode = v.code
			return m, tea.Quit
		}
		m.session.clearSecrets()
		if current := m.current(); current != nil && current.page.Exit != nil && current.page.Exit() {
			return m, m.stop()
		}
		if m.closing {
			return m, m.finishClose()
		}
		return m, m.status(m.current())
	case tea.PasteMsg:
		// Bracketed Paste is delivered separately from key presses in v2.
		// Delegate only to an active text field; Bubbles keeps its existing
		// rune limit and single-line/control-character sanitizer. This never
		// reads a system clipboard or interprets pasted text as menu keys.
		if !m.closing && m.prompt != nil && m.prompt.text {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(v)
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Keystroke() == "ctrl+c" {
			return m, m.stop()
		}
		if m.closing {
			return m, nil
		}
		if v.Code == tea.KeyPgUp {
			m.scroll -= max(1, m.height/2)
			return m, nil
		}
		if v.Code == tea.KeyPgDown {
			m.scroll += max(1, m.height/2)
			return m, nil
		}
		if m.prompt != nil {
			if v.Code == tea.KeyEsc {
				m.answer(result{err: ErrCancelled})
				return m, nil
			}
			if v.Code == tea.KeyEnter {
				r := result{index: m.prompt.index}
				if m.prompt.text {
					r.text = strings.TrimSpace(m.input.Value())
				}
				m.answer(r)
				return m, nil
			}
			if m.prompt.text {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(v)
				return m, cmd
			}
			switch v.Code {
			case tea.KeyUp:
				m.scroll = 0
				m.prompt.index = (m.prompt.index + len(m.prompt.options) - 1) % len(m.prompt.options)
			case tea.KeyDown:
				m.scroll = 0
				m.prompt.index = (m.prompt.index + 1) % len(m.prompt.options)
			}
			return m, nil
		}
		f := m.current()
		if f == nil {
			return m, tea.Quit
		}
		if v.Code == tea.KeyEsc {
			if f.running {
				m.log = "正在读取状态或执行操作；请等待安全完成。Ctrl+C 请求退出。\n"
				return m, nil
			}
			if f.loading {
				f.returnAfterLoad = true
				m.log = "已请求返回，正在等待状态查询安全结束。\n"
				return m, nil
			}
			if len(m.stack) == 1 {
				return m, tea.Quit
			}
			m.pop()
			return m, nil
		}
		if f.running {
			return m, nil
		}
		if len(f.page.Actions) == 0 {
			return m, nil
		}
		switch v.Code {
		case tea.KeyUp:
			m.scroll = 0
			f.selected = (f.selected + len(f.page.Actions) - 1) % len(f.page.Actions)
		case tea.KeyDown:
			m.scroll = 0
			f.selected = (f.selected + 1) % len(f.page.Actions)
		case tea.KeyEnter:
			if f.loading {
				return m, nil
			}
			f.running = true
			m.pending++
			m.log = ""
			return m, m.run(f.id, f.page.Actions[f.selected].Run, false)
		case 'r':
			if v.Mod.Contains(tea.ModCtrl) && !f.loading {
				return m, m.status(f)
			}
		}
	}
	return m, nil
}

func safe(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// View uses cell-aware wrapping and a bounded viewport. No colour, special
// font, mouse or background animation is required for navigation.
func (m *model) View() tea.View {
	view := tea.NewView(m.content())
	view.AltScreen = true
	return view
}

func (m *model) content() string {
	f := m.current()
	if f == nil {
		return ""
	}
	width := max(8, m.width-2)
	var lines []string
	appendWrapped := func(s string) { lines = append(lines, strings.Split(ansi.Wrap(safeOutput(s), width, ""), "\n")...) }
	appendWrapped(f.page.Title)
	for i := range lines {
		lines[i] = m.palette.title.Render(lines[i])
	}
	if f.loading && f.status == "" {
		appendWrapped("正在读取状态…")
	}
	if f.status != "" {
		appendWrapped(f.status)
	}
	// Keep the title and a small status summary visible while the remaining
	// status/actions scroll. Long proxy group lists cannot swallow the title.
	headerCount := min(3, len(lines))
	if m.height < 8 {
		headerCount = min(1, len(lines))
	}
	if m.log != "" {
		appendWrapped("\n" + m.log)
	}
	focus := len(lines)
	if m.prompt != nil {
		appendWrapped("\n" + m.prompt.label)
		if m.prompt.text {
			lines = append(lines, m.input.View())
			focus = len(lines) - 1
		} else {
			for i, option := range m.prompt.options {
				prefix := "  "
				if i == m.prompt.index {
					prefix = "> "
					focus = len(lines)
				}
				start := len(lines)
				appendWrapped(prefix + option)
				if i == m.prompt.index {
					for j := start; j < len(lines); j++ {
						lines[j] = m.palette.selected.Render(lines[j])
					}
				}
			}
		}
	} else if !m.closing {
		group := ""
		for i, a := range f.page.Actions {
			if a.Group != "" && a.Group != group {
				start := len(lines)
				appendWrapped("\n── " + a.Group + " ──")
				for j := start; j < len(lines); j++ {
					lines[j] = m.palette.group.Render(lines[j])
				}
				group = a.Group
			}
			prefix := "  "
			if i == f.selected {
				prefix = "> "
				focus = len(lines)
			}
			start := len(lines)
			appendWrapped(prefix + a.Label)
			if i == f.selected {
				for j := start; j < len(lines); j++ {
					lines[j] = m.palette.selected.Render(lines[j])
				}
			}
		}
	}
	footer := "↑↓ 选择 · Enter 执行 · Esc 返回 · Ctrl+C 退出"
	if m.prompt != nil {
		footer = "↑↓ 选择 · Enter 确认 · Esc 取消"
		if m.prompt.text {
			footer = "输入内容 · Enter 确认 · Esc 取消"
		}
	}
	if f.running && m.prompt == nil {
		footer = "正在执行；Ctrl+C 请求安全退出"
	}
	if m.closing {
		footer = "正在等待当前操作安全完成…"
	}
	room := max(1, m.height-2-headerCount)
	start := headerCount
	if len(lines)-headerCount > room {
		start = max(headerCount, focus-room/2)
		start = min(start, len(lines)-room)
		start = max(headerCount, min(start+m.scroll, len(lines)-room))
		footer = "PgUp/PgDn 滚动 · " + footer
	}
	end := min(len(lines), start+room)
	visible := append(append([]string(nil), lines[:headerCount]...), lines[start:end]...)
	return strings.Join(visible, "\n") + "\n" + m.palette.muted.Render(ansi.Truncate(footer, width, ""))
}
