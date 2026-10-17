package builtin

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

type fakeRunner struct {
	process Process
	command Command
	starts  int
	stops   int
	err     error
	owned   bool
}

func (r *fakeRunner) Start(c Command) (Process, error) {
	r.command, r.starts = c, r.starts+1
	r.process = Process{PID: 4242, Executable: c.Program, Started: "birth-1"}
	return r.process, r.err
}

func (r *fakeRunner) Inspect(pid int) (Process, error) {
	if r.err != nil {
		return Process{}, r.err
	}
	if r.process.PID == 0 {
		return Process{}, os.ErrNotExist
	}
	return r.process, nil
}

func (r *fakeRunner) Stop(p Process) error {
	r.stops++
	if r.process != p {
		return errors.New("fake refused mismatched identity")
	}
	r.process = Process{}
	return r.err
}

func (r *fakeRunner) OwnsEndpoint(Process, string) (bool, error) { return r.owned, nil }

// These headers are never executed. They let installation/architecture tests
// validate ordinary files without compiling or starting a component program.
func nativeFixture(goos, arch string) []byte {
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

func fixtureFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, nativeFixture(runtime.GOOS, runtime.GOARCH), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalLifecyclePreservesConfigurationAndUnknownData(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: filepath.Join(t.TempDir(), "nido"), Runner: runner, Env: []string{"EXAMPLE=isolated"}}
	artifact := fixtureFile(t)
	if err := m.Install("proxy", artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Install("proxy", artifact, ""); err == nil {
		t.Fatal("install overwrote existing program")
	}
	writeFixture(t, filepath.Join(m.Root, "proxy", "config.yaml"), "mixed-port: 7890\n")
	writeFixture(t, filepath.Join(m.Root, "proxy", "unknown", "result.bin"), "experiment result\n")
	if err := m.Start("proxy"); err != nil {
		t.Fatal(err)
	}
	if s := m.Status("proxy"); !s.Installed || !s.Running || s.Problem != "" {
		t.Fatalf("unexpected status: %+v", s)
	}
	if runner.starts != 1 || runner.command.Env[0] != "EXAMPLE=isolated" {
		t.Fatalf("runner command not explicit: %+v", runner.command)
	}
	if err := m.Update("proxy", artifact, ""); err == nil {
		t.Fatal("updated a running program")
	}
	if err := m.Remove("proxy"); err == nil {
		t.Fatal("removed a running program")
	}
	if err := m.Stop("proxy"); err != nil {
		t.Fatal(err)
	}
	if err := m.Update("proxy", artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("proxy"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"config.yaml": "mixed-port: 7890\n", "unknown/result.bin": "experiment result\n"} {
		got, err := os.ReadFile(filepath.Join(m.Root, "proxy", path))
		if err != nil || string(got) != want {
			t.Fatalf("removed or modified %s", path)
		}
	}
	if runner.starts != 1 || runner.stops != 1 {
		t.Fatalf("installation/update/removal triggered processes: %+v", runner)
	}
}

func TestReusedPIDNeverSignalsAnotherProcess(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	if err := m.Install("proxy", fixtureFile(t), ""); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(m.Root, "proxy", "config.yaml"), "mixed-port: 7890\n")
	if err := m.Start("proxy"); err != nil {
		t.Fatal(err)
	}
	runner.process.Executable = "/unrelated/program"
	runner.process.Started = "recycled-pid"
	if err := m.Stop("proxy"); err != nil {
		t.Fatal(err)
	}
	if runner.stops != 0 {
		t.Fatal("recycled PID was signalled")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "process.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale record not cleared")
	}
}

func TestUnknownOrUnreadableProcessFailsClosed(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	if err := m.Install("proxy", fixtureFile(t), ""); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(m.Root, "proxy", "config.yaml"), "mixed-port: 7890\n")
	if err := m.Start("proxy"); err != nil {
		t.Fatal(err)
	}
	runner.err = errors.New("permission denied")
	if err := m.Remove("proxy"); err == nil {
		t.Fatal("unknown process ownership allowed removal")
	}
	if err := m.Stop("proxy"); err == nil || runner.stops != 0 {
		t.Fatal("unknown process ownership allowed stop")
	}
}

func TestOptionalGDSAndUnsafeArtifacts(t *testing.T) {
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	if err := m.Install("gds", "", ""); err == nil {
		t.Fatal("GDS got an implicit online installation")
	}
	if err := m.Install("codex", fixtureFile(t), ""); err == nil {
		t.Fatal("Codex accepted as built-in")
	}
	script := filepath.Join(t.TempDir(), "script")
	writeFixture(t, script, "#!/bin/sh\nexit 0\n")
	if err := m.Install("gds", script, ""); err == nil {
		t.Fatal("runtime-dependent script accepted")
	}
	if err := m.Install("gds", fixtureFile(t), strings.Repeat("0", 64)); err == nil {
		t.Fatal("invalid checksum accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(fixtureFile(t), link); err != nil {
		t.Fatal(err)
	}
	if err := m.Install("gds", link, ""); err == nil {
		t.Fatal("external source symlink accepted")
	}
}

func TestGDSStartIsExplicitlyUnavailableWithoutWritingOrRunning(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: filepath.Join(t.TempDir(), "not-created"), Runner: runner}
	if err := m.Start("gds"); err == nil || !strings.Contains(err.Error(), "尚未适配") {
		t.Fatal("GDS start did not report the missing scheduler adapter")
	}
	if _, err := os.Lstat(m.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsupported GDS start wrote to the software directory")
	}
	if s := m.Status("gds"); s.Installed || s.Configured || s.Problem != "" {
		t.Fatalf("optional missing GDS counted as abnormal: %+v", s)
	}
	artifact := fixtureFile(t)
	if err := m.Install("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	if s := m.Status("gds"); !s.Installed || s.Configured || !strings.Contains(s.Problem, "尚未适配") {
		t.Fatalf("GDS worker counted as a ready scheduler: %+v", s)
	}
	if err := m.Start("gds"); err == nil || runner.starts != 0 {
		t.Fatal("GDS artifact executed without a scheduler adapter")
	}
	if err := m.Update("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(m.Root, "gds", "config.toml"), "preserved user configuration\n")
	if err := m.Remove("gds"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Root, "gds", "config.toml")); string(b) != "preserved user configuration\n" {
		t.Fatal("GDS removal discarded user configuration")
	}
}

func TestGDSStopOnlyUsesRecordedOwnedProcess(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	if err := m.Install("gds", fixtureFile(t), ""); err != nil {
		t.Fatal(err)
	}
	dir, program, _ := m.paths("gds")
	runner.process = Process{PID: 4242, Executable: program, Started: "prior-recorded-process"}
	b, _ := json.Marshal(runner.process)
	if err := os.WriteFile(filepath.Join(dir, "process.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("gds"); err != nil || runner.stops != 1 {
		t.Fatalf("could not stop the exact previously recorded process: %v", err)
	}
	if runner.starts != 0 {
		t.Fatal("GDS cleanup started a component")
	}
}

func TestSymlinkComponentCannotTouchExternalFiles(t *testing.T) {
	outside := t.TempDir()
	writeFixture(t, filepath.Join(outside, "mihomo"), "external executable")
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	if err := os.Symlink(outside, filepath.Join(m.Root, "proxy")); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("proxy"); err == nil {
		t.Fatal("followed external component symlink")
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "mihomo")); string(got) != "external executable" {
		t.Fatal("external data changed")
	}
}

func TestProxyEndpointIsReadOnlyAndDoesNotStart(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	config := filepath.Join(m.Root, "proxy", "config.yaml")
	for _, tc := range []struct{ text, want string }{
		{"mixed-port: 7890\n", "http://127.0.0.1:7890"},
		{"port: 7891\nbind-address: '::1'\n", "http://[::1]:7891"},
		{"socks-port: 7892\n", "socks5h://127.0.0.1:7892"},
	} {
		writeFixture(t, config, tc.text)
		got, err := m.ProxyEndpoint()
		if err != nil || got != tc.want {
			t.Fatalf("endpoint %q, %v", got, err)
		}
		if b, _ := os.ReadFile(config); string(b) != tc.text {
			t.Fatal("endpoint query rewrote config")
		}
	}
	if runner.starts != 0 {
		t.Fatal("endpoint query started service")
	}
	writeFixture(t, config, "mixed-port: 7890\nauthentication: ['private:credential']\n")
	if _, err := m.ProxyEndpoint(); err == nil || strings.Contains(err.Error(), "private:credential") {
		t.Fatal("authenticated endpoint silently exported or leaked credentials")
	}
}

func TestConcurrentOperationsFailInsteadOfReplacingProgram(t *testing.T) {
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	artifact := fixtureFile(t)
	if err := m.Install("gds", artifact, ""); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(m.Root, "gds", ".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err = m.Remove("gds"); err == nil {
		t.Fatal("removed component while another operation holds the lock")
	}
	if s := m.Status("gds"); !s.Installed {
		t.Fatal("competing operation changed the component")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseFixture(t *testing.T, corrupt bool) (*http.Client, string, *int) {
	t.Helper()
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	gz.Write(nativeFixture("linux", "amd64"))
	gz.Close()
	payload := compressed.Bytes()
	count := new(int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*count++
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			name := "mihomo-linux-amd64-v1-v1.2.3.gz"
			digest := hash(payload)
			if corrupt {
				digest = strings.Repeat("0", 64)
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3", "assets": []any{map[string]any{
				"name": name, "browser_download_url": "https://github.com/MetaCubeX/mihomo/releases/download/v1.2.3/" + name,
				"digest": "sha256:" + digest, "size": len(payload),
			}}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, ".gz") {
			t.Errorf("unexpected isolated release path: %s", r.URL.Path)
		}
		w.Write(payload)
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	transport := client.Transport
	u, _ := url.Parse(server.URL)
	client.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Host, copy.URL.Scheme = u.Host, u.Scheme
		copy.Host = u.Host
		return transport.RoundTrip(copy)
	})
	return client, server.URL, count
}

func TestOfficialProxyDownloadUsesCompatibleAssetAndDigest(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "bad-digest"}[corrupt], func(t *testing.T) {
			client, api, count := releaseFixture(t, corrupt)
			m := Manager{Root: t.TempDir(), HTTP: client, API: api, Platform: "linux/amd64", Runner: &fakeRunner{}}
			err := m.Install("proxy", "", "")
			if corrupt && err == nil || !corrupt && err != nil {
				t.Fatalf("install result: %v", err)
			}
			if *count != 2 {
				t.Fatalf("expected only fixture metadata + download; requests=%d", *count)
			}
			if corrupt {
				if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "mihomo")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("bad download was installed")
				}
			}
		})
	}
}

func TestOfficialProxyUpdateRequiresOwnedControllerAndNeverFallsBackAfterPOST(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unclear-result"}[fail], func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer local-secret" {
					t.Fatal("controller auth missing")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/version" {
					io.WriteString(w, `{"meta":true,"version":"v1.0.0"}`)
					return
				}
				if r.Method == http.MethodPost && r.URL.Path == "/upgrade" && r.URL.Query().Get("channel") == "release" {
					posts++
					if fail {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, `{"status":"ok"}`)
					return
				}
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}))
			defer server.Close()
			runner := &fakeRunner{owned: true}
			m := Manager{Root: t.TempDir(), Runner: runner}
			if err := m.Install("proxy", fixtureFile(t), ""); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(m.Root, "proxy", "config.yaml"), "mixed-port: 7890\nexternal-controller: '"+strings.TrimPrefix(server.URL, "http://")+"'\nsecret: local-secret\n")
			if err := m.Start("proxy"); err != nil {
				t.Fatal(err)
			}
			err := m.Update("proxy", "", "")
			if fail && err == nil || !fail && err != nil || posts != 1 {
				t.Fatalf("official update: posts=%d err=%v", posts, err)
			}
			if err != nil && strings.Contains(err.Error(), "local-secret") {
				t.Fatal("error exposed secret")
			}
			runner.owned = false
			if err := m.Update("proxy", "", ""); err == nil || posts != 1 {
				t.Fatal("unowned controller accepted or process overwritten")
			}
		})
	}
}

func TestOfficialUpdateRechecksProgramAndConfiguration(t *testing.T) {
	for _, scenario := range []string{"program-symlink", "program-hardlink", "program-changed", "config-changed", "config-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			gets, posts := 0, 0
			var change func()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/version" {
					gets++
					if change != nil {
						change()
					}
					io.WriteString(w, `{"meta":true,"version":"v1.0.0"}`)
					return
				}
				posts++
				io.WriteString(w, `{"status":"ok"}`)
			}))
			defer server.Close()
			runner := &fakeRunner{owned: true}
			m := Manager{Root: t.TempDir(), Runner: runner}
			if err := m.Install("proxy", fixtureFile(t), ""); err != nil {
				t.Fatal(err)
			}
			dir, program, _ := m.paths("proxy")
			config := filepath.Join(dir, "config.yaml")
			configText := "mixed-port: 7890\nexternal-controller: '" + strings.TrimPrefix(server.URL, "http://") + "'\n"
			writeFixture(t, config, configText)
			if err := m.Start("proxy"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "program-symlink":
				outside := fixtureFile(t)
				if err := os.Remove(program); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, program); err != nil {
					t.Fatal(err)
				}
			case "program-hardlink":
				if err := os.Link(program, filepath.Join(t.TempDir(), "shared-program")); err != nil {
					t.Fatal(err)
				}
			case "program-changed":
				change = func() {
					if err := os.WriteFile(program, append(nativeFixture(runtime.GOOS, runtime.GOARCH), 1), 0700); err != nil {
						t.Error(err)
					}
				}
			case "config-changed":
				change = func() {
					if err := os.WriteFile(config, []byte(configText+"# user changed configuration\n"), 0600); err != nil {
						t.Error(err)
					}
				}
			case "config-symlink":
				outside := filepath.Join(t.TempDir(), "external.yaml")
				writeFixture(t, outside, configText)
				change = func() {
					if err := os.Remove(config); err != nil {
						t.Error(err)
					}
					if err := os.Symlink(outside, config); err != nil {
						t.Error(err)
					}
				}
			}
			if err := m.Update("proxy", "", ""); err == nil {
				t.Fatal("unsafe official update was allowed")
			}
			if posts != 0 {
				t.Fatal("unsafe official update submitted a POST")
			}
			if (scenario == "program-symlink" || scenario == "program-hardlink") && gets != 0 {
				t.Fatal("queried controller before program safety checks")
			}
		})
	}
}

func TestMalformedProxyConfigurationIsNotReady(t *testing.T) {
	runner := &fakeRunner{}
	m := Manager{Root: t.TempDir(), Runner: runner}
	if err := m.Install("proxy", fixtureFile(t), ""); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(m.Root, "proxy", "config.yaml"), "mixed-port: [invalid\n")
	if s := m.Status("proxy"); s.Configured || s.Problem == "" {
		t.Fatalf("malformed config counted as ready: %+v", s)
	}
	if err := m.Start("proxy"); err == nil || runner.starts != 0 {
		t.Fatal("started program with malformed configuration")
	}
}
