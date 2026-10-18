package main

import (
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

type watchdogTestClock struct{ now atomic.Int64 }

func (c *watchdogTestClock) Now() (int64, error) { return c.now.Load(), nil }

type brokenClock struct{}

func (brokenClock) Now() (int64, error) { return 0, errors.New("clock failed") }

func TestProbeWatchdogUsesAbsoluteDeadline(t *testing.T) {
	c := &watchdogTestClock{}
	c.now.Store(100)
	exited := make(chan int, 1)
	stop, err := startProbeWatchdog(c, time.Second, func(code int) { exited <- code })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// Simulate suspend or a delayed driver call. The original absolute deadline
	// expires even though only a few wall-clock milliseconds passed in the test.
	c.now.Store(100 + int64(time.Second))
	select {
	case code := <-exited:
		if code != 124 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("expired probe did not terminate")
	}
	if _, err := startProbeWatchdog(brokenClock{}, time.Second, func(int) {}); err == nil {
		t.Fatal("unknown clock accepted")
	}
}

func TestProbeWatchdogReallyExitsDisposableProcess(t *testing.T) {
	if os.Getenv("GDS_PROBE_WATCHDOG_TEST") == "1" {
		c := &watchdogTestClock{}
		_, err := startProbeWatchdog(c, time.Nanosecond, os.Exit)
		if err != nil {
			os.Exit(125)
		}
		c.now.Store(1)
		time.Sleep(3 * time.Second)
		os.Exit(126)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProbeWatchdogReallyExitsDisposableProcess$")
	cmd.Env = append(os.Environ(), "GDS_PROBE_WATCHDOG_TEST=1")
	err := cmd.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 124 {
		t.Fatalf("probe watchdog did not exit 124: %v", err)
	}
}
