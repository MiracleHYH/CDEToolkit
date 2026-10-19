package nido

import (
	"github.com/charmbracelet/x/term"
	"os"
)

func terminalInput(file *os.File) bool { return file != nil && term.IsTerminal(file.Fd()) }
