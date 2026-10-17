package main

import (
	"os"
	"server-toolkit/internal/stk"
)

func main() { os.Exit((stk.CLI{Out: os.Stdout, Err: os.Stderr}).Run(os.Args[1:])) }
