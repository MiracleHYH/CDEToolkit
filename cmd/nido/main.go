package main

import (
	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
	"github.com/MiracleHYH/CDEToolkit/internal/nido"
	"os"
)

func main() {
	if handled, code := proxy.HandleLauncher(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit((nido.CLI{Out: os.Stdout, Err: os.Stderr}).Run(os.Args[1:]))
}
