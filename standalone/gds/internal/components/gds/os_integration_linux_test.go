//go:build linux && amd64

package gds

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsruntime"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
)

// The executable is a CPU fixture for the real OS runner and monitor protocol.
// Native NVIDIA code is never imported. These entry points exist only in tests.
func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == "__helper" && os.Args[2] == "--" {
		gate := os.NewFile(3, "cpu-helper-authorization")
		var b [1]byte
		if gate == nil {
			os.Exit(1)
		}
		n, err := gate.Read(b[:])
		gate.Close()
		if err != nil || n != 1 || b[0] != 1 {
			os.Exit(1)
		}
		os.Args = append([]string{os.Args[0]}, os.Args[3:]...)
	}
	if len(os.Args) > 1 {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		defer cancel()
		var err error
		switch os.Args[1] {
		case "__invoke-probe":
			if len(os.Args) != 3 {
				os.Exit(1)
			}
			_, err = (&Service{Root: os.Args[2]}).Probe()
		case "--version":
			err = json.NewEncoder(os.Stdout).Encode(gdsapi.Metadata{Product: "gds", Version: "cpu-fixture", Protocol: 1, Platform: "linux/amd64", Native: true})
		case "probe", "__probe":
			f := flag.NewFlagSet("probe", flag.ContinueOnError)
			uuid := f.String("gpu", "", "")
			alias := f.String("uuid", "", "")
			err = f.Parse(os.Args[2:])
			if *uuid == "" {
				*uuid = *alias
			}
			if err == nil {
				err = gdsruntime.RunProbe(os.Stdout, cpuBackend{}, *uuid)
			}
		case "__worker":
			f := flag.NewFlagSet("worker", flag.ContinueOnError)
			uuid := f.String("gpu", "", "")
			token := f.String("token", "", "")
			boot := f.String("boot-id", "", "")
			err = f.Parse(os.Args[2:])
			if err == nil {
				err = gdsruntime.RunWorker(ctx, gdsruntime.WorkerOptions{GPUUUID: *uuid, Token: *token, BootID: *boot}, cpuBackend{})
			}
		case "run":
			f := flag.NewFlagSet("run", flag.ContinueOnError)
			dir := f.String("dir", "", "")
			managed := f.Bool("managed", false, "")
			token := f.String("token", "", "")
			boot := f.String("boot-id", "", "")
			stateHash := f.String("state-sha256", "", "")
			err = f.Parse(os.Args[2:])
			if err == nil {
				var data []byte
				data, err = os.ReadFile(filepath.Join(*dir, "config.toml"))
				if err == nil {
					var config gdsapi.Config
					config, err = gdsapi.ParseConfig(data)
					if err == nil {
						err = gdsruntime.Run(ctx, gdsruntime.RunOptions{Dir: *dir, Config: config, Managed: *managed, Token: *token, BootID: *boot, ExpectedStateHash: *stateHash})
					}
				}
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

type cpuBackend struct{}
type cpuAllocation struct{ path string }

func (cpuBackend) Probe(uuid string) (gdsapi.Sample, error) {
	if _, err := os.Stat("cpu-probe-hang"); err == nil {
		_ = os.WriteFile("cpu-probe-entered", nil, 0600)
		for {
			time.Sleep(time.Second)
		}
	}
	s := gdsapi.Sample{UUID: uuid, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true}
	if b, err := os.ReadFile("cpu-context"); err == nil {
		pid, err := strconv.Atoi(string(b))
		if err != nil {
			return s, err
		}
		s.ComputePIDs = []int{pid}
		s.FreeBytes = 12 << 30
	}
	if _, err := os.Stat("cpu-busy"); err == nil {
		s.ComputePIDs = append(s.ComputePIDs, 9999999)
	}
	return s, nil
}
func (cpuBackend) Allocate(uuid string, bytes, reserve uint64) (gdsruntime.Allocation, error) {
	if err := os.WriteFile("cpu-context", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return nil, err
	}
	return cpuAllocation{"cpu-context"}, nil
}
func (a cpuAllocation) Free() error { return os.Remove(a.path) }

func TestOSHelperCommitFailureNeverAuthorizesExecution(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	data, err := (OSRunner{}).Run(Command{Program: exe, Args: []string{"--version", "--json"}, Commit: func(p gdsapi.Process) error {
		called = true
		if actual, err := gdsutil.Inspect(p.PID); err != nil || !gdsutil.SameBirth(actual, p) {
			t.Errorf("helper not alive at identity commit: %v", err)
		}
		return fmt.Errorf("injected identity persistence failure")
	}})
	if !called || err == nil || len(data) != 0 {
		t.Fatalf("unauthorized metadata executed: called=%v output=%s err=%v", called, data, err)
	}
}

func TestOSParentDeathRetainsExactHelperIdentity(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Root: t.TempDir()}
	if err := s.Install(exe, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Configure(testUUID, ""); err != nil {
		t.Fatal(err)
	}
	dir, _, _ := s.paths()
	if err := os.WriteFile(filepath.Join(dir, "cpu-probe-hang"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(exe, "__invoke-probe", s.Root)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = parent.Process.Kill()
			_ = parent.Wait()
		}
		if err := s.Stop(); err != nil {
			t.Error("helper cleanup:", err)
		}
	})
	entered := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(filepath.Join(dir, "cpu-probe-entered")); err == nil {
			entered = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !entered {
		t.Fatal("CPU helper did not enter query")
	}
	record, _, err := s.readRecord("invocation.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	waited = true
	if _, err := s.actual(record); err != nil {
		t.Fatal("parent death lost the live helper identity:", err)
	}
	if err := s.Remove(); err == nil {
		t.Fatal("parent death allowed removal over a live query")
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.actual(record); !os.IsNotExist(err) {
		t.Fatal("helper exit not confirmed:", err)
	}
}

func TestOSManagedMonitorWorkerLifecycle(t *testing.T) {
	// Every file is confined to temporary roots. The device UUID is a fixture,
	// and no NVIDIA backend or real driver query exists in this executable.
	uuid := "GPU-00000000-0000-0000-0000-000000000031"
	lockPath := filepath.Join("/tmp", fmt.Sprintf("nido-gds-%d", os.Geteuid()), "gpu-"+uuid+".lock")
	_, lockBefore := os.Lstat(lockPath)
	t.Cleanup(func() {
		if os.IsNotExist(lockBefore) {
			_ = os.Remove(lockPath)
			_ = os.Remove(filepath.Dir(lockPath))
		}
	})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Root: t.TempDir(), StartTimeout: 15 * time.Second}
	if err = s.Install(exe, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Configure(uuid, ""); err != nil {
		t.Fatal(err)
	}
	path, _ := s.ConfigPath()
	cfg := gdsapi.DefaultConfig(uuid)
	cfg.IdleSeconds = 1
	cfg.ProbeIntervalSeconds = 1
	data, _ := cfg.Encode()
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Stop(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if err = s.Start(); err != nil {
		t.Fatal(err)
	}
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			st := s.Status()
			if st.Scheduler == want && st.Runtime == "running" {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("no %s: %+v", want, s.Status())
	}
	wait("holding")
	st := s.Status()
	if st.Worker != "running" || st.WorkerPID <= 1 {
		t.Fatalf("worker not owned: %+v", st)
	}
	dir := filepath.Dir(path)
	if err = os.WriteFile(filepath.Join(dir, "cpu-busy"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	wait("observing")
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().Worker == "stopped" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.Status().Worker != "stopped" {
		t.Fatal("external activity did not release worker")
	}
	if err = s.Stop(); err != nil {
		t.Fatal(err)
	}
	if st = s.Status(); st.Runtime != "stopped" || st.Worker != "stopped" {
		t.Fatalf("stop incomplete %+v", st)
	}
	if err = s.Update(exe, ""); err != nil {
		t.Fatal(err)
	}
	if st = s.Status(); st.Runtime != "stopped" {
		t.Fatal("update started monitor")
	}
	if err = s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("configuration removed", err)
	}
	if _, err = s.Logs(10); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
