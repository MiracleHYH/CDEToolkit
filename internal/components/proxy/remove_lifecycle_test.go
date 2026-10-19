package proxy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runningRemoveFixture(t *testing.T) (*Service, *runtimeFakeRunner) {
	t.Helper()
	runner := &runtimeFakeRunner{ready: true}
	s := runtimeService(t, runner)
	s.Env = []string{"HOME=" + t.TempDir()}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	return s, runner
}

func TestRemoveRunningStopsBeforeDeletingAndInstallReusesRetainedData(t *testing.T) {
	s, runner := runningRemoveFixture(t)
	dir, program, _ := s.paths()
	for path, data := range map[string]string{
		"config.yaml.back":        "previous exact config\n",
		"providers/retained.yaml": "proxies: []\n",
		"cache.db":                "core cache",
		"output.log":              "previous log",
		"unknown/user-data":       "unknown data",
		"meta-update/unknown":     "upstream file",
		"auth/session":            "retained credentials and session",
	} {
		coreWrite(t, filepath.Join(dir, path), []byte(data))
	}
	// A manually saved config may differ from the running instance. Removal
	// stops the recorded birth; reinstallation must validate these exact bytes
	// and reuse its existing provider path without resetting either resource.
	coreWrite(t, filepath.Join(dir, "config.yaml"), []byte("mixed-port: 17891\nallow-lan: false\nproxy-providers:\n  retained:\n    type: file\n    path: providers/retained.yaml\nproxy-groups:\n  - name: proxy\n    type: select\n    use: [retained]\nrules: [MATCH,DIRECT]\n"))
	outside := filepath.Join(t.TempDir(), "sentinel")
	coreWrite(t, outside, []byte("external sentinel"))
	link := filepath.Join(dir, "meta-backup")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Fatal(err)
	}
	linkBefore, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	var retained []fileSnapshot
	for _, path := range []string{"config.yaml", "config.yaml.back", "providers/retained.yaml", "cache.db", "output.log", "unknown/user-data", "meta-update/unknown", "auth/session"} {
		before, err := capture(filepath.Join(dir, path), maxConfig, false)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, before)
	}
	sentinel, err := capture(outside, maxConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	retained = append(retained, sentinel)
	assertRetained := func() {
		t.Helper()
		for _, before := range retained {
			if err := before.check(); err != nil {
				t.Fatalf("retained file changed: %s: %v", before.path, err)
			}
		}
		after, err := os.Lstat(link)
		if err != nil || !sameMetadata(linkBefore, after) {
			t.Fatalf("external directory link changed: %v", err)
		}
	}
	core, err := capture(program, maxProgram, false)
	if err != nil {
		t.Fatal(err)
	}
	_, record, err := s.readRecordSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	validations, starts := runner.validations, runner.starts
	if err := s.Preflight("remove", "", ""); err != nil {
		t.Fatal("running remove preflight:", err)
	}
	if runner.stops != 0 || runner.validations != validations || !runner.alive {
		t.Fatal("preflight changed the running instance")
	}
	if err := record.check(); err != nil {
		t.Fatal(err)
	}
	if err := core.check(); err != nil {
		t.Fatal(err)
	}
	runner.onStop = func() {
		if _, err := os.Stat(program); err != nil {
			t.Fatal("program removed before stop completed:", err)
		}
		entered := false
		err := s.withLock(func(directorySnapshot) error { entered = true; return nil })
		if err == nil || entered {
			t.Fatal("remove released the component lock before stopping")
		}
	}
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(program); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("program retained after successful stop:", err)
	}
	stopped, err := s.readRecord()
	if err != nil || stopped.Phase != "stopped" || runner.alive || runner.stops != 1 || runner.validations != validations {
		t.Fatalf("remove did not confirm and record exit: record=%+v err=%v runner=%+v", stopped, err, runner)
	}
	assertRetained()
	if err := s.Remove(); err != nil || runner.stops != 1 {
		t.Fatalf("repeated remove: %v, stops=%d", err, runner.stops)
	}
	originalConfig, err := s.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	var checks []Command
	runner.onRun = func(c Command) {
		checks = append(checks, c)
		if runner.alive {
			t.Fatal("install ran a candidate beside a live instance")
		}
	}
	if err := s.Install(coreArtifact(t), ""); err != nil {
		t.Fatal("reinstall with preserved data:", err)
	}
	if runner.starts != starts || runner.stops != 1 || runner.alive || len(checks) != 2 || checks[0].Args[0] != "-v" || checks[1].Args[0] != "-t" || !bytes.Equal(checks[1].Input, originalConfig.Data) {
		t.Fatalf("reinstall did not validate the preserved config while stopped: checks=%+v", checks)
	}
	assertRetained()
}

func TestRemoveRefusesFailedStopOrUnverifiableIdentityWithoutDeleting(t *testing.T) {
	for _, scenario := range []string{"stop-failure", "kernel-unavailable", "unknown-executable", "invalid-record", "program-symlink", "program-hardlink"} {
		t.Run(scenario, func(t *testing.T) {
			s, runner := runningRemoveFixture(t)
			dir, program, _ := s.paths()
			switch scenario {
			case "stop-failure":
				runner.stopErr = errors.New("stop rejected")
			case "kernel-unavailable":
				runner.inspectErr = errors.New("kernel unavailable")
			case "unknown-executable":
				runner.process.Executable = filepath.Join(s.Root, "unrelated")
			case "invalid-record":
				coreWrite(t, filepath.Join(dir, "process.json"), []byte("invalid record"))
			case "program-symlink", "program-hardlink":
				outside := filepath.Join(t.TempDir(), "external-core")
				if err := os.Rename(program, outside); err != nil {
					t.Fatal(err)
				}
				var err error
				if scenario == "program-symlink" {
					err = os.Symlink(outside, program)
				} else {
					err = os.Link(outside, program)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			programBefore, err := os.Lstat(program)
			if err != nil {
				t.Fatal(err)
			}
			_, record, recordErr := s.readRecordSnapshot()
			if recordErr != nil && scenario != "invalid-record" {
				t.Fatal(recordErr)
			}
			if scenario != "stop-failure" {
				if err := s.Preflight("remove", "", ""); err == nil {
					t.Fatal("preflight accepted unsafe removal")
				}
			}
			if err := s.Remove(); err == nil {
				t.Fatal("remove accepted failed stop or unsafe identity")
			}
			current, err := os.Lstat(program)
			if err != nil || !sameMetadata(programBefore, current) {
				t.Fatalf("program changed despite failed removal: %v", err)
			}
			if err := record.check(); err != nil {
				t.Fatal("failed removal changed process record:", err)
			}
			wantStops := 0
			if scenario == "stop-failure" {
				wantStops = 1
			}
			if !runner.alive || runner.stops != wantStops {
				t.Fatalf("unsafe signal: alive=%v stops=%d", runner.alive, runner.stops)
			}
		})
	}
}

func TestRemoveRetainsReplacementsMadeDuringStop(t *testing.T) {
	for _, replacement := range []string{"program", "program-identical", "record", "record-identical", "lock", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			s, runner := runningRemoveFixture(t)
			dir, program, _ := s.paths()
			target := program
			var saved []byte
			runner.onStop = func() {
				switch replacement {
				case "record", "record-identical":
					target = filepath.Join(dir, "process.json")
				case "lock":
					target = filepath.Join(dir, ".lock")
				case "directory":
					if err := os.Rename(dir, dir+"-original"); err != nil {
						t.Fatal(err)
					}
					coreWrite(t, program, []byte("new owner program"))
					saved = []byte("new owner program")
					return
				}
				saved, _ = os.ReadFile(target)
				if !strings.HasSuffix(replacement, "-identical") {
					saved = append(saved, []byte("new external writer")...)
				}
				info, err := os.Lstat(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(target, target+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, saved, info.Mode().Perm()); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Remove(); err == nil {
				t.Fatal("remove adopted a replacement made during stop")
			}
			if current, err := os.ReadFile(target); err != nil || !bytes.Equal(current, saved) {
				t.Fatalf("external replacement changed: %q %v", current, err)
			}
			if _, err := os.Lstat(program); err != nil {
				t.Fatal("program removed after a concurrent replacement:", err)
			}
		})
	}
}

func TestRemoveStopsRecordedDeletedImageAndNeverSignalsReusedPID(t *testing.T) {
	for _, scenario := range []string{"deleted", "missing", "reused-pid"} {
		t.Run(scenario, func(t *testing.T) {
			s, runner := runningRemoveFixture(t)
			_, program, _ := s.paths()
			if scenario == "reused-pid" {
				runner.process.Started = "new unrelated birth"
			} else {
				runner.process.ExecutableDeleted = true
				if scenario == "missing" {
					if err := os.Remove(program); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.Preflight("remove", "", ""); err != nil {
				t.Fatal(err)
			}
			if err := s.Remove(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(program); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("program still present:", err)
			}
			if scenario == "reused-pid" {
				if !runner.alive || runner.stops != 0 {
					t.Fatal("remove signaled a reused PID")
				}
			} else if runner.alive || runner.stops != 1 {
				t.Fatal("recorded deleted image was not stopped")
			}
		})
	}
}
