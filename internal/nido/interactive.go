package nido

import (
	"os"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

// Menus only fill missing choices. Explicit arguments, previews and scripts
// continue through the existing validation and mutation paths.
func (c *CLI) interactive(o cliOptions) bool {
	if o.yes || o.json || o.dry {
		return false
	}
	if c.UI != nil || c.uiSession != nil {
		return true
	}
	if f, ok := c.In.(*os.File); ok {
		return terminalInput(f)
	}
	if c.In != nil {
		return false
	}
	if terminalInput(os.Stdin) {
		return true
	}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	return terminalInput(f)
}

func (c *CLI) choose(label string, options []string, fallback int) (int, error) {
	var index int
	err := c.withUI(func(ui terminalui.Driver) error {
		var err error
		index, err = ui.Select(label, options, fallback)
		return err
	})
	return index, err
}

func (c *CLI) setting(label, fallback string) (string, error) {
	return c.textPrompt(label, fallback)
}
