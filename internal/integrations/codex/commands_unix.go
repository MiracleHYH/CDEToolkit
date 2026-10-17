//go:build linux || darwin

package codex

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxCommandOutput = 1 << 20

// Each command is confined to its own process group. Cancellation only targets
// this operation and its descendants, never existing Codex processes.
func runCommand(path string, args, env []string, input []byte) ([]byte, error) {
	timeout := 5 * time.Second
	if path == "/bin/sh" {
		timeout = 10 * time.Minute
	} else if filepath.Base(path) == "curl" {
		timeout = 90 * time.Second
	}
	return runCommandTimeout(path, args, env, input, timeout)
}

func commandPath(path string, env []string) (string, error) {
	if invalidPath(path) || path == "" {
		return "", fmt.Errorf("命令路径无效")
	}
	if filepath.IsAbs(path) {
		if !executable(path) {
			return "", fmt.Errorf("命令不可用")
		}
		return path, nil
	}
	if strings.ContainsAny(path, "/\\") {
		return "", fmt.Errorf("命令路径无效")
	}
	for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
		if !filepath.IsAbs(dir) || invalidPath(dir) {
			continue
		}
		candidate := filepath.Join(dir, path)
		if executable(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("命令不可用")
}

type boundedOutput struct {
	mu     sync.Mutex
	data   bytes.Buffer
	limit  int
	cancel context.CancelFunc
	full   bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	remaining := w.limit - w.data.Len()
	if n > remaining {
		_, _ = w.data.Write(p[:remaining])
		w.full = true
		w.cancel()
		return n, nil
	}
	_, _ = w.data.Write(p)
	return n, nil
}

func runCommandTimeout(path string, args, env []string, input []byte, timeout time.Duration) ([]byte, error) {
	resolved, err := commandPath(path, env)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env, cmd.Stdin = env, bytes.NewReader(input)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return os.ErrProcessDone
	}
	cmd.Cancel = kill
	cmd.WaitDelay = 2 * time.Second
	out := &boundedOutput{limit: maxCommandOutput, cancel: cancel}
	diagnostics := &boundedOutput{limit: maxCommandOutput, cancel: cancel}
	// Installer stdout is executable input. Diagnostics must never enter it.
	cmd.Stdout, cmd.Stderr = out, diagnostics
	err = cmd.Run()
	if err != nil {
		_ = kill()
	}
	if out.full || diagnostics.full {
		return nil, fmt.Errorf("命令输出过大")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("命令超时或取消")
	}
	if err != nil {
		return nil, fmt.Errorf("命令执行失败")
	}
	return out.data.Bytes(), nil
}
