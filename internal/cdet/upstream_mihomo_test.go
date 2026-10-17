package cdet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func mihomoFixture(t *testing.T, server *httptest.Server) (*Manager, string, string) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m := &Manager{Store: filepath.Join(root, "components"), Prefix: filepath.Join(root, "tool"), Releases: &ReleaseClient{HTTP: server.Client(), API: "https://example.invalid", OS: runtime.GOOS, Arch: runtime.GOARCH}, State: State{Components: map[string]Component{"proxy": {Installed: true, Backend: "mihomo", Digest: strings.Repeat("a", 64), Origin: "installed"}}}}
	program := filepath.Join(m.Store, programPath("proxy"))
	config := filepath.Join(m.Store, dataPath("proxy"), "config.yaml")
	for _, p := range []string{filepath.Dir(program), filepath.Dir(config)} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.WriteFile(program, adapterNative("before"), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(config, []byte("external-controller: '"+strings.TrimPrefix(server.URL, "http://")+"'\nsecret: 'PRIVATE-CONTROLLER-TOKEN'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	m.Releases.MihomoOwner = func(endpoint, binary string) (bool, error) { return endpoint == server.URL && binary == program, nil }
	return m, program, config
}

func TestMihomoAdapterPreviewsAndUsesOfficialUpdaterWithoutLeakingAuth(t *testing.T) {
	var posts atomic.Int32
	var program string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer PRIVATE-CONTROLLER-TOKEN" {
			t.Error("controller authentication missing")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/version" {
			fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/upgrade" || r.URL.Query().Get("channel") != "release" {
			t.Error("unexpected controller request")
			w.WriteHeader(404)
			return
		}
		n := posts.Add(1)
		if e := os.WriteFile(program, adapterNative(fmt.Sprintf("official-%d", n)), 0755); e != nil {
			t.Error(e)
		}
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	m, p, config := mihomoFixture(t, server)
	program = p
	beforeConfig, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	beforeProgram, e := os.ReadFile(program)
	if e != nil {
		t.Fatal(e)
	}
	info, e := m.mihomoController()
	if e != nil || info == nil || !info.Restart || posts.Load() != 0 {
		t.Fatal("preview changed a component or failed", e)
	}
	encoded, e := json.Marshal(info)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded), "PRIVATE-CONTROLLER-TOKEN") || strings.Contains(fmt.Sprintf("%+v", info), "PRIVATE-CONTROLLER-TOKEN") {
		t.Fatal("preview leaked controller secret")
	}
	afterProgram, _ := os.ReadFile(program)
	if string(beforeProgram) != string(afterProgram) {
		t.Fatal("GET preview changed program")
	}
	for i := 0; i < 2; i++ {
		handled, e := m.updateMihomoOfficial()
		if !handled || e != nil {
			t.Fatal("official program self-update rejected", handled, e)
		}
	}
	if posts.Load() != 2 {
		t.Fatal("official updater was not used")
	}
	afterConfig, _ := os.ReadFile(config)
	if string(beforeConfig) != string(afterConfig) {
		t.Fatal("update modified configuration")
	}
}

func TestMihomoAdapterNeverFallsBackAfterSubmittingPost(t *testing.T) {
	for _, mode := range []string{"http-error", "disconnected", "invalid-result", "invalid-program"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			var program string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
					return
				}
				posts.Add(1)
				switch mode {
				case "http-error":
					w.WriteHeader(500)
					fmt.Fprint(w, "PRIVATE-CONTROLLER-TOKEN")
				case "disconnected":
					c, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					_ = c.Close()
				case "invalid-result":
					fmt.Fprint(w, `{"status":"unknown"}`)
				case "invalid-program":
					if e := os.WriteFile(program, []byte("not a native program"), 0755); e != nil {
						t.Error(e)
					}
					fmt.Fprint(w, `{"status":"ok"}`)
				}
			}))
			defer server.Close()
			m, p, _ := mihomoFixture(t, server)
			program = p
			handled, e := m.updateMihomoOfficial()
			if !handled || e == nil || posts.Load() != 1 {
				t.Fatal("uncertain official update can fall through to download", handled, e, posts.Load())
			}
			if strings.Contains(e.Error(), "PRIVATE-CONTROLLER-TOKEN") {
				t.Fatal("upstream response leaked authentication")
			}
		})
	}
}

func TestMihomoAdapterDoesNotStartMissingOrUnownedControllers(t *testing.T) {
	for _, mode := range []string{"not-running", "foreign-owner", "remote-endpoint", "unproven-owner", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					return
				}
				gets.Add(1)
				if mode == "not-running" {
					w.WriteHeader(503)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, "http://192.0.2.1:9090/version", 302)
					return
				}
				fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
			}))
			defer server.Close()
			m, program, config := mihomoFixture(t, server)
			switch mode {
			case "foreign-owner":
				m.Releases.MihomoOwner = func(string, string) (bool, error) { return false, nil }
			case "remote-endpoint":
				if e := os.WriteFile(config, []byte("external-controller: 192.0.2.1:9090\nsecret: PRIVATE-CONTROLLER-TOKEN\n"), 0600); e != nil {
					t.Fatal(e)
				}
			case "unproven-owner":
				m.Releases.MihomoOwner = nil
			}
			before, _ := os.ReadFile(program)
			beforeConfig, _ := os.ReadFile(config)
			handled, e := m.updateMihomoOfficial()
			if handled || e != nil || posts.Load() != 0 {
				t.Fatal("unavailable or foreign controller was updated", handled, e, posts.Load())
			}
			if (mode == "foreign-owner" || mode == "remote-endpoint" || mode == "unproven-owner") && gets.Load() != 0 {
				t.Fatal("sent credentials before proving local ownership")
			}
			after, _ := os.ReadFile(program)
			afterConfig, _ := os.ReadFile(config)
			if string(before) != string(after) || string(beforeConfig) != string(afterConfig) {
				t.Fatal("fallback eligibility changed program or configuration")
			}
		})
	}
}

func TestMihomoAdapterRechecksOwnershipBeforePost(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
	}))
	defer server.Close()
	m, _, _ := mihomoFixture(t, server)
	checks := 0
	m.Releases.MihomoOwner = func(string, string) (bool, error) { checks++; return checks == 1, nil }
	handled, e := m.updateMihomoOfficial()
	if handled || e != nil || posts.Load() != 0 || checks != 2 {
		t.Fatal("controller ownership not rechecked", handled, e, posts.Load(), checks)
	}
}

func TestMihomoAdapterOwnershipRequiresTheManagedExecutablePath(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	program := filepath.Join(root, "managed-mihomo")
	if e = os.WriteFile(program, adapterNative("managed"), 0755); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(root, "outside-launch-path")
	if e = os.Link(program, alias); e != nil {
		t.Fatal(e)
	}
	if !sameMihomoExecutable(program, program) || sameMihomoExecutable(alias, program) {
		t.Fatal("an external hard link could direct the official updater outside the managed path")
	}
}
