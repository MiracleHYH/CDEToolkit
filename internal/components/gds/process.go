package gds

import (
	"bytes"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Command struct {
	Program, Dir, Log string
	Args, Env         []string
	Input             []byte
	Timeout           time.Duration
	ExpectedProgram   *gdsutil.Snapshot
	// Commit must persist the gated helper's exact kernel identity before any
	// metadata or native query is authorized. It is set for every managed helper.
	Commit func(gdsapi.Process) error
}
type Runner interface {
	Run(Command) ([]byte, error)
	Start(Command, func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error)
	Inspect(int) (gdsapi.Process, error)
	Stop(gdsapi.Process) error
}
type OSRunner struct{}
type UnconfirmedRunError struct {
	Process gdsapi.Process
	Problem string
}

func (e *UnconfirmedRunError) Error() string { return "GDS 子进程退出未确认: " + e.Problem }

type cappedOutput struct {
	mu       sync.Mutex
	b        []byte
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	room := (64 << 10) - len(b.b)
	if n > room {
		b.overflow = true
		p = p[:room]
	}
	b.b = append(b.b, p...)
	return n, nil
}
func (b *cappedOutput) result() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b...), b.overflow
}
func (OSRunner) Run(c Command) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	args := c.Args
	if c.Commit != nil {
		args = append([]string{"__helper", "--"}, args...)
	}
	cmd := exec.Command(c.Program, args...)
	cmd.WaitDelay = time.Second
	cmd.Dir, cmd.Env, cmd.Stdin = c.Dir, c.Env, bytes.NewReader(c.Input)
	var out, errout cappedOutput
	cmd.Stdout, cmd.Stderr = &out, &errout
	var gateReader, gateWriter *os.File
	if c.Commit != nil {
		var err error
		gateReader, gateWriter, err = os.Pipe()
		if err != nil {
			return nil, err
		}
		defer gateReader.Close()
		defer gateWriter.Close()
		cmd.ExtraFiles = []*os.File{gateReader}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	identity, identityErr := gdsutil.Inspect(cmd.Process.Pid)
	if identity.PID == 0 {
		identity.PID = cmd.Process.Pid
	}
	if c.Commit != nil {
		gateReader.Close()
		var authorizationErr error
		if identityErr != nil || identity.Executable != c.Program || identity.ExecutableDeleted {
			authorizationErr = fmt.Errorf("无法核验 GDS 辅助进程授权门前身份: %v", identityErr)
		} else {
			authorizationErr = c.Commit(identity)
		}
		if authorizationErr == nil {
			_, authorizationErr = gateWriter.Write([]byte{1})
		}
		gateWriter.Close()
		if authorizationErr != nil {
			_ = cmd.Process.Kill()
			select {
			case <-done:
				return nil, authorizationErr
			case <-time.After(time.Second):
				return nil, &UnconfirmedRunError{Process: identity, Problem: authorizationErr.Error() + "; 授权失败后未能回收"}
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-done:
	case <-timer.C:
		_ = cmd.Process.Kill()
		cleanup := time.NewTimer(time.Second)
		defer cleanup.Stop()
		select {
		case <-done:
			return nil, fmt.Errorf("GDS 子进程调用超时，已确认退出")
		case <-cleanup.C:
			return nil, &UnconfirmedRunError{Process: identity, Problem: "超时后未能回收"}
		}
	}
	data, overflow := out.result()
	if overflow {
		return nil, fmt.Errorf("GDS 子进程输出超过限制")
	}
	if err != nil {
		return data, fmt.Errorf("GDS 子进程失败: %w", err)
	}
	return data, nil
}
func (OSRunner) Start(c Command, commit func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error) {
	if commit == nil {
		return gdsapi.Process{}, nil, fmt.Errorf("缺少 GDS 启动身份提交回调")
	}
	if err := rotateLog(c.Log); err != nil {
		return gdsapi.Process{}, nil, err
	}
	log, err := os.OpenFile(c.Log, os.O_WRONLY|os.O_APPEND|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return gdsapi.Process{}, nil, err
	}
	defer log.Close()
	info, err := log.Stat()
	if err != nil || !info.Mode().IsRegular() || !gdsutil.SingleLink(info) || !gdsutil.Owned(info) || info.Mode().Perm() != 0600 {
		return gdsapi.Process{}, nil, fmt.Errorf("GDS 日志类型、归属或权限无效")
	}
	current, err := os.Lstat(c.Log)
	if err != nil || !os.SameFile(info, current) {
		return gdsapi.Process{}, nil, fmt.Errorf("GDS 日志路径发生变化")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return gdsapi.Process{}, nil, err
	}
	defer reader.Close()
	defer writer.Close()
	cmd := exec.Command(c.Program, c.Args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	cmd.Stdout, cmd.Stderr = log, log
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return gdsapi.Process{}, nil, err
	}
	reader.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	p, err := gdsutil.Inspect(cmd.Process.Pid)
	if err != nil || p.Executable != c.Program {
		writer.Close()
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
			return p, done, fmt.Errorf("无法核验 GDS 授权门前进程身份；退出未确认")
		}
		return p, done, fmt.Errorf("无法核验 GDS 授权门前进程身份")
	}
	if err = commit(p); err != nil {
		writer.Close()
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
			return p, done, fmt.Errorf("GDS 身份提交失败: %v；退出未确认", err)
		}
		return p, done, err
	}
	if _, err = writer.Write([]byte{1}); err != nil {
		return p, done, fmt.Errorf("GDS 启动授权失败: %w", err)
	}
	return p, done, nil
}

// Rotation occurs before a new managed process starts. Both filenames and the
// containing directory must retain their captured identities until commit.
func rotateLog(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !gdsutil.SingleLink(info) || !gdsutil.Owned(info) || info.Mode().Perm() != 0600 {
		return fmt.Errorf("GDS 日志类型、归属或权限无效")
	}
	if info.Size() < 8<<20 {
		return nil
	}
	before, err := gdsutil.Capture(path, maxProgram, false)
	if err != nil {
		return err
	}
	backup, err := gdsutil.Capture(path+".1", maxProgram, true)
	if err != nil {
		return err
	}
	if backup.Info != nil && backup.Info.Mode().Perm() != 0600 {
		return fmt.Errorf("GDS 轮转日志权限无效")
	}
	guard, err := gdsutil.CaptureDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	if err = before.Check(); err != nil {
		return err
	}
	if err = backup.Check(); err != nil {
		return err
	}
	if err = guard.Check(); err != nil {
		return err
	}
	// Keep the canonical log out of CommitCandidate's removable temporary
	// slot. Copy the captured bytes into .1, then quarantine the original
	// through RemoveExpected so a late editor inode is retained with its path.
	if err = gdsutil.WriteExpected(backup.Path, before.Data, 0600, backup, guard); err != nil {
		return err
	}
	return gdsutil.RemoveExpected(before, guard)
}
func (OSRunner) Inspect(pid int) (gdsapi.Process, error) { return gdsutil.Inspect(pid) }
func (OSRunner) Stop(p gdsapi.Process) error             { return gdsutil.Stop(p) }
