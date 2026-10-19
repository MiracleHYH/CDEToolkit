package terminalui

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ScriptDriver is only selected through explicit Go injection by isolation
// tests. It is never selected by terminal detection, environment or CLI flags.
type ScriptDriver struct {
	reader *bufio.Reader
	output io.Writer
}

func NewScriptDriver(input io.Reader, output io.Writer) *ScriptDriver {
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}
	return &ScriptDriver{reader: bufio.NewReader(input), output: output}
}
func (d *ScriptDriver) line(fallback string) (string, error) {
	value, err := d.reader.ReadString('\n')
	if err != nil {
		return "", ErrClosed
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return value, nil
}
func (d *ScriptDriver) Input(label, fallback string) (string, error) {
	fmt.Fprintf(d.output, "%s [%s]\n", label, fallback)
	value, err := d.line(fallback)
	if strings.EqualFold(value, "q") {
		return "", ErrCancelled
	}
	return value, err
}

func (d *ScriptDriver) InputSecret(label, fallback string) (string, error) {
	fmt.Fprintf(d.output, "%s [隐藏输入]\n", label)
	value, err := d.line(fallback)
	if strings.EqualFold(value, "q") {
		return "", ErrCancelled
	}
	return value, err
}
func (d *ScriptDriver) Select(label string, options []string, fallback int) (int, error) {
	if len(options) == 0 || fallback < 0 || fallback >= len(options) {
		return 0, fmt.Errorf("无效的交互选项")
	}
	fmt.Fprintln(d.output, label)
	for i, option := range options {
		fmt.Fprintf(d.output, "  %d) %s\n", i+1, option)
	}
	value, err := d.line(strconv.Itoa(fallback + 1))
	if err != nil {
		return 0, err
	}
	if strings.EqualFold(value, "q") {
		return 0, ErrCancelled
	}
	if len(options) == 2 && options[0] == "取消" && options[1] == "确认" {
		if strings.EqualFold(value, "y") {
			return 1, nil
		}
		if strings.EqualFold(value, "n") {
			return 0, nil
		}
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > len(options) {
		return 0, ErrCancelled
	}
	return n - 1, nil
}
func (d *ScriptDriver) Page(page Page) (int, error) {
	for {
		if page.Exit != nil && page.Exit() {
			return 0, nil
		}
		fmt.Fprintln(d.output, page.Title)
		if page.Status != nil {
			fmt.Fprintln(d.output, page.Status())
		}
		if page.ActionsFunc != nil {
			page.Actions = page.ActionsFunc()
		}
		for i, a := range page.Actions {
			fmt.Fprintf(d.output, "  %d) %s\n", i+1, a.Label)
		}
		value, err := d.line("0")
		if err != nil {
			return 0, err
		}
		if value == "0" || strings.EqualFold(value, "q") {
			return 0, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > len(page.Actions) {
			return 0, ErrCancelled
		}
		if action := page.Actions[n-1].Run; action != nil {
			action()
		}
	}
}
