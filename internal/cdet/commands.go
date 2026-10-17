package cdet

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

// CommandSpec deliberately keeps environment and input out of persisted plans.
type CommandSpec struct {
	Path  string
	Args  []string
	Env   []string
	Input []byte
}

type CommandRunner func(CommandSpec) ([]byte, error)

const maxCommandOutput = 1 << 20

func (m *Manager) run(spec CommandSpec) ([]byte, error) {
	runner := m.Runner
	if runner == nil {
		runner = runCommand
	}
	out, err := runner(spec)
	if err != nil {
		// A subprocess or a test runner may include credentials in its error.
		// Do not include arguments, environment, stdin, or captured output.
		return nil, fmt.Errorf("外部命令执行失败: %s", filepath.Base(spec.Path))
	}
	if len(out) > maxCommandOutput {
		return nil, fmt.Errorf("外部命令输出过大")
	}
	return out, nil
}

type commandOutput struct {
	mu     sync.Mutex
	data   bytes.Buffer
	limit  int
	cancel context.CancelFunc
	full   bool
}

func (w *commandOutput) Write(p []byte) (int, error) {
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

func commandPath(spec CommandSpec) (string, error) {
	if filepath.IsAbs(spec.Path) {
		return spec.Path, nil
	}
	if spec.Path == "" || strings.ContainsAny(spec.Path, "/\\\x00") {
		return "", fmt.Errorf("命令路径无效")
	}
	// exec.LookPath reads the parent's PATH, rather than CommandSpec.Env.
	// Resolve against the operation's PATH without changing global state.
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, spec.Path)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("命令不可用")
}

func runCommand(spec CommandSpec) ([]byte, error) {
	timeout := 30 * time.Second
	if spec.Path == "/bin/sh" {
		// The upstream installer has its own download timeouts as well.
		timeout = 10 * time.Minute
	}
	return runCommandTimeout(spec, timeout)
}

func runCommandTimeout(spec CommandSpec, timeout time.Duration) ([]byte, error) {
	path, err := commandPath(spec)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, spec.Args...)
	cmd.Env = spec.Env
	cmd.Stdin = bytes.NewReader(spec.Input)
	// Cancel only this operation and its children, including an installer's
	// downloader, so a timeout cannot leave it writing packages in the back.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return os.ErrProcessDone
	}
	cmd.WaitDelay = 2 * time.Second
	out := &commandOutput{limit: maxCommandOutput, cancel: cancel}
	diagnostics := &commandOutput{limit: maxCommandOutput, cancel: cancel}
	// stdout can be executable installer input. Never append diagnostics to it.
	// Both streams remain bounded and credentials in stderr are never returned.
	cmd.Stdout, cmd.Stderr = out, diagnostics
	err = cmd.Run()
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
