//go:build linux

package nido

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Allocate the complete controlling terminal directly. The isolated child
// needs no script utility, shell, extra program or host filesystem mounts.
func quickStartPTY(t *testing.T, cmd *exec.Cmd, prompt, input string) ([]byte, error) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Fatal(errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	var before syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&before))); errno != 0 {
		t.Fatal(errno)
	}
	window := struct{ Rows, Cols, X, Y uint16 }{Rows: 40, Cols: 120}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&window))); errno != 0 {
		t.Fatal(errno)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The UI itself must disable echo and restore the terminal. The fixture does
	// not pre-disable ECHO, which would mask an ordinary plaintext input bug.
	ready := make(chan struct{}, 1)
	type readResult struct {
		data []byte
		err  error
	}
	readDone := make(chan readResult, 1)
	go func() {
		var output bytes.Buffer
		var readErr error
		found := false
		buffer := make([]byte, 4096)
		const maximum = 1 << 20
		for {
			n, e := master.Read(buffer)
			if n > 0 {
				if output.Len()+n > maximum {
					readErr = fmt.Errorf("PTY output exceeds 1 MiB")
					break
				}
				output.Write(buffer[:n])
				if !found && bytes.Contains(output.Bytes(), []byte(prompt)) {
					found = true
					ready <- struct{}{}
				}
			}
			if e != nil {
				if !errors.Is(e, io.EOF) && !errors.Is(e, syscall.EIO) {
					readErr = e
				}
				break
			}
		}
		readDone <- readResult{data: output.Bytes(), err: readErr}
	}()
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	var captured *readResult
	var waitErr error
	select {
	case <-ready:
		if _, err := master.Write([]byte(input)); err != nil {
			_ = cmd.Process.Kill()
			waitErr = <-finished
			if waitErr == nil {
				waitErr = err
			}
		} else {
			select {
			case waitErr = <-finished:
			case result := <-readDone:
				captured = &result
				_ = cmd.Process.Kill()
				waitErr = <-finished
			}
		}
	case result := <-readDone:
		captured = &result
		_ = cmd.Process.Kill()
		waitErr = <-finished
	case waitErr = <-finished:
		if waitErr == nil {
			waitErr = fmt.Errorf("child exited before its input prompt")
		}
	}
	var after syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&after))); errno != 0 {
		if waitErr == nil {
			waitErr = errno
		}
	} else if after != before && waitErr == nil {
		waitErr = fmt.Errorf("child did not restore terminal settings")
	}
	_ = slave.Close()
	if captured == nil {
		select {
		case result := <-readDone:
			captured = &result
		case <-time.After(time.Second):
			_ = master.Close()
			result := <-readDone
			captured = &result
		}
	}
	if captured.err != nil && waitErr == nil {
		waitErr = captured.err
	}
	return captured.data, waitErr
}

func TestQuickStartPTYIsRealAndPrivate(t *testing.T) {
	const marker = "NIDO_QUICKSTART_PTY_CHILD"
	const secret = "fixture-private-terminal-input"
	if os.Getenv(marker) == "1" {
		if !terminalInput(os.Stdin) || !terminalInput(os.Stdout) || !terminalInput(os.Stderr) {
			t.Fatal("child does not have a complete real terminal")
		}
		c := CLI{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
		value, err := c.secretPrompt("fixture terminal input", "")
		if err != nil || value != secret {
			t.Fatal("controlling terminal input mismatch")
		}
		fmt.Fprintln(os.Stdout, "PRIVATE_PTY_INPUT_ACCEPTED")
		return
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "-test.run=^TestQuickStartPTYIsRealAndPrivate$", "-test.count=1")
	cmd.Env = []string{marker + "=1", "HOME=" + t.TempDir(), "PATH=/nonexistent", "TERM=xterm-256color", "NO_COLOR=1"}
	terminal, err := quickStartPTY(t, cmd, "fixture terminal input", secret+"\r")
	if err != nil || !strings.Contains(string(terminal), "PRIVATE_PTY_INPUT_ACCEPTED") {
		t.Fatalf("real PTY fixture failed: %v terminal=%s", err, terminal)
	}
	if bytes.Contains(terminal, []byte(secret)) {
		t.Fatal("PTY echoed secret input")
	}
}
