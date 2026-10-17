package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Command contains no shell text. Input is the captured candidate configuration.
type Command struct {
	Program, Dir, Log, Launcher string
	Args, Env                   []string
	Input                       []byte
	Timeout                     time.Duration
}

// Process is a kernel identity, not merely a PID. Executable may change exactly
// once from the recorded launcher to the intended core through exec.
type Process struct {
	PID               int    `json:"pid"`
	UID               uint32 `json:"uid"`
	Boot              string `json:"boot"`
	Started           string `json:"started"`
	Executable        string `json:"executable"`
	ExecutableDeleted bool   `json:"executable_deleted,omitempty"`
}

type Runner interface {
	Run(Command) ([]byte, error)
	Start(Command, func(Process) error) (Process, <-chan error, error)
	Inspect(int) (Process, error)
	Stop(Process) error
	OwnsEndpoint(Process, string) (bool, error)
}

// OSRunner uses an authorized, one-shot launcher and never starts a daemon.
type OSRunner struct{}

// Only an observed kernel metadata transition uses this sentinel. Permission
// failures and an unexpected executable remain ordinary, non-retryable errors.
// A transition never proves that the process has exited or authorizes a signal.
var errProcessTransition = errors.New("进程内核信息处于过渡状态")

func processMetadataError(directory string, cause error) error {
	if !errors.Is(cause, os.ErrNotExist) {
		return cause
	}
	info, err := os.Stat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("进程目录无效")
	}
	// Never retain ENOENT in this error chain: a missing exe/stat/status is
	// insufficient to conclude that the entire thread group has disappeared.
	return fmt.Errorf("进程子项暂不可读 (%v): %w", cause, errProcessTransition)
}

func singleProcessThread(status []byte) bool {
	found := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Threads:") {
			if found || strings.TrimSpace(strings.TrimPrefix(line, "Threads:")) != "1" {
				return false
			}
			found = true
		}
	}
	return found
}

// cappedOutput bounds memory without making the child block on full output.
type cappedOutput struct {
	mu sync.Mutex
	b  []byte
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if room := (16 << 10) - len(b.b); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.b = append(b.b, p...)
	}
	return n, nil
}

func (OSRunner) Run(c Command) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Program, c.Args...)
	cmd.WaitDelay = time.Second
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, bytes.NewReader(c.Input)
	var output cappedOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.b, fmt.Errorf("核心调用超时")
	}
	return output.b, err
}

func openProcessLog(path string) (*os.File, error) {
	// O_NONBLOCK is necessary even with O_NOFOLLOW: a preexisting FIFO must
	// never suspend start before fstat can reject it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, fmt.Errorf("无法安全打开 Proxy 日志: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, fmt.Errorf("Proxy 日志必须是当前用户的普通文件，且不能是共享硬链接")
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !singleLink(current) || !os.SameFile(info, current) {
		f.Close()
		return nil, fmt.Errorf("Proxy 日志路径在打开期间发生变化")
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (OSRunner) Start(c Command, commit func(Process) error) (Process, <-chan error, error) {
	if c.Launcher == "" || !filepath.IsAbs(c.Launcher) || !filepath.IsAbs(c.Program) || commit == nil {
		return Process{}, nil, fmt.Errorf("缺少受管启动器或启动记录回调")
	}
	log, err := openProcessLog(c.Log)
	if err != nil {
		return Process{}, nil, err
	}
	defer log.Close()
	read, write, err := os.Pipe()
	if err != nil {
		return Process{}, nil, err
	}
	defer read.Close()
	defer write.Close()
	args := append([]string{launcherArgument, c.Program}, c.Args...)
	cmd := exec.Command(c.Launcher, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = c.Dir, c.Env, log, log
	cmd.ExtraFiles = []*os.File{read}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Process{}, nil, fmt.Errorf("Proxy 启动器启动失败: %w", err)
	}
	read.Close()
	p, err := inspectProcess(cmd.Process.Pid)
	if err != nil || p.Executable != c.Launcher {
		write.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Process{}, nil, fmt.Errorf("无法确认 Proxy 启动器身份")
	}
	// The child cannot exec before this callback persists the identity. EOF
	// cancels the launch if the callback fails or the parent disappears.
	if err := commit(p); err != nil {
		write.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return p, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	if _, err := write.Write([]byte{1}); err != nil {
		return p, done, fmt.Errorf("Proxy 启动授权失败")
	}
	write.Close()
	return p, done, nil
}

func (OSRunner) Inspect(pid int) (Process, error) { return inspectProcess(pid) }

func sameBirth(a, b Process) bool {
	return a.PID == b.PID && a.UID == b.UID && a.Boot == b.Boot && a.Started == b.Started
}

func (OSRunner) Stop(p Process) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := signalProcess(p)
		if err == nil {
			break
		}
		if !errors.Is(err, errProcessTransition) {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Proxy 进程身份过渡未完成，未发送停止信号: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return waitProcessExit(p, inspectProcess, deadline)
}

func waitProcessExit(p Process, inspect func(int) (Process, error), deadline time.Time) error {
	for time.Now().Before(deadline) {
		actual, err := inspect(p.PID)
		if errors.Is(err, os.ErrNotExist) || err == nil && !sameBirth(actual, p) {
			return nil
		}
		if err != nil && !errors.Is(err, errProcessTransition) {
			return fmt.Errorf("停止信号已发送，但无法确认 Proxy 已退出: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("Proxy 尚未退出，保留进程记录；未强制终止")
}

func (OSRunner) OwnsEndpoint(p Process, endpoint string) (bool, error) {
	actual, err := inspectProcess(p.PID)
	if err != nil {
		return false, err
	}
	if actual != p {
		return false, fmt.Errorf("Proxy 进程身份已变化")
	}
	owned, err := processOwnsEndpoint(p.PID, endpoint)
	if err != nil || !owned {
		return owned, err
	}
	actual, err = inspectProcess(p.PID)
	if err != nil || actual != p {
		return false, fmt.Errorf("Proxy 监听检查期间进程身份已变化")
	}
	return true, nil
}
