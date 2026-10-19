//go:build !linux

package gdsutil

import "fmt"

func BootTimeNS() (int64, error) { return 0, fmt.Errorf("CLOCK_BOOTTIME 仅支持 Linux") }
