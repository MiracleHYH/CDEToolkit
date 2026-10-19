//go:build !linux && !darwin

package nido

import "os"

func terminalInput(*os.File) bool { return false }
