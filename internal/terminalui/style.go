package terminalui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

type palette struct{ title, group, selected, muted lipgloss.Style }

func newPalette() palette {
	// Use a conservative fixed ANSI palette, with no terminal background or
	// colour-capability queries. Text and the > marker carry all meaning.
	if colorEnabled() {
		return palette{title: lipgloss.NewStyle().Bold(true), group: lipgloss.NewStyle().Faint(true), selected: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")), muted: lipgloss.NewStyle().Faint(true)}
	}
	plain := lipgloss.NewStyle()
	return palette{title: plain, group: plain, selected: plain, muted: plain}
}

func colorEnabled() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor && os.Getenv("TERM") != "" && !strings.EqualFold(os.Getenv("TERM"), "dumb")
}
