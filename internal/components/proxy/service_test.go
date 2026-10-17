package proxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type coreFakeRunner struct {
	commands      []Command
	run           func(Command) ([]byte, error)
	inspect       func(int) (Process, error)
	process       Process
	inspectErr    error
	starts, stops int
}

func (r *coreFakeRunner) Run(c Command) ([]byte, error) {
	r.commands = append(r.commands, c)
	if r.run != nil {
		return r.run(c)
	}
	if len(c.Args) > 0 && c.Args[0] == "-v" {
		return []byte("Mihomo Meta v1.2.3 isolated test"), nil
	}
	return []byte("configuration accepted"), nil
}
func (r *coreFakeRunner) Start(Command, func(Process) error) (Process, <-chan error, error) {
	r.starts++
	return Process{}, nil, errors.New("unexpected start")
}
func (r *coreFakeRunner) Inspect(pid int) (Process, error) {
	if r.inspect != nil {
		return r.inspect(pid)
	}
	if r.inspectErr != nil {
		return Process{}, r.inspectErr
	}
	if r.process.PID == 0 {
		return Process{}, os.ErrNotExist
	}
	return r.process, nil
}
func (r *coreFakeRunner) Stop(Process) error                         { r.stops++; return errors.New("unexpected stop") }
func (r *coreFakeRunner) OwnsEndpoint(Process, string) (bool, error) { return false, nil }

// Deliberately non-runnable headers are sufficient for format tests, and every
// execution in this file is intercepted by coreFakeRunner.
func coreNativeFixture(goos, arch string) []byte {
	if goos == "linux" {
		b := make([]byte, 64)
		copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
		binary.LittleEndian.PutUint16(b[16:], 2)
		machine := uint16(62)
		if arch == "arm64" {
			machine = 183
		}
		binary.LittleEndian.PutUint16(b[18:], machine)
		binary.LittleEndian.PutUint32(b[20:], 1)
		binary.LittleEndian.PutUint16(b[52:], 64)
		binary.LittleEndian.PutUint16(b[54:], 56)
		binary.LittleEndian.PutUint16(b[58:], 64)
		return b
	}
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	cpu := uint32(0x01000007)
	if arch == "arm64" {
		cpu = 0x0100000c
	}
	binary.LittleEndian.PutUint32(b[4:], cpu)
	binary.LittleEndian.PutUint32(b[12:], 2)
	return b
}

func coreArtifact(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, coreNativeFixture(runtime.GOOS, runtime.GOARCH), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func coreService(t *testing.T) (*Service, *coreFakeRunner, string) {
	t.Helper()
	r := &coreFakeRunner{}
	s := &Service{Root: t.TempDir(), Runner: r, Env: []string{"HOME=/isolated", "CLASH_CONFIG_STRING=secret", "CLASH_EXTERNAL_CONTROLLER=0.0.0.0:9000", "SAFE_PATHS=/", "SKIP_SAFE_PATH_CHECK=true", "LD_PRELOAD=/external/code.so", "LD_LIBRARY_PATH=/external", "DYLD_INSERT_LIBRARIES=/external/code.dylib", "DYLD_LIBRARY_PATH=/external", "SSL_CERT_FILE=/isolated/certs"}}
	artifact := coreArtifact(t)
	return s, r, artifact
}

func coreWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

const coreConfig = "mixed-port: 7890\nallow-lan: false\nmode: rule\nrules: [MATCH,DIRECT]\n"

func TestCoreInstallConfigureRemainStoppedAndUseCapturedBytes(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	dir, program, _ := s.paths()
	if _, err := os.Stat(filepath.Join(dir, "config.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("install created configuration", err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	if r.starts != 0 || r.stops != 0 || len(r.commands) != 2 {
		t.Fatalf("unexpected side effect: %+v", r)
	}
	check := r.commands[1]
	if !reflect.DeepEqual(check.Args, []string{"-t", "-d", dir, "-f", "-"}) || string(check.Input) != coreConfig || check.Timeout <= 0 {
		t.Fatalf("candidate not passed to core as captured stdin: %+v", check)
	}
	for _, c := range r.commands {
		if c.Dir != dir {
			t.Fatalf("relative or incorrect core directory: %+v", c)
		}
		for _, env := range c.Env {
			if strings.HasPrefix(env, "CLASH_") || strings.HasPrefix(env, "SAFE_PATHS=") || strings.HasPrefix(env, "SKIP_SAFE_PATH_CHECK=") {
				t.Fatal("unsafe core override", env)
			}
		}
		if !reflect.DeepEqual(c.Env, []string{"HOME=/isolated", "SSL_CERT_FILE=/isolated/certs"}) {
			t.Fatal("needed system variables lost", c.Env)
		}
	}
	for path, mode := range map[string]os.FileMode{dir: 0700, program: 0700, filepath.Join(dir, "config.yaml"): 0600, filepath.Join(dir, ".lock"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
	status := s.Status()
	if status.Program != "present" || status.Config != "readable" || status.Runtime.State != "stopped" {
		t.Fatalf("status: %+v", status)
	}
}

func TestCoreValidationFailsClosedWithoutLeakingCandidateSecrets(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.ConfigureBytes([]byte(coreConfig)); err == nil {
		t.Fatal("configuration accepted without core")
	}
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	path, _ := s.ConfigPath()
	before, _ := os.ReadFile(path)
	for _, invalid := range []string{"", "mixed-port: -1\n", "mixed-port: 7890\nmixed-port: 7891\n"} {
		n := len(r.commands)
		if err := s.ConfigureBytes([]byte(invalid)); err == nil {
			t.Fatal("invalid static configuration accepted")
		}
		if len(r.commands) != n {
			t.Fatal("core ran despite invalid static configuration")
		}
	}
	r.run = func(Command) ([]byte, error) {
		return []byte("https://provider.invalid/private?token=SECRET controller-secret"), errors.New("SECRET")
	}
	err := s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "mode: rule", "mode: certainly-invalid")))
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "provider.invalid") {
		t.Fatalf("unsafe or absent validation error: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed validation overwrote existing configuration")
	}
}

func TestCoreConfigureAndUpdateRejectConcurrentSnapshots(t *testing.T) {
	for _, change := range []string{"config", "program", "directory", "metadata", "source"} {
		t.Run(change, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			dir, program, _ := s.paths()
			config, _ := s.ConfigPath()
			oldProgram, _ := os.ReadFile(program)
			source := filepath.Join(t.TempDir(), "candidate.yaml")
			coreWrite(t, source, []byte(coreConfig))
			mutated := false
			r.run = func(c Command) ([]byte, error) {
				if c.Args[0] == "-v" {
					return []byte("Mihomo Meta v1.2.4"), nil
				}
				if !mutated {
					mutated = true
					switch change {
					case "config":
						coreWrite(t, config, []byte(strings.ReplaceAll(coreConfig, "7890", "7891")))
					case "program":
						coreWrite(t, program, append(oldProgram, 1))
					case "directory":
						if err := os.Rename(dir, dir+"-moved"); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(dir, 0700); err != nil {
							t.Fatal(err)
						}
					case "metadata":
						if err := os.Chmod(config, 0640); err != nil {
							t.Fatal(err)
						}
					case "source":
						coreWrite(t, source, []byte(strings.ReplaceAll(coreConfig, "7890", "7892")))
					}
				}
				return nil, nil
			}
			var err error
			if change == "source" {
				err = s.Configure(source)
			} else {
				err = s.Update(artifact, "")
			}
			if err == nil {
				t.Fatal("concurrent change was overwritten")
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("replacement changed runtime")
			}
		})
	}
}

func TestCoreUpdatePreservesUpstreamDirectoriesAndUserData(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	dir, _, _ := s.paths()
	outside := t.TempDir()
	coreWrite(t, filepath.Join(outside, "mihomo"), []byte("external program"))
	if err := os.Symlink(outside, filepath.Join(dir, "meta-backup")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"meta-update/unknown", "providers/cache", "unknown/user-data"} {
		coreWrite(t, filepath.Join(dir, relative), []byte("keep"))
	}
	if err := s.Update(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "mihomo")); string(data) != "external program" {
		t.Fatal("external backup target changed")
	}
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"meta-update/unknown", "providers/cache", "unknown/user-data", "config.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, relative)); err != nil {
			t.Fatal("user file removed", relative, err)
		}
	}
	if r.starts != 0 || r.stops != 0 {
		t.Fatal("update/remove implicitly started or stopped service")
	}
}

func TestCoreMutationRefusesActiveOrUnknownProcesses(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "unknown"}[unknown], func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			_, program, _ := s.paths()
			launcher := filepath.Join(s.Root, "launcher")
			p := Process{PID: 4242, UID: uint32(os.Geteuid()), Boot: "boot", Started: "birth", Executable: launcher}
			if err := s.saveRecord(processRecord{Process: p, Program: program, Launcher: launcher, ProgramHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64), Endpoints: []string{"http://127.0.0.1:7890"}, Phase: "running"}); err != nil {
				t.Fatal(err)
			}
			r.process = p
			if unknown {
				r.inspectErr = errors.New("kernel unavailable")
			}
			n := len(r.commands)
			for _, action := range []func() error{func() error { return s.Update(artifact, "") }, func() error { return s.ConfigureBytes([]byte(coreConfig)) }, s.Remove} {
				if err := action(); err == nil {
					t.Fatal("mutating active/unknown process state allowed")
				}
			}
			if len(r.commands) != n || r.stops != 0 || r.starts != 0 {
				t.Fatal("mutation attempted runtime side effects")
			}
		})
	}
}

func TestCoreReadOnlyQueriesAndDryRunCreateNothing(t *testing.T) {
	s, r, artifact := coreService(t)
	s.Root = filepath.Join(s.Root, "absent")
	if err := s.Preflight("install", artifact, ""); err != nil {
		t.Fatal(err)
	}
	if status := s.Status(); status.Program != "missing" || status.Config != "missing" {
		t.Fatalf("unexpected absent status %+v", status)
	}
	if _, err := s.ConfigPath(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only calls created root", err)
	}
	if len(r.commands) != 0 {
		t.Fatal("dry run executed core")
	}
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	n := len(r.commands)
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if err := s.Preflight("start", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Endpoint(""); err != nil {
		t.Fatal(err)
	}
	s.Status()
	if len(r.commands) != n {
		t.Fatal("read-only query executed core")
	}
}

func TestCoreUnsafeFilesCannotBlockOrEscape(t *testing.T) {
	for _, target := range []string{".lock", "config.yaml", "mihomo", "output.log"} {
		for _, kind := range []string{"fifo", "symlink", "hardlink"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				s, _, artifact := coreService(t)
				dir, _, _ := s.paths()
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, target)
				outside := filepath.Join(t.TempDir(), "outside")
				coreWrite(t, outside, []byte("unchanged"))
				switch kind {
				case "fifo":
					if err := syscall.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(outside, path); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan error, 1)
				go func() {
					if target == "output.log" {
						_, err := s.Logs(20)
						done <- err
					} else {
						done <- s.Install(artifact, "")
					}
				}()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("unsafe file accepted")
					}
				case <-time.After(time.Second):
					t.Fatal("special file blocked operation")
				}
				if data, _ := os.ReadFile(outside); string(data) != "unchanged" {
					t.Fatal("linked external file changed")
				}
			})
		}
	}
}

func TestCoreLogsReadBoundedTail(t *testing.T) {
	s, _, _ := coreService(t)
	dir, _, _ := s.paths()
	coreWrite(t, filepath.Join(dir, "output.log"), []byte("line 1\nline 2\nline 3\n"))
	got, err := s.Logs(2)
	if err != nil || got != "line 2\nline 3" {
		t.Fatalf("tail: %q %v", got, err)
	}
	for _, bad := range []int{-1, 0, 10001} {
		if _, err := s.Logs(bad); err == nil {
			t.Fatal("invalid tail accepted")
		}
	}
}

func TestCoreConfigureRejectsPublicSourceAndSnapshotMetadataChanges(t *testing.T) {
	s, _, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "private.yaml")
	coreWrite(t, source, []byte(coreConfig))
	if err := os.Chmod(source, 0644); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func() error{func() error { return s.Configure(source) }, func() error { return s.Preflight("configure", source, "") }} {
		if err := check(); err == nil {
			t.Fatal("public candidate source accepted")
		}
	}
	if err := os.Chmod(source, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := capture(source, maxConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	changeTime := func(info os.FileInfo) any {
		stat := reflect.ValueOf(info.Sys())
		if stat.Kind() == reflect.Pointer && !stat.IsNil() {
			for _, name := range []string{"Ctim", "Ctimespec"} {
				if field := stat.Elem().FieldByName(name); field.IsValid() {
					return field.Interface()
				}
			}
		}
		t.Fatal("fixture cannot read the native change-time field")
		return nil
	}
	originalChangeTime := changeTime(before.info)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := os.Chmod(source, 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(source, 0600); err != nil {
			t.Fatal(err)
		}
		current, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		// Linux tmpfs may give adjacent chmod calls the same coarse clock
		// tick. Observe a real ctime change before testing its rejection;
		// the snapshot checker itself is deliberately not the poll condition.
		if !reflect.DeepEqual(originalChangeTime, changeTime(current)) {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("fixture chmod did not cross a native change-time tick")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := before.check(); err == nil {
		t.Fatal("restored chmod concealed change-time metadata mutation")
	}
}

func TestCoreTemporaryCleanupRetainsSubstitutedUnknownFile(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	var candidatePath string
	r.run = func(c Command) ([]byte, error) {
		candidatePath = c.Program
		if err := os.Rename(c.Program, c.Program+"-original"); err != nil {
			t.Fatal(err)
		}
		coreWrite(t, c.Program, []byte("unknown replacement"))
		return []byte("Mihomo Meta v1.2.4"), nil
	}
	if err := s.Update(artifact, ""); err == nil {
		t.Fatal("substituted candidate accepted")
	}
	if data, err := os.ReadFile(candidatePath); err != nil || string(data) != "unknown replacement" {
		t.Fatalf("cleanup removed an unknown substituted path: %q %v", data, err)
	}
}

func TestCoreLongOperationsRejectReplacedOperationLock(t *testing.T) {
	for _, action := range []string{"update", "configure", "validate"} {
		t.Run(action, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			dir, program, _ := s.paths()
			config := filepath.Join(dir, "config.yaml")
			originalProgram, _ := os.ReadFile(program)
			originalConfig, _ := os.ReadFile(config)
			r.run = func(c Command) ([]byte, error) {
				lock := filepath.Join(dir, ".lock")
				if err := os.Rename(lock, lock+"-old"); err != nil {
					t.Fatal(err)
				}
				coreWrite(t, lock, []byte("new operation lock"))
				return []byte("Mihomo Meta v1.2.4"), nil
			}
			var err error
			switch action {
			case "update":
				err = s.Update(artifact, "")
			case "configure":
				err = s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "7890", "7891")))
			case "validate":
				err = s.Validate()
			}
			if err == nil {
				t.Fatal("operation accepted a replaced lock inode")
			}
			if current, _ := os.ReadFile(program); !bytes.Equal(current, originalProgram) {
				t.Fatal("program changed after operation lost its lock")
			}
			if current, _ := os.ReadFile(config); !bytes.Equal(current, originalConfig) {
				t.Fatal("configuration changed after operation lost its lock")
			}
		})
	}
}

func TestCoreRemoveRefusesUnknownFilesAtProgramPath(t *testing.T) {
	for _, item := range []struct {
		data []byte
		mode os.FileMode
	}{{[]byte("user notes"), 0600}, {[]byte("#!/bin/sh\n"), 0700}, {coreNativeFixture(runtime.GOOS, runtime.GOARCH), 0600}} {
		s, r, artifact := coreService(t)
		_, program, _ := s.paths()
		coreWrite(t, program, item.data)
		if err := os.Chmod(program, item.mode); err != nil {
			t.Fatal(err)
		}
		for _, action := range []func() error{s.Remove, func() error { return s.Preflight("remove", "", "") }, func() error { return s.Update(artifact, "") }, func() error { return s.Preflight("update", artifact, "") }} {
			if err := action(); err == nil {
				t.Fatal("remove accepted unknown/non-executable program path")
			}
		}
		if current, err := os.ReadFile(program); err != nil || !bytes.Equal(current, item.data) {
			t.Fatalf("unknown file removed: %q %v", current, err)
		}
		if len(r.commands) != 0 {
			t.Fatal("remove executed an unknown program")
		}
	}
}

func coreStoppedRecord(t *testing.T, s *Service) processRecord {
	t.Helper()
	_, program, _ := s.paths()
	launcher := filepath.Join(s.Root, "launcher")
	p := Process{PID: 4242, UID: uint32(os.Geteuid()), Boot: "boot", Started: "birth", Executable: launcher}
	r := processRecord{Process: p, Program: program, Launcher: launcher, ProgramHash: strings.Repeat("a", 64), ConfigHash: strings.Repeat("b", 64), Endpoints: []string{"http://127.0.0.1:7890"}, Phase: "stopped"}
	if err := s.saveRecord(r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCoreMutationsBindSnapshotsBeforeInitialStoppedInspection(t *testing.T) {
	for _, action := range []string{"update", "configure", "remove"} {
		for _, mutation := range []string{"lock", "program", "program-identical"} {
			t.Run(action+"/"+mutation, func(t *testing.T) {
				s, r, artifact := coreService(t)
				if err := s.Install(artifact, ""); err != nil {
					t.Fatal(err)
				}
				if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
					t.Fatal(err)
				}
				coreStoppedRecord(t, s)
				dir, program, _ := s.paths()
				originalConfig, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
				programData, _ := os.ReadFile(program)
				replacement := append(programData, 1)
				if mutation == "program-identical" {
					// The new writer owns a different inode even when bytes and
					// permissions match. Hash equality cannot authorize its removal.
					replacement = append([]byte(nil), programData...)
				}
				inspects := 0
				r.inspect = func(int) (Process, error) {
					inspects++
					if inspects == 1 {
						if mutation == "lock" {
							lock := filepath.Join(dir, ".lock")
							if err := os.Rename(lock, lock+"-original"); err != nil {
								t.Fatal(err)
							}
							coreWrite(t, lock, []byte("new lock"))
						} else {
							if err := os.Rename(program, program+"-original"); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(program, replacement, 0700); err != nil {
								t.Fatal(err)
							}
						}
					}
					return Process{}, os.ErrNotExist
				}
				commands := len(r.commands)
				var err error
				switch action {
				case "update":
					err = s.Update(artifact, "")
				case "configure":
					err = s.ConfigureBytes([]byte(strings.ReplaceAll(coreConfig, "7890", "7891")))
				case "remove":
					err = s.Remove()
				}
				if err == nil {
					t.Fatal("operation adopted changes made during its first stopped inspection")
				}
				if len(r.commands) != commands {
					t.Fatal("core executed after initial snapshot was replaced")
				}
				if current, _ := os.ReadFile(filepath.Join(dir, "config.yaml")); !bytes.Equal(current, originalConfig) {
					t.Fatal("existing configuration changed")
				}
				if mutation != "lock" {
					if current, err := os.ReadFile(program); err != nil || !bytes.Equal(current, replacement) {
						t.Fatal("externally replaced program was changed or removed")
					}
				}
			})
		}
	}
}

func TestCoreAtomicWriteExpectedRetainsNewTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	coreWrite(t, path, []byte("old"))
	before, err := capture(path, maxConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	coreWrite(t, path, []byte("new external configuration"))
	if err := atomicWriteExpected(path, []byte("operation candidate"), 0600, before); err == nil {
		t.Fatal("atomic writer accepted a target different from the original snapshot")
	}
	if data, _ := os.ReadFile(path); string(data) != "new external configuration" {
		t.Fatalf("new target overwritten: %q", data)
	}
}

func TestCoreInheritedEnvironmentCannotOverrideValidatedCore(t *testing.T) {
	for key, value := range map[string]string{
		"CLASH_CONFIG_STRING": "secret override",
		"LD_PRELOAD":          "/isolated/injected.so", "LD_LIBRARY_PATH": "/isolated/libraries",
		"DYLD_INSERT_LIBRARIES": "/isolated/injected.dylib", "DYLD_LIBRARY_PATH": "/isolated/libraries",
		"HTTP_PROXY": "http://127.0.0.1:3128", "SSL_CERT_FILE": "/isolated/certificates.pem",
	} {
		t.Setenv(key, value)
	}
	s, r, artifact := coreService(t)
	s.Env = nil // Match the real CLI's os.Environ inheritance path.
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.commands {
		seen := map[string]string{}
		for _, env := range c.Env {
			key, value, _ := strings.Cut(env, "=")
			if strings.HasPrefix(key, "CLASH_") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
				t.Fatalf("core inherited a configuration/loader override: %s", key)
			}
			seen[key] = value
		}
		if seen["HTTP_PROXY"] != "http://127.0.0.1:3128" || seen["SSL_CERT_FILE"] != "/isolated/certificates.pem" {
			t.Fatal("normal proxy/certificate environment was not preserved")
		}
	}
}

func TestCoreOperationCannotAdoptNewLockAtActionHandoff(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	commands := len(r.commands)
	err := s.withLock(func(directory directorySnapshot) error {
		lock := filepath.Join(directory.path, ".lock")
		if err := os.Rename(lock, lock+"-original"); err != nil {
			t.Fatal(err)
		}
		coreWrite(t, lock, []byte("new lock at handoff"))
		return s.install(artifact, "", true, directory)
	})
	if err == nil || len(r.commands) != commands {
		t.Fatalf("operation adopted a new lock before capturing its inputs: %v", err)
	}
}

func TestCoreAtomicWriteExpectedCannotAdoptNewLock(t *testing.T) {
	s, _, _ := coreService(t)
	err := s.withLock(func(directory directorySnapshot) error {
		path := filepath.Join(directory.path, "config.yaml")
		coreWrite(t, path, []byte("original target"))
		before, err := capture(path, maxConfig, false)
		if err != nil {
			t.Fatal(err)
		}
		lock := filepath.Join(directory.path, ".lock")
		if err := os.Rename(lock, lock+"-original"); err != nil {
			t.Fatal(err)
		}
		coreWrite(t, lock, []byte("new lock before writer"))
		err = atomicWriteExpected(path, []byte("candidate"), 0600, before, directory)
		if current, _ := os.ReadFile(path); string(current) != "original target" {
			t.Fatal("writer replaced the target after losing its original lock")
		}
		return err
	})
	if err == nil {
		t.Fatal("writer adopted a newly observed lock")
	}
}

func TestCorePreflightUsesActionSpecificRuntimeAuthorization(t *testing.T) {
	for _, state := range []string{"program-changed", "config-changed", "image-deleted", "starting", "unknown", "corrupt-record"} {
		t.Run(state, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			record := coreStoppedRecord(t, s)
			_, program, _ := s.paths()
			programData, _ := os.ReadFile(program)
			record.ProgramHash = hash(programData)
			config, _ := s.ReadConfig()
			record.ConfigHash = config.Digest
			record.Phase = "running"
			if err := s.saveRecord(record); err != nil {
				t.Fatal(err)
			}
			r.process = record.Process
			r.process.Executable = program
			switch state {
			case "program-changed":
				coreWrite(t, program, append(programData, 1))
				if err := os.Chmod(program, 0700); err != nil {
					t.Fatal(err)
				}
			case "config-changed":
				coreWrite(t, filepath.Join(filepath.Dir(program), "config.yaml"), []byte(strings.ReplaceAll(coreConfig, "7890", "7891")))
			case "image-deleted":
				r.process.ExecutableDeleted = true
			case "starting":
				r.process.Executable = record.Launcher
			case "unknown":
				r.inspectErr = errors.New("kernel identity unavailable")
			case "corrupt-record":
				coreWrite(t, filepath.Join(filepath.Dir(program), "process.json"), []byte("{}"))
			}
			commands := len(r.commands)
			for _, action := range []string{"start", "stop", "restart", "validate"} {
				err := s.Preflight(action, "", "")
				wantError := action == "start" || action != "validate" && (state == "unknown" || state == "corrupt-record")
				if (err != nil) != wantError {
					t.Fatalf("%s runtime=%s: error=%v wantError=%v", action, state, err, wantError)
				}
			}
			if len(r.commands) != commands || r.starts != 0 || r.stops != 0 {
				t.Fatal("preflight executed a core or controlled a process")
			}
		})
	}
}
