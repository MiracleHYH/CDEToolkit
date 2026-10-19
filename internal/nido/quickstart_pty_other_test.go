//go:build !linux

package nido

import (
	"os/exec"
	"testing"
)

func quickStartPTY(t *testing.T, _ *exec.Cmd, _ string) ([]byte, error) {
	t.Helper()
	t.Fatal("real quick start PTY acceptance requires Linux")
	return nil, nil
}
