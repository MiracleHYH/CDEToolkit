//go:build linux

package nido

import (
	"bytes"
	"context"
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

// The real-core namespace contains only three static artifacts and private
// devices. Allocate a controlling terminal directly, without adding script,
// a shell, shared libraries or broader host mounts to that namespace.
func quickStartPTY(t *testing.T, cmd *exec.Cmd, input string) ([]byte, error) {
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
	var terminal syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&terminal))); errno != 0 {
		t.Fatal(errno)
	}
	terminal.Lflag &^= syscall.ECHO | syscall.ECHONL
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&terminal))); errno != 0 {
		t.Fatal(errno)
	}
	cmd.Stdin = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	var output bytes.Buffer
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, io.LimitReader(master, 1<<20))
		close(copied)
	}()
	if _, err := master.Write([]byte(input)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	err = cmd.Wait()
	select {
	case <-copied:
	case <-time.After(time.Second):
		_ = master.Close()
		<-copied
	}
	return output.Bytes(), err
}

func TestQuickStartPTYIsRealAndPrivate(t *testing.T) {
	const marker = "NIDO_QUICKSTART_PTY_CHILD"
	const secret = "fixture-private-terminal-input"
	if os.Getenv(marker) == "1" {
		if !terminalInput(os.Stdin) {
			t.Fatal("child input is not a real terminal")
		}
		c := CLI{Err: os.Stderr}
		value, err := c.ask("fixture terminal input", "")
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
	cmd.Env = []string{marker + "=1", "HOME=" + t.TempDir(), "PATH=/nonexistent"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	terminal, err := quickStartPTY(t, cmd, secret+"\n")
	if err != nil || !strings.Contains(stdout.String(), "PRIVATE_PTY_INPUT_ACCEPTED") {
		t.Fatalf("real PTY fixture failed: %v stdout=%s stderr=%s terminal=%s", err, stdout.String(), stderr.String(), terminal)
	}
	if strings.Contains(stdout.String()+stderr.String()+string(terminal), secret) {
		t.Fatal("PTY echoed secret input")
	}
}
