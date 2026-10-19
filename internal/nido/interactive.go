package nido

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Menus only fill missing choices. Explicit arguments, previews and scripts
// continue through the existing validation and mutation paths.
func (c *CLI) interactive(o cliOptions) bool {
	return !o.yes && !o.json && !o.dry && (c.Interactive || terminalInput(os.Stdin))
}

func (c *CLI) choose(label string, options []string, fallback int) (int, error) {
	if len(options) == 0 || fallback < 0 || fallback >= len(options) {
		return 0, fmt.Errorf("无效的交互选项")
	}
	fmt.Fprintln(c.Err, label)
	for i, option := range options {
		fmt.Fprintf(c.Err, "  %d) %s\n", i+1, option)
	}
	for {
		value, err := c.ask("选择编号，q 取消", strconv.Itoa(fallback+1))
		if err != nil {
			return 0, err
		}
		if strings.EqualFold(value, "q") || strings.EqualFold(value, "quit") {
			return 0, fmt.Errorf("操作已取消")
		}
		n, err := strconv.Atoi(value)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintln(c.Err, "请输入列表中的编号，或 q 取消。")
	}
}

func (c *CLI) setting(label, fallback string) (string, error) {
	value, err := c.ask(label+"；q 取消", fallback)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(value, "q") || strings.EqualFold(value, "quit") {
		return "", fmt.Errorf("操作已取消")
	}
	return value, nil
}
