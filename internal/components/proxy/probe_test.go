package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProbeUsesSelectedProxyAndDoesNotFollowRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !r.URL.IsAbs() || r.Host != "target.invalid" {
			t.Errorf("request did not use HTTP proxy: %s %s", r.URL, r.Host)
		}
		w.Header().Set("Location", "https://must-not-be-contacted.invalid/")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	runner := &runtimeFakeRunner{ready: true}
	service := runtimeService(t, runner)
	path, _ := service.ConfigPath()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("mixed-port: %s\n", u.Port())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_PROXY", "http://must-not-be-used.invalid:1")
	result, err := service.Probe("", "http://target.invalid/path?token=not-returned")
	if err != nil || result.StatusCode != 302 || requests.Load() != 1 {
		t.Fatalf("probe=%+v err=%v requests=%d", result, err, requests.Load())
	}
	if strings.Contains(fmt.Sprint(result), "not-returned") {
		t.Fatal("probe result exposed target query")
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe("", "http://target.invalid/"); err == nil || requests.Load() != 1 {
		t.Fatal("stopped proxy was probed")
	}
}

func TestProbeValidatesTargetAndRuntimeBeforeNetwork(t *testing.T) {
	for _, target := range []string{"https://host.invalid:99999/", "http://host.invalid:0/", "https://host.invalid:/", "file:///tmp/data", "https://user:password@host.invalid/", "https://host.invalid/#secret", "https://host.invalid/\n"} {
		if err := CheckProbeURL(target); err == nil {
			t.Fatalf("invalid target accepted: %q", target)
		}
	}
	runner := &runtimeFakeRunner{ready: true}
	service := runtimeService(t, runner)
	path, _ := service.ConfigPath()
	if err := os.WriteFile(path, []byte("mixed-port: 17891\nauthentication: ['user:password']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe("", "https://target.invalid/"); err == nil || strings.Contains(err.Error(), "password") {
		t.Fatalf("auth probe: %v", err)
	}
	if err := os.WriteFile(path, []byte("mixed-port: 17891\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mixed-port: 17892\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe("", "https://target.invalid/"); err == nil {
		t.Fatal("changed endpoint probed")
	}
}

func TestProbeRejectsRecordReplacementBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	runner := &runtimeFakeRunner{ready: true}
	service := runtimeService(t, runner)
	configPath, _ := service.ConfigPath()
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("mixed-port: %s\n", u.Port())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	path, _ := service.recordPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replace only the private fixture's record while the readiness observation
	// is in progress. Identical bytes still belong to a different file capture.
	runner.onOwns = func() {
		if err := atomicWrite(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Probe("", "http://target.invalid/"); err == nil || requests.Load() != 0 {
		t.Fatalf("record replacement was probed: err=%v requests=%d", err, requests.Load())
	}
}

func TestProbeRejectsInstanceChangesDuringRequest(t *testing.T) {
	for _, change := range []string{"record", "configuration", "program", "listener ownership"} {
		t.Run(change, func(t *testing.T) {
			var requests atomic.Int32
			var lostOwnership atomic.Bool
			var mutate func() error
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if err := mutate(); err != nil {
					t.Errorf("private fixture mutation: %v", err)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			runner := &runtimeFakeRunner{ready: true}
			service := runtimeService(t, runner)
			configPath, _ := service.ConfigPath()
			config := []byte(fmt.Sprintf("mixed-port: %s\n", u.Port()))
			if err := os.WriteFile(configPath, config, 0600); err != nil {
				t.Fatal(err)
			}
			if err := service.Start(); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "record":
				path, _ := service.recordPath()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				mutate = func() error { return atomicWrite(path, data, 0600) }
			case "configuration":
				mutate = func() error { return os.WriteFile(configPath, append(config, []byte("mode: direct\n")...), 0600) }
			case "program":
				_, program, err := service.paths()
				if err != nil {
					t.Fatal(err)
				}
				mutate = func() error { return os.WriteFile(program, []byte("changed private fixture program"), 0700) }
			case "listener ownership":
				runner.onOwns = func() { runner.ready = !lostOwnership.Load() }
				mutate = func() error { lostOwnership.Store(true); return nil }
			}
			if _, err := service.Probe("", "http://target.invalid/"); err == nil || requests.Load() != 1 {
				t.Fatalf("changed instance reported success: err=%v requests=%d", err, requests.Load())
			}
		})
	}
}
