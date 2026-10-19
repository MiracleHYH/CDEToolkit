package conda

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDefaultRunnerUsesProvidedEnvironmentAndExplicitCondaExecutable(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "fixture conda")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = config ] && [ \"$2\" = --show ] || exit 9\nprintf '%s' \"$CONDA_ENVS_PATH\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/nonexistent", "CONDA_EXE=" + program, "CONDA_ENVS_PATH=/chosen/envs"}
	data, err := runCommand("conda", []string{"config", "--show"}, env)
	if err != nil || string(data) != "/chosen/envs" {
		t.Fatalf("default command ignored explicit env: %q %v", data, err)
	}
	if _, err := runCommand("python", nil, env); err == nil {
		t.Fatal("command lookup leaked ambient process PATH")
	}
	if _, err := commandPath("conda", []string{"CONDA_EXE=./relative", "PATH=/usr/bin"}); err == nil {
		t.Fatal("accepted relative CONDA_EXE")
	}
}

func TestDefaultRunnerBoundsOutputAndHidesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "conda")
	script := "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 20000 ]; do printf '%080d\\n' \"$i\"; i=$((i+1)); done\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if data, err := runCommand("conda", nil, []string{"PATH=" + dir}); err == nil || data != nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("unbounded output: bytes=%d err=%v", len(data), err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'secret-token' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand("conda", nil, []string{"PATH=" + dir}); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("upstream diagnostics leaked: %v", err)
	}
}

func TestBoundedOutputCancelsWithoutRetainingOversizedPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := boundedOutput{cancel: cancel}
	data := bytes.Repeat([]byte("x"), maxOutput+100)
	if n, err := out.Write(data); n != len(data) || err != nil || out.data.Len() != maxOutput || !out.full || ctx.Err() == nil {
		t.Fatalf("output bound failed: n=%d len=%d full=%v ctx=%v", n, out.data.Len(), out.full, ctx.Err())
	}
}

func TestDefaultRunnerContextCancellationStopsOwnedProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux target uses /proc to distinguish an exited child from a live process")
	}
	dir := t.TempDir()
	program := filepath.Join(dir, "fixture conda")
	childFile := filepath.Join(dir, "child.pid")
	script := "#!/bin/sh\nprintf 'PRIVATE-CANCEL-DIAGNOSTIC' >&2\nsleep 30 &\nprintf '%s\\n' \"$!\" > \"$NIDO_TEST_CHILD_PID\"\nwait\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		s := Service{Ctx: ctx, Env: []string{"PATH=/usr/bin:/bin", "CONDA_EXE=" + program, "NIDO_TEST_CHILD_PID=" + childFile}}
		data, err := s.run("conda")
		done <- result{data: data, err: err}
	}()
	var child int
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waitChild:
	for {
		if data, err := os.ReadFile(childFile); err == nil {
			child, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && child > 1 {
				break waitChild
			}
		}
		select {
		case early := <-done:
			t.Fatalf("fixture exited before cancellation: %v", early.err)
		case <-deadline:
			t.Fatal("fixture did not start its child")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.data != nil || strings.Contains(got.err.Error(), "PRIVATE-CANCEL-DIAGNOSTIC") {
			t.Fatalf("cancellation lost identity or exposed output: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parent cancellation waited for the ten-second command timeout")
	}
	// The command's process group must include the shell's child. In a
	// container an exited child may remain as an init-owned zombie briefly.
	deadline = time.After(time.Second)
	for {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat"))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if end := strings.LastIndex(string(data), ") "); end >= 0 && strings.HasPrefix(string(data[end+2:]), "Z ") {
			break
		}
		select {
		case <-deadline:
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("cancellation left the owned command's child running")
		case <-ticker.C:
		}
	}
}

func TestDefaultRunnerContextCancelledBeforeLookupDoesNotExecute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data, err := runCommandContext(ctx, "conda", nil, []string{"PATH=/nonexistent"})
	if data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context reached command lookup: %v", err)
	}
}
