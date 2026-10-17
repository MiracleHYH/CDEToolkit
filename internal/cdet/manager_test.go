package cdet

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, p, text string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func apply(t *testing.T, p *Plan, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err != nil {
		t.Fatal(err)
	}
}

type fixture struct{ root, prefix, store, user, core, codex, proxy string }

func newFixture(t *testing.T) fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{root: root, prefix: filepath.Join(root, "mount", "cli"), store: filepath.Join(root, "mount", "components"), user: filepath.Join(root, "user"), core: filepath.Join(root, "build", "cdet")}
	f.codex = filepath.Join(f.user, ".codex")
	f.proxy = filepath.Join(f.user, ".config", "mihomo")
	for _, key := range []string{"CDET_PREFIX", "CDET_STORE", "CDET_HOME", "STK_HOME", "CODEX_HOME", "CODEX_SQLITE_HOME", "XDG_CONFIG_HOME", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", f.user)
	tools := filepath.Join(root, "fixture-tools")
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	write(t, f.core, string(fakeNative("cdet fixture")), 0755)
	write(t, filepath.Join(f.codex, "bin", "codex"), string(fakeNative("old codex fixture")), 0755)
	for path, text := range map[string]string{"auth.json": "AUTH-SENTINEL", "config.toml": "model = 'preserved'\n", "sessions/one.jsonl": "SESSION-SENTINEL", "archived_sessions/old.jsonl": "ARCHIVE-SENTINEL", "state.sqlite": "DB-SENTINEL", "state.sqlite-wal": "WAL-SENTINEL", "state.sqlite-shm": "SHM-SENTINEL", "skills/private/SKILL.md": "private skill", "plugins/local/settings.json": "private plugin"} {
		write(t, filepath.Join(f.codex, path), text, 0600)
	}
	write(t, filepath.Join(f.proxy, "mihomo"), string(fakeNative("proxy fixture")), 0755)
	write(t, filepath.Join(f.proxy, "config.yaml"), "mixed-port: 7890\nsecret: PROXY-SECRET-SENTINEL\nrule-providers:\n  private:\n    path: ./rules/private.yaml\n", 0600)
	write(t, filepath.Join(f.proxy, "rules", "private.yaml"), "payload: []\n", 0600)
	write(t, filepath.Join(f.user, ".bashrc"), "USER-BASHRC-SENTINEL\n", 0600)
	// curl never contacts the network. The only executed installer is this
	// local synthetic script; the native payload itself is never run here.
	payload := filepath.Join(root, "fixture-native-codex")
	write(t, payload, string(fakeNative("official fixture")), 0755)
	t.Setenv("CDET_FIXTURE_CODEX", payload)
	t.Setenv("CDET_FIXTURE_CURL_LOG", filepath.Join(root, "curl-calls"))
	t.Setenv("CDET_FIXTURE_FORBIDDEN_LOG", filepath.Join(root, "forbidden-calls"))
	write(t, filepath.Join(tools, "curl"), "#!/bin/sh\nprintf 'fixture curl\\n' >> \"$CDET_FIXTURE_CURL_LOG\"\n/bin/cat <<'CDET_FAKE_INSTALLER'\nset -eu\n/bin/mkdir -p \"$CODEX_INSTALL_DIR\"\n/bin/cp \"$CDET_FIXTURE_CODEX\" \"$CODEX_INSTALL_DIR/codex\"\n/bin/chmod 0755 \"$CODEX_INSTALL_DIR/codex\"\nCDET_FAKE_INSTALLER\n", 0755)
	for _, name := range []string{"systemctl", "supervisorctl", "sudo"} {
		write(t, filepath.Join(tools, name), "#!/bin/sh\nprintf 'forbidden\\n' >> \"$CDET_FIXTURE_FORBIDDEN_LOG\"\nexit 98\n", 0755)
	}
	return f
}
func (f fixture) manager(t *testing.T, o Options) *Manager {
	t.Helper()
	if o.Prefix == "" {
		o.Prefix = f.prefix
	}
	if o.Store == "" {
		o.Store = f.store
	}
	m, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.core
	m.Releases = newUpstream(t).client
	m.Releases.OS, m.Releases.Arch = runtime.GOOS, runtime.GOARCH
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		if len(spec.Args) == 1 && spec.Args[0] == "--version" {
			return []byte("codex-cli 0.1.0\n"), nil
		}
		if len(spec.Args) == 1 && spec.Args[0] == "-v" {
			return []byte("Mihomo Meta v1.0.0 linux amd64\n"), nil
		}
		return runCommand(spec)
	}
	return m
}
func (f fixture) init(t *testing.T) {
	t.Helper()
	p, e := f.manager(t, Options{}).Init()
	apply(t, p, e)
}
func (f fixture) install(t *testing.T, names ...string) {
	t.Helper()
	p, e := f.manager(t, Options{}).Install(names)
	apply(t, p, e)
}
func tree(t *testing.T, path string) *Tree {
	t.Helper()
	s, e := snapshotTree(path)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func TestInitAndReadOnlyCommandsUseOnlyTwoDestinations(t *testing.T) {
	f := newFixture(t)
	before := tree(t, f.user).RawDigest
	var out, stderr bytes.Buffer
	if (CLI{Out: &out, Err: &stderr}).Run([]string{"--prefix", f.prefix, "--store", f.store, "status", "--json"}) != 0 {
		t.Fatal(stderr.String())
	}
	if !absent(f.prefix) || !absent(f.store) {
		t.Fatal("read-only status created roots")
	}
	f.init(t)
	m := f.manager(t, Options{})
	if len(m.State.Components) != 0 || tree(t, f.user).RawDigest != before {
		t.Fatal("init installed components or changed user files")
	}
	if !regular(filepath.Join(f.prefix, "bin", "cdet")) || !regular(filepath.Join(f.store, ".cdet", "state.json")) {
		t.Fatal("incorrect layout")
	}
	p, e := m.Init()
	apply(t, p, e)
	if tree(t, f.user).RawDigest != before {
		t.Fatal("repeat init changed user files")
	}
	out.Reset()
	stderr.Reset()
	input := strings.NewReader(filepath.Join(f.root, "interactive-cli") + "\n" + filepath.Join(f.root, "interactive-store") + "\n")
	if (CLI{In: input, Out: &out, Err: &stderr}).Run([]string{"init", "--dry-run"}) != 0 {
		t.Fatal(stderr.String())
	}
	if strings.Count(out.String(), "目录 [") != 2 || !absent(filepath.Join(f.root, "interactive-cli")) {
		t.Fatal("init must only ask two roots and preview must not write", out.String())
	}
}

func TestFullImportPreservesSourcesAndOwnsCompleteCopies(t *testing.T) {
	f := newFixture(t)
	if e := os.Symlink(filepath.Join(f.codex, "skills"), filepath.Join(f.codex, "skill-link")); e != nil {
		t.Fatal(e)
	}
	original, codex, proxy := tree(t, f.user), tree(t, f.codex), tree(t, f.proxy)
	f.init(t)
	m := f.manager(t, Options{})
	p, e := m.Install([]string{"codex", "proxy"})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Imports) != 2 || len(m.State.Components) != 0 || !absent(filepath.Join(f.store, "codex")) || !absent(filepath.Join(f.root, "curl-calls")) {
		t.Fatal("planning wrote files or ran installer")
	}
	b, _ := json.Marshal(p)
	for _, secret := range []string{"AUTH-SENTINEL", "PROXY-SECRET-SENTINEL", "SESSION-SENTINEL", "private skill"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("plan exposed source contents")
		}
	}
	apply(t, p, nil)
	if tree(t, f.user).RawDigest != original.RawDigest || tree(t, filepath.Join(f.store, "codex")).Digest != codex.Digest || tree(t, filepath.Join(f.store, "proxy")).Digest != proxy.Digest {
		t.Fatal("incomplete copy or source mutation")
	}
	if link, e := os.Readlink(filepath.Join(f.store, "codex", "skill-link")); e != nil || link != "skills" {
		t.Fatal("internal absolute link not relocated", link, e)
	}
	if !regular(filepath.Join(f.store, programPath("codex"))) || !absent(filepath.Join(f.store, ".cdet", "programs")) {
		t.Fatal("unexpected program/version layout")
	}
	if e := os.Rename(f.user, f.user+"-retired"); e != nil {
		t.Fatal(e)
	}
	m = f.manager(t, Options{})
	path, args, env, e := m.Command("codex", []string{"one argument"})
	if e != nil || path != filepath.Join(f.store, programPath("codex")) || len(args) != 1 || !contains(env, "CODEX_HOME="+filepath.Join(f.store, "codex")) {
		t.Fatal("runtime depends on old source", path, e)
	}
	p, e = m.Install([]string{"codex", "proxy"})
	if e != nil || len(p.Steps) != 0 {
		t.Fatal("repeat install not idempotent", e)
	}
	if _, ok := m.State.Components["gds"]; ok {
		t.Fatal("GDS enabled implicitly")
	}
}

func TestFreshCodexUsesOfficialInstallerWithoutImportingExternalHome(t *testing.T) {
	f := newFixture(t)
	before := tree(t, f.user).RawDigest
	f.init(t)
	p, e := f.manager(t, Options{Fresh: true}).Install([]string{"codex"})
	apply(t, p, e)
	if len(p.Imports) != 0 || len(tree(t, filepath.Join(f.store, "codex")).entries) != 1 || tree(t, f.user).RawDigest != before {
		t.Fatal("fresh installation imported or altered external HOME")
	}
	c := f.manager(t, Options{}).State.Components["codex"]
	if !c.Installed || c.Backend != "official" || c.Origin != "installed" || !regular(filepath.Join(f.store, programPath("codex"))) {
		t.Fatal("fresh official installation not registered")
	}
}

func TestMountMoveRebuildsOnlyTwoRoots(t *testing.T) {
	for _, mode := range []string{"together", "store-only", "install-only"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			f.install(t, "codex", "proxy")
			before := tree(t, filepath.Join(f.store, "codex")).RawDigest
			prefix, store := f.prefix, f.store
			switch mode {
			case "together":
				if e := os.Rename(filepath.Join(f.root, "mount"), filepath.Join(f.root, "new-mount")); e != nil {
					t.Fatal(e)
				}
				prefix = filepath.Join(f.root, "new-mount", "cli")
				store = filepath.Join(f.root, "new-mount", "components")
			case "store-only":
				store = filepath.Join(f.root, "new-components")
				if e := os.Rename(f.store, store); e != nil {
					t.Fatal(e)
				}
				if _, e := New(Options{Prefix: prefix}); e == nil {
					t.Fatal("missing mount became empty store")
				}
			case "install-only":
				prefix = filepath.Join(f.root, "new-cli")
				if e := os.Rename(f.prefix, prefix); e != nil {
					t.Fatal(e)
				}
			}
			m := f.manager(t, Options{Prefix: prefix, Store: store})
			if !m.Relocated {
				t.Fatal("move not recognized")
			}
			if _, _, _, e := m.Command("codex", nil); e == nil {
				t.Fatal("command allowed before path rebuild")
			}
			p, e := m.Repair()
			apply(t, p, e)
			m = f.manager(t, Options{Prefix: prefix, Store: store})
			path, _, env, e := m.Command("codex", nil)
			if e != nil || !within(store, path) || !contains(env, "CODEX_HOME="+filepath.Join(store, "codex")) || tree(t, filepath.Join(store, "codex")).RawDigest != before {
				t.Fatal("repair changed data or did not rebuild runtime", e)
			}
			if mode != "install-only" && !absent(f.store) {
				t.Fatal("old store recreated")
			}
			p, e = m.Repair()
			apply(t, p, e)
			if tree(t, filepath.Join(store, "codex")).RawDigest != before {
				t.Fatal("repeat repair changed HOME")
			}
		})
	}
}

func TestSourceDriftAndUnknownTargetsNeverOverwriteData(t *testing.T) {
	for _, mode := range []string{"source-change", "unknown-target", "external-link"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			if mode == "unknown-target" {
				write(t, filepath.Join(f.store, "codex", "auth.json"), "EXISTING-UNMANAGED", 0600)
			}
			if mode == "external-link" {
				if e := os.Symlink(f.core, filepath.Join(f.codex, "external")); e != nil {
					t.Fatal(e)
				}
			}
			p, e := f.manager(t, Options{}).Install([]string{"codex"})
			if mode == "source-change" {
				if e != nil {
					t.Fatal(e)
				}
				write(t, filepath.Join(f.codex, "auth.json"), "LATER-AUTH", 0600)
				e = Apply(p)
			}
			if e == nil {
				t.Fatal("unsafe operation accepted", mode)
			}
			if mode == "unknown-target" {
				if read(t, filepath.Join(f.store, "codex", "auth.json")) != "EXISTING-UNMANAGED" {
					t.Fatal("unmanaged HOME overwritten")
				}
			} else if !absent(filepath.Join(f.store, "codex")) {
				t.Fatal("failed import published partial HOME")
			}
		})
	}
}

func TestProgramUpdateAndRemovalPreserveOpaqueMutableData(t *testing.T) {
	f := opaqueCodexFixture(t)
	f.init(t)
	f.install(t, "codex", "proxy")
	data := filepath.Join(f.store, "codex")
	before := tree(t, data).RawDigest
	write(t, filepath.Join(f.root, "fixture-native-codex"), string(fakeNative("updated official fixture")), 0755)
	p, e := f.manager(t, Options{}).Update([]string{"codex"})
	apply(t, p, e)
	if tree(t, data).RawDigest != before {
		t.Fatal("program update changed HOME")
	}
	// The official installer/updater owns its program. A later legitimate
	// replacement must not be treated as a user-data or managed-entry conflict.
	write(t, filepath.Join(f.store, programPath("codex")), string(fakeNative("upstream self update")), 0755)
	if _, _, _, e := f.manager(t, Options{}).Command("codex", nil); e != nil {
		t.Fatal("upstream self update rejected", e)
	}
	write(t, filepath.Join(f.store, ".cdet", "bin", "unknown-user-file"), "KEEP-UNKNOWN", 0600)
	p, e = f.manager(t, Options{}).Remove([]string{"codex"})
	apply(t, p, e)
	if tree(t, data).RawDigest != before || !absent(filepath.Join(f.store, programPath("codex"))) || read(t, filepath.Join(f.store, ".cdet", "bin", "unknown-user-file")) != "KEEP-UNKNOWN" {
		t.Fatal("remove changed HOME or unknown files")
	}
	p, e = f.manager(t, Options{}).Remove([]string{"self"})
	apply(t, p, e)
	if tree(t, data).RawDigest != before || !regular(filepath.Join(f.store, "proxy", "config.yaml")) {
		t.Fatal("self removal deleted component data")
	}
}

func TestOptionalGDSAndRemovedCLIControls(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	if _, e := f.manager(t, Options{}).Install([]string{"gds"}); e == nil {
		t.Fatal("GDS guessed an upstream")
	}
	gds := filepath.Join(f.root, "gds-fixture")
	write(t, gds, "#!/bin/sh\nexit 77\n", 0755)
	p, e := f.manager(t, Options{Fresh: true, Artifacts: map[string]string{"gds": gds}}).Install([]string{"gds"})
	apply(t, p, e)
	if !f.manager(t, Options{}).State.Components["gds"].Installed {
		t.Fatal("explicit GDS not registered")
	}
	for _, args := range [][]string{{"rollback", "codex"}, {"repair", "--recover"}, {"update", "self"}, {"install", "codex", "--artifact", "codex=/unused"}, {"install", "codex", "--sha256", "codex=unused"}, {"install", "codex", "--codex-mode", "home"}} {
		var out, stderr bytes.Buffer
		if (CLI{Out: &out, Err: &stderr}).Run(append([]string{"--prefix", f.prefix, "--store", f.store}, args...)) == 0 {
			t.Fatal("obsolete or unsupported CLI accepted", args)
		}
	}
	if !absent(filepath.Join(f.root, "forbidden-calls")) {
		t.Fatal("a service command was executed")
	}
}

func TestFailedOfficialInstallKeepsPublishedHomeAndCanRetry(t *testing.T) {
	f := opaqueCodexFixture(t)
	f.init(t)
	original := tree(t, f.codex).RawDigest
	m := f.manager(t, Options{})
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "/bin/sh" {
			return nil, errors.New("synthetic installer failure")
		}
		return runCommand(spec)
	}
	p, e := m.Install([]string{"codex"})
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(p); e == nil {
		t.Fatal("installer failure ignored")
	}
	data := filepath.Join(f.store, "codex")
	published := tree(t, data).RawDigest
	if tree(t, f.codex).RawDigest != original || f.manager(t, Options{}).State.Components["codex"].Installed {
		t.Fatal("failed installer changed source or claimed success")
	}
	p, e = f.manager(t, Options{}).Install([]string{"codex"})
	apply(t, p, e)
	if len(p.Imports) != 0 || tree(t, data).RawDigest != published {
		t.Fatal("retry copied over or deleted published HOME")
	}
}

func TestPublishedHomeReceiptOnlyResumesAnUnchangedOwnedCopy(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "later-user-file"}[change], func(t *testing.T) {
			f := opaqueCodexFixture(t)
			f.init(t)
			m := f.manager(t, Options{})
			if e := os.Symlink(filepath.Join(f.codex, "skills"), filepath.Join(f.codex, "internal-link")); e != nil {
				t.Fatal(e)
			}
			// Simulate an interruption after the atomic directory publish and
			// before component state registration, without a replayable journal.
			if e := m.publishData("codex", tree(t, f.codex)); e != nil {
				t.Fatal(e)
			}
			data := filepath.Join(f.store, "codex")
			if change {
				write(t, filepath.Join(data, "later-user-file"), "NEW-USER-DATA", 0600)
			}
			before := tree(t, data).RawDigest
			p, e := f.manager(t, Options{}).Install([]string{"codex"})
			if change {
				if e == nil {
					t.Fatal("changed unpublished HOME automatically adopted")
				}
			} else {
				apply(t, p, e)
			}
			if tree(t, data).RawDigest != before {
				t.Fatal("resume deleted or changed published HOME")
			}
		})
	}
}

func TestCLIUpdatePreviewAndDeclinedConfirmationDoNotRunInstaller(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex")
	// Only loopback connection failures are possible for CLI's metadata GET;
	// no real GitHub request or existing local proxy is used by this test.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	before := read(t, filepath.Join(f.root, "curl-calls"))
	data := tree(t, filepath.Join(f.store, "codex")).RawDigest
	var out, stderr bytes.Buffer
	base := []string{"--prefix", f.prefix, "--store", f.store, "update", "codex"}
	if (CLI{Out: &out, Err: &stderr}).Run(append(base, "--dry-run", "--json")) != 0 {
		t.Fatal(stderr.String())
	}
	var preview Plan
	if e := json.Unmarshal(out.Bytes(), &preview); e != nil || len(preview.Steps) != 1 {
		t.Fatal("preview is not a single JSON plan", out.String(), e)
	}
	out.Reset()
	stderr.Reset()
	if (CLI{In: strings.NewReader("n\n"), Out: &out, Err: &stderr}).Run(base) == 0 || !strings.Contains(stderr.String(), "未确认") {
		t.Fatal("declined update executed", stderr.String())
	}
	if read(t, filepath.Join(f.root, "curl-calls")) != before || tree(t, filepath.Join(f.store, "codex")).RawDigest != data {
		t.Fatal("preview or declined confirmation invoked installer or changed HOME")
	}
}
