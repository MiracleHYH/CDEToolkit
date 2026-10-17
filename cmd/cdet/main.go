package main

import (
	"github.com/MiracleHYH/CDEToolkit/internal/cdet"
	"os"
)

func main() { os.Exit((cdet.CLI{Out: os.Stdout, Err: os.Stderr}).Run(os.Args[1:])) }
