package stk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct{ base, local, home, codex, binary, proxy, config, projects, data, manager string }

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(content), mode); e != nil {
		t.Fatal(e)
	}
}
func read(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f := fixture{base: base, local: filepath.Join(base, "fast", ".local"), projects: filepath.Join(base, "fast", "proj"), data: filepath.Join(base, "bulk", "results")}
	f.home = filepath.Join(f.local, "stk")
	f.codex = filepath.Join(f.local, ".codex")
	f.binary = filepath.Join(f.codex, "packages", "standalone", "v1", "bin", "codex")
	f.proxy = filepath.Join(f.local, "external", "mihomo")
	f.config = filepath.Join(f.local, "private", "proxy.yaml")
	f.manager = filepath.Join(base, "fixture-manager")
	for _, p := range []string{f.projects, f.data} {
		if e = os.MkdirAll(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	write(t, f.binary, "#!/bin/sh\nprintf 'fixture-codex %s\\n' \"$CODEX_HOME\"\n", 0755)
	write(t, f.proxy, "#!/bin/sh\nprintf 'fixture-proxy\\n'\n", 0755)
	write(t, f.manager, "#!/bin/sh\nexit 99\n", 0755)
	write(t, f.config, "# KEEP-COMMENT\nlisteners:\n  - name: common\n    type: mixed\n    listen: 127.0.0.1\n    port: 7890\npassword: KEEP-SECRET\n", 0600)
	write(t, filepath.Join(f.codex, "auth.json"), "AUTH-SENTINEL", 0600)
	write(t, filepath.Join(f.codex, "sessions", "session.jsonl"), "SESSION-SENTINEL", 0600)
	write(t, filepath.Join(f.codex, "state.sqlite"), "DATABASE-SENTINEL", 0600)
	write(t, filepath.Join(f.codex, "config.toml"), "model = 'preserved'\n", 0600)
	return f
}

func (f fixture) managerFor(t *testing.T, opts Options) *Manager {
	t.Helper()
	if opts.Home == "" {
		opts.Home = f.home
	}
	if opts.Roots == nil {
		opts.Roots = map[string]string{"local": f.local, "projects": f.projects, "data": f.data}
	}
	if opts.Binaries == nil {
		opts.Binaries = map[string]string{"proxy": f.proxy, "codex": f.binary}
	}
	if opts.CodexHome == "" {
		opts.CodexHome = f.codex
	}
	if opts.ProxyConfig == "" {
		opts.ProxyConfig = f.config
	}
	m, e := New(opts)
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	m.Path = ""
	m.UserHome = filepath.Join(f.base, "user")
	return m
}

func apply(t *testing.T, p *Plan, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e != nil {
		t.Fatal(e)
	}
}

func TestAdoptPreservesExternalDataAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	before := read(t, f.config)
	p, e := m.Setup([]string{"proxy", "codex"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(f.home); !os.IsNotExist(e) {
		t.Fatal("preview created manager directory")
	}
	b, _ := json.Marshal(p)
	if bytes.Contains(b, []byte("KEEP-SECRET")) {
		t.Fatal("plan leaked secret")
	}
	apply(t, p, nil)
	for name, value := range map[string]string{"auth.json": "AUTH-SENTINEL", "sessions/session.jsonl": "SESSION-SENTINEL", "state.sqlite": "DATABASE-SENTINEL"} {
		if read(t, filepath.Join(f.codex, name)) != value {
			t.Fatal("modified component data", name)
		}
	}
	if read(t, f.config) != before {
		t.Fatal("modified external proxy config")
	}
	m = f.managerFor(t, Options{})
	if m.State.Modules["proxy"].OwnedBinary || m.State.Modules["codex"].OwnedBinary {
		t.Fatal("external binary became owned")
	}
	if _, ok := m.State.Modules["gds"]; ok {
		t.Fatal("GDS was enabled implicitly")
	}
	p, e = m.Setup([]string{"proxy", "codex"})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Changes) != 0 {
		for _, c := range p.Changes {
			t.Log(c.Path)
		}
		t.Fatal("second setup changed files")
	}
	if e := exec.Command("/bin/sh", "-n", filepath.Join(f.home, "env.sh")).Run(); e != nil {
		t.Fatal(e)
	}
}

func TestMountMoveRebuildsPathsLinksAndServiceTemplates(t *testing.T) {
	f := newFixture(t)
	current := filepath.Join(f.codex, "packages", "standalone", "current")
	if e := os.Symlink("/gone/.codex/packages/standalone/v1", current); e != nil {
		t.Fatal(e)
	}
	entry := filepath.Join(f.codex, "bin", "codex")
	if e := os.MkdirAll(filepath.Dir(entry), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("/gone/.codex/packages/standalone/current/bin/codex", entry); e != nil {
		t.Fatal(e)
	}
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"proxy", "codex"})
	apply(t, p, e)
	if target, _ := os.Readlink(current); filepath.IsAbs(target) {
		t.Fatal("current link is absolute")
	}
	oldFast := filepath.Dir(f.local)
	newFast := filepath.Join(f.base, "new-fast")
	if e := os.Rename(oldFast, newFast); e != nil {
		t.Fatal(e)
	}
	newHome := filepath.Join(newFast, ".local", "stk")
	m, e = New(Options{Home: newHome})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	if m.Roots["local"] != filepath.Join(newFast, ".local") || m.Roots["projects"] != filepath.Join(newFast, "proj") || m.Roots["data"] != f.data {
		t.Fatal("incorrect root rebasing", m.Roots)
	}
	p, e = m.Repair()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(oldFast); !os.IsNotExist(e) {
		t.Fatal("preview recreated old mount")
	}
	apply(t, p, nil)
	for _, rel := range []string{"env.sh", "services/proxy.supervisor.conf", "services/proxy.service"} {
		if strings.Contains(read(t, filepath.Join(newHome, rel)), oldFast) {
			t.Fatal("stale generated path", rel)
		}
	}
	newCodex := filepath.Join(newFast, ".local", ".codex")
	if read(t, filepath.Join(newCodex, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("auth changed")
	}
	m, e = New(Options{Home: newHome})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	p, e = m.Repair()
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Changes) != 0 {
		t.Fatal("repair is not idempotent")
	}
	_, _, env, e := m.Command("codex", nil)
	if e != nil {
		t.Fatal(e)
	}
	if !contains(env, "CODEX_HOME="+newCodex) {
		t.Fatal("exec has stale home")
	}
}

func TestConfigMigrationRequiresExplicitOwnership(t *testing.T) {
	f := newFixture(t)
	config := "[projects.\"" + f.projects + "/sample\"]\ntrust_level='trusted'\n[mcp_servers.demo]\ncommand='" + f.local + "/bin/mcp'\nargs=['" + f.projects + "/script.py']\npassword='" + f.local + "/KEEP-SECRET'\n"
	write(t, filepath.Join(f.codex, "config.toml"), config, 0600)
	m := f.managerFor(t, Options{ManageConfig: true})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	oldLocal := f.local
	newFast := filepath.Join(f.base, "new-fast")
	if e = os.Rename(filepath.Dir(f.local), newFast); e != nil {
		t.Fatal(e)
	}
	m, e = New(Options{Home: filepath.Join(newFast, ".local", "stk")})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	p, e = m.Repair()
	apply(t, p, e)
	after := read(t, filepath.Join(newFast, ".local", ".codex", "config.toml"))
	if !strings.Contains(after, oldLocal+"/KEEP-SECRET") || !strings.Contains(after, newFast+"/proj/script.py") || !strings.Contains(after, newFast+"/.local/bin/mcp") {
		t.Fatal("incorrect scoped config migration", after)
	}
}

func TestDuplicateAndMissingCandidatesFailWithoutWriting(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Binaries: map[string]string{}})
	other := filepath.Join(f.base, "other")
	write(t, filepath.Join(other, "mihomo"), "#!/bin/sh\nexit 0", 0755)
	m.Path = filepath.Dir(f.proxy) + string(os.PathListSeparator) + other
	if _, e := m.Setup([]string{"proxy"}); e == nil {
		t.Fatal("ambiguous candidate selected silently")
	}
	if _, e := os.Stat(f.home); !os.IsNotExist(e) {
		t.Fatal("failed setup created state")
	}
	m = f.managerFor(t, Options{})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
	if e = os.Remove(f.proxy); e != nil {
		t.Fatal(e)
	}
	m = f.managerFor(t, Options{Binaries: map[string]string{}})
	m.Path = other
	if _, e = m.Discover("proxy"); e == nil {
		t.Fatal("silently switched registered binary")
	}
}

func TestMissingRootNeverInitializesReplacement(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
	missing := filepath.Join(f.base, "unmounted")
	m, e = New(Options{Home: f.home, Roots: map[string]string{"local": missing}})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	if _, e = m.Repair(); e == nil {
		t.Fatal("unmounted root accepted")
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("empty replacement root created")
	}
}

func TestPlanRejectsDriftAndRollsBackFailure(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	env := filepath.Join(f.home, "env.sh")
	old := read(t, env)
	p = &Plan{Home: f.home}
	if e = p.add(env, "write", "test", []byte("new"), "", 0600); e != nil {
		t.Fatal(e)
	}
	write(t, env, "external edit", 0600)
	if e = Apply(p); e == nil {
		t.Fatal("stale plan applied")
	}
	write(t, env, old, 0600)
	p = &Plan{Home: f.home}
	_ = p.add(env, "write", "test", []byte("new"), "", 0600)
	p.Changes = append(p.Changes, Change{Path: filepath.Join(f.home, "failure"), Kind: "injected-failure", Before: Fingerprint{Kind: "absent"}})
	if e = Apply(p); e == nil {
		t.Fatal("failure was ignored")
	}
	if read(t, env) != old {
		t.Fatal("rollback did not restore file")
	}
	if _, e = os.Stat(filepath.Join(f.home, "pending.json")); !os.IsNotExist(e) {
		t.Fatal("successful rollback left pending operation")
	}
}

func TestGeneratedFileConflictAndSymlinkParents(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	write(t, filepath.Join(f.home, "bin", "codex"), "user changed wrapper", 0755)
	m = f.managerFor(t, Options{})
	if _, e = m.Repair(); e == nil {
		t.Fatal("overwrote external wrapper edit")
	}
	link := filepath.Join(f.base, "alias")
	if e = os.Symlink(f.local, link); e != nil {
		t.Fatal(e)
	}
	if _, e = New(Options{Home: filepath.Join(link, "stk")}); e == nil {
		t.Fatal("symlink parent accepted")
	}
}

func TestServiceIsOnlyCalledExplicitlyAndUsesRelocatedConfig(t *testing.T) {
	f := newFixture(t)
	log := filepath.Join(f.base, "service-calls")
	controller := filepath.Join(f.local, "controllers", "supervisorctl")
	write(t, controller, "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+quote(log)+"\n", 0755)
	serviceConfig := filepath.Join(f.local, "supervisor.conf")
	write(t, serviceConfig, "[supervisord]\n", 0600)
	m := f.managerFor(t, Options{Services: map[string]Service{"proxy": {Backend: "supervisor", Name: "existing-mihomo", Controller: Ref{Path: controller}, Config: Ref{Path: serviceConfig}}}})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
	if _, e = os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("setup called service controller")
	}
	newFast := filepath.Join(f.base, "new-fast")
	if e = os.Rename(filepath.Dir(f.local), newFast); e != nil {
		t.Fatal(e)
	}
	m, e = New(Options{Home: filepath.Join(newFast, ".local", "stk")})
	if e != nil {
		t.Fatal(e)
	}
	p, e = m.Repair()
	apply(t, p, e)
	if _, e = os.Stat(log); !os.IsNotExist(e) {
		t.Fatal("repair called service controller")
	}
	m, e = New(Options{Home: m.Home})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.ServiceAction("proxy", "start"); e != nil {
		t.Fatal(e)
	}
	got := read(t, log)
	if !strings.Contains(got, filepath.Join(newFast, ".local", "supervisor.conf")) || !strings.HasSuffix(got, "start\nexisting-mihomo\n") {
		t.Fatal(got)
	}
}

func TestExplicitArtifactInstallAndRejectedDigest(t *testing.T) {
	f := newFixture(t)
	newCodex := filepath.Join(f.local, "new-codex")
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}, CodexHome: newCodex})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	m, e = New(Options{Home: f.home})
	if e != nil {
		t.Fatal(e)
	}
	if !m.State.Modules["codex"].OwnedBinary || !directory(newCodex) {
		t.Fatal("artifact was not installed in isolated manager home")
	}
	if _, e = newReleaseClient().artifactBytes(Artifact{Source: f.binary, Release: Release{SHA256: strings.Repeat("0", 64)}}, "codex"); e == nil {
		t.Fatal("bad digest accepted")
	}
	if _, e = newReleaseClient().artifactBytes(Artifact{Source: "http://example.invalid/binary"}, "codex"); e == nil {
		t.Fatal("insecure download accepted")
	}
	if _, e = newReleaseClient().artifactBytes(Artifact{Source: "https://example.invalid/binary"}, "codex"); e == nil {
		t.Fatal("download without digest accepted")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../codex", Mode: 0755, Size: 1})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	archive := filepath.Join(f.base, "evil.tar.gz")
	write(t, archive, buf.String(), 0600)
	if _, e = newReleaseClient().artifactBytes(Artifact{Source: archive}, "codex"); e == nil {
		t.Fatal("unsafe archive accepted")
	}
}

func TestFindHomeAndReadOnlyCLI(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	home, e := FindHome("/does-not-exist", m.State.Instance, []string{filepath.Dir(f.local)})
	if e != nil || home != f.home {
		t.Fatal(home, e)
	}
	state := read(t, filepath.Join(f.home, "state.json"))
	var out, errs bytes.Buffer
	if code := (CLI{&out, &errs}).Run([]string{"--home", f.home, "status", "--json"}); code != 0 {
		t.Fatal(errs.String())
	}
	if read(t, filepath.Join(f.home, "state.json")) != state {
		t.Fatal("status wrote state")
	}
	if _, e = exec.LookPath("sh"); e != nil {
		t.Fatal(e)
	}
	if got := parseMounts("1 0 0:1 / / rw - overlay overlay rw\n2 1 0:2 / /fast rw - gpfs gpfs rw\n", "/fast/.local"); got.Target != "/fast" || got.FSType != "gpfs" {
		t.Fatal(got)
	}
}

func contains(items []string, value string) bool {
	for _, s := range items {
		if s == value {
			return true
		}
	}
	return false
}
