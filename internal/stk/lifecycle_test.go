package stk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type upstream struct {
	client                                                  *ReleaseClient
	version                                                 string
	requests                                                map[string]int
	corrupt, missingDigest, prerelease, duplicate, wrongURL bool
	status                                                  int
}

// The payload is a minimal, non-running ELF fixture; no downloaded code is
// executed. Both API and asset requests terminate at the TLS test server.
func fakeELF(version string) []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(b[16:], 2)
	binary.LittleEndian.PutUint16(b[18:], 62)
	binary.LittleEndian.PutUint32(b[20:], 1)
	binary.LittleEndian.PutUint16(b[52:], 64)
	return append(b, version...)
}

func packed(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if name == "" {
		_, _ = gz.Write(data)
	} else {
		tw := tar.NewWriter(gz)
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0755}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(data)
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{version: "1.2.3", requests: map[string]int{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.requests[r.URL.Path]++
		if u.status != 0 {
			w.WriteHeader(u.status)
			return
		}
		name, repo := "codex", "openai/codex"
		if strings.Contains(r.URL.Path, "MetaCubeX") {
			name, repo = "proxy", "MetaCubeX/mihomo"
		}
		tag, _ := releaseTag(name, u.version)
		names, _ := assetNames(name, tag, "linux", "amd64")
		member := ""
		if name == "codex" {
			member = strings.TrimSuffix(names[0], ".tar.gz")
		}
		payload := packed(t, member, fakeELF(name+u.version))
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			hash := "sha256:" + digest(payload)
			if u.missingDigest {
				hash = ""
			}
			download := "https://github.com/" + repo + "/releases/download/" + tag + "/" + names[0]
			if u.wrongURL {
				download = "https://github.com/unrelated/project/releases/download/" + tag + "/" + names[0]
			}
			a := map[string]any{"name": names[0], "browser_download_url": download, "digest": hash, "size": len(payload)}
			assets := []any{a}
			if u.duplicate {
				assets = append(assets, a)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "prerelease": u.prerelease, "assets": assets})
			return
		}
		if u.corrupt {
			payload = append(payload, 'x')
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	transport := server.Client().Transport
	u.client = &ReleaseClient{OS: "linux", Arch: "amd64", API: "https://api.github.com", HTTP: &http.Client{Timeout: time.Second * 10, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		copyURL := *r.URL
		clone.URL = &copyURL
		clone.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return transport.RoundTrip(clone)
	})}}
	return u
}

func assertPreserved(t *testing.T, f fixture, proxy, config string) {
	t.Helper()
	for name, want := range map[string]string{"auth.json": "AUTH-SENTINEL", "sessions/session.jsonl": "SESSION-SENTINEL", "state.sqlite": "DATABASE-SENTINEL", "config.toml": "model = 'preserved'\n"} {
		if read(t, filepath.Join(f.codex, name)) != want {
			t.Fatal("user data changed", name)
		}
	}
	if read(t, f.config) != config || read(t, f.proxy) != proxy {
		t.Fatal("external component changed")
	}
}

func TestOnlineInstallPreviewUpdateAndIdempotency(t *testing.T) {
	f := newFixture(t)
	u := newUpstream(t)
	fresh := filepath.Join(f.local, "fresh-codex")
	opts := Options{Artifacts: map[string]string{"codex": "latest", "proxy": "latest"}, Binaries: map[string]string{}, CodexHome: fresh, DryRun: true}
	m := f.managerFor(t, opts)
	m.Releases = u.client
	p, err := m.Install([]string{"proxy", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Deferred || len(p.Downloads) != 2 {
		t.Fatal("preview did not resolve releases")
	}
	if err = Apply(p); err == nil {
		t.Fatal("deferred plan applied")
	}
	if _, err = os.Lstat(f.home); !os.IsNotExist(err) {
		t.Fatal("preview wrote files")
	}
	for path := range u.requests {
		if !strings.HasPrefix(path, "/repos/") {
			t.Fatal("preview downloaded a binary")
		}
	}
	opts.DryRun = false
	m = f.managerFor(t, opts)
	m.Releases = u.client
	p, err = m.Install([]string{"proxy", "codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{Binaries: map[string]string{}, CodexHome: fresh})
	m.Releases = u.client
	for _, name := range []string{"proxy", "codex"} {
		mod := m.State.Modules[name]
		if !mod.OwnedBinary || mod.Release == nil || mod.Release.Version == "" {
			t.Fatal("missing provenance")
		}
	}
	before := len(u.requests)
	p, err = m.Update([]string{"proxy", "codex"})
	if err != nil || len(p.Changes) != 0 || len(u.requests) != before {
		t.Fatal("same release was not idempotent", err)
	}
	for _, d := range p.Downloads {
		if d.Needed {
			t.Fatal("same release was downloaded")
		}
	}
	u.version = "1.2.4"
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{Binaries: map[string]string{}, CodexHome: fresh})
	m.Releases = u.client
	codex, _ := m.resolve(m.State.Modules["codex"].Binary)
	if !bytes.Equal([]byte(read(t, codex)), fakeELF("codex1.2.4")) {
		t.Fatal("update did not replace payload")
	}
	if m.State.Modules["proxy"].Release.Version != "v1.2.3" {
		t.Fatal("updated an unselected module")
	}
	// The recorded relative paths remain repairable after the fast mount moves.
	newFast := filepath.Join(f.base, "fast-new")
	if err = os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Home: filepath.Join(newFast, ".local", "stk")})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Repair()
	apply(t, p, err)
	codex, _ = m.resolve(m.State.Modules["codex"].Binary)
	if !strings.HasPrefix(codex, newFast) || !executable(codex) {
		t.Fatal("updated install did not relocate")
	}
}

func TestReleaseValidationAndCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*upstream)
	}{
		{"digest", func(u *upstream) { u.missingDigest = true }},
		{"prerelease", func(u *upstream) { u.prerelease = true }},
		{"duplicate", func(u *upstream) { u.duplicate = true }},
		{"repository", func(u *upstream) { u.wrongURL = true }},
		{"network", func(u *upstream) { u.status = 503 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := newUpstream(t)
			tc.change(u)
			if _, err := u.client.resolve("codex", "latest", ""); err == nil {
				t.Fatal("invalid release accepted")
			}
		})
	}
	u := newUpstream(t)
	if _, err := u.client.resolve("codex", "1.2.2", ""); err == nil {
		t.Fatal("wrong pinned version accepted")
	}
	if _, err := u.client.resolve("codex", "latest", strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong supplied digest accepted")
	}
	if _, err := u.client.resolve("codex", "../../escape", ""); err == nil {
		t.Fatal("bad version accepted")
	}
	names, err := assetNames("proxy", "v1.2.3", "linux", "amd64")
	if err != nil || strings.Join(names, ",") != "mihomo-linux-amd64-v1-v1.2.3.gz,mihomo-linux-amd64-compatible-v1.2.3.gz" {
		t.Fatal(names, err)
	}
	if _, err = assetNames("codex", "rust-v1.2.3", "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	if _, err = assetNames("codex", "rust-v1.2.3", "linux", "386"); err == nil {
		t.Fatal("unsupported arch accepted")
	}
	if err = verifyPlatform(fakeELF("x"), "linux/arm64"); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	if _, err = secureURL("https://user:secret@example.invalid/a"); err == nil {
		t.Fatal("credential URL accepted")
	}
}

// Reduced, public upstream metadata snapshots verify actual asset names
// without making the test depend on a live release or downloading programs.
func TestPublishedReleaseMetadataSnapshots(t *testing.T) {
	for _, name := range []string{"proxy", "codex"} {
		data, err := os.ReadFile(filepath.Join("testdata", "releases", name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, goos := range []string{"linux", "darwin"} {
			for _, arch := range []string{"amd64", "arm64"} {
				client := &ReleaseClient{OS: goos, Arch: arch, API: "https://api.github.com", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
				})}}
				a, err := client.resolve(name, "latest", "")
				if err != nil || !validDigest(a.SHA256) {
					t.Fatal(name, goos, arch, err)
				}
				if name == "proxy" && arch == "amd64" && !strings.Contains(a.Asset, "-amd64-v1-") {
					t.Fatal("selected a CPU-specific default", a.Asset)
				}
			}
		}
	}
}

func TestInstallAdoptsWithoutNetworkAndGDSRemainsOptional(t *testing.T) {
	f := newFixture(t)
	u := newUpstream(t)
	m := f.managerFor(t, Options{})
	m.Releases = u.client
	p, err := m.Install(nil)
	apply(t, p, err)
	if len(u.requests) != 0 {
		t.Fatal("adoption contacted network")
	}
	m = f.managerFor(t, Options{Binaries: map[string]string{}})
	m.Releases = u.client
	if _, err = m.Install([]string{"gds"}); err == nil {
		t.Fatal("GDS implicitly installed")
	}
	if _, ok := m.State.Modules["gds"]; ok {
		t.Fatal("GDS enabled")
	}
}

func TestExternalUpdateTakeoverAndSafeUninstall(t *testing.T) {
	f := newFixture(t)
	beforeProxy, beforeConfig := read(t, f.proxy), read(t, f.config)
	rc := filepath.Join(f.base, "bashrc")
	userContent := "# own content\n"
	write(t, rc, userContent, 0644)
	m := f.managerFor(t, Options{ShellFiles: []string{rc}})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	if _, err = m.Update([]string{"codex"}); err == nil {
		t.Fatal("external binary update accepted")
	}
	artifact := filepath.Join(f.base, "codex-new")
	write(t, artifact, "#!/bin/sh\nprintf new\n", 0755)
	m = f.managerFor(t, Options{Takeover: true, Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	assertPreserved(t, f, beforeProxy, beforeConfig)
	write(t, filepath.Join(f.home, "run", "proxy", "cache.db"), "CACHE-SENTINEL", 0600)
	write(t, filepath.Join(f.home, "tools", "codex", "user-note"), "KEEP-NOTE", 0600)
	// Later edits outside our Shell block must survive removal.
	write(t, rc, read(t, rc)+"# later edit\n", 0644)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"codex"})
	apply(t, p, err)
	if executable(filepath.Join(f.home, "tools", "codex", "codex")) || regular(filepath.Join(f.home, "bin", "codex")) {
		t.Fatal("owned program remained")
	}
	if !regular(filepath.Join(f.home, "bin", "mihomo")) {
		t.Fatal("unselected module removed")
	}
	if strings.Contains(read(t, filepath.Join(f.home, "env.sh")), "CODEX_HOME=") {
		t.Fatal("stale Codex environment")
	}
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"stk"})
	apply(t, p, err)
	assertPreserved(t, f, beforeProxy, beforeConfig)
	if read(t, rc) != userContent+"# later edit\n" {
		t.Fatal("user shell content changed")
	}
	if read(t, filepath.Join(f.home, "run", "proxy", "cache.db")) != "CACHE-SENTINEL" || read(t, filepath.Join(f.home, "tools", "codex", "user-note")) != "KEEP-NOTE" {
		t.Fatal("user files removed")
	}
	for _, rel := range []string{"stk", "env.sh", "bin/mihomo", "services/proxy.service"} {
		if regular(filepath.Join(f.home, rel)) {
			t.Fatal("generated file remained", rel)
		}
	}
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"stk"})
	if err != nil || len(p.Changes) != 0 {
		t.Fatal("uninstall is not idempotent", err)
	}
}

func TestUpdateFailuresAndUninstallDriftPreserveFiles(t *testing.T) {
	f := newFixture(t)
	u := newUpstream(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"proxy": "latest"}, Binaries: map[string]string{}})
	m.Releases = u.client
	p, err := m.Setup([]string{"proxy"})
	apply(t, p, err)
	owned := filepath.Join(f.home, "tools", "proxy", "mihomo")
	before := read(t, owned)
	state := read(t, filepath.Join(f.home, "state.json"))
	m = f.managerFor(t, Options{})
	m.Releases = u.client
	u.version = "1.2.4"
	u.corrupt = true
	if _, err = m.Update([]string{"proxy"}); err == nil {
		t.Fatal("corrupt payload accepted")
	}
	if read(t, owned) != before || read(t, filepath.Join(f.home, "state.json")) != state {
		t.Fatal("failed download changed installation")
	}
	p, err = m.Uninstall([]string{"proxy"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, owned, "edited by owner", 0755)
	if err = Apply(p); err == nil {
		t.Fatal("concurrent drift accepted")
	}
	if read(t, owned) != "edited by owner" {
		t.Fatal("user edit removed")
	}
	m = f.managerFor(t, Options{})
	if _, err = m.Uninstall([]string{"proxy"}); err == nil {
		t.Fatal("modified owned file removed")
	}
}

func TestRemovalRollbackAndMetadataRace(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	// Failure after the removal must restore both the launcher and metadata.
	p.Changes = append(p.Changes, Change{Path: filepath.Join(f.home, "invalid"), Kind: "invalid", Before: Fingerprint{Kind: "absent"}, After: Fingerprint{Kind: "absent"}})
	if err = Apply(p); err == nil {
		t.Fatal("failed operation succeeded")
	}
	if !executable(filepath.Join(f.home, "bin", "codex")) {
		t.Fatal("removed launcher not restored")
	}
	m = f.managerFor(t, Options{})
	// An edit after New but before planning is also a conflict, not just edits
	// between planning and Apply.
	write(t, filepath.Join(f.home, "config.toml"), read(t, filepath.Join(f.home, "config.toml"))+"\n# concurrent owner edit\n", 0600)
	p, err = m.Uninstall([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("stale manager overwrote a newer configuration")
	}
	if !strings.Contains(read(t, filepath.Join(f.home, "config.toml")), "concurrent owner edit") {
		t.Fatal("lost owner edit")
	}
}

func TestSelfUpdateUsesSamePipelineAndRepairDoesNotDowngrade(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	newManager := filepath.Join(f.base, "new-manager")
	write(t, newManager, "#!/bin/sh\nprintf stk-new\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"stk": newManager}})
	p, err = m.Update([]string{"stk"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Repair()
	apply(t, p, err)
	if read(t, filepath.Join(f.home, "stk")) != read(t, newManager) {
		t.Fatal("repair downgraded manager")
	}
	if _, err = m.Update([]string{"stk"}); err == nil {
		t.Fatal("self update guessed an unconfigured repository")
	}
}

func TestArtifactSelectionAndRedirectFailures(t *testing.T) {
	f := newFixture(t)
	c := newReleaseClient()
	wrong := filepath.Join(f.base, "wrong.tar.gz")
	write(t, wrong, string(packed(t, "codex", []byte("fake"))), 0600)
	if _, err := c.artifactBytes(Artifact{Source: wrong}, "proxy"); err == nil {
		t.Fatal("installed a different module from tar")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/insecure", http.StatusFound)
	}))
	defer server.Close()
	c.HTTP = server.Client()
	if _, err := c.get(server.URL, 100); err == nil {
		t.Fatal("insecure redirect accepted")
	}
	truncated := filepath.Join(f.base, "truncated.gz")
	data := packed(t, "", []byte("binary"))
	write(t, truncated, string(data[:len(data)-3]), 0600)
	if _, err := c.artifactBytes(Artifact{Source: truncated}, "proxy"); err == nil {
		t.Fatal("truncated gzip accepted")
	}
}

func TestLifecycleCLIRejectsIgnoredOptions(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"update", "codex", "--codex-home", f.codex},
		{"uninstall", "codex", "--manage-config"},
		{"setup", "codex", "--takeover"},
		{"install", "codex", "--install", "proxy=latest"},
		{"update", "codex", "--install", "codex=latest", "--binary", "codex=" + f.binary},
		{"uninstall", "codex", "codex"},
	} {
		var out, errors bytes.Buffer
		code := (CLI{Out: &out, Err: &errors}).Run(append([]string{"--home", f.home}, args...))
		if code == 0 {
			t.Fatal("ignored dangerous option", fmt.Sprint(args))
		}
	}
	if _, err := os.Lstat(f.home); !os.IsNotExist(err) {
		t.Fatal("invalid command wrote files")
	}
}

func TestLifecycleNeverInvokesRegisteredServiceController(t *testing.T) {
	f := newFixture(t)
	controller := filepath.Join(f.base, "fake-controller")
	log := filepath.Join(f.base, "unexpected-service-call")
	write(t, controller, "#!/bin/sh\nprintf called >> "+quote(log)+"\n", 0755)
	artifact := filepath.Join(f.base, "mihomo-one")
	write(t, artifact, "#!/bin/sh\nprintf one\n", 0755)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"proxy": artifact}, Services: map[string]Service{"proxy": {Backend: "supervisor", Name: "existing-proxy", Controller: Ref{Path: controller}}}})
	p, err := m.Setup([]string{"proxy"})
	apply(t, p, err)
	write(t, artifact, "#!/bin/sh\nprintf two\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"proxy": artifact}})
	p, err = m.Update([]string{"proxy"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"stk"})
	apply(t, p, err)
	if _, err = os.Lstat(log); !os.IsNotExist(err) {
		t.Fatal("lifecycle invoked service controller")
	}
}

func TestInterruptedRemovalRecovery(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	j := testJournal(t, f.home, m.State.Instance, p.Changes)
	data, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(data), 0600)
	removed := ""
	for _, c := range p.Changes {
		if c.Kind == "remove" {
			removed = c.Path
			if err = os.Remove(c.Path); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if removed == "" {
		t.Fatal("test did not remove a file")
	}
	if err = Recover(f.home); err != nil {
		t.Fatal(err)
	}
	if !regular(removed) {
		t.Fatal("interrupted removal was not restored")
	}
}

func TestMissingApplicationHomeBlocksUpdateWithoutRecreatingData(t *testing.T) {
	f := newFixture(t)
	artifact := filepath.Join(f.base, "codex-payload")
	write(t, artifact, "#!/bin/sh\nprintf new\n", 0755)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	if err = os.Rename(f.codex, f.codex+"-unmounted"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update([]string{"codex"}); err == nil {
		t.Fatal("missing data home accepted")
	}
	if _, err = os.Lstat(f.codex); !os.IsNotExist(err) {
		t.Fatal("missing data home recreated")
	}
}
