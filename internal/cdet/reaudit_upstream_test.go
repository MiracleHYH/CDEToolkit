package cdet

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestReauditCommandSeparatesStdoutFromDiagnosticStderr(t *testing.T) {
	out, err := runCommand(CommandSpec{Path: "/bin/sh", Args: []string{"-s"}, Env: os.Environ(), Input: []byte("printf '#!/bin/sh\\n# DOWNLOADED-SCRIPT\\n'\nprintf 'DOWNLOAD-WARNING\\n' >&2\n")})
	if err != nil || string(out) != "#!/bin/sh\n# DOWNLOADED-SCRIPT\n" {
		t.Fatal("stderr became executable installer input", string(out), err)
	}
}

func TestReauditCodexInstallerRejectsExternalWriteParents(t *testing.T) {
	for _, relative := range []string{".cdet/bin", "codex/packages", "codex/packages/standalone", "codex/packages/standalone/releases"} {
		t.Run(relative, func(t *testing.T) {
			m := networkTestManager(t)
			external := t.TempDir()
			target := filepath.Join(m.Store, relative)
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, target); err != nil {
				t.Fatal(err)
			}
			calls := 0
			m.Runner = func(CommandSpec) ([]byte, error) { calls++; return []byte("# FAKE-INSTALLER"), nil }
			if err := m.installCodex(); err == nil || calls != 0 {
				t.Fatal("upstream installer could write through an external managed path", calls, err)
			}
			if entries, err := os.ReadDir(external); err != nil || len(entries) != 0 {
				t.Fatal("external directory was modified", err)
			}
		})
	}
}

func TestReauditCodexInstallerRejectsAnOrphanedHelperEntry(t *testing.T) {
	for _, kind := range []string{"directory", "file", "link"} {
		t.Run(kind, func(t *testing.T) {
			m := networkTestManager(t)
			helper := filepath.Join(m.Store, ".cdet", "bin", "codex-code-mode-host")
			if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(helper, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				write(t, helper, "UNKNOWN-HELPER-SENTINEL", 0755)
			case "link":
				if err := os.Symlink(t.TempDir(), helper); err != nil {
					t.Fatal(err)
				}
			}
			before, err := fingerprint(helper)
			if err != nil {
				t.Fatal(err)
			}
			m.Runner = func(CommandSpec) ([]byte, error) {
				t.Fatal("orphaned helper reached upstream installer")
				return nil, nil
			}
			if err := m.installCodex(); err == nil {
				t.Fatal("an orphaned helper could be overwritten")
			}
			after, err := fingerprint(helper)
			if err != nil || after != before {
				t.Fatal("unrecognized helper was changed", err)
			}
		})
	}
}

func TestReauditCodexInstallerIsolatesInheritedCurlConfiguration(t *testing.T) {
	m := networkTestManager(t)
	userConfig := t.TempDir()
	t.Setenv("CURL_HOME", userConfig)
	t.Setenv("XDG_CONFIG_HOME", userConfig)
	write(t, filepath.Join(userConfig, ".curlrc"), "USER-CONFIG-SENTINEL", 0600)
	calls := 0
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		calls++
		home := envValue(spec.Env, "HOME")
		if envValue(spec.Env, "CURL_HOME") != home || envValue(spec.Env, "XDG_CONFIG_HOME") != home || home == userConfig {
			t.Fatal("installer download inherited a user's curl configuration")
		}
		if spec.Path == "curl" && (len(spec.Args) == 0 || spec.Args[0] != "-q" || !strings.Contains(strings.Join(spec.Args, " "), "--proto-redir =https")) {
			t.Fatal("installer fetch permits curlrc side effects or an HTTP redirect")
		}
		return []byte("# FAKE-INSTALLER"), nil
	}
	if err := m.installCodex(); err != nil || calls != 2 || read(t, filepath.Join(userConfig, ".curlrc")) != "USER-CONFIG-SENTINEL" {
		t.Fatal("isolated installer failed or changed user curl config", err)
	}
}

func TestReauditCodexInstallerRechecksHomeAndMountAfterDownload(t *testing.T) {
	for _, changed := range []string{"missing-home", "linked-home", "missing-store"} {
		t.Run(changed, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			m := f.manager(t, Options{})
			home := filepath.Join(m.Store, "codex")
			write(t, filepath.Join(home, "auth.json"), "AUTH-SENTINEL", 0600)
			shellCalls := 0
			m.Runner = func(spec CommandSpec) ([]byte, error) {
				if spec.Path == "/bin/sh" {
					shellCalls++
					return nil, nil
				}
				if spec.Path != "curl" {
					t.Fatal("unexpected command")
				}
				switch changed {
				case "missing-home", "linked-home":
					if err := os.Rename(home, home+"-preserved"); err != nil {
						t.Fatal(err)
					}
					if changed == "linked-home" {
						if err := os.Symlink(t.TempDir(), home); err != nil {
							t.Fatal(err)
						}
					}
				case "missing-store":
					if err := os.Rename(m.Store, m.Store+"-preserved"); err != nil {
						t.Fatal(err)
					}
				}
				return []byte("# FAKE-INSTALLER"), nil
			}
			if err := m.installCodex(); err == nil || shellCalls != 0 {
				t.Fatal("an installer ran after its data root or mount vanished", shellCalls, err)
			}
			preserved := filepath.Join(home+"-preserved", "auth.json")
			if changed == "missing-store" {
				preserved = filepath.Join(m.Store+"-preserved", "codex", "auth.json")
			}
			if read(t, preserved) != "AUTH-SENTINEL" {
				t.Fatal("original data changed")
			}
		})
	}
}

func TestReauditCurrentVersionDoesNotDependOnNetworkConfiguration(t *testing.T) {
	m := networkTestManager(t)
	write(t, filepath.Join(m.Store, "proxy", "config.yaml"), "mixed-port: [invalid yaml", 0600)
	for _, key := range []string{"CODEX_MANAGED_PACKAGE_ROOT", "CODEX_MANAGED_BY_NPM", "CODEX_MANAGED_BY_BUN", "CODEX_MANAGED_BY_PNPM", "CODEX_MANAGED_BY_VITE_PLUS"} {
		t.Setenv(key, "STALE-EXTERNAL-PACKAGE")
	}
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path != filepath.Join(m.Store, programPath("codex")) {
			t.Fatal("local version probe ran another program")
		}
		for _, entry := range spec.Env {
			if strings.Contains(entry, "STALE-EXTERNAL-PACKAGE") {
				t.Fatal("local version probe inherited a parent installation's package root")
			}
		}
		return []byte("codex-cli 0.130.0\n"), nil
	}
	version, err := m.softwareVersion("codex")
	if err != nil || version != "0.130.0" {
		t.Fatal("invalid network settings hid a local software version", version, err)
	}
}

func TestReauditMihomoControllerDoesNotDependOnOutboundProxyConfiguration(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
	}))
	defer server.Close()
	m, _, config := mihomoFixture(t, server)
	write(t, config, read(t, config)+"mixed-port: 70000\n", 0600)
	handled, err := m.updateMihomoOfficial()
	if !handled || err != nil || posts.Load() != 1 {
		t.Fatal("a direct local controller depended on outbound proxy setup", handled, posts.Load(), err)
	}
}

func TestReauditUpdateDoesNotEnableOnlineRestartAfterConfirmation(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	m := f.manager(t, Options{})
	program := filepath.Join(m.Store, programPath("proxy"))
	write(t, program, string(fakeNative("managed-proxy")), 0755)
	var owned bool
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		gets.Add(1)
		fmt.Fprint(w, `{"meta":true,"version":"v1.2.3"}`)
	}))
	defer server.Close()
	write(t, filepath.Join(m.Store, "proxy", "config.yaml"), "external-controller: '"+strings.TrimPrefix(server.URL, "http://")+"'\n", 0600)
	m.State.Components["proxy"] = Component{Installed: true, Backend: "mihomo", Origin: "installed", Digest: digest(fakeNative("managed-proxy"))}
	if err := m.saveState(); err != nil {
		t.Fatal(err)
	}
	m.Releases = newReleaseClient()
	m.Releases.API = "https://metadata.invalid"
	m.Releases.MihomoOwner = func(string, string) (bool, error) { return owned, nil }
	m.Releases.HTTP = &http.Client{Transport: networkRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.String(), server.URL+"/") {
			return http.DefaultTransport.RoundTrip(r)
		}
		if r.Method != http.MethodGet || r.URL.Host != "metadata.invalid" || !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Fatal("unexpected external request", r.Method, r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.3"}`))}, nil
	})}
	p, err := m.Update([]string{"proxy"})
	if err != nil || len(p.Warnings) != 0 || gets.Load() != 0 || posts.Load() != 0 {
		t.Fatal("offline preview performed an online controller operation", err)
	}
	// The managed component starts while the user reviews the offline preview.
	// A shared lease supplies a busy sentinel without starting any real service.
	owned = true
	lease, err := m.componentLock("proxy", syscall.LOCK_SH)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = m.Apply(p); err == nil || !strings.Contains(err.Error(), "正在运行") || posts.Load() != 0 || gets.Load() != 0 {
		t.Fatal("an offline-confirmed update unexpectedly POSTed an online restart", gets.Load(), posts.Load(), err)
	}
	// An explicit artifact is an offline choice even when a controller exists.
	m.Options.Artifacts = map[string]string{"proxy": filepath.Join(f.root, "explicit-artifact")}
	p, err = m.Update([]string{"proxy"})
	if err != nil || len(p.Warnings) != 0 || gets.Load() != 0 || posts.Load() != 0 {
		t.Fatal("explicit artifact preview selected an online restart", gets.Load(), posts.Load(), err)
	}
}
