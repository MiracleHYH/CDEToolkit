package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeUpgradeReobservesSameBirthImageAndAllowsRepeatedCoreBackup(t *testing.T) {
	var f *controllerFixture
	f = newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/upgrade" {
			return false
		}
		if r.Method != http.MethodPost || r.URL.RawQuery != "channel=release" {
			t.Error("native upgrade gained force/custom policy", r.Method, r.URL.RawQuery)
		}
		f.fakeUpgrade(t)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return true
	})
	f.s.HTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("Nido downloader used for native update")
		return nil, errors.New("unexpected download")
	})}
	f.s.APIHTTP = f.s.HTTP
	f.s.GitHubProxy = "auto"
	var imageChecks atomic.Int32
	f.runner.image = func(Process, string) (bool, error) { return imageChecks.Add(1) > 2, nil }
	dir := filepath.Dir(f.program)
	var data []fileSnapshot
	for _, name := range []string{"config.yaml", "config.yaml.back", "providers/cache", "auth.json", "session.db", "unknown"} {
		path := filepath.Join(dir, name)
		if name != "config.yaml" {
			coreWrite(t, path, []byte("preserved "+name))
		}
		snap, err := capture(path, maxConfig, false)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, snap)
	}
	before, err := f.s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	commands := len(f.runner.commands)
	for i := 0; i < 2; i++ {
		if err := f.s.Upgrade(); err != nil {
			t.Fatal(err)
		}
		after, err := f.s.readRecord()
		if err != nil {
			t.Fatal(err)
		}
		program, _ := os.ReadFile(f.program)
		if after.Process != before.Process || after.Launcher != before.Launcher || after.ProgramHash == before.ProgramHash || after.ProgramHash != hash(program) || after.ControllerDigest == "" {
			t.Fatalf("incorrect same-birth upgrade record: %+v", after)
		}
		before = after
	}
	if imageChecks.Load() < 4 || f.upgrades.Load() != 2 {
		t.Fatal("upgrade did not wait for new image/repeat", imageChecks.Load(), f.upgrades.Load())
	}
	for _, snap := range data {
		if err := snap.check(); err != nil {
			t.Fatal("upgrade changed data", snap.path, err)
		}
	}
	if len(f.runner.commands) != commands || f.runner.starts != 0 || f.runner.stops != 0 {
		t.Fatal("native update executed a candidate or controlled process externally")
	}
	record, _ := os.ReadFile(filepath.Join(dir, "process.json"))
	if bytes.Contains(record, []byte(f.secret)) {
		t.Fatal("process record copied controller secret")
	}
}

func TestNativeUpgradeAcknowledgementCannotAdoptUnprovedImageOrRecord(t *testing.T) {
	for _, problem := range []string{"no-effect", "old-image", "old-version", "birth", "record", "config", "lock", "extra-backup"} {
		t.Run(problem, func(t *testing.T) {
			var f *controllerFixture
			var applied atomic.Bool
			f = newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/upgrade" {
					return false
				}
				if problem != "no-effect" {
					f.fakeUpgrade(t)
				}
				switch problem {
				case "old-version":
					f.version.Store("v1.2.3")
				case "record":
					coreWrite(t, filepath.Join(filepath.Dir(f.program), "process.json"), []byte("new external process record"))
				case "config":
					data, _ := os.ReadFile(f.configPath)
					coreWrite(t, f.configPath, append([]byte("# external edit\n"), data...))
				case "lock":
					lock := filepath.Join(filepath.Dir(f.program), ".lock")
					if err := os.Rename(lock, lock+".old"); err != nil {
						t.Fatal(err)
					}
					coreWrite(t, lock, []byte("external lock"))
				case "extra-backup":
					coreWrite(t, filepath.Join(filepath.Dir(f.program), "meta-backup", "unknown"), []byte("external backup data"))
				}
				applied.Store(true)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
				return true
			})
			f.s.StartTimeout = 65 * time.Millisecond
			if problem == "old-image" {
				f.runner.image = func(Process, string) (bool, error) { return false, nil }
			}
			base := f.runner.process
			if problem == "birth" {
				f.runner.inspect = func(int) (Process, error) {
					p := base
					if applied.Load() {
						p.Started = "other-birth"
					}
					return p, nil
				}
			}
			before, _ := capture(filepath.Join(filepath.Dir(f.program), "process.json"), 32<<10, false)
			if err := f.s.Upgrade(); err == nil {
				t.Fatal("HTTP 200 adopted unconfirmed native result")
			}
			if problem != "record" {
				if err := before.check(); err != nil {
					t.Fatal("unconfirmed upgrade rewrote record", err)
				}
			} else {
				data, _ := os.ReadFile(before.path)
				if string(data) != "new external process record" {
					t.Fatal("unknown replacement record overwritten")
				}
			}
			if f.upgrades.Load() > 1 || f.runner.starts != 0 || f.runner.stops != 0 {
				t.Fatal("unconfirmed native operation retried or controlled processes")
			}
		})
	}
}

func TestNativeUpgradeRejectsUnsafeCoreDirectoriesBeforeRequest(t *testing.T) {
	for _, kind := range []string{"update-file", "update-directory", "update-link", "backup-link", "unknown-backup", "backup-nonnative", "backup-hardlink", "backup-public-write"} {
		t.Run(kind, func(t *testing.T) {
			f := newControllerFixture(t, nil)
			dir := filepath.Dir(f.program)
			external := filepath.Join(t.TempDir(), "sentinel")
			coreWrite(t, external, []byte("external sentinel"))
			switch kind {
			case "update-file":
				coreWrite(t, filepath.Join(dir, "meta-update"), []byte("unknown updater file"))
			case "update-directory":
				coreWrite(t, filepath.Join(dir, "meta-update", "unknown"), []byte("unknown updater data"))
			case "update-link":
				if err := os.Symlink(filepath.Dir(external), filepath.Join(dir, "meta-update")); err != nil {
					t.Fatal(err)
				}
			case "backup-link":
				if err := os.Symlink(filepath.Dir(external), filepath.Join(dir, "meta-backup")); err != nil {
					t.Fatal(err)
				}
			case "unknown-backup":
				coreWrite(t, filepath.Join(dir, "meta-backup", "unknown"), []byte("unknown data"))
			case "backup-nonnative":
				coreWrite(t, filepath.Join(dir, "meta-backup", "mihomo"), []byte("user notes"))
			case "backup-hardlink":
				if err := os.MkdirAll(filepath.Join(dir, "meta-backup"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, filepath.Join(dir, "meta-backup", "mihomo")); err != nil {
					t.Fatal(err)
				}
			case "backup-public-write":
				if err := os.MkdirAll(filepath.Join(dir, "meta-backup"), 0777); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(dir, "meta-backup"), 0777); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.s.Upgrade(); err == nil {
				t.Fatal("unsafe native updater directory accepted")
			}
			if f.requests.Load() != 0 {
				t.Fatal("unsafe directories reached controller")
			}
			if data, _ := os.ReadFile(external); string(data) != "external sentinel" {
				t.Fatal("external data changed")
			}
			if kind == "update-directory" {
				if data, err := os.ReadFile(filepath.Join(dir, "meta-update", "unknown")); err != nil || string(data) != "unknown updater data" {
					t.Fatal("existing updater directory cleaned")
				}
			}
		})
	}
}

func TestControllerReloadFixedPathReobservesNewListenersAndBindsLegacyRecord(t *testing.T) {
	var applied atomic.Bool
	var f *controllerFixture
	f = newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/configs" {
			return false
		}
		var body struct {
			Path    string `json:"path"`
			Payload string `json:"payload"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Path != f.configPath || body.Payload != "" || r.Method != http.MethodPut || r.URL.RawQuery != "force=true" {
			t.Error("reload did not use fixed native path")
		}
		applied.Store(true)
		w.WriteHeader(http.StatusNoContent)
		return true
	})
	before, err := f.s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	before.ControllerAddress, before.ControllerDigest = "", ""
	if err = f.s.saveRecord(before); err != nil {
		t.Fatal(err)
	}
	// An unchanged legacy record can establish its controller binding only
	// during this explicit action, never during a read-only status query.
	if err = f.s.Reload(); err != nil {
		t.Fatal(err)
	}
	record, err := f.s.readRecord()
	if err != nil || record.ControllerDigest == "" {
		t.Fatal("legacy binding not captured", err)
	}
	data, _ := os.ReadFile(f.configPath)
	coreWrite(t, f.configPath, bytes.ReplaceAll(data, []byte("7890"), []byte("7891")))
	applied.Store(false)
	f.runner.owns = func(_ Process, endpoint string) (bool, error) {
		if endpoint == f.server.URL {
			return true, nil
		}
		if strings.HasSuffix(endpoint, ":7890") {
			return !applied.Load(), nil
		}
		return applied.Load(), nil
	}
	config, _ := capture(f.configPath, maxConfig, false)
	commands := len(f.runner.commands)
	if err = f.s.Reload(); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.readRecord()
	if err != nil {
		t.Fatal(err)
	}
	if after.Process != before.Process || after.ConfigHash != hash(config.data) || len(after.Endpoints) != 1 || !strings.HasSuffix(after.Endpoints[0], ":7891") {
		t.Fatalf("reload record incorrect: %+v", after)
	}
	if err := config.check(); err != nil {
		t.Fatal("reload rewrote main config", err)
	}
	if len(f.runner.commands) != commands || f.runner.starts != 0 || f.runner.stops != 0 {
		t.Fatal("reload ran parallel -t or restarted process")
	}
}

func TestControllerReloadFailurePreservesOriginalRecordAndNoAutomaticFallback(t *testing.T) {
	for _, problem := range []string{"parse-error", "missing-listener", "config-replaced", "record-replaced"} {
		t.Run(problem, func(t *testing.T) {
			var f *controllerFixture
			var applied atomic.Bool
			f = newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/configs" {
					return false
				}
				applied.Store(true)
				switch problem {
				case "parse-error":
					http.Error(w, "SECRET provider URL", http.StatusBadRequest)
					return true
				case "config-replaced":
					coreWrite(t, f.configPath, []byte("# external replacement\n"+coreConfig))
				case "record-replaced":
					coreWrite(t, filepath.Join(filepath.Dir(f.program), "process.json"), []byte("external record"))
				}
				w.WriteHeader(http.StatusNoContent)
				return true
			})
			f.s.StartTimeout = 60 * time.Millisecond
			if problem == "missing-listener" {
				f.runner.owns = func(_ Process, endpoint string) (bool, error) {
					return endpoint == f.server.URL || !applied.Load(), nil
				}
			}
			record, _ := capture(filepath.Join(filepath.Dir(f.program), "process.json"), 32<<10, false)
			err := f.s.Reload()
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("reload failure not bounded/safe: %v", err)
			}
			if problem != "record-replaced" {
				if err := record.check(); err != nil {
					t.Fatal("reload failure rewrote record", err)
				}
			}
			if f.runner.starts != 0 || f.runner.stops != 0 {
				t.Fatal("reload failure fell back to stop/start")
			}
		})
	}
}

func TestNativeUpgradePlatformGateAndCancellationDoNotSendDuplicateMutation(t *testing.T) {
	f := newControllerFixture(t, nil)
	f.s.Platform = "darwin/arm64"
	if err := f.s.Upgrade(); err == nil || f.requests.Load() != 0 {
		t.Fatal("unsupported platform sent native update")
	}
	f.s.Platform = "linux/amd64"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.s.UpgradeContext(ctx); err == nil || !errors.Is(err, context.Canceled) || f.requests.Load() != 0 {
		t.Fatalf("cancelled update made a request: %v", err)
	}
}

func TestControllerReloadPreflightIsLocalAndDoesNotMutateSnapshots(t *testing.T) {
	f := newControllerFixture(t, nil)
	data, _ := os.ReadFile(f.configPath)
	coreWrite(t, f.configPath, append([]byte("# pending reload\n"), data...))
	var snapshots []fileSnapshot
	for _, path := range []string{f.configPath, f.program, filepath.Join(filepath.Dir(f.program), "process.json")} {
		snapshot, err := capture(path, maxProgram, false)
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
	}
	commands := len(f.runner.commands)
	if err := f.s.Preflight("reload", "", ""); err != nil {
		t.Fatal(err)
	}
	if f.requests.Load() != 0 || len(f.runner.commands) != commands || f.runner.starts != 0 || f.runner.stops != 0 {
		t.Fatal("reload preview made a request or executed a core")
	}
	for _, snapshot := range snapshots {
		if err := snapshot.check(); err != nil {
			t.Fatal("reload preview changed files", err)
		}
	}
}

func TestNativeUpgradeObservationDeadlineCancelsVersionRequest(t *testing.T) {
	var f *controllerFixture
	var upgraded atomic.Bool
	f = newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/upgrade" {
			f.fakeUpgrade(t)
			upgraded.Store(true)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return true
		}
		if r.URL.Path == "/version" && upgraded.Load() {
			<-r.Context().Done()
			return true
		}
		return false
	})
	f.s.StartTimeout = 60 * time.Millisecond
	before, _ := capture(filepath.Join(filepath.Dir(f.program), "process.json"), 32<<10, false)
	started := time.Now()
	if err := f.s.Upgrade(); err == nil {
		t.Fatal("late version response accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("version request escaped observation deadline")
	}
	if err := before.check(); err != nil {
		t.Fatal("timed-out version observation rewrote record", err)
	}
	if f.upgrades.Load() != 1 {
		t.Fatal("timed-out mutation retried")
	}
}
