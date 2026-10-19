package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type controllerFakeRunner struct {
	*coreFakeRunner
	owns  func(Process, string) (bool, error)
	image func(Process, string) (bool, error)
}

func (r *controllerFakeRunner) OwnsEndpoint(p Process, endpoint string) (bool, error) {
	if r.owns != nil {
		return r.owns(p, endpoint)
	}
	return true, nil
}
func (r *controllerFakeRunner) RunningImageMatches(p Process, path string) (bool, error) {
	if r.image != nil {
		return r.image(p, path)
	}
	return true, nil
}

type controllerFixture struct {
	s                   *Service
	runner              *controllerFakeRunner
	server              *httptest.Server
	configPath, program string
	secret              string
	version             atomic.Value
	requests            atomic.Int32
	upgrades            atomic.Int32
	handler             func(http.ResponseWriter, *http.Request) bool
}

func newControllerFixture(t *testing.T, handler func(http.ResponseWriter, *http.Request) bool) *controllerFixture {
	t.Helper()
	s, runner, artifact := coreService(t)
	s.Platform = "linux/amd64"
	if err := os.WriteFile(artifact, coreNativeFixture("linux", "amd64"), 0700); err != nil {
		t.Fatal(err)
	}
	f := &controllerFixture{s: s, runner: &controllerFakeRunner{coreFakeRunner: runner}, secret: "controller-test-private-secret", handler: handler}
	f.version.Store("v1.2.3")
	s.Runner = f.runner
	s.StartTimeout = 150 * time.Millisecond
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+f.secret {
			http.Error(w, "SECRET wrong token", http.StatusUnauthorized)
			return
		}
		if f.handler != nil && f.handler(w, r) {
			return
		}
		switch r.URL.Path {
		case "/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": true, "version": f.version.Load().(string)})
		case "/proxies":
			_, _ = w.Write([]byte(`{"proxies":{"PROXY":{"type":"URLTest","now":"node","all":["node"]},"node":{"type":"Shadowsocks","history":[{"time":"2026-10-19T10:00:00Z","delay":32}]}}}`))
		case "/providers/proxies":
			_, _ = w.Write([]byte(`{"providers":{"sub":{"type":"Proxy","vehicleType":"HTTP","updatedAt":"2026-10-19T10:00:00Z"},"local /中文":{"type":"Proxy","vehicleType":"File"},"builtin":{"type":"Proxy","vehicleType":"Compatible"}}}`))
		case "/providers/rules":
			_, _ = w.Write([]byte(`{"providers":{"sub":{"type":"Rule","vehicleType":"File"}}}`))
		default:
			http.Error(w, "SECRET unknown route", http.StatusNotFound)
		}
	}))
	t.Cleanup(f.server.Close)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	f.configPath, _ = s.ConfigPath()
	_, f.program, _ = s.paths()
	data := []byte(coreConfig + "external-controller: " + strings.TrimPrefix(f.server.URL, "http://") + "\nsecret: " + f.secret + "\n")
	if err := s.ConfigureBytes(data); err != nil {
		t.Fatal(err)
	}
	config, err := s.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	record := coreStoppedRecord(t, s)
	record.ProgramHash, record.ConfigHash, record.Phase = hash(coreNativeFixture("linux", "amd64")), config.Digest, "running"
	record.ControllerAddress, record.ControllerDigest = config.Controller.Address, config.Controller.Digest()
	if err := s.saveRecord(record); err != nil {
		t.Fatal(err)
	}
	runner.process = record.Process
	runner.process.Executable = f.program
	return f
}

func TestControllerOverviewAllowsPartialDataListenersAndNeverUsesAmbientProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	f := newControllerFixture(t, nil)
	f.runner.owns = func(_ Process, endpoint string) (bool, error) { return endpoint == f.server.URL, nil }
	before, _ := capture(filepath.Join(filepath.Dir(f.program), "process.json"), 32<<10, false)
	commands := len(f.runner.commands)
	view, err := f.s.Overview()
	if err != nil || view.Version != "v1.2.3" || len(view.Groups) != 1 || view.Groups[0].Node != "node" {
		t.Fatalf("partial overview unavailable: %+v %v", view, err)
	}
	if err := before.check(); err != nil {
		t.Fatal("read-only overview rewrote process record", err)
	}
	if len(f.runner.commands) != commands || f.runner.starts != 0 || f.runner.stops != 0 {
		t.Fatal("overview executed a core or controlled runtime")
	}
	if err := f.s.RefreshProvider("proxy", "sub"); err == nil {
		t.Fatal("write allowed without all proxy listeners")
	}
}

func TestControllerStrictPathsResponsesAndCredentials(t *testing.T) {
	for _, problem := range []string{"redirect", "status", "large", "json", "trailing"} {
		t.Run(problem, func(t *testing.T) {
			f := newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch problem {
				case "redirect":
					w.Header().Set("Location", "http://127.0.0.1:1/stolen")
					w.WriteHeader(http.StatusFound)
				case "status":
					http.Error(w, "SECRET subscription-token", http.StatusInternalServerError)
				case "large":
					_, _ = w.Write(bytes.Repeat([]byte("x"), maxControllerResponse+1))
				case "json":
					_, _ = w.Write([]byte(`{"meta":true,"version":`))
				case "trailing":
					_, _ = w.Write([]byte(`{"meta":true,"version":"v1.2.3"} {}`))
				}
				return true
			})
			_, err := f.s.Overview()
			if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), f.secret) {
				t.Fatalf("unsafe/absent controller error: %v", err)
			}
			if f.requests.Load() != 1 {
				t.Fatal("unexpected redirect/retry", f.requests.Load())
			}
		})
	}
	f := newControllerFixture(t, nil)
	c, err := f.s.controllerSession(false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	for _, route := range []struct{ method, path string }{{"POST", "/restart"}, {"DELETE", "/connections"}, {"PATCH", "/configs"}, {"GET", "/logs"}, {"POST", "/upgrade?channel=release&force=true"}, {"PUT", "/configs?force=false"}, {"GET", "http://outside.invalid/version"}} {
		if _, err := c.request(context.Background(), route.method, route.path, nil, http.StatusOK, false, false); err == nil {
			t.Fatal("arbitrary controller API allowed", route)
		}
	}
	if f.requests.Load() != 0 {
		t.Fatal("rejected API reached controller")
	}
}

func TestControllerRejectsUnknownIdentityAndControlDriftWithoutRequest(t *testing.T) {
	for _, change := range []string{"birth", "uid", "boot", "image", "deleted", "program", "record", "controller-owner", "controller-fields", "legacy-changed"} {
		t.Run(change, func(t *testing.T) {
			f := newControllerFixture(t, nil)
			switch change {
			case "birth":
				f.runner.process.Started = "other"
			case "uid":
				f.runner.process.UID++
			case "boot":
				f.runner.process.Boot = "other"
			case "image":
				f.runner.process.Executable = "/unrelated"
			case "deleted":
				f.runner.process.ExecutableDeleted = true
			case "program":
				coreWrite(t, f.program, []byte("external data"))
			case "record":
				coreWrite(t, filepath.Join(filepath.Dir(f.program), "process.json"), []byte("corrupt record"))
			case "controller-owner":
				f.runner.owns = func(Process, string) (bool, error) { return false, nil }
			case "controller-fields":
				data, _ := os.ReadFile(f.configPath)
				coreWrite(t, f.configPath, bytes.ReplaceAll(data, []byte(f.secret), []byte("changed secret")))
			case "legacy-changed":
				r, err := f.s.readRecord()
				if err != nil {
					t.Fatal(err)
				}
				r.ControllerAddress, r.ControllerDigest = "", ""
				if err = f.s.saveRecord(r); err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(f.configPath)
				coreWrite(t, f.configPath, append([]byte("# edited\n"), data...))
			}
			if _, err := f.s.Overview(); err == nil {
				t.Fatal("unknown controller accepted")
			}
			if f.requests.Load() != 0 {
				t.Fatal("unowned controller received request")
			}
		})
	}
}

func TestControllerProviderRefreshUsesNativeHTTPAndFileRoutes(t *testing.T) {
	var refresh atomic.Int32
	f := newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut {
			if r.URL.Query().Encode() != "" {
				t.Error("refresh query introduced")
			}
			refresh.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		return false
	})
	providers, err := f.s.Providers()
	if err != nil || len(providers) != 4 {
		t.Fatalf("providers: %+v %v", providers, err)
	}
	for _, p := range []struct{ kind, name string }{{"proxy", "sub"}, {"proxy", "local /中文"}, {"rule", "sub"}} {
		if err := f.s.RefreshProvider(p.kind, p.name); err != nil {
			t.Fatal(err)
		}
	}
	if refresh.Load() != 3 {
		t.Fatal("native refresh routes not reached")
	}
	for _, p := range []struct{ kind, name string }{{"proxy", "builtin"}, {"rule", "missing"}, {"other", "sub"}, {"proxy", ".."}} {
		if err := f.s.RefreshProvider(p.kind, p.name); err == nil {
			t.Fatal("invalid refresh accepted", p)
		}
	}
	if refresh.Load() != 3 || f.runner.starts != 0 || f.runner.stops != 0 {
		t.Fatal("invalid refresh mutated core state")
	}
}

func TestControllerContextCancellationDoesNotRetryOrExposeSecrets(t *testing.T) {
	started := make(chan struct{})
	f := newControllerFixture(t, func(w http.ResponseWriter, r *http.Request) bool { close(started); <-r.Context().Done(); return true })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := f.s.OverviewContext(ctx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), f.secret) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller cancellation did not return")
	}
	if f.requests.Load() != 1 {
		t.Fatal("cancelled request retried")
	}
}

func (f *controllerFixture) fakeUpgrade(t *testing.T) {
	t.Helper()
	n := f.upgrades.Add(1)
	before, err := os.ReadFile(f.program)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(filepath.Dir(f.program), "meta-backup")
	if err = os.MkdirAll(backup, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(backup, "mihomo"), before, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(f.program, f.program+fmt.Sprint(".old-", n)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.program, append(before, byte(n)), 0700); err != nil {
		t.Fatal(err)
	}
	f.version.Store(fmt.Sprintf("v1.2.%d", 3+n))
}
