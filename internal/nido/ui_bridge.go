package nido

import (
	"errors"
	"fmt"
	"sync"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

type menuAction struct {
	id, label, group string
	run              func() int
}

// runInteractiveTask gives a complete command, including its multiple prompts
// and slow operations, one terminal session. It is also safe inside a page.
func (c *CLI) runInteractiveTask(run func() int) int {
	if c.UI != nil || c.uiSession != nil {
		return run()
	}
	s, err := c.newUISession()
	if err != nil {
		return c.fail(err)
	}
	restore := c.releaseUI(s)
	defer restore()
	code, err := s.RunTask(run)
	restore()
	if err != nil {
		return c.fail(err)
	}
	return code
}

// withUI shares the current terminal owner, or creates one for a standalone
// interactive command. Explicit --yes/--json commands never enter this bridge.
func (c *CLI) withUI(run func(terminalui.Driver) error) (err error) {
	defer func() {
		if errors.Is(err, terminalui.ErrClosed) {
			c.promptClosed = true
		}
	}()
	if c.UI != nil {
		return run(c.UI)
	}
	if c.uiSession != nil {
		return run(c.uiSession)
	}
	s, err := c.newUISession()
	if err != nil {
		c.promptClosed = true
		return err
	}
	restore := c.releaseUI(s)
	defer restore()
	var taskErr error
	_, err = s.RunTask(func() int { taskErr = run(s); return 0 })
	if err != nil {
		c.promptClosed = true
		return err
	}
	return taskErr
}

func (c *CLI) newUISession() (*terminalui.Session, error) {
	s, err := terminalui.New(c.In, c.Err)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (c *CLI) releaseUI(s *terminalui.Session) func() {
	previousOut, previousErr := c.Out, c.Err
	c.uiSession = s
	c.Out = s
	c.Err = s
	var once sync.Once
	return func() { once.Do(func() { c.Out = previousOut; c.Err = previousErr; c.uiSession = nil }) }
}

func (c *CLI) textPrompt(label, fallback string) (string, error) {
	var text string
	err := c.withUI(func(ui terminalui.Driver) error { var err error; text, err = ui.Input(label, fallback); return err })
	return text, err
}

func (c *CLI) secretPrompt(label, fallback string) (string, error) {
	var text string
	err := c.withUI(func(ui terminalui.Driver) error {
		secret, ok := ui.(terminalui.SecretDriver)
		if !ok {
			return fmt.Errorf("当前交互驱动不支持安全隐藏输入")
		}
		var err error
		text, err = secret.InputSecret(label, fallback)
		return err
	})
	return text, err
}

func (c *CLI) confirmPrompt(what string) error {
	choice, err := c.choose(what, []string{"取消", "确认"}, 0)
	if err != nil {
		return err
	}
	if choice != 1 {
		return terminalui.ErrCancelled
	}
	return nil
}

func (c *CLI) componentPage(title string, status func() string, actions []menuAction) int {
	return c.showComponentPage(terminalui.Page{Title: title, Status: status, Actions: terminalActions(actions)})
}

func terminalActions(actions []menuAction) []terminalui.Action {
	result := make([]terminalui.Action, 0, len(actions))
	for _, a := range actions {
		result = append(result, terminalui.Action{ID: a.id, Label: a.label, Group: a.group, Run: a.run})
	}
	return result
}

func (c *CLI) componentDynamicPage(title string, status func() string, actions func() []menuAction) int {
	return c.showComponentPage(terminalui.Page{Title: title, Status: status, ActionsFunc: func() []terminalui.Action { return terminalActions(actions()) }})
}

func (c *CLI) showComponentPage(p terminalui.Page) int {
	p.Exit = func() bool { return c.restartRequired || c.promptClosed }
	if c.UI != nil {
		code, err := c.UI.Page(p)
		if errors.Is(err, terminalui.ErrClosed) {
			c.promptClosed = true
			return 0
		}
		if err != nil && !errors.Is(err, terminalui.ErrCancelled) {
			return c.fail(err)
		}
		return code
	}
	if c.uiSession != nil {
		code, err := c.uiSession.Page(p)
		if err != nil && !errors.Is(err, terminalui.ErrCancelled) {
			return c.fail(err)
		}
		return code
	}
	s, err := c.newUISession()
	if err != nil {
		return c.fail(err)
	}
	restore := c.releaseUI(s)
	defer restore()
	code, err := s.Run(p)
	restore()
	if err != nil {
		return c.fail(fmt.Errorf("终端交互失败: %w", err))
	}
	return code
}
