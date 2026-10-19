//go:build linux

package nido

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type codexTTYLog struct {
	mu      sync.Mutex
	data    bytes.Buffer
	changed chan struct{}
}

func (l *codexTTYLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	if l.data.Len() < 1<<20 {
		_, _ = l.data.Write(data)
	}
	l.mu.Unlock()
	select {
	case l.changed <- struct{}{}:
	default:
	}
	return len(data), nil
}

func (l *codexTTYLog) snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.String()
}

// This fixture drives the real terminal by observed prompts rather than
// feeding queued line answers into a numeric/script-mode implementation.
func codexPageTTY(t *testing.T, cmd *exec.Cmd, confirm, cancelChoice bool) string {
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
	size := struct{ rows, columns, x, y uint16 }{45, 140, 0, 0}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size))); errno != 0 {
		t.Fatal(errno)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	_ = slave.Close()
	log := &codexTTYLog{changed: make(chan struct{}, 1)}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(log, master); close(copied) }()
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	wait := func(marker string) {
		t.Helper()
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for !strings.Contains(log.snapshot(), marker) {
			select {
			case err := <-finished:
				t.Fatalf("terminal ended before %q: %v\n%s", marker, err, log.snapshot())
			case <-log.changed:
			case <-deadline.C:
				t.Fatalf("terminal did not display %q\n%s", marker, log.snapshot())
			}
		}
	}
	send := func(keys string) {
		t.Helper()
		if _, err := master.Write([]byte(keys)); err != nil {
			t.Fatal(err)
		}
	}
	wait("Nido 不管理 Codex 进程")
	send("\r") // first page action: associate an existing directory
	wait("发现 CODEX_HOME 环境变量")
	if cancelChoice {
		send("\x1b")
	} else {
		send("\r") // accept the discovered home without editing its path
		wait("关联外部 Codex 目录")
		if confirm {
			send("\x1b[B\r") // ↓ selects Confirm; Enter commits
		} else {
			send("\r") // confirmation defaults to Cancel
		}
	}
	if confirm {
		wait("已关联；目录未移动")
	} else {
		wait("操作已取消")
	}
	// Escape may arrive while a short save is returning. Retry only after
	// observing its result; it never enters another action or confirmation.
	exitDeadline := time.NewTimer(5 * time.Second)
	defer exitDeadline.Stop()
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case err := <-finished:
			_ = master.Close()
			<-copied
			if err != nil {
				t.Fatalf("real keyboard session failed: %v\n%s", err, log.snapshot())
			}
			return log.snapshot()
		case <-retry.C:
			send("\x1b")
		case <-exitDeadline.C:
			t.Fatalf("Escape did not return from Codex page\n%s", log.snapshot())
		}
	}
}

func TestCodexPageRealKeyboardAssociationAndCancellation(t *testing.T) {
	const childMarker = "NIDO_CODEX_PAGE_CHILD"
	if os.Getenv(childMarker) == "1" {
		c := CLI{Out: os.Stdout, Err: os.Stderr}
		if code := c.Run([]string{"--root", os.Getenv("NIDO_CODEX_PAGE_ROOT"), "codex", "configure"}); code != 0 {
			t.Fatalf("Codex page exited with %d", code)
		}
		return
	}
	for _, scenario := range []struct {
		name                  string
		confirm, cancelChoice bool
	}{
		{"arrow-enter-association", true, false},
		{"default-cancel-confirmation", false, false},
		{"escape-cancel-discovery", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(home, "external codex")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"auth.json", ".env", "session.jsonl"} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte("PRIVATE-SENTINEL "+file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			program, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, program, "-test.run=^TestCodexPageRealKeyboardAssociationAndCancellation$", "-test.count=1")
			cmd.Env = []string{childMarker + "=1", "NIDO_CODEX_PAGE_ROOT=" + m.Root, "HOME=" + home,
				"CODEX_HOME=" + dir, "PATH=/nonexistent", "TERM=xterm-256color"}
			output := codexPageTTY(t, cmd, scenario.confirm, scenario.cancelChoice)
			reloaded, err := New(m.Root)
			if err != nil || (reloaded.Config.Codex.Home == dir) != scenario.confirm {
				t.Fatal("wrong keyboard association result", err)
			}
			if !scenario.confirm {
				after, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
				if !bytes.Equal(after, before) {
					t.Fatal("keyboard cancellation wrote Nido configuration")
				}
			}
			for _, file := range []string{"auth.json", ".env", "session.jsonl"} {
				got, err := os.ReadFile(filepath.Join(dir, file))
				if err != nil || string(got) != "PRIVATE-SENTINEL "+file {
					t.Fatal("keyboard flow changed external Codex data", file, err)
				}
			}
			if strings.Contains(output, "PRIVATE-SENTINEL") || strings.Contains(output, "选择编号") || strings.Contains(output, "y/N") {
				t.Fatal("real keyboard page exposed private data or line-input interaction")
			}
		})
	}
}
