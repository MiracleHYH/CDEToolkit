package main

import (
	"github.com/MiracleHYH/CDEToolkit/internal/stk"
	"os"
)

func main() { os.Exit((stk.CLI{Out: os.Stdout, Err: os.Stderr}).Run(os.Args[1:])) }
