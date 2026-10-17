package main

import (
	"github.com/MiracleHYH/CDEToolkit/internal/nido"
	"os"
)

func main() { os.Exit((nido.CLI{Out: os.Stdout, Err: os.Stderr}).Run(os.Args[1:])) }
