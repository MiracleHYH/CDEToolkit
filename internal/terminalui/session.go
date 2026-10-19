// Package terminalui provides Nido's single terminal owner. Business operations
// run outside the event loop; their questions are messages to the same program.
package terminalui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

var ErrCancelled = errors.New("操作已取消")
var ErrClosed = errors.New("交互终端已关闭")

type Action struct {
	ID, Label, Group string
	Run              func() int
}

type Page struct {
	Title   string
	Status  func() string
	Actions []Action
	// ActionsFunc is queried beside Status on entry and after actions. It
	// replaces Actions without polling or reading business state in View.
	ActionsFunc func() []Action
	// Exit is checked after a completed action, e.g. after replacing Nido's
	// running executable. It is not polled in the background.
	Exit func() bool
}

// Driver is an explicit injection seam for isolated tests and a synchronous
// bridge for existing component operations. Production always uses Session.
type Driver interface {
	Page(Page) (int, error)
	Select(string, []string, int) (int, error)
	Input(string, string) (string, error)
}

// SecretDriver is optional so business-test drivers do not silently acquire
// a plaintext fallback when a component starts asking for credentials.
type SecretDriver interface {
	InputSecret(string, string) (string, error)
}

type Session struct {
	input      io.Reader
	output     io.Writer
	owned      *os.File
	program    *tea.Program
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	once       sync.Once
	capture    bool
	outputMu   sync.Mutex
	transcript string
	secrets    []string
}

// New requires an actual terminal. When stdin is a pipe, the controlling
// terminal is used without consuming any bytes from the pipe.
func New(input io.Reader, output io.Writer) (*Session, error) {
	var owned *os.File
	if input == nil {
		if term.IsTerminal(os.Stdin.Fd()) {
			input = os.Stdin
		} else {
			f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				return nil, fmt.Errorf("交互需要真实终端；自动化请使用明确子命令和 --yes")
			}
			owned, input, output = f, f, f
		}
	}
	f, ok := input.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		if owned != nil {
			_ = owned.Close()
		}
		return nil, fmt.Errorf("交互需要真实终端；自动化请使用明确子命令和 --yes")
	}
	if output == nil {
		output = f
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{input: input, output: output, owned: owned, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}

func (s *Session) Context() context.Context { return s.ctx }

func (s *Session) RequestExit() { _ = s.send(exitRequest{}) }

func (s *Session) Run(page Page) (int, error) { return s.run(page, nil) }

// RunTask gives a standalone component command the same responsive terminal
// lifecycle, including questions, progress output and safe terminal restoration.
func (s *Session) RunTask(task func() int) (int, error) {
	return s.run(Page{Title: "Nido"}, task)
}

func (s *Session) run(page Page, task func() int) (int, error) {
	defer s.once.Do(func() {
		s.cancel()
		s.clearSecrets()
		close(s.done)
		if s.owned != nil {
			_ = s.owned.Close()
		}
	})
	s.capture = task != nil
	m := newModel(s, page, task)
	profile := colorprofile.ASCII
	if colorEnabled() {
		profile = colorprofile.ANSI
	}
	s.program = tea.NewProgram(m, tea.WithInput(s.input), tea.WithOutput(s.output), tea.WithColorProfile(profile), tea.WithFPS(30), tea.WithFilter(func(current tea.Model, msg tea.Msg) tea.Msg {
		state := current.(*model)
		switch msg.(type) {
		case tea.InterruptMsg:
			return exitRequest{}
		case tea.QuitMsg:
			if state.pending > 0 || state.queries > 0 {
				return exitRequest{}
			}
		}
		return msg
	}))
	final, err := s.program.Run()
	// Program.Run restores the terminal before returning. Standalone commands
	// must leave their result in the normal screen; menu actions keep it only
	// in the page. Capture is synchronous with Write, not a UI-message race.
	if task != nil {
		if replayErr := s.replay(); err == nil && replayErr != nil {
			err = replayErr
		}
	}
	if err != nil {
		return 1, err
	}
	return final.(*model).exitCode, nil
}

func (s *Session) send(msg tea.Msg) error {
	select {
	case <-s.ctx.Done():
		return ErrCancelled
	case <-s.done:
		return ErrCancelled
	default:
	}
	if s.program == nil {
		return fmt.Errorf("交互尚未启动")
	}
	s.program.Send(msg)
	return nil
}

func (s *Session) Page(page Page) (int, error) {
	done := make(chan result, 1)
	if err := s.send(pageRequest{page: page, done: done}); err != nil {
		return 0, err
	}
	select {
	case r := <-done:
		return r.index, r.err
	case <-s.done:
		return 0, ErrCancelled
	}
}

func (s *Session) Select(label string, options []string, fallback int) (int, error) {
	if len(options) == 0 || fallback < 0 || fallback >= len(options) {
		return 0, fmt.Errorf("无效的交互选项")
	}
	r, err := s.prompt(promptRequest{label: label, options: append([]string(nil), options...), index: fallback})
	return r.index, err
}

func (s *Session) Input(label, fallback string) (string, error) {
	r, err := s.prompt(promptRequest{label: label, fallback: fallback, text: true})
	return r.text, err
}

func (s *Session) InputSecret(label, fallback string) (string, error) {
	s.rememberSecret(fallback)
	r, err := s.prompt(promptRequest{label: label, fallback: fallback, text: true, secret: true})
	if err == nil {
		s.rememberSecret(r.text)
	}
	return r.text, err
}

func (s *Session) prompt(p promptRequest) (result, error) {
	p.done = make(chan result, 1)
	if err := s.send(p); err != nil {
		return result{}, err
	}
	select {
	case r := <-p.done:
		return r, r.err
	case <-s.ctx.Done():
		return result{}, ErrCancelled
	case <-s.done:
		return result{}, ErrCancelled
	}
}

// Output is bounded twice: each write and the model's retained log. Components
// do not write directly to the terminal while Bubble Tea owns it.
func (s *Session) Write(data []byte) (int, error) {
	n := len(data)
	text := s.protectOutput(strings.ToValidUTF8(string(data), "?"))
	if len(text) > 8192 {
		text = strings.ToValidUTF8(text[len(text)-8192:], "?")
	}
	if s.capture {
		s.outputMu.Lock()
		s.transcript += text
		if len(s.transcript) > 16384 {
			s.transcript = strings.ToValidUTF8(s.transcript[len(s.transcript)-16384:], "?")
		}
		s.outputMu.Unlock()
	}
	if err := s.send(outputMessage(text)); err != nil {
		return n, nil
	}
	return n, nil
}

func (s *Session) replay() error {
	s.outputMu.Lock()
	text := strings.TrimRight(s.transcript, "\r\n")
	s.transcript = ""
	s.outputMu.Unlock()
	text = s.protectOutput(text)
	if text == "" {
		return nil
	}
	_, err := fmt.Fprint(s.output, "\n"+text+"\n")
	return err
}

func (s *Session) rememberSecret(value string) {
	if value == "" {
		return
	}
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	for _, known := range s.secrets {
		if known == value {
			return
		}
	}
	s.secrets = append(s.secrets, value)
}

func (s *Session) clearSecrets() { s.outputMu.Lock(); s.secrets = nil; s.outputMu.Unlock() }

func (s *Session) protectOutput(value string) string {
	s.outputMu.Lock()
	for _, secret := range s.secrets {
		value = strings.ReplaceAll(value, secret, "[隐藏]")
	}
	s.outputMu.Unlock()
	return safeOutput(value)
}
