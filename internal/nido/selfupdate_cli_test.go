package nido

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/selfupdate"
)

type updateCLITransport func(*http.Request) (*http.Response, error)

func (f updateCLITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateCLIHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func updateCLIBinaries(t *testing.T) map[string][]byte {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "nido"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/MiracleHYH/CDEToolkit\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("package main\nimport \"fmt\"\nvar version=\"dev\"\nfunc main(){fmt.Println(\"nido \"+version)}\n")
	if err := os.WriteFile(filepath.Join(dir, "cmd", "nido", "main.go"), source, 0600); err != nil {
		t.Fatal(err)
	}
	binaries := map[string][]byte{}
	for _, version := range []string{"0.5.0", "0.6.0", "9.9.9"} {
		path := filepath.Join(dir, "nido-"+version)
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-buildvcs=false", "-ldflags=-X main.version="+version, "-o", path, "./cmd/nido")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build isolated update fixture: %v: %s", err, out)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		binaries[version] = b
	}
	return binaries
}

func updateCLIArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "nido", Mode: 0755, Size: int64(len(binary)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, ModTime: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func updateCLIClient(t *testing.T, archive []byte, corrupt bool) (*http.Client, *[]string) {
	t.Helper()
	name := "nido-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	base := "https://github.com/MiracleHYH/CDEToolkit/releases/download/v0.6.0/"
	sidecar := []byte(updateCLIHash(archive) + "  " + name + "\n")
	metadata, _ := json.Marshal(map[string]any{"tag_name": "v0.6.0", "assets": []map[string]any{
		{"name": name, "browser_download_url": base + name, "size": len(archive), "digest": "sha256:" + updateCLIHash(archive)},
		{"name": name + ".sha256", "browser_download_url": base + name + ".sha256", "size": len(sidecar), "digest": "sha256:" + updateCLIHash(sidecar)},
	}})
	var calls []string
	return &http.Client{Transport: updateCLITransport(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		var data []byte
		switch req.URL.String() {
		case "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest", "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/tags/v0.6.0":
			data = metadata
		case base + name + ".sha256":
			data = sidecar
		case base + name:
			data = archive
			if corrupt {
				data = append(append([]byte(nil), archive...), 1)
			}
		default:
			t.Fatalf("unexpected request: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}, &calls
}

func TestSelfUpdateCLIWithIsolatedPublicRelease(t *testing.T) {
	binaries := updateCLIBinaries(t)
	for _, tc := range []struct {
		name                          string
		check, dry, explicit, corrupt bool
		current, candidate            string
		wantCalls                     int
		wantFailure                   bool
	}{
		{name: "check-metadata-only", check: true, current: "0.5.0", candidate: "0.6.0", wantCalls: 1},
		{name: "dry-run-offline", dry: true, current: "0.5.0", candidate: "0.6.0", wantCalls: 0},
		{name: "same-version-no-change", current: "0.6.0", candidate: "0.6.0", wantCalls: 1},
		{name: "pinned-same-version-no-download", explicit: true, current: "0.6.0", candidate: "0.6.0", wantCalls: 0},
		{name: "latest-update", current: "0.5.0", candidate: "0.6.0", wantCalls: 4},
		{name: "pinned-update", explicit: true, current: "0.5.0", candidate: "0.6.0", wantCalls: 3},
		{name: "corrupt-package", explicit: true, corrupt: true, current: "0.5.0", candidate: "0.6.0", wantCalls: 3, wantFailure: true},
		{name: "candidate-wrong-version", explicit: true, current: "0.5.0", candidate: "9.9.9", wantCalls: 3, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(m.Root, "bin", "nido")
			before := binaries[tc.current]
			if err := os.WriteFile(program, before, 0755); err != nil {
				t.Fatal(err)
			}
			m.Executable = program
			m.executableInfo, _ = os.Stat(program)
			priorVersion := Version
			Version = tc.current
			t.Cleanup(func() { Version = priorVersion })
			protected := map[string][]byte{}
			for _, path := range []string{filepath.Join(m.Root, "config.toml"), filepath.Join(m.Root, "env.sh"), filepath.Join(m.Root, "proxy", "config.yaml"), filepath.Join(m.Root, "gds", "state.json"), filepath.Join(home, ".codex", "auth.json"), filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", ".env"), filepath.Join(home, ".codex", "state.db"), filepath.Join(home, ".codex", "sessions", "one.jsonl"), filepath.Join(home, ".bashrc"), filepath.Join(home, ".zshrc")} {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					data = []byte("USER FIXTURE\n")
					err = os.WriteFile(path, data, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				protected[path] = data
			}
			client, calls := updateCLIClient(t, updateCLIArchive(t, binaries[tc.candidate]), tc.corrupt)
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, UpdateHTTP: client}
			o := cliOptions{check: tc.check, dry: tc.dry, json: true, yes: true, githubProxy: "direct"}
			if tc.explicit {
				o.version = "0.6.0"
			}
			code := c.update(m, o, nil)
			if (code != 0) != tc.wantFailure || len(*calls) != tc.wantCalls {
				t.Fatalf("code=%d calls=%v out=%q err=%q", code, *calls, out.String(), stderr.String())
			}
			if !tc.wantFailure && !json.Valid(out.Bytes()) {
				t.Fatal("CLI emitted invalid JSON", out.String())
			}
			want := before
			if !tc.wantFailure && !tc.check && !tc.dry && tc.current != "0.6.0" {
				want = binaries[tc.candidate]
			}
			if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, want) {
				t.Fatal("unexpected program after update", err)
			}
			for path, want := range protected {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
					t.Fatal("update changed protected data", path, err)
				}
			}
			if stages, _ := filepath.Glob(filepath.Join(filepath.Dir(program), ".nido-update-*")); len(stages) != 0 {
				t.Fatal("staged candidates leaked", stages)
			}
		})
	}
}

func TestSelfUpdateCLIRejectsInvalidOrInactiveEntryBeforeHTTP(t *testing.T) {
	binaries := updateCLIBinaries(t)
	for _, tc := range []struct {
		name     string
		options  cliOptions
		args     []string
		inactive bool
		current  string
	}{
		{name: "check-pinned", options: cliOptions{check: true, version: "0.6.0"}},
		{name: "non-stable-version", options: cliOptions{version: "v0.6.0"}},
		{name: "insecure-proxy", options: cliOptions{proxy: "file:///tmp/wrong"}},
		{name: "unknown-argument", args: []string{"other"}},
		{name: "inactive-entry", inactive: true},
		{name: "downgrade", options: cliOptions{version: "0.4.0"}},
		{name: "development-current", current: "dev", options: cliOptions{version: "0.6.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(m.Root, "bin", "nido")
			if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
				t.Fatal(err)
			}
			m.Executable = program
			m.executableInfo, _ = os.Stat(program)
			previousVersion := Version
			Version = tc.current
			if Version == "" {
				Version = "0.5.0"
			}
			t.Cleanup(func() { Version = previousVersion })
			if tc.inactive {
				m.Executable = filepath.Join(m.UserHome, "unrelated-nido")
				if err := os.WriteFile(m.Executable, binaries["0.5.0"], 0755); err != nil {
					t.Fatal(err)
				}
			}
			client := &http.Client{Transport: updateCLITransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid update attempted HTTP")
				return nil, fmt.Errorf("forbidden")
			})}
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, UpdateHTTP: client}
			o := tc.options
			o.githubProxy = "direct"
			o.dry = true
			o.json = true
			o.yes = true
			if code := c.update(m, o, tc.args); code == 0 {
				t.Fatal("accepted invalid self-update", out.String())
			}
			if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, binaries["0.5.0"]) {
				t.Fatal("invalid update changed entry", err)
			}
		})
	}
}

func TestSelfUpdateRefusesDirectoryAndEntryReplacementDuringFetch(t *testing.T) {
	for _, scenario := range []string{"bin-symlink", "bin-replaced", "entry-replaced", "entry-mode"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(m.Root, "bin")
			program := filepath.Join(bin, "nido")
			before, err := os.ReadFile(program)
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(home, "outside")
			if err := os.Mkdir(outside, 0700); err != nil {
				t.Fatal(err)
			}
			outsideProgram := filepath.Join(outside, "nido")
			if err := os.WriteFile(outsideProgram, before, 0755); err != nil {
				t.Fatal(err)
			}
			fetch := func() (selfupdate.Prepared, error) {
				switch scenario {
				case "bin-symlink", "bin-replaced":
					if err := os.Rename(bin, bin+"-old"); err != nil {
						t.Fatal(err)
					}
					if scenario == "bin-symlink" {
						if err := os.Symlink(outside, bin); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.Mkdir(bin, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(program, before, 0755); err != nil {
							t.Fatal(err)
						}
					}
				case "entry-replaced":
					staged := filepath.Join(bin, "user-replacement")
					if err := os.WriteFile(staged, before, 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(staged, program); err != nil {
						t.Fatal(err)
					}
				case "entry-mode":
					if err := os.Chmod(program, 0644); err != nil {
						t.Fatal(err)
					}
				}
				return selfupdate.Prepared{Data: []byte("INERT CANDIDATE"), Version: "0.6.0"}, nil
			}
			probe := func(string) (string, error) {
				t.Fatal("changed directory/entry reached candidate execution")
				return "", nil
			}
			if err := m.updateSelf(before, fetch, probe); err == nil {
				t.Fatal("accepted management directory or entry replacement")
			}
			if got, err := os.ReadFile(outsideProgram); err != nil || !bytes.Equal(got, before) {
				t.Fatal("update changed unrelated outside program", err)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
				t.Fatal("update wrote candidates outside its installation", entries, err)
			}
			if scenario == "entry-mode" {
				if info, err := os.Stat(program); err != nil || info.Mode().Perm() != 0644 {
					t.Fatal("update discarded concurrent permission change")
				}
			}
		})
	}
}

func TestSelfUpdateRetainsEntryIdentityAcrossConfirmation(t *testing.T) {
	binaries := updateCLIBinaries(t)
	for _, scenario := range []string{"entry-replaced", "bin-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(m.Root, "bin")
			program := filepath.Join(bin, "nido")
			if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
				t.Fatal(err)
			}
			m.Executable = program
			m.executableInfo, _ = os.Stat(program)
			before, err := m.selfTarget()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "bin-replaced" {
				if err := os.Rename(bin, bin+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(bin, 0700); err != nil {
					t.Fatal(err)
				}
			}
			staged := filepath.Join(bin, "user-entry")
			if err := os.WriteFile(staged, before, 0755); err != nil {
				t.Fatal(err)
			}
			userInfo, err := os.Stat(staged)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(staged, program); err != nil {
				t.Fatal(err)
			}
			called := false
			err = m.updateSelf(before, func() (selfupdate.Prepared, error) {
				called = true
				return selfupdate.Prepared{Data: binaries["0.6.0"], Version: "0.6.0"}, nil
			}, nil)
			if err == nil || called {
				t.Fatalf("accepted a replacement during confirmation: fetch=%v err=%v", called, err)
			}
			current, err := os.Stat(program)
			if err != nil || !os.SameFile(userInfo, current) {
				t.Fatal("update discarded the user's replacement", err)
			}
			if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, before) {
				t.Fatal("update changed a replacement entry", err)
			}
		})
	}
}

func TestSelfUpdateRefusesStartupEntryReplacementBeforeInspection(t *testing.T) {
	binaries := updateCLIBinaries(t)
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(m.Root, "bin", "nido")
	if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
		t.Fatal(err)
	}
	m.Executable = program
	m.executableInfo, _ = os.Stat(program)
	staged := filepath.Join(m.Root, "bin", "replacement")
	if err := os.WriteFile(staged, binaries["0.5.0"], 0755); err != nil {
		t.Fatal(err)
	}
	userInfo, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, program); err != nil {
		t.Fatal(err)
	}
	if _, err := m.selfTarget(); err == nil {
		t.Fatal("startup identity was replaced by a pathname stat")
	}
	current, err := os.Stat(program)
	if err != nil || !os.SameFile(current, userInfo) {
		t.Fatal("rejected startup replacement was modified", err)
	}
}

func TestSelfUpdateCleanupPreservesReplacedCandidateAndDirectory(t *testing.T) {
	for _, scenario := range []string{"candidate-replaced", "bin-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(m.Root, "bin")
			program := filepath.Join(bin, "nido")
			before, err := os.ReadFile(program)
			if err != nil {
				t.Fatal(err)
			}
			var replaced string
			userData := []byte("USER-REPLACEMENT-MUST-REMAIN\n")
			probe := func(candidate string) (string, error) {
				replaced = candidate
				if scenario == "bin-replaced" {
					if err := os.Rename(bin, bin+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(bin, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(program, before, 0755); err != nil {
						t.Fatal(err)
					}
				}
				staged := filepath.Join(bin, "user-candidate")
				if err := os.WriteFile(staged, userData, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(staged, candidate); err != nil {
					t.Fatal(err)
				}
				return "nido 0.6.0", nil
			}
			err = m.updateSelf(before, func() (selfupdate.Prepared, error) {
				return selfupdate.Prepared{Data: []byte("INERT CANDIDATE"), Version: "0.6.0"}, nil
			}, probe)
			if err == nil {
				t.Fatal("accepted a replaced candidate or directory")
			}
			if got, err := os.ReadFile(replaced); err != nil || !bytes.Equal(got, userData) {
				t.Fatal("failure cleanup removed somebody else's file", err)
			}
			if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, before) {
				t.Fatal("failed update changed the entry", err)
			}
		})
	}
}

func TestSelfUpdateCLIPinsConfirmedReleaseVersion(t *testing.T) {
	binaries := updateCLIBinaries(t)
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprintf("tag-drift-%v", drift), func(t *testing.T) {
			m, _ := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(m.Root, "bin", "nido")
			if err := os.WriteFile(program, binaries["0.5.0"], 0755); err != nil {
				t.Fatal(err)
			}
			m.Executable = program
			m.executableInfo, _ = os.Stat(program)
			previous := Version
			Version = "0.5.0"
			t.Cleanup(func() { Version = previous })
			configBefore, err := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			client, calls := updateCLIClient(t, updateCLIArchive(t, binaries["0.6.0"]), false)
			base := client.Transport
			client.Transport = updateCLITransport(func(req *http.Request) (*http.Response, error) {
				resp, err := base.RoundTrip(req)
				if err != nil {
					return nil, err
				}
				if drift && strings.HasSuffix(req.URL.Path, "/tags/v0.6.0") {
					data, err := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					var metadata map[string]any
					if err := json.Unmarshal(data, &metadata); err != nil {
						t.Fatal(err)
					}
					metadata["tag_name"] = "v0.7.0"
					data, err = json.Marshal(metadata)
					if err != nil {
						t.Fatal(err)
					}
					resp.Body = io.NopCloser(bytes.NewReader(data))
					resp.ContentLength = int64(len(data))
				}
				return resp, nil
			})
			var out, stderr bytes.Buffer
			c := CLI{Out: &out, Err: &stderr, UpdateHTTP: client}
			code := c.update(m, cliOptions{yes: true, json: true, githubProxy: "direct"}, nil)
			if (code != 0) != drift {
				t.Fatalf("unexpected result %d %s %s", code, out.String(), stderr.String())
			}
			latestCalls := 0
			for _, url := range *calls {
				if strings.HasSuffix(url, "/latest") {
					latestCalls++
				}
			}
			if latestCalls != 1 || len(*calls) < 2 || !strings.HasSuffix((*calls)[1], "/tags/v0.6.0") {
				t.Fatal("update did not pin the confirmed release", *calls)
			}
			want := binaries["0.6.0"]
			if drift {
				want = binaries["0.5.0"]
				if len(*calls) != 2 {
					t.Fatal("tag drift fetched an asset", *calls)
				}
			}
			if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, want) {
				t.Fatal("wrong installed program after tag confirmation", err)
			}
			if got, err := os.ReadFile(filepath.Join(m.Root, "config.toml")); err != nil || !bytes.Equal(got, configBefore) {
				t.Fatal("tag confirmation modified configuration", err)
			}
		})
	}
}

func TestCandidateVersionProbeIsBoundedAndEnvironmentIsolated(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "candidate")
	script := "#!/bin/sh\n[ -z \"${HOME+x}${HTTP_PROXY+x}${NIDO_HOME+x}${CODEX_HOME+x}\" ] || exit 95\nprintf 'nido 0.6.0\\n'\n"
	if err := os.WriteFile(program, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if got, err := candidateVersionWithTimeout(program, time.Second); err != nil || got != "nido 0.6.0" {
		t.Fatalf("probe=%q err=%v", got, err)
	}
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%0130d' 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := candidateVersionWithTimeout(program, time.Second); err == nil {
		t.Fatal("accepted oversized version output")
	}
	marker := filepath.Join(dir, "child-survived")
	script = "#!/bin/sh\n(sleep 0.4; printf leaked > " + shellQuote(marker) + ") &\nsleep 10\n"
	if err := os.WriteFile(program, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := candidateVersionWithTimeout(program, 100*time.Millisecond); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("probe timeout was not bounded: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("candidate descendant survived its process group timeout")
	}
	if stages, _ := filepath.Glob(filepath.Join(dir, ".nido-update-*")); len(stages) != 0 {
		t.Fatal("probe created unexpected installation files")
	}
}
