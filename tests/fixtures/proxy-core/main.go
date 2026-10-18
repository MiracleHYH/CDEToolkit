// This inert executable is only a CLI installation/configuration fixture.
// It never opens a listener, forwards traffic, or starts a service.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-v" {
		fmt.Println("Mihomo Meta v0.0.0 fixture")
		return
	}
	if len(os.Args) == 6 && os.Args[1] == "-t" && os.Args[2] == "-d" && os.Args[4] == "-f" && os.Args[5] == "-" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 4<<20+1))
		if err == nil && len(data) > 0 && len(data) <= 4<<20 {
			return
		}
	}
	fmt.Fprintln(os.Stderr, "inert fixture: only version and stdin configuration checks are allowed")
	os.Exit(97)
}
