package builtin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type Command struct {
	Program, Dir, Log string
	Args, Env         []string
}

// Started is an OS-provided process birth identifier, not saved application
// state. It prevents a recycled PID from authorizing a signal.
type Process struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    string `json:"started"`
}

type Runner interface {
	Start(Command) (Process, error)
	Inspect(pid int) (Process, error)
	Stop(Process) error
}

// EndpointOwner is optional for fake runners. Without ownership verification a
// running Proxy must be stopped before downloaded-file updates are permitted.
type EndpointOwner interface {
	OwnsEndpoint(Process, string) (bool, error)
}

type osRunner struct{}

func (osRunner) Start(c Command) (Process, error) {
	log, err := os.OpenFile(c.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return Process{}, err
	}
	defer log.Close()
	info, err := log.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Process{}, fmt.Errorf("组件日志不是普通文件")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return Process{}, fmt.Errorf("组件日志不能是共享硬链接")
	}
	cmd := exec.Command(c.Program, c.Args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = c.Dir, c.Env, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return Process{}, fmt.Errorf("组件启动失败")
	}
	p, err := inspectProcess(cmd.Process.Pid)
	if err != nil || p.Executable != c.Program {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Process{}, fmt.Errorf("组件启动后无法确认进程归属")
	}
	go func() { _ = cmd.Wait() }()
	return p, nil
}

func (osRunner) Inspect(pid int) (Process, error) { return inspectProcess(pid) }

func (osRunner) Stop(p Process) error {
	actual, err := inspectProcess(p.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || actual != p {
		return fmt.Errorf("无法确认进程归属，未发送停止信号")
	}
	if err = syscall.Kill(p.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		actual, err = inspectProcess(p.PID)
		if errors.Is(err, os.ErrNotExist) || err == nil && actual != p {
			return nil
		}
		if err != nil {
			return fmt.Errorf("停止信号已发送，无法确认组件是否退出")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("组件尚未退出，保留进程记录；未强制终止")
}

func (osRunner) OwnsEndpoint(p Process, endpoint string) (bool, error) {
	actual, err := inspectProcess(p.PID)
	if err != nil || actual != p {
		return false, err
	}
	return processOwnsEndpoint(p.PID, endpoint)
}
