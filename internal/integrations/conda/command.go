package conda

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

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func commandPath(name string, env []string) (string, error) {
	if name == "conda" {
		if path, ok := envValue(env, "CONDA_EXE"); ok && path != "" {
			if !filepath.IsAbs(path) || strings.IndexFunc(path, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || !executable(path) {
				return "", fmt.Errorf("CONDA_EXE 不是可执行的绝对路径")
			}
			return path, nil
		}
	}
	path, _ := envValue(env, "PATH")
	names := []string{name}
	if name == "python" {
		names = append(names, "python3")
	}
	for _, candidateName := range names {
		for _, dir := range filepath.SplitList(path) {
			if !filepath.IsAbs(dir) {
				continue
			}
			candidate := filepath.Join(dir, candidateName)
			if executable(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("当前 PATH 中没有可用的 %s", name)
}

type boundedOutput struct {
	mu     sync.Mutex
	data   bytes.Buffer
	full   bool
	cancel context.CancelFunc
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(data)
	remaining := maxOutput - w.data.Len()
	if n > remaining {
		_, _ = w.data.Write(data[:remaining])
		w.full = true
		w.cancel()
	} else {
		_, _ = w.data.Write(data)
	}
	return n, nil
}

func runCommand(name string, args, env []string) ([]byte, error) {
	return runCommandContext(context.Background(), name, args, env)
}

func runCommandContext(parent context.Context, name string, args, env []string) ([]byte, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, fmt.Errorf("只读命令已取消: %w", err)
	}
	path, err := commandPath(name, env)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Env = env
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != syscall.ESRCH {
			return err
		}
		return os.ErrProcessDone
	}
	command.Cancel = kill
	command.WaitDelay = time.Second
	out := &boundedOutput{cancel: cancel}
	diagnostics := &boundedOutput{cancel: cancel}
	command.Stdout, command.Stderr = out, diagnostics
	err = command.Run()
	if err != nil {
		_ = kill()
	}
	if out.full || diagnostics.full {
		return nil, fmt.Errorf("命令输出超过 1 MiB")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("只读命令超时或取消（最长 10 秒）: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("只读命令执行失败")
	}
	return out.data.Bytes(), nil
}
