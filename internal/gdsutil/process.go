package gdsutil

import (
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"os"
	"strings"
	"syscall"
	"time"
)

var errProcessTransition = errors.New("进程内核信息处于过渡状态")

func Inspect(pid int) (gdsapi.Process, error) { return inspectProcess(pid) }
func SameBirth(a, b gdsapi.Process) bool {
	return a.PID == b.PID && a.UID == b.UID && a.Boot == b.Boot && a.Started == b.Started
}
func sameBirth(a, b gdsapi.Process) bool { return SameBirth(a, b) }

// Stop targets only a fully verified birth. TERM has a bounded cleanup window;
// KILL is appropriate for these disposable monitor/worker processes only.
func Stop(p gdsapi.Process) error {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := signalProcess(p, sig); err != nil {
			return err
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			actual, err := Inspect(p.PID)
			if errors.Is(err, os.ErrNotExist) || err == nil && !SameBirth(actual, p) {
				return nil
			}
			if err != nil && !errors.Is(err, errProcessTransition) {
				return fmt.Errorf("无法确认 GDS 进程退出: %w", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return fmt.Errorf("GDS 进程尚未确认退出；保留身份记录")
}
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
