package gdsruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
)

// This clock is confined to CPU helper processes. Production workers always
// use CLOCK_BOOTTIME; no wall-clock fallback exists in the shipped runtime.
type testWallClock struct{}

func (testWallClock) Now() (int64, error) { return time.Now().UnixNano(), nil }

type fakeBackend struct{}
type fakeAllocation struct{}

var errFakeFree = errors.New("fake CUDA Free failed")

func (fakeBackend) Probe(uuid string) (gdsapi.Sample, error) {
	switch os.Getenv("GDS_TEST_PROBE_MODE") {
	case "hang":
		for {
			time.Sleep(time.Hour)
		}
	case "oversized":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 128<<10))
		return sample(), nil
	case "trailing":
		fmt.Fprint(os.Stdout, "{}\n")
		return sample(), nil
	case "incomplete":
		s := sample()
		s.Complete = false
		return s, nil
	}
	s := sample()
	s.UUID = uuid
	if b, e := os.ReadFile(os.Getenv("GDS_TEST_CONTEXT")); e == nil {
		pid, _ := strconv.Atoi(string(b))
		s.ComputePIDs = []int{pid}
	}
	return s, nil
}
func (fakeBackend) Allocate(uuid string, bytes, reserve uint64) (Allocation, error) {
	if os.Getenv("GDS_TEST_ALLOCATE_MODE") == "hang" {
		select {}
	}
	if os.Getenv("GDS_TEST_ALLOCATE_MODE") == "fail" {
		return nil, fmt.Errorf("fake OOM competition")
	}
	if p := os.Getenv("GDS_TEST_CONTEXT"); p != "" {
		if e := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
			return nil, e
		}
	}
	return fakeAllocation{}, nil
}
func (fakeAllocation) Free() error {
	if os.Getenv("GDS_TEST_FREE_MODE") == "hang" {
		select {}
	}
	if p := os.Getenv("GDS_TEST_CONTEXT"); p != "" {
		_ = os.WriteFile(p+".freed", []byte("freed"), 0600)
		_ = os.Remove(p)
	}
	if os.Getenv("GDS_TEST_FREE_MODE") == "fail" {
		return errFakeFree
	}
	return nil
}

func TestMain(m *testing.M) {
	if os.Getenv("GDS_TEST_HELPER") == "1" && len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "__probe":
			f := flag.NewFlagSet("probe", flag.ContinueOnError)
			uuid := f.String("gpu", "", "")
			err = f.Parse(os.Args[2:])
			if err == nil {
				err = RunProbe(os.Stdout, fakeBackend{}, *uuid)
			}
		case "__worker":
			f := flag.NewFlagSet("worker", flag.ContinueOnError)
			uuid := f.String("gpu", "", "")
			token := f.String("token", "", "")
			boot := f.String("boot-id", "", "")
			err = f.Parse(os.Args[2:])
			if err == nil {
				err = RunWorker(context.Background(), WorkerOptions{GPUUUID: *uuid, Token: *token, BootID: *boot, Clock: testWallClock{}, lockValidated: true}, fakeBackend{})
			}
		case "__monitor":
			f := flag.NewFlagSet("monitor", flag.ContinueOnError)
			dir := f.String("dir", "", "")
			lock := f.String("test-lock", "", "")
			managed := f.Bool("managed", false, "")
			err = f.Parse(os.Args[2:])
			if err == nil {
				var c gdsapi.Config
				var b []byte
				b, err = gdsutil.Read(filepath.Join(*dir, "config.toml"), 64<<10)
				if err == nil {
					c, err = gdsapi.ParseConfig(b)
				}
				if err == nil {
					ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
					defer stop()
					err = Run(ctx, RunOptions{Dir: *dir, Config: c, Managed: *managed, Clock: testWallClock{}, testLockPath: *lock})
				}
			}
		case "__lock_pin":
			// Deliberately discard the wrapper exactly as a long-running
			// worker may do after its last validation call.
			err = func() error { return retainDeviceLock(os.NewFile(3, "inherited-device-lock")) }()
			if err == nil {
				for i := 0; i < 10; i++ {
					runtime.GC()
					time.Sleep(10 * time.Millisecond)
				}
				fmt.Fprintln(os.Stdout, "READY")
				_, err = io.Copy(io.Discard, os.Stdin)
			}
		default:
			os.Exit(m.Run())
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func helperEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GDS_TEST_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv("GDS_TEST_CONTEXT", filepath.Join(t.TempDir(), "context"))
	t.Setenv("GDS_TEST_PROBE_MODE", "")
	t.Setenv("GDS_TEST_ALLOCATE_MODE", "")
	t.Setenv("GDS_TEST_FREE_MODE", "")
}
func testExecutable(t *testing.T) string {
	t.Helper()
	p, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func testBoot(t *testing.T) string {
	t.Helper()
	b, e := gdsutil.BootID()
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func launchTestWorker(t *testing.T) *workerChild {
	return launchTestWorkerStderr(t, os.Stderr)
}

func launchTestWorkerStderr(t *testing.T, stderr io.Writer) *workerChild {
	t.Helper()
	// These CPU protocol helpers do not need procfs or macOS process entitlements.
	// The production launcher and exact process identities are exercised by the
	// Linux monitor integration tests below.
	cmd := exec.Command(testExecutable(t), "__worker", "--gpu", testUUID, "--token", "0123456789abcdef0123456789abcdef", "--boot-id", "test-boot") // gitleaks:allow deterministic CPU protocol fixture, not a credential
	stdin, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	w := &workerChild{cmd: cmd, stdin: stdin, responses: make(chan workerResponse, 1), done: make(chan error, 1), token: "0123456789abcdef0123456789abcdef", uuid: testUUID, boot: "test-boot", bytes: 1 << 20, reserve: 1 << 20, process: gdsapi.Process{PID: cmd.Process.Pid}} // gitleaks:allow deterministic CPU protocol fixture, not a credential
	go func() { w.done <- cmd.Wait() }()
	go func() {
		scanner := protocolScanner(stdout)
		for {
			r, e := readMessage[gdsapi.Ready](scanner)
			w.responses <- workerResponse{r, e}
			if e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		if !w.reaped {
			_ = cmd.Process.Kill()
			select {
			case <-w.done:
				w.reaped = true
			case <-time.After(2 * time.Second):
			}
		}
	})
	return w
}
func awaitExit(t *testing.T, w *workerChild, max time.Duration) error {
	t.Helper()
	select {
	case e := <-w.done:
		w.reaped = true
		return e
	case <-time.After(max):
		t.Fatal("worker did not exit")
		return nil
	}
}
func leaseAt(w *workerChild, kind string, deadline int64) gdsapi.Lease {
	w.seq++
	return gdsapi.Lease{Type: kind, Protocol: 1, Boot: w.boot, Token: w.token, UUID: w.uuid, Seq: w.seq, ExpiresAtBootNS: deadline, Bytes: w.bytes, MinFreeBytes: w.reserve}
}
func waitForFile(t *testing.T, path string, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file never appeared: %s", path)
}
func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, e := os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("unexpected allocation/file %s: %v", path, e)
	}
}

func TestWorkerAuthorizationAndEOFRelease(t *testing.T) {
	helperEnv(t)
	w := launchTestWorker(t)
	time.Sleep(100 * time.Millisecond)
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
	if e := w.exchange(context.Background(), "ALLOCATE", time.Now().UnixNano()); e != nil {
		t.Fatal(e)
	}
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT"), time.Second)
	if e := w.exchange(context.Background(), "RENEW", time.Now().UnixNano()); e != nil {
		t.Fatal(e)
	}
	if e := w.stdin.Close(); e != nil {
		t.Fatal(e)
	}
	if e := awaitExit(t, w, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}
func TestWorkerParentDiesBeforeAuthorization(t *testing.T) {
	helperEnv(t)
	w := launchTestWorker(t)
	_ = w.stdin.Close()
	if e := awaitExit(t, w, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}

func TestCanceledWorkerNeverConsumesQueuedAuthorization(t *testing.T) {
	helperEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	token := "0123456789abcdef0123456789abcdef" // gitleaks:allow deterministic CPU protocol fixture, not a credential
	lease := gdsapi.Lease{Type: "ALLOCATE", Protocol: 1, Boot: "boot", Token: token, UUID: testUUID, Seq: 1, ExpiresAtBootNS: time.Now().Add(time.Second).UnixNano(), Bytes: 1 << 20, MinFreeBytes: 1 << 20}
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	err = RunWorker(ctx, WorkerOptions{GPUUUID: testUUID, Token: token, BootID: "boot", Clock: testWallClock{}, lockValidated: true, Input: strings.NewReader(string(data) + "\n"), Output: io.Discard}, fakeBackend{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled worker continued startup: %v", err)
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}
func TestWorkerExpiredBufferedAuthorizationNeverAllocates(t *testing.T) {
	helperEnv(t)
	w := launchTestWorker(t)
	l := leaseAt(w, "ALLOCATE", time.Now().Add(-time.Millisecond).UnixNano())
	if e := w.write(context.Background(), l); e != nil {
		t.Fatal(e)
	}
	if e := awaitExit(t, w, 2*time.Second); e == nil {
		t.Fatal("expired authorization accepted")
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}
func TestWorkerWatchdogPrecedesWedgedAllocation(t *testing.T) {
	helperEnv(t)
	t.Setenv("GDS_TEST_ALLOCATE_MODE", "hang")
	w := launchTestWorker(t)
	l := leaseAt(w, "ALLOCATE", time.Now().Add(time.Second).UnixNano())
	if e := w.write(context.Background(), l); e != nil {
		t.Fatal(e)
	}
	e := awaitExit(t, w, 5*time.Second)
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 124 {
		t.Fatalf("watchdog exit: %v", e)
	}
}
func TestWorkerWatchdogBoundsWedgedFree(t *testing.T) {
	helperEnv(t)
	t.Setenv("GDS_TEST_FREE_MODE", "hang")
	w := launchTestWorker(t)
	if e := w.exchange(context.Background(), "ALLOCATE", time.Now().UnixNano()); e != nil {
		t.Fatal(e)
	}
	_ = w.stdin.Close()
	e := awaitExit(t, w, 4*time.Second)
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 124 {
		t.Fatalf("cleanup watchdog exit: %v", e)
	}
}

func TestWorkerPreservesTriggerAndFreeErrors(t *testing.T) {
	for _, mode := range []string{"protocol", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			helperEnv(t)
			t.Setenv("GDS_TEST_FREE_MODE", "fail")
			var stderr bytes.Buffer
			w := launchTestWorkerStderr(t, &stderr)
			lease := leaseAt(w, "ALLOCATE", time.Now().Add(300*time.Millisecond).UnixNano())
			if err := w.write(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			select {
			case reply := <-w.responses:
				if reply.err != nil {
					t.Fatal(reply.err)
				}
			case <-time.After(time.Second):
				t.Fatal("worker did not allocate")
			}
			want := "worker lease expired"
			if mode == "protocol" {
				want = "unknown worker command"
				invalid := leaseAt(w, "INVALID", time.Now().Add(time.Second).UnixNano())
				if err := w.write(context.Background(), invalid); err != nil {
					t.Fatal(err)
				}
			}
			if err := awaitExit(t, w, 3*time.Second); err == nil {
				t.Fatal("worker failure was not reported")
			}
			if !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), errFakeFree.Error()) {
				t.Fatalf("cleanup lost trigger or Free error: %q", stderr.String())
			}
		})
	}
}

func fullStderrPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	fd := int(writer.Fd())
	if err = syscall.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 4096)
	for {
		_, err = syscall.Write(fd, chunk)
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = syscall.SetNonblock(fd, false); err != nil {
		t.Fatal(err)
	}
	return reader, writer
}

func TestParentDeathFreeFailureKeepsHardExitFenceWithBlockedStderr(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux parent death and process identity integration")
	}
	helperEnv(t)
	t.Setenv("GDS_TEST_FREE_MODE", "fail")
	_, stderr := fullStderrPipe(t)
	monitor, done, dir := startTestMonitorProgramStderr(t, testExecutable(t), stderr)
	worker := workerRecordAt(t, dir)
	t.Cleanup(func() { _ = gdsutil.Stop(worker.Process) })
	if err := monitor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not exit")
	}
	// EOF initiates the real RunWorker cleanup. Free then fails and TestMain
	// reports that error synchronously, just like cmd/gds main. Its full stderr
	// must not stop the existing watchdog from terminating this orphan.
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
	waitProcessGone(t, worker.Process, CleanupTimeout+3*time.Second)
}

func TestProtocolFailureKeepsHardExitFenceWithBlockedStderr(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process-exit integration")
	}
	helperEnv(t)
	_, stderr := fullStderrPipe(t)
	w := launchTestWorkerStderr(t, stderr)
	if err := w.exchange(context.Background(), "ALLOCATE", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	invalid := leaseAt(w, "INVALID", time.Now().Add(time.Second).UnixNano())
	if err := w.write(context.Background(), invalid); err != nil {
		t.Fatal(err)
	}
	err := awaitExit(t, w, CleanupTimeout+2*time.Second)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 124 {
		t.Fatalf("protocol failure disabled hard exit fence: %v", err)
	}
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
}
func TestWorkerStoppedAcrossLeaseDoesNotRevive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	helperEnv(t)
	w := launchTestWorker(t)
	l := leaseAt(w, "ALLOCATE", time.Now().Add(time.Second).UnixNano())
	if e := w.write(context.Background(), l); e != nil {
		t.Fatal(e)
	}
	select {
	case r := <-w.responses:
		if r.err != nil {
			t.Fatal(r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("no READY")
	}
	if e := w.cmd.Process.Signal(syscall.SIGSTOP); e != nil {
		t.Fatal(e)
	}
	// A queued renewal retains its old absolute deadline after SIGCONT.
	renewal := leaseAt(w, "RENEW", time.Now().Add(time.Second).UnixNano())
	if e := w.write(context.Background(), renewal); e != nil {
		t.Fatal(e)
	}
	time.Sleep(1200 * time.Millisecond)
	if e := w.cmd.Process.Signal(syscall.SIGCONT); e != nil {
		t.Fatal(e)
	}
	if e := awaitExit(t, w, 2*time.Second); e == nil {
		t.Fatal("paused worker revived an expired lease")
	}
}
func TestWorkerOversizedProtocolFailsClosed(t *testing.T) {
	helperEnv(t)
	w := launchTestWorker(t)
	_, _ = io.WriteString(w.stdin, strings.Repeat("x", MaxMessageBytes+100)+"\n")
	if e := awaitExit(t, w, 2*time.Second); e == nil {
		t.Fatal("oversized protocol accepted")
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}
func TestProbeBoundedTimeoutOutputAndCompleteness(t *testing.T) {
	for _, mode := range []string{"hang", "oversized", "trailing", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			helperEnv(t)
			t.Setenv("GDS_TEST_PROBE_MODE", mode)
			start := time.Now()
			if _, e := Probe(context.Background(), testExecutable(t), testUUID); e == nil {
				t.Fatal("invalid probe accepted")
			}
			if time.Since(start) > ProbeTimeout+CleanupTimeout+time.Second {
				t.Fatal("probe was unbounded")
			}
		})
	}
	t.Run("success", func(t *testing.T) {
		helperEnv(t)
		s, e := Probe(context.Background(), testExecutable(t), testUUID)
		if e != nil || !s.Complete || s.UUID != testUUID {
			t.Fatalf("probe: %+v %v", s, e)
		}
	})
}
func TestProtocolRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, s := range []string{"{\"unknown\":true}\n", "{} {}\n", strings.Repeat("x", MaxMessageBytes+1) + "\n"} {
		if _, e := readMessage[gdsapi.Lease](protocolScanner(strings.NewReader(s))); e == nil {
			t.Fatal("malformed line accepted")
		}
	}
}
func TestBoundedBufferNeverRetainsUnboundedOutput(t *testing.T) {
	b := boundedBuffer{limit: 10}
	_, _ = b.Write([]byte(strings.Repeat("a", 100)))
	_, _ = b.Write([]byte("b"))
	v, overflow := b.result()
	if len(v) != 10 || !overflow {
		t.Fatal(len(v), overflow)
	}
}

func startTestMonitor(t *testing.T) (*exec.Cmd, <-chan error, string) {
	return startTestMonitorProgram(t, testExecutable(t))
}

func startTestMonitorProgram(t *testing.T, executable string) (*exec.Cmd, <-chan error, string) {
	return startTestMonitorProgramStderr(t, executable, os.Stderr)
}

func startTestMonitorProgramStderr(t *testing.T, executable string, stderr io.Writer) (*exec.Cmd, <-chan error, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := config()
	c.IdleSeconds = 1
	b, _ := c.Encode()
	if e := os.WriteFile(filepath.Join(dir, "config.toml"), b, 0600); e != nil {
		t.Fatal(e)
	}
	lockDir := t.TempDir()
	if e := os.Chmod(lockDir, 0700); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(executable, "__monitor", "--dir", dir, "--test-lock", filepath.Join(lockDir, "gpu.lock"))
	cmd.Stderr = stderr
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		b, e := os.ReadFile(filepath.Join(dir, "state.json"))
		var state gdsapi.State
		if e == nil && json.Unmarshal(b, &state) == nil && state.Scheduler == "holding" {
			return cmd, done, dir
		}
		select {
		case e := <-done:
			t.Fatalf("monitor exited before holding: %v state=%s", e, b)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	lastState, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	t.Fatalf("monitor never reached holding: %s", lastState)
	return nil, nil, ""
}
func workerRecordAt(t *testing.T, dir string) gdsapi.Record {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(dir, "worker.json"))
	if e != nil {
		t.Fatal(e)
	}
	var r gdsapi.Record
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	return r
}
func waitProcessGone(t *testing.T, p gdsapi.Process, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		actual, e := gdsutil.Inspect(p.PID)
		if errors.Is(e, os.ErrNotExist) || e == nil && !gdsutil.SameBirth(actual, p) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("worker remains alive pid=%d", p.PID)
}
func TestMonitorSIGKILLReleasesWorkerByEOF(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process identity integration")
	}
	helperEnv(t)
	cmd, done, dir := startTestMonitor(t)
	w := workerRecordAt(t, dir)
	if e := cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor not reaped")
	}
	waitProcessGone(t, w.Process, 3*time.Second)
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
}
func TestMonitorSIGSTOPCannotRenewWorker(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process identity integration")
	}
	if testing.Short() {
		t.Skip("15-second expiry counterexample")
	}
	helperEnv(t)
	cmd, _, dir := startTestMonitor(t)
	w := workerRecordAt(t, dir)
	if e := cmd.Process.Signal(syscall.SIGSTOP); e != nil {
		t.Fatal(e)
	}
	defer cmd.Process.Signal(syscall.SIGCONT)
	waitProcessGone(t, w.Process, LeaseDuration+3*time.Second)
}

func TestMonitorTERMConfirmsWorkerAndRecordsExit(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process identity integration")
	}
	helperEnv(t)
	cmd, done, dir := startTestMonitor(t)
	worker := workerRecordAt(t, dir)
	if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("monitor stop did not complete")
	}
	waitProcessGone(t, worker.Process, time.Second)
	record := workerRecordAt(t, dir)
	if record.Phase != "stopped" {
		t.Fatal("worker exit record", record.Phase)
	}
	b, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	var state gdsapi.State
	if e = json.Unmarshal(b, &state); e != nil {
		t.Fatal(e)
	}
	if state.Runtime != "exited" || state.WorkerPID != 0 || state.AllocatedBytes != 0 {
		t.Fatalf("stopped state: %+v", state)
	}
}

func TestUnknownPreviousRunRetainsEvidence(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	record := gdsapi.Record{Schema: 1, Token: "0123456789abcdef0123456789abcdef", Process: gdsapi.Process{PID: 99, Boot: "boot", Started: "born", Executable: "/gds"}, UUID: testUUID, ConfigHash: gdsutil.Hash([]byte("config")), ProgramHash: gdsutil.Hash([]byte("program")), Phase: "stopped"} // gitleaks:allow deterministic CPU protocol fixture, not a credential
	if e := writeJSON(filepath.Join(dir, "process.json"), record); e != nil {
		t.Fatal(e)
	}
	state := gdsapi.State{Schema: 1, Boot: "boot", Token: record.Token, UUID: testUUID, ConfigHash: record.ConfigHash, Runtime: "unknown", Scheduler: "unavailable", Problem: "probe could not be reaped"}
	if e := writeJSON(filepath.Join(dir, "state.json"), state); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e := rejectUnknownPreviousRun(dir, "boot", false); e == nil {
		t.Fatal("unknown run allowed")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(after) != string(before) {
		t.Fatal("unknown evidence modified")
	}
	if e := rejectUnknownPreviousRun(dir, "new-kernel-boot", false); e != nil {
		t.Fatal("previous kernel boot cannot retain a live child:", e)
	}
	after, _ = os.ReadFile(filepath.Join(dir, "state.json"))
	if string(after) != string(before) {
		t.Fatal("previous boot evidence modified during preflight")
	}
	// The parent replaces process.json before authorizing a managed start.
	// State's own boot provenance must still permit recovery after reboot.
	record.Process.Boot = "new-kernel-boot"
	if e := writeJSON(filepath.Join(dir, "process.json"), record); e != nil {
		t.Fatal(e)
	}
	if e := rejectUnknownPreviousRun(dir, "new-kernel-boot", true); e != nil {
		t.Fatal("managed previous boot evidence rejected:", e)
	}
	record.Process.Boot = "boot"
	if e := writeJSON(filepath.Join(dir, "process.json"), record); e != nil {
		t.Fatal(e)
	}
	state.Token = "fedcba9876543210fedcba9876543210" // gitleaks:allow deterministic CPU protocol fixture, not a credential
	if e := writeJSON(filepath.Join(dir, "state.json"), state); e != nil {
		t.Fatal(e)
	}
	if e := rejectUnknownPreviousRun(dir, "boot", true); e == nil {
		t.Fatal("same-boot unknown with mismatched token was ignored")
	}
	state.Runtime = "exited"
	if e := writeJSON(filepath.Join(dir, "state.json"), state); e != nil {
		t.Fatal(e)
	}
	if e := rejectUnknownPreviousRun(dir, "boot", true); e != nil {
		t.Fatal("valid managed stale state rejected:", e)
	}
	if e := rejectUnknownPreviousRun(dir, "boot", false); e == nil {
		t.Fatal("unmanaged mismatched historical identity was ignored")
	}
	state.Token = record.Token
	state.Runtime = "unknown"
	state.Boot = "forged-previous-boot"
	if e := writeJSON(filepath.Join(dir, "state.json"), state); e != nil {
		t.Fatal(e)
	}
	if e := rejectUnknownPreviousRun(dir, "boot", false); e == nil {
		t.Fatal("standalone accepted forged old boot without matching process provenance")
	}
}

func TestPreviousStateRejectsInvalidStructureAndMissingOwnership(t *testing.T) {
	base := gdsapi.State{Schema: 1, Boot: "boot", Token: "0123456789abcdef0123456789abcdef", UUID: testUUID, ConfigHash: gdsutil.Hash([]byte("config")), Runtime: "exited", Scheduler: "observing"} // gitleaks:allow deterministic CPU protocol fixture, not a credential
	record := gdsapi.Record{Schema: 1, Token: base.Token, Process: gdsapi.Process{PID: 99, Boot: "boot", Started: "born", Executable: "/gds"}, UUID: testUUID, ConfigHash: base.ConfigHash, ProgramHash: gdsutil.Hash([]byte("program")), Phase: "stopped"}
	for name, change := range map[string]func(*gdsapi.State){
		"schema":         func(s *gdsapi.State) { s.Schema = 0 },
		"boot":           func(s *gdsapi.State) { s.Boot = "" },
		"token":          func(s *gdsapi.State) { s.Token = "" },
		"uuid":           func(s *gdsapi.State) { s.UUID = "GPU-0" },
		"hash":           func(s *gdsapi.State) { s.ConfigHash = "bad" },
		"runtime":        func(s *gdsapi.State) { s.Runtime = "garbled" },
		"scheduler":      func(s *gdsapi.State) { s.Scheduler = "garbled" },
		"identity":       func(s *gdsapi.State) { s.ConfigHash = gdsutil.Hash([]byte("different config")) },
		"missing-record": func(s *gdsapi.State) {},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			_ = os.Chmod(dir, 0700)
			if name != "missing-record" {
				if err := writeJSON(filepath.Join(dir, "process.json"), record); err != nil {
					t.Fatal(err)
				}
			}
			state := base
			change(&state)
			path := filepath.Join(dir, "state.json")
			if err := writeJSON(path, state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = rejectUnknownPreviousRun(dir, "boot", true); err == nil {
				t.Fatal("unverifiable state was accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("unverifiable evidence was modified")
			}
		})
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rejectUnknownPreviousRun(dir, "boot", false); err == nil {
		t.Fatal("empty JSON state was accepted")
	}
}

func TestManagedStateSnapshotAuthorization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	absent, err := gdsutil.Capture(path, 64<<10, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkExpectedState(absent, true, "absent"); err != nil {
		t.Fatal(err)
	}
	data := []byte("previous authorized state")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	present, err := gdsutil.Capture(path, 64<<10, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"absent", "", gdsutil.Hash([]byte("different state"))} {
		if err = checkExpectedState(present, true, expected); err == nil {
			t.Fatalf("changed state accepted authorization %q", expected)
		}
	}
	if err = checkExpectedState(present, true, gdsutil.Hash(data)); err != nil {
		t.Fatal(err)
	}
	if err = checkExpectedState(absent, true, gdsutil.Hash(data)); err == nil {
		t.Fatal("state deletion accepted")
	}
	if err = checkExpectedState(present, false, gdsutil.Hash(data)); err == nil {
		t.Fatal("standalone start accepted managed-only authorization")
	}
}

func TestLeaseExpiryUsesNormalThreadCleanup(t *testing.T) {
	helperEnv(t)
	w := launchTestWorker(t)
	lease := leaseAt(w, "ALLOCATE", time.Now().Add(time.Second).UnixNano())
	if e := w.write(context.Background(), lease); e != nil {
		t.Fatal(e)
	}
	select {
	case response := <-w.responses:
		if response.err != nil {
			t.Fatal(response.err)
		}
	case <-time.After(time.Second):
		t.Fatal("no READY")
	}
	if e := awaitExit(t, w, 3*time.Second); e == nil {
		t.Fatal("lease expiry must be reported")
	}
	waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
}

func TestInheritedDeviceLockPreventsCrossRootOverlap(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux inherited file locking")
	}
	helperEnv(t)
	monitor, done, dir := startTestMonitor(t)
	record := workerRecordAt(t, dir)
	worker, err := os.FindProcess(record.Process.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = worker.Signal(syscall.SIGCONT); _ = gdsutil.Stop(record.Process) }()
	if err = worker.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	if err = monitor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor not reaped")
	}
	// Use another isolated Nido root with the same GPU lock. The worker inherited
	// the original open file description, so its paused process must still fence it.
	other := t.TempDir()
	_ = os.Chmod(other, 0700)
	b, _ := config().Encode()
	if err = os.WriteFile(filepath.Join(other, "config.toml"), b, 0600); err != nil {
		t.Fatal(err)
	}
	lockPath := monitor.Args[len(monitor.Args)-1]
	contender := exec.Command(testExecutable(t), "__monitor", "--dir", other, "--test-lock", lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	contender = exec.CommandContext(ctx, contender.Path, contender.Args[1:]...)
	output, err := contender.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "GPU already reserved") {
		t.Fatalf("cross-root lock was not fenced: %v %s", err, output)
	}
	assertAbsent(t, filepath.Join(other, "worker.json"))
	if err = worker.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	waitProcessGone(t, record.Process, 3*time.Second)
}

func TestWorkerDeviceLockSurvivesFileFinalizerUntilProcessExit(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux inherited file locking")
	}
	helperEnv(t)
	path := filepath.Join(t.TempDir(), "device.lock")
	lock, err := gdsutil.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	cmd := exec.Command(testExecutable(t), "__lock_pin")
	cmd.ExtraFiles = []*os.File{lock}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make(chan string, 1)
	go func() {
		var line [6]byte
		_, _ = io.ReadFull(stdout, line[:])
		ready <- string(line[:])
	}()
	select {
	case line := <-ready:
		if line != "READY\n" {
			t.Fatalf("lock helper was not ready: %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lock helper did not complete GC pressure")
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if contender, err := gdsutil.Lock(path); err == nil {
		contender.Close()
		t.Fatal("worker's device fence was released by os.File finalizer")
	}
	_ = stdin.Close()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lock helper did not exit")
	}
	contender, err := gdsutil.Lock(path)
	if err != nil {
		t.Fatal("device fence was not released at process exit:", err)
	}
	contender.Close()
}

func TestManagedMonitorParentDeathBeforeGateHasNoSideEffects(t *testing.T) {
	helperEnv(t)
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	data, _ := config().Encode()
	if e := os.WriteFile(filepath.Join(dir, "config.toml"), data, 0600); e != nil {
		t.Fatal(e)
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	defer writer.Close()
	child := exec.Command(testExecutable(t), "__monitor", "--dir", dir, "--managed")
	child.ExtraFiles = []*os.File{reader}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer child.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	time.Sleep(100 * time.Millisecond)
	for _, name := range []string{"process.json", "worker.json", "state.json", ".run.lock"} {
		assertAbsent(t, filepath.Join(dir, name))
	}
	_ = writer.Close()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("EOF authorized monitor")
		}
	case <-time.After(time.Second):
		t.Fatal("gate EOF did not stop monitor")
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}

func TestAuthorizationGateWaitIsCancelable(t *testing.T) {
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if e := readGate(ctx, reader); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("gate cancel: %v", e)
	}
}

func TestCanceledMonitorRejectsQueuedAuthorization(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = readGate(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued authorization revived canceled monitor: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "never-created")
	if err = Run(ctx, RunOptions{Dir: dir, Config: config()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled monitor continued startup: %v", err)
	}
	assertAbsent(t, dir)
}

func TestExecutableReplacementStopsFurtherChildrenAndReleasesWorker(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux executable identity integration")
	}
	helperEnv(t)
	image, err := os.ReadFile(testExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "gds-test-helper")
	if err = os.WriteFile(program, image, 0700); err != nil {
		t.Fatal(err)
	}
	monitor, done, dir := startTestMonitorProgram(t, program)
	worker := workerRecordAt(t, dir)
	replacement := program + ".candidate"
	if err = os.WriteFile(replacement, append(image, 0), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(replacement, program); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("changed executable did not fail closed")
		}
	case <-time.After(6 * time.Second):
		_ = monitor.Process.Kill()
		t.Fatal("monitor continued after executable replacement")
	}
	waitProcessGone(t, worker.Process, time.Second)
	data, e := os.ReadFile(filepath.Join(dir, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	var state gdsapi.State
	if e = json.Unmarshal(data, &state); e != nil {
		t.Fatal(e)
	}
	if state.Scheduler != "unavailable" || !strings.Contains(state.Problem, "executable changed") {
		t.Fatalf("replacement state: %+v", state)
	}
}

func TestRuntimeRecordPreservesConcurrentReplacement(t *testing.T) {
	for _, mode := range []string{"rename", "in-place"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			guard, err := gdsutil.CaptureDirectory(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "state.json")
			record, err := captureRuntimeRecord(path, guard)
			if err != nil {
				t.Fatal(err)
			}
			if err = record.write(map[string]string{"owner": "monitor"}); err != nil {
				t.Fatal(err)
			}
			data := []byte("{\"owner\":\"concurrent-editor\"}\n")
			target := path
			if mode == "rename" {
				target += ".replacement"
			}
			if err = os.WriteFile(target, data, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "rename" {
				if err = os.Rename(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err = record.write(map[string]string{"owner": "monitor-final-cleanup"}); err == nil {
				t.Fatal("concurrent record was adopted by runtime writer")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(data) {
				t.Fatalf("concurrent content changed: %s %v", got, err)
			}
		})
	}
}

func TestRuntimeRecordConflictReleasesWorkerAndPreservesEditor(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process identity integration")
	}
	for _, name := range []string{"process.json", "worker.json", "state.json"} {
		t.Run(name, func(t *testing.T) {
			helperEnv(t)
			monitor, done, dir := startTestMonitor(t)
			worker := workerRecordAt(t, dir)
			path := filepath.Join(dir, name)
			data := []byte("{\"owner\":\"concurrent-editor\"}\n")
			if err := os.WriteFile(path+".replacement", data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".replacement", path); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("changed runtime record did not fail closed")
				}
			case <-time.After(6 * time.Second):
				_ = monitor.Process.Kill()
				t.Fatal("monitor continued under replaced ownership evidence")
			}
			waitProcessGone(t, worker.Process, time.Second)
			waitForFile(t, os.Getenv("GDS_TEST_CONTEXT")+".freed", time.Second)
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(data) {
				t.Fatalf("concurrent content changed: %s %v", got, err)
			}
		})
	}
}

func TestStandaloneMonitorParticipatesInMutationLock(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Linux process identity integration")
	}
	helperEnv(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := config().Encode()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := gdsutil.Lock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testExecutable(t), "__monitor", "--dir", dir)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("standalone monitor bypassed mutation lock: %s", output)
	}
	for _, name := range []string{".run.lock", "process.json", "worker.json", "state.json"} {
		assertAbsent(t, filepath.Join(dir, name))
	}
	assertAbsent(t, os.Getenv("GDS_TEST_CONTEXT"))
}
