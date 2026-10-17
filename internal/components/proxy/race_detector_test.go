//go:build race

package proxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestIsolatedRaceDetectorCapturesChild(t *testing.T) {
	path := raceReportTestDirectory(t)
	reports, err := prepareIsolatedRaceReports(path)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestIsolatedRaceDetectorChildFixture$")
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "GORACE" && key != "NIDO_TEST_RACE_REPORT_DIR" && key != "NIDO_PROXY_REAL_RACE_FIXTURE" {
			child.Env = append(child.Env, value)
		}
	}
	child.Env = append(child.Env, "NIDO_TEST_RACE_REPORT_DIR="+path,
		"GORACE=halt_on_error=1 log_path="+filepath.Join(path, "race"),
		"NIDO_PROXY_REAL_RACE_FIXTURE=1")
	out, err := child.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 66 {
		t.Fatalf("deliberately racing child did not exit with detector code 66: %v\n%s", err, out)
	}
	if err := reports.check(); err == nil || !strings.Contains(err.Error(), "WARNING: DATA RACE") {
		t.Fatalf("detached child detector report was not rejected: %v", err)
	}
}

func TestIsolatedRaceDetectorChildFixture(t *testing.T) {
	if os.Getenv("NIDO_PROXY_REAL_RACE_FIXTURE") != "1" {
		return
	}
	// This opt-in child is the only intentionally racing fixture. Its
	// report belongs to a separate temporary directory and is removed by
	// the parent test after validating the collector.
	var value int
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	for worker := 0; worker < 2; worker++ {
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 10000; iteration++ {
				value++
			}
		}()
	}
	close(start)
	workers.Wait()
	t.Fatalf("race detector missed the deliberate child fixture: value=%d", value)
}
