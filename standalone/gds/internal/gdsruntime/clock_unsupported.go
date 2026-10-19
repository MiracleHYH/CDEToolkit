//go:build !linux || !amd64

package gdsruntime

import "fmt"

func bootTimeNS() (int64, error) { return 0, fmt.Errorf("GDS requires Linux amd64 CLOCK_BOOTTIME") }
