package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
)

// Fixture construction deliberately bypasses the production writer's expected
// snapshot. Production lifecycle writes must always use saveRecordExpected.
func (s *Service) saveRecord(r processRecord) error {
	path, err := s.recordPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}

// The process fixture is this test executable, explicitly selected through a
// private test environment. It only opens temporary loopback listeners; no
// Mihomo program, external subscription, installation or user data is used.
func TestMain(m *testing.M) {
	if handled, exit := HandleLauncher(os.Args[1:]); handled {
		os.Exit(exit)
	}
	if os.Getenv("NIDO_PROXY_RUNTIME_HELPER") == "1" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v":
			fmt.Println("Mihomo Meta v-test")
			os.Exit(0)
		case "-t":
			_, _ = io.Copy(io.Discard, os.Stdin)
			if os.Getenv("NIDO_PROXY_RUNTIME_MODE") == "validate-sleep" {
				time.Sleep(time.Minute)
			}
			os.Exit(0)
		case "-d":
			os.Exit(runtimeHelper())
		}
	}
	reports, err := prepareIsolatedRaceReports(os.Getenv("NIDO_TEST_RACE_REPORT_DIR"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if reports.path != "" {
		// The parent's detector starts with stderr output. Create and verify
		// the private directory before giving future children a log_path;
		// ThreadSanitizer may otherwise create that directory with mode 0755.
		if err := os.Setenv("GORACE", "halt_on_error=1 log_path="+filepath.Join(reports.path, "race")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	status := m.Run()
	// Detached launchers/helpers may finish after their Start call returns.
	// Their detector reports must fail this test process as well.
	if err := reports.check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		status = 1
	}
	os.Exit(status)
}

func runtimeHelper() int {
	if marker := os.Getenv("NIDO_PROXY_RUNTIME_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("exec"), 0600)
	}
	if os.Getenv("NIDO_PROXY_RUNTIME_MODE") == "exit" {
		return 23
	}
	if os.Getenv("NIDO_PROXY_RUNTIME_MODE") == "exit-zero" {
		return 0
	}
	var listeners []net.Listener
	if os.Getenv("NIDO_PROXY_RUNTIME_MODE") != "no-listen" {
		if len(os.Args) != 5 {
			return 91
		}
		data, err := os.ReadFile(os.Args[4])
		if err != nil {
			return 92
		}
		config, err := ParseConfig(data, os.Args[2])
		if err != nil {
			return 93
		}
		for _, endpoint := range config.Endpoints {
			network := "tcp4"
			if net.ParseIP(endpoint.Host).To4() == nil {
				network = "tcp6"
			}
			listener, err := net.Listen(network, net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)))
			if err != nil {
				return 24
			}
			listeners = append(listeners, listener)
		}
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	<-stop
	for _, listener := range listeners {
		listener.Close()
	}
	return 0
}

type runtimeFakeRunner struct {
	process                    Process
	alive                      bool
	ready                      bool
	starts, stops, validations int
	stopErr                    error
	exitErr                    error
	inspectErr                 error
	onRun                      func(Command)
	onStart                    func(Command)
	onInspect                  func()
	onOwns                     func()
	onStop                     func()
	last                       Command
}

func (r *runtimeFakeRunner) Run(c Command) ([]byte, error) {
	r.validations++
	if r.onRun != nil {
		r.onRun(c)
	}
	return []byte("Mihomo Meta v-test"), nil
}
func (r *runtimeFakeRunner) Start(c Command, commit func(Process) error) (Process, <-chan error, error) {
	r.starts++
	r.last = c
	p := Process{PID: 43210, UID: uint32(os.Geteuid()), Boot: "test-boot", Started: strconv.Itoa(100 + r.starts), Executable: c.Launcher}
	r.process, r.alive = p, true
	if r.onStart != nil {
		r.onStart(c)
	}
	if err := commit(p); err != nil {
		r.alive = false
		return p, nil, err
	}
	r.process, r.alive = p, true
	r.process.Executable = c.Program
	done := make(chan error, 1)
	if r.exitErr != nil {
		r.alive = false
		done <- r.exitErr
		close(done)
	}
	return p, done, nil
}
func (r *runtimeFakeRunner) Inspect(pid int) (Process, error) {
	if r.onInspect != nil {
		r.onInspect()
	}
	if r.inspectErr != nil {
		if errors.Is(r.inspectErr, errProcessTransition) {
			return r.process, r.inspectErr
		}
		return Process{}, r.inspectErr
	}
	if !r.alive || pid != r.process.PID {
		return Process{}, os.ErrNotExist
	}
	return r.process, nil
}
func (r *runtimeFakeRunner) Stop(p Process) error {
	r.stops++
	if r.onStop != nil {
		r.onStop()
	}
	if r.stopErr != nil {
		return r.stopErr
	}
	if r.alive && r.process == p {
		r.alive = false
	}
	return nil
}
func (r *runtimeFakeRunner) OwnsEndpoint(p Process, _ string) (bool, error) {
	if r.onOwns != nil {
		r.onOwns()
	}
	return r.alive && r.process == p && r.ready, nil
}

func runtimeService(t *testing.T, runner Runner) *Service {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "proxy")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	// Mock runners never execute the program. Keep their fixture small so
	// hashing a race test binary cannot consume the simulated readiness deadline.
	// OSRunner tests still launch the real, isolated helper executable.
	data := coreNativeFixture(runtime.GOOS, runtime.GOARCH)
	if runner == nil {
		data, err = os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "mihomo"), data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("mixed-port: 17891\nallow-lan: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return &Service{Root: root, Runner: runner, Launcher: executable, StartTimeout: time.Second}
}

func TestRuntimeReadyAndConfigChange(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	s.Env = []string{"PATH=/usr/bin", "CLASH_CONFIG_STRING=bad", "SAFE_PATHS=/", "SKIP_SAFE_PATH_CHECK=true"}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	status, err := s.Runtime()
	if err != nil || status.State != "running" || !status.Listeners || status.ConfigChanged {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if err := s.Start(); err != nil || runner.starts != 1 {
		t.Fatalf("idempotent start: %v (%d)", err, runner.starts)
	}
	if got := strings.Join(runner.last.Env, " "); strings.Contains(got, "CLASH_") || strings.Contains(got, "SAFE_PATH") {
		t.Fatalf("unsafe environment: %s", got)
	}
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte("mixed-port: 17892\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err = s.Runtime()
	if err != nil || !status.ConfigChanged || status.State != "running" {
		t.Fatalf("changed=%+v err=%v", status, err)
	}
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("changed start: %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	status, err = s.Runtime()
	if err != nil || status.State != "stopped" || runner.stops != 1 {
		t.Fatalf("stop=%+v err=%v calls=%d", status, err, runner.stops)
	}
}

func TestRuntimeDoesNotAcceptUnownedListener(t *testing.T) {
	runner := &runtimeFakeRunner{ready: false}
	s := runtimeService(t, runner)
	s.StartTimeout = 70 * time.Millisecond
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("start: %v", err)
	}
	if runner.starts != 1 || runner.stops != 1 || runner.alive {
		t.Fatalf("unsafe cleanup: %+v", runner)
	}
	r, err := s.readRecord()
	if err != nil || r.Phase != "stopped" {
		t.Fatalf("record=%+v %v", r, err)
	}
}

func TestRuntimeReadinessCannotSucceedAfterStartupDeadline(t *testing.T) {
	for _, delayedObservation := range []int{1, 2} {
		t.Run(strconv.Itoa(delayedObservation), func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: true}
			s := runtimeService(t, runner)
			s.StartTimeout = 50 * time.Millisecond
			observations := 0
			runner.onOwns = func() {
				observations++
				if observations == delayedObservation {
					time.Sleep(75 * time.Millisecond)
				}
			}
			if err := s.Start(); err == nil || !strings.Contains(err.Error(), "启动超时") {
				t.Fatalf("late readiness was accepted: %v", err)
			}
			if runner.starts != 1 || runner.stops != 1 || runner.alive {
				t.Fatalf("late launch was not safely stopped: starts=%d stops=%d alive=%v", runner.starts, runner.stops, runner.alive)
			}
			record, err := s.readRecord()
			if err != nil || record.Phase != "stopped" {
				t.Fatalf("late launch retained a running phase: %+v %v", record, err)
			}
		})
	}
}

func TestRuntimeTimeoutKeepsUnstoppableIdentity(t *testing.T) {
	runner := &runtimeFakeRunner{stopErr: fmt.Errorf("refuses exit")}
	s := runtimeService(t, runner)
	s.StartTimeout = 70 * time.Millisecond
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "保留记录") {
		t.Fatalf("start: %v", err)
	}
	if err := s.Start(); err == nil || runner.starts != 1 {
		t.Fatalf("replaced live starting: %v (%d)", err, runner.starts)
	}
	if err := s.requireStopped(); err == nil {
		t.Fatal("live starting allowed mutation")
	}
	if err := s.Stop(); err == nil {
		t.Fatal("failed stop reported success")
	}
}

func TestRuntimeEarlyExitAndReadOnlyStatus(t *testing.T) {
	runner := &runtimeFakeRunner{exitErr: fmt.Errorf("exit status 23")}
	s := runtimeService(t, runner)
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "23") {
		t.Fatalf("early exit: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	checks := runner.validations
	status, err := s.Runtime()
	after, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if err != nil || status.State != "exited" || string(before) != string(after) || runner.validations != checks {
		t.Fatalf("read-only status=%+v %v", status, err)
	}
}

func TestProcessMetadataMissingChildDoesNotMeanPIDGone(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "pid")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, child := range []string{"exe", "stat", "status"} {
		cause := &os.PathError{Op: "read", Path: filepath.Join(directory, child), Err: os.ErrNotExist}
		err := processMetadataError(directory, cause)
		if !errors.Is(err, errProcessTransition) || errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing %s was treated as an absent PID: %v", child, err)
		}
	}
	if err := processMetadataError(directory, os.ErrPermission); !errors.Is(err, os.ErrPermission) || errors.Is(err, errProcessTransition) {
		t.Fatalf("permission error became retryable: %v", err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := processMetadataError(directory, os.ErrNotExist); !errors.Is(err, os.ErrNotExist) || errors.Is(err, errProcessTransition) {
		t.Fatalf("confirmed missing directory not absent: %v", err)
	}
	if err := os.WriteFile(directory, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := processMetadataError(directory, os.ErrNotExist); err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, errProcessTransition) {
		t.Fatalf("non-directory inode treated as a PID: %v", err)
	}
}

func TestProcessSingleDeadThreadRequiresExplicitOne(t *testing.T) {
	for _, test := range []struct {
		name, status string
		want         bool
	}{
		{"one", "State:\tZ (zombie)\nThreads:\t1\n", true},
		{"zero-not-proof", "Threads:\t0\n", false},
		{"workers-remain", "Threads:\t2\n", false},
		{"missing", "State:\tZ (zombie)\n", false},
		{"invalid", "Threads:\tunknown\n", false},
		{"duplicate", "Threads:\t1\nThreads:\t1\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := singleProcessThread([]byte(test.status)); got != test.want {
				t.Fatalf("single thread=%v want=%v", got, test.want)
			}
		})
	}
}

func TestProcessExitWaitsOnlyForClassifiedTransition(t *testing.T) {
	p := Process{PID: 43210, UID: uint32(os.Geteuid()), Boot: "test-boot", Started: "test-birth", Executable: "/fixture/core"}
	for _, kind := range []string{"exits", "persistent", "permission"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			inspect := func(int) (Process, error) {
				calls++
				if kind == "permission" {
					return Process{}, os.ErrPermission
				}
				if kind == "exits" && calls > 1 {
					return Process{}, os.ErrNotExist
				}
				return p, fmt.Errorf("exit threads still changing: %w", errProcessTransition)
			}
			err := waitProcessExit(p, inspect, time.Now().Add(55*time.Millisecond))
			if kind == "exits" {
				if err != nil || calls < 2 {
					t.Fatalf("transient exit rejected: %v calls=%d", err, calls)
				}
			} else if kind == "persistent" {
				if err == nil || calls < 2 || errors.Is(err, os.ErrNotExist) {
					t.Fatalf("persistent threads claimed stopped: %v calls=%d", err, calls)
				}
			} else if !errors.Is(err, os.ErrPermission) || calls != 1 {
				t.Fatalf("permission error was swallowed/retried: %v calls=%d", err, calls)
			}
		})
	}
}

func TestRuntimeStartupWaitsForStableExecObservation(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true, inspectErr: fmt.Errorf("exe changed: %w", errProcessTransition)}
	s := runtimeService(t, runner)
	observations := 0
	runner.onInspect = func() {
		observations++
		if observations == 3 {
			runner.inspectErr = nil
		}
	}
	if err := s.Start(); err != nil || runner.starts != 1 || runner.stops != 0 || observations < 3 {
		t.Fatalf("legitimate exec transition rejected: %v observations=%d", err, observations)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStartupTransitionDoesNotHideUnknownIdentity(t *testing.T) {
	for _, kind := range []string{"permission", "program", "different-birth"} {
		t.Run(kind, func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: true}
			s := runtimeService(t, runner)
			observations := 0
			runner.onInspect = func() {
				observations++
				runner.inspectErr = fmt.Errorf("exe transition: %w", errProcessTransition)
				if kind == "different-birth" {
					runner.process.Started = "foreign-birth"
				} else if observations > 1 {
					if kind == "permission" {
						runner.inspectErr = os.ErrPermission
					} else {
						runner.inspectErr = nil
						runner.process.Executable = "/foreign/program"
					}
				}
			}
			if err := s.Start(); err == nil || runner.stops != 0 || !runner.alive || observations > 2 {
				t.Fatalf("unknown identity was accepted or retried: %v observations=%d", err, observations)
			}
		})
	}
}

func TestRuntimeStopWaitsForExitThreadTransitionBeforeRecordWrite(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	observations := 0
	runner.onStop = func() {
		runner.inspectErr = fmt.Errorf("exit threads: %w", errProcessTransition)
		runner.onInspect = func() {
			observations++
			if record, err := s.readRecord(); err != nil || record.Phase != "running" {
				t.Fatalf("transition prematurely wrote stopped: %+v %v", record, err)
			}
			if observations == 3 {
				runner.inspectErr, runner.onInspect = nil, nil
			}
		}
	}
	if err := s.Stop(); err != nil || observations < 3 {
		t.Fatalf("exit transition rejected: %v observations=%d", err, observations)
	}
	if record, err := s.readRecord(); err != nil || record.Phase != "stopped" {
		t.Fatalf("confirmed exit not committed: %+v %v", record, err)
	}
}

func TestRuntimeTransitionDeadlinePreservesRunningRecord(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	record, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	runner.inspectErr = fmt.Errorf("threads still present: %w", errProcessTransition)
	if _, err := s.waitRecordActual(record, nil, time.Now().Add(45*time.Millisecond)); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("persistent transition claimed absent: %v", err)
	}
	if current, err := s.readRecord(); err != nil || current.Phase != "running" || !runner.alive || runner.stops != 0 {
		t.Fatalf("persistent transition changed record/process: %+v %v", current, err)
	}
	runner.inspectErr = nil
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeUnknownIdentityBlocksStopAndReplacement(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	runner.process.Executable = "/other/program"
	if err := s.Stop(); err == nil || runner.stops != 0 {
		t.Fatalf("wrong program signaled: %v", err)
	}
	if err := s.Start(); err == nil || runner.starts != 1 {
		t.Fatalf("wrong program replaced: %v", err)
	}
	runner.inspectErr = os.ErrPermission
	if err := s.Stop(); err == nil {
		t.Fatal("unknown treated as gone")
	}
}

func TestRuntimeReusedPIDAndPreviousBootAreNotStopped(t *testing.T) {
	for _, kind := range []string{"birth", "boot"} {
		t.Run(kind, func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: true}
			s := runtimeService(t, runner)
			if err := s.Start(); err != nil {
				t.Fatal(err)
			}
			if kind == "birth" {
				runner.process.Started = "200"
			} else {
				runner.process.Boot = "next-boot"
			}
			if err := s.Stop(); err != nil || runner.stops != 0 || !runner.alive {
				t.Fatalf("reused identity stopped: %v", err)
			}
			status, err := s.Runtime()
			if err != nil || status.State != "stopped" {
				t.Fatalf("status %+v %v", status, err)
			}
		})
	}
}

func TestRuntimeRestartPreservesOldOnInvalidConfig(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte("mixed-port: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(); err == nil || runner.stops != 0 || !runner.alive {
		t.Fatalf("invalid restart: %v", err)
	}
}

func TestRuntimeRestartAppliesValidCandidateWithNewBirth(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	oldBirth := runner.process.Started
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte("mixed-port: 17892\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Restart(); err != nil {
		t.Fatal(err)
	}
	if runner.starts != 2 || runner.stops != 1 || runner.process.Started == oldBirth {
		t.Fatalf("restart identity: %+v", runner)
	}
	status, err := s.Runtime()
	if err != nil || status.State != "running" || status.ConfigChanged {
		t.Fatalf("restart status %+v %v", status, err)
	}
}

func TestRuntimeRestartChecksCandidateAgainBeforeStopping(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	runner.onRun = func(Command) { path, _ := s.ConfigPath(); _ = os.WriteFile(path, []byte("mixed-port: 0\n"), 0600) }
	if err := s.Restart(); err == nil || runner.stops != 0 || !runner.alive {
		t.Fatalf("changed restart stopped old instance: %v", err)
	}
}

func TestRuntimeRestartPreservesRecordReplacedDuringValidation(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	var preserved []byte
	runner.onRun = func(Command) {
		replacement.Process.PID++
		replacement.Process.Started = "replacement-birth"
		if err := s.saveRecord(replacement); err != nil {
			t.Fatal(err)
		}
		runner.process = replacement.Process
		runner.process.Executable = replacement.Program
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.Restart(); err == nil || runner.stops != 0 || runner.starts != 1 || !runner.alive {
		t.Fatalf("restart adopted the replacement: %v starts=%d stops=%d alive=%v", err, runner.starts, runner.stops, runner.alive)
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if string(current) != string(preserved) {
		t.Fatal("restart overwrote replacement record")
	}
}

func TestRuntimeLiveLauncherCannotBeOverwritten(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	runner.process.Executable = s.Launcher
	if err := s.Start(); err == nil || runner.starts != 1 {
		t.Fatalf("live launcher replaced: %v", err)
	}
	status, err := s.Runtime()
	if err != nil || status.State != "starting" {
		t.Fatalf("launcher status %+v %v", status, err)
	}
	if err := s.Stop(); err != nil || runner.stops != 1 {
		t.Fatalf("launcher stop %v", err)
	}
}

func TestRuntimeCandidateChangeBeforeCommitCancelsLaunch(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	runner.onStart = func(Command) { path, _ := s.ConfigPath(); _ = os.WriteFile(path, []byte("mixed-port: 17892\n"), 0600) }
	if err := s.Start(); err == nil || runner.alive {
		t.Fatalf("changed candidate launched: %v", err)
	}
	if _, err := s.readRecord(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected record: %v", err)
	}
}

func TestRuntimeProgramReplacementDuringReadinessCannotSucceed(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	_, program, _ := s.paths()
	once := false
	runner.onOwns = func() {
		if once {
			return
		}
		once = true
		data, err := os.ReadFile(program)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(program, append(data, 0), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Start(); err == nil || !strings.Contains(err.Error(), "程序在启动期间发生变化") {
		t.Fatalf("replaced program ready: %v", err)
	}
	if runner.alive || runner.stops != 1 {
		t.Fatalf("replaced program not safely stopped: %+v", runner)
	}
}

func TestRuntimeProgramHashMismatchIsUnknownButCanStop(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	_, program, _ := s.paths()
	data, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, append(data, 0), 0700); err != nil {
		t.Fatal(err)
	}
	status, err := s.Runtime()
	if err == nil || status.State != "unknown" || status.Diagnostic == "" {
		t.Fatalf("mismatch status=%+v %v", status, err)
	}
	if err := s.Stop(); err != nil || runner.stops != 1 {
		t.Fatalf("could not stop precise identity: %v", err)
	}
	status, err = s.Runtime()
	if err != nil || status.State != "stopped" {
		t.Fatalf("stopped program mismatch %+v %v", status, err)
	}
}

func TestRuntimeNewRecordDuringCoreValidationIsPreserved(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	_, program, _ := s.paths()
	programData, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	config, err := s.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	p := Process{PID: 54321, UID: uint32(os.Geteuid()), Boot: "test-boot", Started: "later", Executable: s.Launcher}
	newRecord := processRecord{Process: p, Program: program, Launcher: s.Launcher, ProgramHash: hash(programData), ConfigHash: config.Digest, Endpoints: []string{config.Endpoints[0].URL()}, Phase: "running"}
	var preserved []byte
	runner.onRun = func(Command) {
		if err := s.saveRecord(newRecord); err != nil {
			t.Fatal(err)
		}
		runner.process, runner.alive = p, true
		runner.process.Executable = program
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.Start(); err == nil {
		t.Fatal("new record overwritten")
	}
	current, err := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(preserved) || !runner.alive || runner.stops != 0 {
		t.Fatal("new instance record or process was modified")
	}
}

func TestRuntimeNewRecordDuringStopWaitIsPreserved(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	old, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	newRecord := old
	newRecord.Process.PID++
	newRecord.Process.Started = "later"
	var preserved []byte
	runner.onStop = func() {
		if err := s.saveRecord(newRecord); err != nil {
			t.Fatal(err)
		}
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.Stop(); err == nil {
		t.Fatal("stop overwrote replaced record")
	}
	current, err := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(preserved) {
		t.Fatal("new process record was modified")
	}
}

func TestRuntimeReplacedRecordDuringReadinessStopsOnlyThisLaunch(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(strconv.FormatBool(ready), func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: ready}
			s := runtimeService(t, runner)
			if !ready {
				s.StartTimeout = 70 * time.Millisecond
			}
			var preserved []byte
			runner.onOwns = func() {
				runner.onOwns = nil
				replacement, err := s.readRecord()
				if err != nil {
					t.Fatal(err)
				}
				replacement.Process.PID++
				replacement.Process.Started = "other-birth"
				if err := s.saveRecord(replacement); err != nil {
					t.Fatal(err)
				}
				preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
			}
			if err := s.Start(); err == nil || !strings.Contains(err.Error(), "已安全停止") {
				t.Fatalf("untracked launch was not stopped: %v", err)
			}
			current, err := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
			if err != nil || string(current) != string(preserved) || runner.alive || runner.stops != 1 {
				t.Fatalf("replacement changed or old child survived: err=%v alive=%v stops=%d", err, runner.alive, runner.stops)
			}
		})
	}
}

func TestRuntimeStartCannotAdoptRecordReplacedDuringInitialInspect(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	old, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	var preserved []byte
	runner.onInspect = func() {
		runner.onInspect = nil
		old.Process.PID++
		old.Process.Started = "other-birth"
		if err := s.saveRecord(old); err != nil {
			t.Fatal(err)
		}
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.Start(); err == nil || runner.starts != 1 {
		t.Fatalf("adopted a replacement record: %v (starts %d)", err, runner.starts)
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if string(current) != string(preserved) {
		t.Fatal("replacement record changed")
	}
}

func TestRuntimeStopRechecksRecordAfterInspectBeforeSignal(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	var preserved []byte
	runner.onInspect = func() {
		runner.onInspect = nil
		replacement.Process.PID++
		replacement.Process.Started = "other-birth"
		if err := s.saveRecord(replacement); err != nil {
			t.Fatal(err)
		}
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.Stop(); err == nil || runner.stops != 0 || !runner.alive {
		t.Fatalf("record replaced during inspect authorized stop: %v", err)
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if string(current) != string(preserved) {
		t.Fatal("replacement record changed")
	}
}

func TestRuntimeStoppedCheckRejectsRecordReplacedDuringInspect(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	var preserved []byte
	runner.onInspect = func() {
		runner.onInspect = nil
		replacement.Process.PID++
		replacement.Process.Started = "other-birth"
		if err := s.saveRecord(replacement); err != nil {
			t.Fatal(err)
		}
		preserved, _ = os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	}
	if err := s.requireStopped(); err == nil {
		t.Fatal("replacement record authorized a stopped-state mutation")
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if string(current) != string(preserved) {
		t.Fatal("replacement record changed")
	}
}

func TestRuntimeStopCannotCommitThroughReplacedLock(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	runner.onStop = func() {
		path := filepath.Join(s.Root, "proxy", ".lock")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(); err == nil || runner.alive {
		t.Fatalf("replaced lock accepted: %v (alive %v)", err, runner.alive)
	}
	current, _ := os.ReadFile(filepath.Join(s.Root, "proxy", "process.json"))
	if string(current) != string(before) {
		t.Fatal("stop committed through replacement lock")
	}
}

func TestRuntimeValidationCannotProceedAfterLockReplacement(t *testing.T) {
	for _, action := range []string{"start", "restart"} {
		t.Run(action, func(t *testing.T) {
			runner := &runtimeFakeRunner{ready: true}
			s := runtimeService(t, runner)
			if action == "restart" {
				if err := s.Start(); err != nil {
					t.Fatal(err)
				}
			}
			starts := runner.starts
			runner.onRun = func(Command) {
				path := filepath.Join(s.Root, "proxy", ".lock")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			if action == "restart" {
				err = s.Restart()
			} else {
				err = s.Start()
			}
			if err == nil || runner.starts != starts || runner.stops != 0 || runner.alive != (action == "restart") {
				t.Fatalf("%s adopted replaced lock: %v starts=%d stops=%d alive=%v", action, err, runner.starts, runner.stops, runner.alive)
			}
		})
	}
}

func TestRuntimeDeletedImageIsUnknownButPreciselyStoppable(t *testing.T) {
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	runner.process.ExecutableDeleted = true
	status, err := s.Runtime()
	if err == nil || status.State != "unknown" || status.Listeners || !strings.Contains(status.Diagnostic, "映像") {
		t.Fatalf("deleted image claimed ready: %+v %v", status, err)
	}
	if err := s.Stop(); err != nil || runner.alive || runner.stops != 1 {
		t.Fatalf("exact deleted image could not stop: %v", err)
	}
}

func TestProcessLogRejectsFIFOAndLinksWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if f, err := openProcessLog(fifo); err == nil {
		f.Close()
		t.Fatal("accepted FIFO")
	}
	if time.Since(start) > time.Second {
		t.Fatal("FIFO blocked")
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"symbolic", "hard"} {
		path := filepath.Join(dir, kind)
		var err error
		if kind == "hard" {
			err = os.Link(target, path)
		} else {
			err = os.Symlink(target, path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if f, err := openProcessLog(path); err == nil {
			f.Close()
			t.Fatalf("accepted %s link", kind)
		}
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original" {
		t.Fatal("link target modified")
	}
}

func TestOSProcessKernelIdentityAndEndpointOwnership(t *testing.T) {
	requireKernelIdentity(t)
	before, err := inspectProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("LC_ALL")
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	after, err := inspectProcess(os.Getpid())
	if err != nil || before != after {
		t.Fatalf("locale changed identity: %+v %+v %v (old %s)", before, after, err, old)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	owned, err := (OSRunner{}).OwnsEndpoint(before, "http://"+listener.Addr().String())
	if err != nil || !owned {
		t.Fatalf("own listener: %v %v", owned, err)
	}
	for _, host := range []string{"127.0.0.2", "::1"} {
		owned, err = (OSRunner{}).OwnsEndpoint(before, "http://"+net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil || owned {
			t.Fatalf("wrong address/family accepted %s: %v %v", host, owned, err)
		}
	}
	wrong := before
	wrong.Started += "-wrong"
	if err := (OSRunner{}).Stop(wrong); err == nil {
		t.Fatal("wrong identity authorized signal")
	}
}

func TestOSLauncherCommitsBeforeExecAndStopsIsolatedHelper(t *testing.T) {
	requireKernelIdentity(t)
	s := runtimeService(t, nil)
	s.StartTimeout = 3 * time.Second
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("mixed-port: %d\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	s.Env = append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1")
	t.Cleanup(func() { _ = s.Stop() })
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	status, err := s.Runtime()
	if err != nil || status.State != "running" {
		t.Fatalf("%+v %v", status, err)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	status, err = s.Runtime()
	if err != nil || status.State != "stopped" {
		t.Fatalf("%+v %v", status, err)
	}
}

func TestLinuxRepeatedLauncherExecTransitions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs exec/exit transition regression")
	}
	requireKernelIdentity(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := runtimeService(t, nil)
	s.StartTimeout = 3 * time.Second
	s.Env = append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1")
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("mixed-port: %d\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Errorf("isolated repeated launcher cleanup failed: %v", err)
		}
	})
	var previous Process
	for iteration := 0; iteration < 16; iteration++ {
		if err := s.Start(); err != nil {
			t.Fatalf("iteration %d start: %v", iteration, err)
		}
		record, err := s.readRecord()
		if err != nil || sameBirth(record.Process, previous) {
			t.Fatalf("iteration %d reused birth: %+v %v", iteration, record, err)
		}
		actual, err := s.recordActual(record)
		if err != nil || !sameBirth(actual, record.Process) || actual.Executable != record.Program {
			t.Fatalf("iteration %d exec identity: %+v %v", iteration, actual, err)
		}
		previous = record.Process
		if err := s.Stop(); err != nil {
			t.Fatalf("iteration %d stop: %v", iteration, err)
		}
		observed, err := s.runner().Inspect(previous.PID)
		if !errors.Is(err, os.ErrNotExist) && (err != nil || sameBirth(observed, previous)) {
			t.Fatalf("iteration %d old child still present: %v", iteration, err)
		}
	}
}

func TestOSLauncherRecordFailureAndParentEOFNeverExec(t *testing.T) {
	requireKernelIdentity(t)
	s := runtimeService(t, nil)
	dir, program, _ := s.paths()
	marker := filepath.Join(dir, "executed")
	env := append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1", "NIDO_PROXY_RUNTIME_MARKER="+marker)
	p, _, err := (OSRunner{}).Start(Command{Program: program, Dir: dir, Log: filepath.Join(dir, "output.log"), Launcher: s.Launcher, Args: []string{"-d", dir, "-f", filepath.Join(dir, "config.yaml")}, Env: env}, func(p Process) error { return fmt.Errorf("record failure") })
	if err == nil {
		t.Fatal("record failure ignored")
	}
	if _, err := inspectProcess(p.PID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untracked child survived: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("core executed before record")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(s.Launcher, launcherArgument, program, "-d", dir, "-f", filepath.Join(dir, "config.yaml"))
	cmd.Env = env
	cmd.Dir = dir
	cmd.ExtraFiles = []*os.File{read}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	read.Close()
	write.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unauthorized launcher succeeded")
		}
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		t.Fatal("launcher ignored parent EOF")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("core executed after parent disappeared")
	}
}

func requireKernelIdentity(t *testing.T) {
	t.Helper()
	if _, err := inspectProcess(os.Getpid()); err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("当前 sandbox 拒绝只读内核身份接口；需在允许该读取的环境执行 OS 隔离测试")
		}
		t.Fatal(err)
	}
}

func TestOSRunnerCoreCallTimeoutIsBounded(t *testing.T) {
	s := runtimeService(t, nil)
	dir, program, _ := s.paths()
	start := time.Now()
	_, err := (OSRunner{}).Run(Command{Program: program, Dir: dir, Args: []string{"-t"}, Env: append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1", "NIDO_PROXY_RUNTIME_MODE=validate-sleep"), Timeout: 40 * time.Millisecond})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not bounded: %v (%v)", err, time.Since(start))
	}
}

func helperServiceAtPort(t *testing.T, port int, mode string) *Service {
	t.Helper()
	s := runtimeService(t, nil)
	s.StartTimeout = time.Second
	path, _ := s.ConfigPath()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("mixed-port: %d\n", port)), 0600); err != nil {
		t.Fatal(err)
	}
	s.Env = append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1", "NIDO_PROXY_RUNTIME_MODE="+mode)
	t.Cleanup(func() { _ = s.Stop() })
	return s
}

func TestOSRuntimeOccupiedPortAndEarlyExit(t *testing.T) {
	requireKernelIdentity(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, test := range []struct{ mode, code string }{{"", "24"}, {"exit", "23"}, {"exit-zero", "0"}} {
		t.Run(test.mode+test.code, func(t *testing.T) {
			s := helperServiceAtPort(t, port, test.mode)
			// A race-instrumented helper may delay os.Exit(0) by one second.
			// Keep that test instrumentation outside the startup deadline.
			s.StartTimeout = 3 * time.Second
			err := s.Start()
			if err == nil || !strings.Contains(err.Error(), "(exit status "+test.code+")") {
				t.Fatalf("exit status lost: %v", err)
			}
			status, e := s.Runtime()
			if e != nil || status.State != "exited" || status.Listeners {
				t.Fatalf("external listener accepted %+v %v", status, e)
			}
		})
	}
}

func TestOSRuntimeImageReplacementRetainsStopAuthority(t *testing.T) {
	requireKernelIdentity(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	s := helperServiceAtPort(t, port, "")
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	r, err := s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.Program)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(r.Program), "replacement")
	if err := os.WriteFile(replacement, append(data, 0), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, r.Program); err != nil {
		t.Fatal(err)
	}
	p, err := s.recordActual(r)
	if err != nil || !sameBirth(p, r.Process) {
		t.Fatalf("disk replacement lost original birth: %+v %v", p, err)
	}
	if runtime.GOOS == "linux" && !p.ExecutableDeleted {
		t.Fatal("Linux lost the running image's deleted marker")
	}
	status, err := s.Runtime()
	if err == nil || status.State != "unknown" || status.Listeners {
		t.Fatalf("replacement claimed ready: %+v %v", status, err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("could not stop exact birth after replacement: %v", err)
	}
}

func TestLauncherRejectsInvalidAuthorizationWithoutExec(t *testing.T) {
	for _, kind := range []string{"regular-file", "wrong-token"} {
		t.Run(kind, func(t *testing.T) {
			s := runtimeService(t, nil)
			dir, program, _ := s.paths()
			marker := filepath.Join(dir, "executed")
			var input *os.File
			if kind == "regular-file" {
				var err error
				input, err = os.CreateTemp(dir, "authorization")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := input.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
				if _, err := input.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
			} else {
				var write *os.File
				var err error
				input, write, err = os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := write.Write([]byte{0}); err != nil {
					t.Fatal(err)
				}
				write.Close()
			}
			defer input.Close()
			cmd := exec.Command(s.Launcher, launcherArgument, program, "-d", dir, "-f", filepath.Join(dir, "config.yaml"))
			cmd.Env = append(os.Environ(), "NIDO_PROXY_RUNTIME_HELPER=1", "NIDO_PROXY_RUNTIME_MARKER="+marker)
			cmd.Dir, cmd.ExtraFiles = dir, []*os.File{input}
			if err := cmd.Run(); err == nil {
				t.Fatal("launcher accepted invalid authorization")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("core executed without valid authorization")
			}
		})
	}
}

func TestOSRuntimeRootsNeverStopEachOther(t *testing.T) {
	requireKernelIdentity(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	a := helperServiceAtPort(t, port, "")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	b := helperServiceAtPort(t, port, "")
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(); err == nil {
		t.Fatal("second root claimed another root's listener")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	status, err := a.Runtime()
	if err != nil || status.State != "running" {
		t.Fatalf("root A was affected: %+v %v", status, err)
	}
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
}
