package gds

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testUUID = "GPU-11111111-2222-3333-4444-555555555555"

type fakeRunner struct {
	processes   map[int]gdsapi.Process
	commands    []Command
	stopped     []int
	run         func(Command) ([]byte, error)
	runIdentity func(Command) gdsapi.Process
	inspect     func(int) (gdsapi.Process, error)
	start       func(Command, func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error)
	stopErr     error
	ready       bool
}

func (r *fakeRunner) Run(c Command) (data []byte, err error) {
	r.commands = append(r.commands, c)
	p := gdsapi.Process{PID: 124000 + len(r.commands), UID: uint32(os.Geteuid()), Boot: "isolated-boot", Started: fmt.Sprintf("helper-birth-%d", len(r.commands)), Executable: c.Program}
	if r.runIdentity != nil {
		p = r.runIdentity(c)
	}
	if c.Commit != nil {
		r.processes[p.PID] = p
		if err = c.Commit(p); err != nil {
			delete(r.processes, p.PID)
			return nil, err
		}
		defer func() {
			var unknown *UnconfirmedRunError
			if !errors.As(err, &unknown) {
				delete(r.processes, p.PID)
			}
		}()
	}
	if r.run != nil {
		return r.run(c)
	}
	if len(c.Args) > 0 && c.Args[0] == "--version" {
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.1.0", Protocol: 1, Platform: "linux/amd64", Native: true})
	}
	return json.Marshal(gdsapi.Sample{UUID: testUUID, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true})
}
func (r *fakeRunner) Inspect(pid int) (gdsapi.Process, error) {
	if r.inspect != nil {
		return r.inspect(pid)
	}
	p, ok := r.processes[pid]
	if !ok {
		return p, os.ErrNotExist
	}
	return p, nil
}
func (r *fakeRunner) Stop(p gdsapi.Process) error {
	r.stopped = append(r.stopped, p.PID)
	if r.stopErr != nil {
		return r.stopErr
	}
	delete(r.processes, p.PID)
	return nil
}
func (r *fakeRunner) Start(c Command, commit func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error) {
	if r.start != nil {
		return r.start(c, commit)
	}
	boot := "isolated-boot"
	p := gdsapi.Process{PID: 123456, UID: uint32(os.Geteuid()), Boot: boot, Started: "test-birth", Executable: c.Program}
	r.processes[p.PID] = p
	if e := commit(p); e != nil {
		delete(r.processes, p.PID)
		return p, nil, e
	}
	done := make(chan error)
	if r.ready {
		b, e := gdsutil.Read(filepath.Join(c.Dir, "process.json"), 32<<10)
		if e != nil {
			return p, done, e
		}
		var rec gdsapi.Record
		json.Unmarshal(b, &rec)
		state := gdsapi.State{Schema: 1, Boot: rec.Process.Boot, Token: rec.Token, UUID: rec.UUID, ConfigHash: rec.ConfigHash, Runtime: "running", Scheduler: "idle", Timestamp: time.Now().Unix(), ObservedBootNS: 1000000000}
		b, _ = json.Marshal(state)
		if e = gdsutil.Write(filepath.Join(c.Dir, "state.json"), b, 0600); e != nil {
			return p, done, e
		}
	}
	return p, done, nil
}
func elfFixture() []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 62)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	binary.LittleEndian.PutUint16(b[54:], 56)
	binary.LittleEndian.PutUint16(b[58:], 64)
	return b
}
func serviceFixture(t *testing.T) (*Service, *fakeRunner, string) {
	t.Helper()
	r := &fakeRunner{processes: map[int]gdsapi.Process{}, ready: true}
	s := &Service{Root: t.TempDir(), Runner: r, Platform: "linux/amd64", StartTimeout: 80 * time.Millisecond, BootID: func() (string, error) { return "isolated-boot", nil }, BootTimeNS: func() (int64, error) { return 1000000000, nil }, Env: []string{"CUDA_VISIBLE_DEVICES=GPU-visible", "LD_PRELOAD=/bad", "LD_LIBRARY_PATH=/bad", "HOME=/private"}}
	a := filepath.Join(t.TempDir(), "gds")
	if e := os.WriteFile(a, elfFixture(), 0700); e != nil {
		t.Fatal(e)
	}
	return s, r, a
}
func installedFixture(t *testing.T) (*Service, *fakeRunner, string) {
	s, r, a := serviceFixture(t)
	if e := s.Install(a, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.Configure(testUUID, ""); e != nil {
		t.Fatal(e)
	}
	return s, r, a
}
func writeRecord(t *testing.T, s *Service, name string, pid int) gdsapi.Record {
	t.Helper()
	dir, program, _ := s.paths()
	cfg, _ := gdsutil.Read(filepath.Join(dir, "config.toml"), maxConfig)
	core, _ := gdsutil.Read(program, maxProgram)
	boot := "isolated-boot"
	rec := gdsapi.Record{Schema: 1, Token: strings.Repeat("a", 48), Process: gdsapi.Process{PID: pid, UID: uint32(os.Geteuid()), Boot: boot, Started: fmt.Sprintf("birth-%d", pid), Executable: program}, ConfigHash: gdsutil.Hash(cfg), ProgramHash: gdsutil.Hash(core), UUID: testUUID, Phase: "running"}
	b, _ := json.Marshal(rec)
	if e := gdsutil.Write(filepath.Join(dir, name), b, 0600); e != nil {
		t.Fatal(e)
	}
	return rec
}
func TestInstallConfigureAndRemovePreserveData(t *testing.T) {
	s, r, a := installedFixture(t)
	dir, p, _ := s.paths()
	for _, name := range []string{"output.log", "user-file", "state.json"} {
		os.WriteFile(filepath.Join(dir, name), []byte("history"), 0600)
	}
	record := writeRecord(t, s, "process.json", 123472)
	state := gdsapi.State{Schema: 1, Boot: record.Process.Boot, Token: record.Token, UUID: record.UUID, ConfigHash: record.ConfigHash, Runtime: "exited", Scheduler: "observing"}
	stateData, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), stateData, 0600); err != nil {
		t.Fatal(err)
	}
	if e := s.Remove(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	for _, name := range []string{"config.toml", "output.log", "user-file", "state.json"} {
		if _, e := os.Stat(filepath.Join(dir, name)); e != nil {
			t.Fatal(e)
		}
	}
	if len(r.stopped) != 0 {
		t.Fatal("remove stopped process")
	}
	if e := s.Install(a, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.Configure(testUUID, ""); e == nil {
		t.Fatal("default overwrote config")
	}
}
func TestRemoveRefusesUnknownTextAndHardlinks(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0700} {
		t.Run(mode.String(), func(t *testing.T) {
			s, _, _ := serviceFixture(t)
			dir, p, _ := s.paths()
			os.Mkdir(dir, 0700)
			os.WriteFile(p, []byte("private user data"), mode)
			if e := s.Remove(); e == nil {
				t.Fatal("removed unknown text")
			}
			b, _ := os.ReadFile(p)
			if string(b) != "private user data" {
				t.Fatal("data changed")
			}
		})
	}
	s, _, a := installedFixture(t)
	_, p, _ := s.paths()
	link := filepath.Join(t.TempDir(), "hardlink")
	if e := os.Link(p, link); e != nil {
		t.Fatal(e)
	}
	if e := s.Update(a, ""); e == nil {
		t.Fatal("updated shared inode")
	}
	if e := s.Remove(); e == nil {
		t.Fatal("removed shared inode")
	}
}
func TestUpdateRejectsConcurrentProgramAndConfigEdits(t *testing.T) {
	for _, target := range []string{"gds", "config.toml"} {
		t.Run(target, func(t *testing.T) {
			s, r, a := installedFixture(t)
			dir, _, _ := s.paths()
			edit := []byte("concurrent user edit")
			r.run = func(Command) ([]byte, error) {
				if e := os.WriteFile(filepath.Join(dir, target), edit, 0700); e != nil {
					t.Fatal(e)
				}
				return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.2.0", Protocol: 1, Platform: "linux/amd64", Native: true})
			}
			if e := s.Update(a, ""); e == nil {
				t.Fatal("committed over concurrent edit")
			}
			b, _ := os.ReadFile(filepath.Join(dir, target))
			if !bytes.Equal(b, edit) {
				t.Fatal("concurrent edit lost")
			}
		})
	}
}
func TestStartCommitsBeforeAuthorizationAndStopsAllIdentities(t *testing.T) {
	s, r, _ := installedFixture(t)
	if e := s.Start(); e != nil {
		t.Fatal(e)
	}
	st := s.Status()
	if st.Runtime != "running" || st.Scheduler != "idle" || st.Stale {
		t.Fatalf("status %+v", st)
	}
	worker := writeRecord(t, s, "worker.json", 123457)
	r.processes[worker.Process.PID] = worker.Process
	if e := s.Stop(); e != nil {
		t.Fatal(e)
	}
	if len(r.stopped) != 2 || r.stopped[0] != 123456 || r.stopped[1] != 123457 {
		t.Fatalf("stop order %v", r.stopped)
	}
	if e := s.requireStopped(); e != nil {
		t.Fatal(e)
	}
}
func TestStartFailureCleansMonitorAndStopCannotClaimUnconfirmedWorker(t *testing.T) {
	s, r, _ := installedFixture(t)
	r.ready = false
	if e := s.Start(); e == nil {
		t.Fatal("start succeeded without ready")
	}
	if len(r.processes) != 0 {
		t.Fatal("monitor survived failed start")
	}
	worker := writeRecord(t, s, "worker.json", 123458)
	r.processes[worker.Process.PID] = worker.Process
	r.stopErr = errors.New("blocked kernel state")
	if e := s.Stop(); e == nil {
		t.Fatal("unconfirmed worker stop succeeded")
	}
	if e := s.Remove(); e == nil {
		t.Fatal("removed while worker survives")
	}
}
func TestOrphanWorkerAndUnknownIdentityPreventChanges(t *testing.T) {
	s, r, a := installedFixture(t)
	worker := writeRecord(t, s, "worker.json", 123459)
	r.processes[worker.Process.PID] = worker.Process
	if e := s.Update(a, ""); e == nil {
		t.Fatal("orphan worker ignored")
	}
	if e := s.Stop(); e != nil {
		t.Fatal(e)
	}
	r.inspect = func(int) (gdsapi.Process, error) { return gdsapi.Process{}, os.ErrPermission }
	if e := s.Remove(); e == nil {
		t.Fatal("unknown record treated as absent")
	}
	if len(r.stopped) != 1 {
		t.Fatal("signalled unknown process")
	}
}
func TestReusedPIDDoesNotAuthorizeSignal(t *testing.T) {
	s, r, _ := installedFixture(t)
	rec := writeRecord(t, s, "worker.json", 123460)
	reused := rec.Process
	reused.Started = "new-birth"
	r.processes[reused.PID] = reused
	if e := s.Stop(); e != nil {
		t.Fatal(e)
	}
	if len(r.stopped) != 0 {
		t.Fatal("signalled reused PID")
	}
}
func TestStaticStatusAndPreviewDoNotExecuteOrCreateFiles(t *testing.T) {
	s, r, _ := serviceFixture(t)
	s.Root = filepath.Join(t.TempDir(), "absent")
	if st := s.Status(); st.Runtime != "stopped" {
		t.Fatal(st)
	}
	if len(r.commands) != 0 {
		t.Fatal("status executed binary")
	}
	if _, e := os.Stat(s.Root); !os.IsNotExist(e) {
		t.Fatal("status created root")
	}
	if e := s.Preflight("install", filepath.Join(t.TempDir(), "absent"), ""); e == nil {
		t.Fatal("missing artifact passed")
	}
	if _, e := os.Stat(s.Root); !os.IsNotExist(e) {
		t.Fatal("preview created root")
	}
	if e := s.Preflight("start", "", ""); e == nil {
		t.Fatal("uninstalled start passed")
	}
}
func TestConfigurePreviewChecksCandidateAndRuntime(t *testing.T) {
	s, _, _ := installedFixture(t)
	bad := filepath.Join(t.TempDir(), "invalid.toml")
	os.WriteFile(bad, []byte("schema=1\n"), 0600)
	if e := s.Preflight("configure", bad, ""); e == nil {
		t.Fatal("invalid candidate passed")
	}
	if e := s.Preflight("configure", bad+"missing", ""); e == nil {
		t.Fatal("missing candidate passed")
	}
	before, _ := s.ReadConfig()
	if before.GPUUUID != testUUID {
		t.Fatal("preview changed config")
	}
}
func TestDriverFailureDoesNotStopOldInstanceDuringRestart(t *testing.T) {
	s, r, _ := installedFixture(t)
	if e := s.Start(); e != nil {
		t.Fatal(e)
	}
	r.run = func(c Command) ([]byte, error) {
		if c.Args[0] == "probe" {
			return nil, errors.New("driver unavailable")
		}
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.1.0", Protocol: 1, Platform: "linux/amd64", Native: true})
	}
	if e := s.Restart(); e == nil {
		t.Fatal("restart accepted invalid candidate")
	}
	if len(r.stopped) != 0 || len(r.processes) != 1 {
		t.Fatal("old instance stopped before preflight")
	}
}
func TestProbeNeverExpandsDeviceVisibility(t *testing.T) {
	s, r, _ := installedFixture(t)
	if _, e := s.Probe(); e != nil {
		t.Fatal(e)
	}
	last := r.commands[len(r.commands)-1]
	if len(last.Env) != 1 || last.Env[0] != "CUDA_VISIBLE_DEVICES=GPU-visible" {
		t.Fatalf("unsafe env %q", last.Env)
	}
	if len(last.Args) != 3 || last.Args[0] != "probe" || last.Args[2] != testUUID {
		t.Fatal(last.Args)
	}
}
func TestIncompatibleProtocolRejectedBeforeInstall(t *testing.T) {
	s, r, a := serviceFixture(t)
	r.run = func(Command) ([]byte, error) {
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.1.0", Protocol: 2, Platform: "linux/amd64", Native: true})
	}
	if e := s.Install(a, ""); e == nil {
		t.Fatal("incompatible protocol installed")
	}
	_, p, _ := s.paths()
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("program committed")
	}
}

func TestStatusFreshnessUsesBootClockInsteadOfWallClock(t *testing.T) {
	s, _, _ := installedFixture(t)
	if e := s.Start(); e != nil {
		t.Fatal(e)
	}
	dir, _, _ := s.paths()
	b, _ := gdsutil.Read(filepath.Join(dir, "state.json"), 64<<10)
	var state gdsapi.State
	json.Unmarshal(b, &state)
	state.Timestamp = 1
	b, _ = json.Marshal(state)
	gdsutil.Write(filepath.Join(dir, "state.json"), b, 0600)
	if st := s.Status(); st.Runtime != "running" || st.Stale {
		t.Fatalf("wall clock invalidated fresh sample %+v", st)
	}
	s.BootTimeNS = func() (int64, error) { return int64(22 * time.Second), nil }
	if st := s.Status(); !st.Stale || st.Runtime != "running" {
		t.Fatalf("stale boot clock ignored %+v", st)
	}
}
func TestRestartRejectsProcessRecordReplacementBeforeStop(t *testing.T) {
	s, r, _ := installedFixture(t)
	if e := s.Start(); e != nil {
		t.Fatal(e)
	}
	dir, _, _ := s.paths()
	r.run = func(c Command) ([]byte, error) {
		if c.Args[0] == "probe" {
			b, _ := gdsutil.Read(filepath.Join(dir, "process.json"), 32<<10)
			var rec gdsapi.Record
			json.Unmarshal(b, &rec)
			rec.Token = strings.Repeat("b", 48)
			b, _ = json.Marshal(rec)
			gdsutil.Write(filepath.Join(dir, "process.json"), b, 0600)
			return json.Marshal(gdsapi.Sample{UUID: testUUID, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true})
		}
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.1.0", Protocol: 1, Platform: "linux/amd64", Native: true})
	}
	if e := s.Restart(); e == nil {
		t.Fatal("restart adopted replaced process record")
	}
	if len(r.stopped) != 0 {
		t.Fatal("stopped replacement record")
	}
}
func TestInstallRejectsNonNativeStub(t *testing.T) {
	s, r, a := serviceFixture(t)
	r.run = func(Command) ([]byte, error) {
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "0.1.0", Protocol: 1, Platform: "linux/amd64", Native: false})
	}
	if e := s.Install(a, ""); e == nil {
		t.Fatal("installed stub lacking native backend")
	}
}
func TestLogRotationIsBoundedAndRejectsUnknownPaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "output.log")
	data := bytes.Repeat([]byte{'x'}, 8<<20)
	if e := os.WriteFile(p, data, 0600); e != nil {
		t.Fatal(e)
	}
	if e := rotateLog(p); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("active log not rotated")
	}
	info, e := os.Stat(p + ".1")
	if e != nil || info.Size() != int64(len(data)) {
		t.Fatal("rotation lost log")
	}
	os.WriteFile(p, data, 0600)
	os.Remove(p + ".1")
	os.Symlink(filepath.Join(dir, "user-file"), p+".1")
	if e := rotateLog(p); e == nil {
		t.Fatal("rotation followed backup symlink")
	}
}

func TestUnknownProbeExitPersistsAfterMonitorDeath(t *testing.T) {
	s, r, a := installedFixture(t)
	rec := writeRecord(t, s, "process.json", 123461)
	dir, _, _ := s.paths()
	unknown := gdsapi.State{Schema: 1, Boot: rec.Process.Boot, Token: rec.Token, UUID: rec.UUID, ConfigHash: rec.ConfigHash, Runtime: "unknown", Scheduler: "unavailable", Problem: "probe reap not confirmed"}
	b, _ := json.Marshal(unknown)
	if e := gdsutil.Write(filepath.Join(dir, "state.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if st := s.Status(); st.Runtime != "unknown" || st.State == nil {
		t.Fatal(st)
	}
	for _, operation := range []func() error{func() error { return s.Update(a, "") }, s.Remove, s.Start, s.Stop, s.Restart} {
		if e := operation(); e == nil {
			t.Fatal("unconfirmed child permitted mutation")
		}
	}
	calls := len(r.commands)
	if _, e := s.Probe(); e == nil {
		t.Fatal("created probe after unknown reap")
	}
	if len(r.commands) != calls {
		t.Fatal("unknown guard executed child")
	}
	after, _ := gdsutil.Read(filepath.Join(dir, "process.json"), 32<<10)
	var final gdsapi.Record
	json.Unmarshal(after, &final)
	if final.Phase != "running" {
		t.Fatal("unknown history overwritten as stopped")
	}
}

func TestUnconfirmedExplicitProbePersistsExactHelperAndCanBeStopped(t *testing.T) {
	s, r, a := installedFixture(t)
	_, program, _ := s.paths()
	p := gdsapi.Process{PID: 123462, UID: uint32(os.Geteuid()), Boot: "isolated-boot", Started: "probe-birth", Executable: program}
	r.runIdentity = func(Command) gdsapi.Process { return p }
	r.processes[p.PID] = p
	r.run = func(Command) ([]byte, error) {
		return nil, &UnconfirmedRunError{Process: p, Problem: "injected unconfirmed reap"}
	}
	if _, e := s.Probe(); e == nil {
		t.Fatal("unconfirmed probe succeeded")
	}
	if st := s.Status(); st.Invocation != "unknown" || st.Runtime != "unknown" {
		t.Fatal(st)
	}
	for _, operation := range []func() error{s.Start, func() error { return s.Update(a, "") }, s.Remove} {
		if e := operation(); e == nil {
			t.Fatal("unconfirmed helper ignored")
		}
	}
	if e := s.Stop(); e != nil {
		t.Fatal(e)
	}
	if len(r.stopped) != 1 || r.stopped[0] != p.PID {
		t.Fatal(r.stopped)
	}
	r.run = nil
	r.runIdentity = nil
	if e := s.Update(a, ""); e != nil {
		t.Fatal(e)
	}
}
func TestUnconfirmedMetadataRetainsCandidateUntilExactHelperExits(t *testing.T) {
	s, r, a := serviceFixture(t)
	candidate := ""
	r.runIdentity = func(c Command) gdsapi.Process {
		return gdsapi.Process{PID: 123463, UID: uint32(os.Geteuid()), Boot: "isolated-boot", Started: "metadata-birth", Executable: c.Program}
	}
	r.run = func(c Command) ([]byte, error) {
		candidate = c.Program
		p := gdsapi.Process{PID: 123463, UID: uint32(os.Geteuid()), Boot: "isolated-boot", Started: "metadata-birth", Executable: c.Program}
		r.processes[p.PID] = p
		return nil, &UnconfirmedRunError{Process: p, Problem: "candidate reap unconfirmed"}
	}
	if e := s.Install(a, ""); e == nil {
		t.Fatal("unconfirmed metadata installed")
	}
	if candidate == "" {
		t.Fatal("candidate not executed")
	}
	if _, e := os.Stat(candidate); e != nil {
		t.Fatal("removed live candidate image")
	}
	if e := s.Install(a, ""); e == nil {
		t.Fatal("installed over live helper")
	}
	if e := s.Stop(); e != nil {
		t.Fatal(e)
	}
	r.run = nil
	r.runIdentity = nil
	if e := s.Install(a, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(candidate); e != nil {
		t.Fatal("deleted prior candidate history")
	}
}
func TestInvocationRecordCannotAuthorizeUnrelatedExecutable(t *testing.T) {
	s, r, _ := installedFixture(t)
	rec := writeRecord(t, s, "invocation.json", 123464)
	rec.Process.Executable = "/bin/sh"
	r.processes[rec.Process.PID] = rec.Process
	dir, _, _ := s.paths()
	b, _ := json.Marshal(rec)
	gdsutil.Write(filepath.Join(dir, "invocation.json"), b, 0600)
	if e := s.Stop(); e == nil {
		t.Fatal("unrelated executable authorized")
	}
	if len(r.stopped) != 0 {
		t.Fatal("signalled unrelated executable")
	}
}

func TestExplicitProbeSerializesProgramAndConfigurationMutations(t *testing.T) {
	s, r, artifact := installedFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	r.run = func(Command) ([]byte, error) {
		close(entered)
		<-release
		return json.Marshal(gdsapi.Sample{UUID: testUUID, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true})
	}
	done := make(chan error, 1)
	go func() { _, err := s.Probe(); done <- err }()
	<-entered
	for _, mutation := range []func() error{
		func() error { return s.Update(artifact, "") }, s.Remove, s.Stop,
		func() error { return s.Configure(testUUID, "") },
		func() error { _, err := s.Probe(); return err },
	} {
		if err := mutation(); err == nil || !strings.Contains(err.Error(), "锁已被持有") {
			t.Errorf("operation overlapped live probe: %v", err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r.run = nil
	if err := s.Update(artifact, ""); err != nil {
		t.Fatal("completed probe retained operation lock:", err)
	}
}

func TestProbeRejectsConfigurationChangedDuringNativeQuery(t *testing.T) {
	s, r, _ := installedFixture(t)
	path, _ := s.ConfigPath()
	cfg := gdsapi.DefaultConfig(testUUID)
	cfg.IdleSeconds++
	edited, _ := cfg.Encode()
	r.run = func(Command) ([]byte, error) {
		if err := os.WriteFile(path, edited, 0600); err != nil {
			return nil, err
		}
		return json.Marshal(gdsapi.Sample{UUID: testUUID, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true})
	}
	if _, err := s.Probe(); err == nil {
		t.Fatal("accepted query from an obsolete configuration")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, edited) {
		t.Fatal("concurrent configuration edit was lost:", err)
	}
}

func TestCorruptStateCannotHideUnconfirmedExitAfterMonitorDeath(t *testing.T) {
	s, r, artifact := installedFixture(t)
	writeRecord(t, s, "process.json", 123465)
	dir, _, _ := s.paths()
	statePath := filepath.Join(dir, "state.json")
	corrupt := []byte(`{"schema":1,"runtime":"unknown"`)
	if err := os.WriteFile(statePath, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	calls := len(r.commands)
	for _, operation := range []func() error{
		s.Start, s.Remove, func() error { return s.Update(artifact, "") },
		func() error { _, err := s.Probe(); return err },
	} {
		if err := operation(); err == nil {
			t.Error("corrupt state authorized a new native child or mutation")
		}
	}
	if got := s.Status(); got.Runtime != "unknown" || !got.Stale {
		t.Fatal(got)
	}
	if len(r.commands) != calls {
		t.Fatal("native query executed with corrupt exit evidence")
	}
	if got, _ := os.ReadFile(statePath); !bytes.Equal(got, corrupt) {
		t.Fatal("corrupt state evidence was overwritten")
	}
}

func TestUnknownELFCannotBeExecutedOverwrittenOrRemoved(t *testing.T) {
	for _, scenario := range []string{"missing-receipt", "changed-program", "corrupt-receipt", "interrupted-commit"} {
		t.Run(scenario, func(t *testing.T) {
			s, r, artifact := installedFixture(t)
			dir, program, _ := s.paths()
			receiptPath := filepath.Join(dir, "installed.json")
			switch scenario {
			case "missing-receipt":
				if err := os.Remove(receiptPath); err != nil {
					t.Fatal(err)
				}
			case "changed-program":
				if err := os.WriteFile(program, append(elfFixture(), []byte("user edit")...), 0700); err != nil {
					t.Fatal(err)
				}
			case "corrupt-receipt":
				if err := os.WriteFile(receiptPath, []byte("user data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "interrupted-commit":
				before, receipt, err := s.receiptSnapshot(false)
				if err != nil {
					t.Fatal(err)
				}
				receipt.SHA256 = strings.Repeat("f", 64)
				data, _ := json.Marshal(receipt)
				if err = gdsutil.WriteExpected(receiptPath, data, 0600, before); err != nil {
					t.Fatal(err)
				}
			}
			programBefore, _ := os.ReadFile(program)
			receiptBefore, _ := os.ReadFile(receiptPath)
			calls := len(r.commands)
			for _, operation := range []func() error{
				s.Check, s.Start, s.Remove, func() error { return s.Update(artifact, "") },
				func() error { _, err := s.Probe(); return err },
				func() error { return s.Preflight("update", artifact, "") },
				func() error { return s.Preflight("remove", "", "") },
			} {
				if err := operation(); err == nil {
					t.Error("unknown ELF was treated as an owned GDS installation")
				}
			}
			if got := s.Status(); got.Program != "invalid" {
				t.Fatal(got)
			}
			if got, _ := os.ReadFile(program); !bytes.Equal(got, programBefore) {
				t.Fatal("unknown ELF was modified")
			}
			if got, _ := os.ReadFile(receiptPath); !bytes.Equal(got, receiptBefore) {
				t.Fatal("unknown receipt was modified")
			}
			if len(r.commands) != calls {
				t.Fatal("unknown ELF was executed")
			}
		})
	}
}

func TestUpdatePreservesConcurrentInstallationReceiptEdit(t *testing.T) {
	s, r, artifact := installedFixture(t)
	dir, program, _ := s.paths()
	before, _ := os.ReadFile(program)
	path := filepath.Join(dir, "installed.json")
	edited := []byte("concurrent installation receipt edit")
	r.run = func(Command) ([]byte, error) {
		if err := os.WriteFile(path, edited, 0600); err != nil {
			return nil, err
		}
		return json.Marshal(gdsapi.Metadata{Product: "gds", Version: "new", Protocol: 1, Platform: "linux/amd64", Native: true})
	}
	if err := s.Update(artifact, ""); err == nil {
		t.Fatal("updated over a concurrent installation receipt edit")
	}
	if got, _ := os.ReadFile(program); !bytes.Equal(got, before) {
		t.Fatal("program committed after receipt ownership changed")
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, edited) {
		t.Fatal("concurrent receipt edit was lost")
	}
}

func TestHelperIdentityIsPersistedBeforeNativeExecution(t *testing.T) {
	s, r, _ := installedFixture(t)
	r.run = func(c Command) ([]byte, error) {
		record, _, err := s.readRecord("invocation.json")
		if err != nil {
			return nil, err
		}
		if actual, err := s.actual(record); err != nil || actual.Executable != c.Program {
			return nil, fmt.Errorf("native query ran before its exact identity was persisted: %v", err)
		}
		return json.Marshal(gdsapi.Sample{UUID: testUUID, TotalBytes: 16 << 30, FreeBytes: 16 << 30, Complete: true, Supported: true})
	}
	if _, err := s.Probe(); err != nil {
		t.Fatal(err)
	}
	if err := s.invocationStopped(); err != nil {
		t.Fatal("completed helper still blocks operations:", err)
	}
}

func TestELFLoaderCannotEscapeSystemLibraryDirectories(t *testing.T) {
	for _, loader := range []string{"/lib/../tmp/custom-loader", "/lib64/../../tmp/custom-loader", "/lib/loader\x00ignored", "/lib64//custom-loader"} {
		data := append(elfFixture(), make([]byte, 56)...)
		binary.LittleEndian.PutUint64(data[32:], 64) // e_phoff
		binary.LittleEndian.PutUint16(data[56:], 1)  // e_phnum
		binary.LittleEndian.PutUint32(data[64:], 3)  // PT_INTERP
		binary.LittleEndian.PutUint64(data[72:], uint64(len(data)))
		binary.LittleEndian.PutUint64(data[96:], uint64(len(loader)+1))
		data = append(data, append([]byte(loader), 0)...)
		if err := nativeProgram(data, "linux/amd64"); err == nil {
			t.Errorf("accepted loader %q", loader)
		}
	}
}

func TestEditedUnknownStateCannotHideCurrentBootExitEvidence(t *testing.T) {
	for _, field := range []string{"boot", "boot-changed", "token", "uuid", "config-hash", "runtime"} {
		t.Run(field, func(t *testing.T) {
			s, r, artifact := installedFixture(t)
			record := writeRecord(t, s, "process.json", 123467)
			state := gdsapi.State{Schema: 1, Boot: record.Process.Boot, Token: record.Token, UUID: record.UUID, ConfigHash: record.ConfigHash, Runtime: "unknown", Scheduler: "unavailable", Problem: "unconfirmed native child"}
			switch field {
			case "boot":
				state.Boot = ""
			case "boot-changed":
				state.Boot = "prior-boot"
			case "token":
				state.Token = strings.Repeat("b", 48)
			case "uuid":
				state.UUID = "GPU-00000000-0000-0000-0000-000000000001"
			case "config-hash":
				state.ConfigHash = strings.Repeat("e", 64)
			case "runtime":
				state.Runtime = "unrecognized"
			}
			dir, _, _ := s.paths()
			path := filepath.Join(dir, "state.json")
			data, _ := json.Marshal(state)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			calls := len(r.commands)
			for _, operation := range []func() error{s.Start, s.Remove, s.Stop, func() error { return s.Update(artifact, "") }, func() error { _, err := s.Probe(); return err }} {
				if err := operation(); err == nil {
					t.Error("edited current-boot state authorized mutation or another native child")
				}
			}
			if len(r.commands) != calls {
				t.Fatal("native child executed after state identity was edited")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
				t.Fatal("unconfirmed state evidence was overwritten")
			}
		})
	}
}

func TestOldBootUnknownStateAllowsNewManagedStart(t *testing.T) {
	for _, recordBoot := range []string{"prior-boot"} {
		t.Run(recordBoot, func(t *testing.T) {
			s, _, _ := installedFixture(t)
			record := writeRecord(t, s, "process.json", 123468)
			record.Process.Boot = recordBoot
			dir, _, _ := s.paths()
			data, _ := json.Marshal(record)
			if err := os.WriteFile(filepath.Join(dir, "process.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			state := gdsapi.State{Schema: 1, Boot: "prior-boot", Token: strings.Repeat("c", 48), UUID: record.UUID, ConfigHash: record.ConfigHash, Runtime: "unknown", Scheduler: "unavailable", Problem: "previous-boot unconfirmed query"}
			data, _ = json.Marshal(state)
			if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.Start(); err != nil {
				t.Fatal("prior-boot state blocked a new managed start:", err)
			}
			if got := s.Status(); got.Runtime != "running" || got.State == nil || got.State.Boot != "isolated-boot" {
				t.Fatal(got)
			}
		})
	}
}

func TestOldBootPIDDoesNotInspectItsReusedCurrentOwner(t *testing.T) {
	s, r, _ := installedFixture(t)
	record := writeRecord(t, s, "process.json", 123470)
	record.Process.Boot = "prior-boot"
	r.inspect = func(int) (gdsapi.Process, error) {
		t.Fatal("inspected a PID whose recorded kernel boot has already ended")
		return gdsapi.Process{}, os.ErrPermission
	}
	if _, err := s.actual(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old boot did not prove the recorded child absent:", err)
	}
	r.inspect = func(int) (gdsapi.Process, error) { return gdsapi.Process{}, os.ErrPermission }
	record.Process.Boot = "isolated-boot"
	if _, err := s.actual(record); !errors.Is(err, os.ErrPermission) {
		t.Fatal("current boot ownership error was ignored:", err)
	}
}

func TestReadyStateRequiresMatchingExplicitBoot(t *testing.T) {
	for _, boot := range []string{"", "different-boot"} {
		t.Run(boot, func(t *testing.T) {
			s, _, _ := installedFixture(t)
			if err := s.Start(); err != nil {
				t.Fatal(err)
			}
			record, _, err := s.readRecord("process.json")
			if err != nil {
				t.Fatal(err)
			}
			state, err := s.readyState(record)
			if err != nil {
				t.Fatal(err)
			}
			state.Boot = boot
			data, _ := json.Marshal(state)
			dir, _, _ := s.paths()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.readyState(record); err == nil {
				t.Fatal("accepted readiness without the exact recorded boot")
			}
			if got := s.Status(); got.Runtime == "running" || !got.Stale {
				t.Fatal("mismatched state boot appeared running:", got)
			}
		})
	}
}

func TestCurrentBootStateCannotBeHiddenByOldBootRecord(t *testing.T) {
	s, r, artifact := installedFixture(t)
	record := writeRecord(t, s, "process.json", 123469)
	record.Process.Boot = "prior-boot"
	dir, _, _ := s.paths()
	data, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(dir, "process.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	state := gdsapi.State{Schema: 1, Boot: "isolated-boot", Token: record.Token, UUID: record.UUID, ConfigHash: record.ConfigHash, Runtime: "unknown", Scheduler: "unavailable", Problem: "current-boot unconfirmed query"}
	data, _ = json.Marshal(state)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	calls := len(r.commands)
	for _, operation := range []func() error{s.Start, s.Remove, func() error { return s.Update(artifact, "") }} {
		if err := operation(); err == nil {
			t.Error("old record hid current-boot unknown evidence")
		}
	}
	if len(r.commands) != calls {
		t.Fatal("old record authorized native child despite current-boot evidence")
	}
}

func TestStartHandsOffStateHashAndRefusesLateStateEdit(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			s, r, _ := installedFixture(t)
			dir, program, _ := s.paths()
			path := filepath.Join(dir, "state.json")
			expectedHash := "absent"
			if present {
				record := writeRecord(t, s, "process.json", 123470)
				state := gdsapi.State{Schema: 1, Boot: record.Process.Boot, Token: record.Token, UUID: record.UUID, ConfigHash: record.ConfigHash, Runtime: "exited", Scheduler: "observing"}
				data, _ := json.Marshal(state)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				expectedHash = gdsutil.Hash(data)
			}
			edited := []byte("concurrent state edit after parent validation")
			checked := false
			r.start = func(c Command, commit func(gdsapi.Process) error) (gdsapi.Process, <-chan error, error) {
				checked = true
				found := ""
				for i, arg := range c.Args {
					if arg == "--state-sha256" && i+1 < len(c.Args) {
						found = c.Args[i+1]
					}
				}
				if found != expectedHash {
					t.Errorf("state handoff hash = %q, want %q", found, expectedHash)
				}
				if err := os.WriteFile(path, edited, 0600); err != nil {
					return gdsapi.Process{}, nil, err
				}
				p := gdsapi.Process{PID: 123471, UID: uint32(os.Geteuid()), Boot: "isolated-boot", Started: "gated-monitor-birth", Executable: program}
				r.processes[p.PID] = p
				err := commit(p)
				delete(r.processes, p.PID)
				if err == nil {
					t.Error("monitor was authorized after the checked state changed")
					err = fmt.Errorf("unexpected authorization")
				}
				return p, nil, err
			}
			if err := s.Start(); err == nil || !checked {
				t.Fatalf("late state edit accepted or handoff not reached: %v", err)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, edited) {
				t.Fatal("late state edit was lost")
			}
		})
	}
}

func TestStateWithoutProcessRecordCannotAuthorizeNewQueriesOrMutations(t *testing.T) {
	s, r, artifact := installedFixture(t)
	dir, _, _ := s.paths()
	path := filepath.Join(dir, "state.json")
	state := gdsapi.State{Schema: 1, Boot: "isolated-boot", Token: strings.Repeat("a", 48), UUID: testUUID, ConfigHash: strings.Repeat("b", 64), Runtime: "unknown", Scheduler: "unavailable", Problem: "unconfirmed query, process record lost"}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	calls := len(r.commands)
	for _, operation := range []func() error{
		s.Start, s.Remove, func() error { return s.Update(artifact, "") },
		func() error { _, err := s.Probe(); return err },
	} {
		if err := operation(); err == nil {
			t.Error("missing process record hid existing exit evidence")
		}
	}
	if len(r.commands) != calls {
		t.Fatal("native child executed without a process ownership record")
	}
	if got := s.Status(); got.Runtime != "unknown" {
		t.Fatal(got)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("orphan state evidence was overwritten")
	}
}

func TestStopStillReleasesVerifiedWorkerWhenStateIsUnknownOrCorrupt(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			s, r, _ := installedFixture(t)
			monitor := writeRecord(t, s, "process.json", 123473)
			worker := writeRecord(t, s, "worker.json", 123474)
			r.processes[monitor.Process.PID] = monitor.Process
			r.processes[worker.Process.PID] = worker.Process
			state := gdsapi.State{Schema: 1, Boot: monitor.Process.Boot, Token: monitor.Token, UUID: monitor.UUID, ConfigHash: monitor.ConfigHash, Runtime: "unknown", Scheduler: "releasing", Problem: "another native query exit unconfirmed"}
			data, _ := json.Marshal(state)
			if corrupt {
				data = []byte(`{"schema":1,"runtime":"unknown"`)
			}
			dir, _, _ := s.paths()
			path := filepath.Join(dir, "state.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			// The CLI performs this preflight before invoking Stop.
			if err := s.Preflight("stop", "", ""); err != nil {
				t.Fatal("exit evidence prevented precise stop attempt:", err)
			}
			if err := s.Stop(); err == nil {
				t.Fatal("stop claimed all native children were confirmed exited")
			}
			if len(r.stopped) != 2 || r.stopped[0] != monitor.Process.PID || r.stopped[1] != worker.Process.PID {
				t.Fatal("verified monitor and worker were not stopped in order:", r.stopped)
			}
			if _, err := s.actual(worker); !os.IsNotExist(err) {
				t.Fatal("verified worker remained live:", err)
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
				t.Fatal("unknown exit evidence was overwritten")
			}
		})
	}
}
