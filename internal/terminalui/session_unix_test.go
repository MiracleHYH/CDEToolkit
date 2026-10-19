//go:build linux || darwin

package terminalui

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

type lockedBuffer struct {
	sync.Mutex
	b bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) { b.Lock(); defer b.Unlock(); return b.b.Write(p) }
func (b *lockedBuffer) String() string              { b.Lock(); defer b.Unlock(); return b.b.String() }

func waitFor(t *testing.T, fn func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func terminalPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("PTY unavailable: %v", err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	if err := pty.Setsize(slave, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func TestRealPTYKeysPromptResizeDelayedActionAndRestoration(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	master, slave := terminalPair(t)
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	started, release := make(chan struct{}), make(chan struct{})
	var actionCount atomic.Int32
	var selected atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(Page{Title: "Nido PTY 测试", Actions: []Action{
			{ID: "unused", Label: "第一个操作", Run: func() int { actionCount.Add(100); return 0 }},
			{ID: "run", Label: "第二个操作", Run: func() int {
				actionCount.Add(1)
				index, e := s.Select("是否执行测试操作", []string{"取消", "确认"}, 0)
				if e != nil {
					return 0
				}
				selected.Store(int32(index))
				if index == 0 {
					return 0
				}
				close(started)
				<-release
				io.WriteString(s, "隔离操作已完成\n")
				return 0
			}},
		}})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "第二个操作") }, "initial view")
	master.Write([]byte("\x1b[B\r"))
	waitFor(t, func() bool { return strings.Contains(output.String(), "是否执行测试操作") }, "confirmation")
	master.Write([]byte("\x1b[B\r"))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("arrow/enter did not confirm")
	}
	s.program.Send(tea.WindowSizeMsg{Width: 32, Height: 9})
	master.Write([]byte{3})
	waitFor(t, func() bool { return strings.Contains(output.String(), "安全完成") }, "responsive cancellation")
	select {
	case <-done:
		t.Fatal("exited before in-flight action completed")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not exit")
	}
	if actionCount.Load() != 1 || selected.Load() != 1 {
		t.Fatalf("actions=%d selected=%d", actionCount.Load(), selected.Load())
	}
	after, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal state was not restored")
	}
}

func TestRealPTYEscCancelsOnlyPrompt(t *testing.T) {
	t.Setenv("TERM", "xterm")
	master, slave := terminalPair(t)
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	result := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(Page{Title: "Esc isolation", Actions: []Action{{ID: "a", Label: "Ask", Run: func() int { _, e := s.Input("输入路径", "/tmp"); result <- e; return 0 }}}})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "Ask") }, "page")
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return strings.Contains(output.String(), "输入路径") }, "input")
	master.Write([]byte{27})
	select {
	case err := <-result:
		if err != ErrCancelled {
			t.Fatalf("prompt=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not cancel")
	}
	select {
	case <-done:
		t.Fatal("Esc cancelled app instead of prompt")
	default:
	}
	// Wait for the action result before returning from the page.
	time.Sleep(80 * time.Millisecond)
	master.Write([]byte{27})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not return from page")
	}
}

func TestRealPTYIdleDoesNotQueryOrRepaint(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	master, slave := terminalPair(t)
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	var queries atomic.Int32
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := s.Run(Page{Title: "idle performance", Status: func() string { queries.Add(1); return "ready" }, Actions: []Action{{ID: "a", Label: "Idle"}}})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "ready") }, "first ready frame")
	startup := time.Since(started)
	time.Sleep(100 * time.Millisecond)
	beforeBytes := len(output.String())
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
		t.Fatal(err)
	}
	if len(output.String()) != beforeBytes {
		t.Fatal("idle terminal repainted")
	}
	if queries.Load() != 1 {
		t.Fatalf("idle status queried %d times", queries.Load())
	}
	cpu := time.Duration(after.Utime.Sec-before.Utime.Sec+after.Stime.Sec-before.Stime.Sec)*time.Second + time.Duration(after.Utime.Usec-before.Utime.Usec+after.Stime.Usec-before.Stime.Usec)*time.Microsecond
	t.Logf("first ready frame=%s; idle 400ms process CPU=%s; status queries=%d; idle output bytes=0", startup, cpu, queries.Load())
	master.Write([]byte{27})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle program did not exit")
	}
}

func TestTerminalInitializationHelper(t *testing.T) {
	if os.Getenv("NIDO_TERMINALUI_INITIALIZATION_HELPER") != "1" {
		return
	}
	s, err := New(os.Stdin, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RunTask(func() int {
		if _, err := s.Select("初始化完成：方向键选择", []string{"取消", "确认"}, 0); err != nil {
			return 1
		}
		io.WriteString(s, "安装目录：/tmp/nido helper\n. '/tmp/nido helper/env.sh'\n")
		io.WriteString(s, "proxy error: http://private-token:password@127.0.0.1:9999\n")
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProcessStartupNeedsNoOSCResponseAndReplaysResult(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		t.Run(map[bool]string{false: "ansi", true: "no-color"}[noColor], func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTerminalInitializationHelper$")
			env := []string{}
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "TERM=") || strings.HasPrefix(entry, "NO_COLOR=") || strings.HasPrefix(entry, "GORACE=") || strings.HasPrefix(entry, "NIDO_TERMINALUI_INITIALIZATION_HELPER=") {
					continue
				}
				env = append(env, entry)
			}
			env = append(env, "PATH=/nonexistent", "TERM=xterm-256color", "NIDO_TERMINALUI_INITIALIZATION_HELPER=1", "GORACE=atexit_sleep_ms=0")
			if noColor {
				env = append(env, "NO_COLOR=1")
			}
			cmd.Env = env
			started := time.Now()
			master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			var output lockedBuffer
			go io.Copy(&output, master)
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			deadline := time.Now().Add(1500 * time.Millisecond)
			for !strings.Contains(output.String(), "初始化完成") && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !strings.Contains(output.String(), "初始化完成") {
				cancel()
				<-wait
				t.Fatalf("startup waited for unresponsive terminal: %q", output.String())
			}
			firstFrame := time.Since(started)
			exitStarted := time.Now()
			master.Write([]byte{'\r'})
			select {
			case err := <-wait:
				if err != nil {
					t.Fatalf("helper failed: %v output=%q", err, output.String())
				}
			case <-time.After(1500 * time.Millisecond):
				cancel()
				<-wait
				t.Fatal("terminal exit waited for a response")
			}
			// Allow the PTY reader to consume the process's final write.
			waitFor(t, func() bool { return strings.Contains(output.String(), "REDACTED") }, "replayed result")
			transcript := output.String()
			if strings.Contains(transcript, "\x1b]11;?") || strings.Contains(transcript, "\x1b]10;?") {
				t.Fatalf("unexpected OSC query: %q", transcript)
			}
			if strings.Contains(transcript, "private-token") || strings.Contains(transcript, "password") {
				t.Fatalf("credential leaked: %q", transcript)
			}
			lastRestore := strings.LastIndex(transcript, "\x1b[?1049l")
			if lastRestore < 0 || !strings.Contains(transcript[lastRestore:], ". '/tmp/nido helper/env.sh'") {
				t.Fatalf("result disappeared with alt-screen: %q", transcript)
			}
			if count := strings.Count(transcript[lastRestore:], ". '/tmp/nido helper/env.sh'"); count != 1 {
				t.Fatalf("result replayed %d times", count)
			}
			t.Logf("first frame=%s; exit=%s; no OSC response supplied; final result survived alt-screen", firstFrame, time.Since(exitStarted))
		})
	}
}

func TestRealPTYSecretDefaultsValuesAndCancelStayHidden(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	master, slave := terminalPair(t)
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	const original = "https://private-user:private-password@private.example/subscription?token=old-default-secret"
	const replacement = "replacement-password-123"
	values := make(chan string, 2)
	cancelled := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.RunTask(func() int {
			v, e := s.InputSecret("保留已有秘密", original)
			if e != nil {
				return 1
			}
			values <- v
			io.WriteString(s, "已有地址检查失败："+v+"\n")
			v, e = s.InputSecret("输入新秘密", "")
			if e != nil {
				return 1
			}
			values <- v
			// Even an accidental component error cannot retain this operation's
			// submitted secret in the normal-screen replay.
			io.WriteString(s, "配置失败："+v+"\n")
			_, e = s.InputSecret("取消秘密输入", original)
			cancelled <- e
			return 0
		})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "保留已有秘密") }, "masked default")
	if strings.Contains(output.String(), original) {
		t.Fatal("default secret displayed")
	}
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return strings.Contains(output.String(), "输入新秘密") }, "second secret")
	master.Write(append([]byte(replacement), '\r'))
	waitFor(t, func() bool { return strings.Contains(output.String(), "取消秘密输入") }, "secret cancellation")
	master.Write([]byte{27})
	select {
	case err := <-cancelled:
		if err != ErrCancelled {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("program did not finish")
	}
	if got := <-values; got != original {
		t.Fatalf("default changed: %q", got)
	}
	if got := <-values; got != replacement {
		t.Fatalf("entered secret changed: %q", got)
	}
	transcript := output.String()
	for _, secret := range []string{original, "private-password", "old-default-secret", replacement} {
		if strings.Contains(transcript, secret) {
			t.Fatalf("secret appeared in terminal output: %q", transcript)
		}
	}
	if !strings.Contains(transcript, "[隐藏]") {
		t.Fatal("output redaction was not replayed")
	}
}

func TestRealPTYBracketedPasteNormalAndSecret(t *testing.T) {
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "1")
	master, slave := terminalPair(t)
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	values := make(chan string, 2)
	done := make(chan error, 1)
	const secret = "https://private.invalid/sub?token=bracketed-paste-secret-甲乙"
	go func() {
		_, err := s.RunTask(func() int {
			value, err := s.Input("粘贴普通路径", "")
			if err != nil {
				return 1
			}
			values <- value
			value, err = s.InputSecret("粘贴私密地址", "")
			if err != nil {
				return 1
			}
			values <- value
			return 0
		})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "粘贴普通路径") }, "ordinary pasted path")
	master.Write([]byte("\x1b[200~/tmp/目录 A\x1b[201~"))
	waitFor(t, func() bool { return strings.Contains(output.String(), "/tmp/目录 A") }, "rendered pasted path")
	master.Write([]byte("\x1b[D\x1b[3~乙\r"))
	waitFor(t, func() bool { return strings.Contains(output.String(), "粘贴私密地址") }, "secret paste prompt")
	master.Write([]byte("\x1b[200~" + secret + "\x1b[201~"))
	// Let a real frame render the active secret, so the privacy assertion
	// does not pass merely because Enter immediately hid the input.
	time.Sleep(80 * time.Millisecond)
	if strings.Contains(output.String(), "bracketed-paste-secret") {
		t.Fatal("pasted secret echoed")
	}
	master.Write([]byte{'\r'})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("paste session did not complete")
	}
	if value := <-values; value != "/tmp/目录 乙" {
		t.Fatalf("plain paste/edit=%q", value)
	}
	if value := <-values; value != secret {
		t.Fatalf("secret paste changed=%q", value)
	}
	if transcript := output.String(); strings.Contains(transcript, "bracketed-paste-secret") || strings.Contains(transcript, "\x1b]52;") {
		t.Fatalf("secret/clipboard operation in output: %q", transcript)
	}
}

func BenchmarkIdleView(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(&Session{ctx: ctx, cancel: cancel}, Page{Title: "Nido", Actions: []Action{{ID: "a", Label: "Proxy"}, {ID: "b", Label: "Codex"}, {ID: "c", Label: "Conda / pip"}}}, nil)
	for i := 0; i < b.N; i++ {
		_ = m.View().Content
	}
}

// The opt-in path must point to the freshly built Nido test artifact. This
// measures a real production executable under an empty HOME and no tools,
// instead of mistaking a race-instrumented Go test process for product RSS.
func TestNidoProcessPerformanceProbe(t *testing.T) {
	program := os.Getenv("NIDO_UI_PERF_BINARY")
	if program == "" {
		t.Skip("freshly built Nido artifact not supplied")
	}
	home := t.TempDir()
	root := home + "/nido-uninitialized"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "--root", root)
	cmd.Env = []string{"HOME=" + home, "PATH=/nonexistent", "TERM=xterm-256color", "NO_COLOR=1"}
	started := time.Now()
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 110, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var output lockedBuffer
	go io.Copy(&output, master)
	waitFor(t, func() bool { return strings.Contains(output.String(), "初始化 / 刷新环境") }, "production first frame")
	firstFrame := time.Since(started)
	time.Sleep(100 * time.Millisecond)
	idleBytes := len(output.String())
	time.Sleep(500 * time.Millisecond)
	if len(output.String()) != idleBytes {
		t.Fatal("production UI repainted while idle")
	}
	exitStarted := time.Now()
	master.Write([]byte{27})
	if err := cmd.Wait(); err != nil {
		t.Fatalf("production UI failed: %v %q", err, output.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("menu created root: %v", err)
	}
	usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Fatal("missing process resource usage")
	}
	rssKiB := usage.Maxrss
	if runtime.GOOS == "darwin" {
		rssKiB /= 1024
	}
	info, err := os.Stat(program)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production artifact=%s; binary=%d bytes; first frame=%s; idle=500ms zero output; exit=%s; total CPU=%s; peak RSS=%d KiB; elapsed=%s", program, info.Size(), firstFrame, time.Since(exitStarted), cmd.ProcessState.UserTime()+cmd.ProcessState.SystemTime(), rssKiB, time.Since(started))
}

func TestRealPTYDynamicLifecycleActionsCancelAndRestore(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	master, slave := terminalPair(t)
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(slave, slave)
	if err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	go io.Copy(&output, master)
	var installed atomic.Bool
	var queries, factories, installs, removes, confirms atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(Page{Title: "Proxy Lifecycle PTY", Status: func() string { return "SNAPSHOT " + strconv.Itoa(int(queries.Add(1))) }, ActionsFunc: func() []Action {
			factories.Add(1)
			if !installed.Load() {
				return []Action{{ID: "install", Label: "安装组件", Run: func() int { installs.Add(1); installed.Store(true); return 0 }}}
			}
			return []Action{{ID: "start", Label: "运行操作"}, {ID: "remove", Label: "删除组件", Run: func() int {
				confirms.Add(1)
				choice, err := s.Select("确认删除测试组件", []string{"取消", "确认"}, 0)
				if err != nil || choice == 0 {
					return 1
				}
				removes.Add(1)
				installed.Store(false)
				return 0
			}}}
		}})
		done <- err
	}()
	waitFor(t, func() bool { return strings.Contains(output.String(), "安装组件") }, "initial dynamic page")
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return strings.Contains(output.String(), "运行操作") && factories.Load() == 2 }, "installed actions")
	master.Write([]byte("\x1b[B\r"))
	waitFor(t, func() bool {
		return confirms.Load() == 1 && strings.Contains(output.String(), "确认删除测试组件")
	}, "first delete confirmation")
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return factories.Load() == 3 }, "cancel refresh")
	time.Sleep(80 * time.Millisecond)
	if !installed.Load() || removes.Load() != 0 {
		t.Fatal("default cancellation removed component")
	}
	// The cancelled action keeps focus by stable ID. Enter selects remove again.
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return confirms.Load() == 2 }, "focus retained after cancel")
	// Let the prompt view reach the terminal before choosing confirmation.
	time.Sleep(50 * time.Millisecond)
	master.Write([]byte("\x1b[B\r"))
	waitFor(t, func() bool { return factories.Load() == 4 }, "removed actions")
	time.Sleep(80 * time.Millisecond)
	if installed.Load() || removes.Load() != 1 {
		t.Fatal("confirmed remove did not complete")
	}
	// Removing the last selected action clamps focus to install without a page
	// restart. Enter must not invoke a stale delete closure.
	master.Write([]byte{'\r'})
	waitFor(t, func() bool { return factories.Load() == 5 }, "reinstalled actions")
	time.Sleep(80 * time.Millisecond)
	if !installed.Load() || installs.Load() != 2 || removes.Load() != 1 {
		t.Fatal("stale lifecycle action executed")
	}
	master.Write([]byte{27})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dynamic page did not exit")
	}
	after, err := term.GetState(slave.Fd())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("dynamic page failed to restore terminal", err)
	}
	if queries.Load() != 5 || factories.Load() != 5 {
		t.Fatal("dynamic page polled state", queries.Load(), factories.Load())
	}
}
