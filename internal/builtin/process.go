package builtin

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Started is an OS-provided process birth identifier, not saved application
// state. It prevents a recycled PID from authorizing a signal.
type Process struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Started    string `json:"started"`
}

type Runner interface {
	Inspect(pid int) (Process, error)
	Stop(Process) error
}

type osRunner struct{}

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
