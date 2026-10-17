package stk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditReadoptionRejectsLaterLinkEdit(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.codex, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/gone/.codex/packages/standalone/v1/bin/codex", entry); err != nil {
		t.Fatal(err)
	}
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	other := filepath.Join(f.base, "other-codex")
	write(t, other, "#!/bin/sh\nexit 0\n", 0755)
	if err = os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(other, entry); err != nil {
		t.Fatal(err)
	}
	m = f.managerFor(t, Options{})
	if _, err = m.Setup([]string{"codex"}); err == nil {
		t.Fatal("repeated setup would overwrite the user's new component link")
	}
	if target, _ := os.Readlink(entry); target != other {
		t.Fatal("changed link during planning")
	}
}

func TestAuditDeferredConfigurationMigrationRetainsOldRoots(t *testing.T) {
	f := newFixture(t)
	original := "[mcp_servers.demo]\ncommand='" + f.local + "/bin/demo'\n"
	write(t, filepath.Join(f.codex, "config.toml"), original, 0600)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	newFast := filepath.Join(f.base, "fast-new")
	if err = os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(newFast, ".local", "stk")
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Repair()
	apply(t, p, err)
	m, err = New(Options{Home: home, ManageConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Setup([]string{"codex"})
	apply(t, p, err)
	got := read(t, filepath.Join(newFast, ".local", ".codex", "config.toml"))
	if strings.Contains(got, f.local+"/bin/demo") || !strings.Contains(got, newFast+"/.local/bin/demo") {
		t.Fatal("repair lost the old prefix before configuration ownership was granted")
	}
}

func TestAuditMoveOnlyManagerDoesNotMoveApplicationRoots(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	home := filepath.Join(f.local, "managers", "stk")
	if err = os.MkdirAll(filepath.Dir(home), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(f.home, home); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if m.Roots["local"] != f.local {
		t.Fatal("moving only STK changed the application root", m.Roots)
	}
}

func TestAuditMissingProxyDataIsNotRecreated(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"proxy"})
	apply(t, p, err)
	data := filepath.Join(f.home, "run", "proxy")
	if err = os.Rename(data, data+"-unmounted"); err != nil {
		t.Fatal(err)
	}
	m = f.managerFor(t, Options{})
	if _, err = m.Repair(); err == nil {
		t.Fatal("repair would replace missing application data with an empty directory")
	}
	if _, err = os.Lstat(data); !os.IsNotExist(err) {
		t.Fatal("recreated missing proxy data")
	}
}

func TestAuditIdenticalPreexistingOutputDoesNotGrantOwnership(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	body := "#!/bin/sh\n# server-toolkit-go managed launcher\nSTK_HOME=$(CDPATH= cd -- \"$(dirname -- \"$0\")/..\" && pwd -P) || exit\nexec \"$STK_HOME/stk\" --home \"$STK_HOME\" exec codex -- \"$@\"\n"
	path := filepath.Join(f.home, "bin", "codex")
	write(t, path, body, 0755)
	if _, err := m.Setup([]string{"codex"}); err == nil {
		t.Fatal("identical unowned file silently became uninstallable STK property")
	}
}

func TestAuditMigrationDoesNotRewriteCredentialLikeKeys(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	m.State.Roots = map[string]Root{"local": {Path: "/old-root"}}
	m.Roots = map[string]string{"local": "/new-root"}
	original := []byte("[mcp_servers.demo]\ncommand='/old-root/bin/demo'\n[mcp_servers.demo.env]\npath='/old-root/KEEP-SECRET'\n[unknown]\ncwd='/old-root/KEEP-OTHER'\n")
	after, err := m.rewriteConfig("codex", original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "/old-root/KEEP-SECRET") || !strings.Contains(string(after), "/old-root/KEEP-OTHER") {
		t.Fatal("configuration migration crossed the known schema boundary")
	}
}

func TestAuditRecoveryRejectsForeignInstance(t *testing.T) {
	f1, f2 := newFixture(t), newFixture(t)
	m1 := f1.managerFor(t, Options{})
	p, err := m1.Setup([]string{"codex"})
	apply(t, p, err)
	m2 := f2.managerFor(t, Options{})
	p, err = m2.Setup([]string{"codex"})
	apply(t, p, err)
	m1 = f1.managerFor(t, Options{})
	path := filepath.Join(f1.home, "env.sh")
	p = &Plan{Home: f1.home}
	if err = p.add(path, "write", "fixture", []byte("after"), "", 0600); err != nil {
		t.Fatal(err)
	}
	j := testJournal(t, f2.home, m1.State.Instance, p.Changes)
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f2.home, "pending.json"), string(raw), 0600)
	write(t, path, "after", 0600)
	if err = Recover(f2.home); err == nil {
		t.Fatal("recovery for one instance restored another instance's paths")
	}
	if read(t, path) != "after" {
		t.Fatal("foreign recovery changed its target")
	}
}

func testJournal(t *testing.T, home, instance string, changes []Change) journal {
	t.Helper()
	id, err := instanceID()
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Home: home, Instance: instance, Transaction: id, Changes: changes}
	j := journalFor(p)
	for _, b := range j.Entries {
		if err = snapshot(b); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func TestAuditSuccessfulTakeoverRollbackPreservesExternalData(t *testing.T) {
	f := newFixture(t)
	beforeBinary, beforeConfig := read(t, f.binary), read(t, f.config)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	oldState := read(t, filepath.Join(f.home, "state.json"))
	artifact := filepath.Join(f.base, "next-codex")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m = f.managerFor(t, Options{Takeover: true, Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(f.home, "tools", "codex", "codex")); err != nil {
		t.Fatal("rollback preview changed files")
	}
	apply(t, p, nil)
	if read(t, filepath.Join(f.home, "state.json")) != oldState {
		t.Fatal("rollback did not restore management relationships")
	}
	if read(t, f.binary) != beforeBinary || read(t, f.config) != beforeConfig {
		t.Fatal("rollback changed external files")
	}
	assertPreserved(t, f, read(t, f.proxy), beforeConfig)
	if _, err = os.Lstat(filepath.Join(f.home, "tools", "codex", "codex")); !os.IsNotExist(err) {
		t.Fatal("takeover copy remained")
	}
	if _, err = os.Lstat(filepath.Join(f.home, "last-update.json")); !os.IsNotExist(err) {
		t.Fatal("consumed rollback record remained")
	}
}

func TestAuditUpdateFailureRestoresProgramsMetadataAndPreviousHistory(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "after-first-replacement", true: "after-history-commit"}[failLast], func(t *testing.T) {
			f := newFixture(t)
			m := f.managerFor(t, Options{Artifacts: map[string]string{"proxy": f.proxy, "codex": f.binary}})
			p, err := m.Setup([]string{"proxy", "codex"})
			apply(t, p, err)
			artifact := filepath.Join(f.base, "codex-version-two")
			write(t, artifact, "#!/bin/sh\nprintf two\n", 0755)
			m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
			p, err = m.Update([]string{"codex"})
			apply(t, p, err)
			paths := []string{filepath.Join(f.home, "tools", "codex", "codex"), filepath.Join(f.home, "tools", "proxy", "mihomo"), filepath.Join(f.home, "state.json"), filepath.Join(f.home, "config.toml"), filepath.Join(f.home, "last-update.json")}
			before := map[string]string{}
			for _, path := range paths {
				before[path] = read(t, path)
			}
			write(t, artifact, "#!/bin/sh\nprintf three\n", 0755)
			proxyArtifact := filepath.Join(f.base, "proxy-next")
			write(t, proxyArtifact, "#!/bin/sh\nprintf next-proxy\n", 0755)
			m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact, "proxy": proxyArtifact}})
			p, err = m.Update([]string{"codex", "proxy"})
			if err != nil {
				t.Fatal(err)
			}
			failAt := 0
			if failLast {
				failAt = len(p.Changes) - 1
			}
			observedReplacement := false
			p.testHook = func(stage string, index int) error {
				if stage == "change" && index == failAt {
					observedReplacement = read(t, paths[0]) != before[paths[0]]
					return os.ErrPermission
				}
				return nil
			}
			if err = Apply(p); err == nil || !observedReplacement {
				t.Fatal("test did not fail after replacing a program")
			}
			for _, path := range paths {
				if read(t, path) != before[path] {
					t.Fatal("partial rollback left a changed file", path)
				}
			}
			if _, err = os.Lstat(filepath.Join(f.home, "pending.json")); !os.IsNotExist(err) {
				t.Fatal("completed recovery left pending")
			}
			m = f.managerFor(t, Options{})
			p, err = m.Rollback()
			apply(t, p, err)
			if read(t, paths[0]) != read(t, f.binary) {
				t.Fatal("failed update destroyed previous successful update's rollback backup")
			}
		})
	}
}

func TestAuditSnapshotRaceDoesNotReplaceTargetsOrOldBackups(t *testing.T) {
	f := newFixture(t)
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(f.home, "tools", "codex", "codex")
	beforeState := read(t, filepath.Join(f.home, "state.json"))
	p.testHook = func(stage string, index int) error {
		if stage == "snapshot" && index == 0 {
			write(t, owned, "user edit during snapshot", 0755)
		}
		return nil
	}
	if err = Apply(p); err == nil {
		t.Fatal("snapshot race was not detected")
	}
	if read(t, owned) != "user edit during snapshot" || read(t, filepath.Join(f.home, "state.json")) != beforeState {
		t.Fatal("snapshot race overwrote a target")
	}
	if _, err = os.Lstat(filepath.Join(f.home, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("snapshot race recorded an applied transaction")
	}
}

func TestAuditRollbackRejectsCorruptBackupAndTargetDrift(t *testing.T) {
	for _, corrupt := range []bool{true, false} {
		t.Run(map[bool]string{true: "backup-corruption", false: "later-user-edit"}[corrupt], func(t *testing.T) {
			f := newFixture(t)
			m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
			p, err := m.Setup([]string{"codex"})
			apply(t, p, err)
			artifact := filepath.Join(f.base, "next")
			write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
			m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
			p, err = m.Update([]string{"codex"})
			apply(t, p, err)
			owned := filepath.Join(f.home, "tools", "codex", "codex")
			if corrupt {
				j, e := readJournal(filepath.Join(f.home, "last-update.json"), f.home)
				if e != nil {
					t.Fatal(e)
				}
				for _, b := range j.Entries {
					if b.File != "" {
						write(t, b.File, "corrupt", 0600)
						break
					}
				}
			} else {
				write(t, owned, "user changed program", 0755)
			}
			before := read(t, owned)
			m = f.managerFor(t, Options{})
			if _, err = m.Rollback(); err == nil {
				t.Fatal("unsafe rollback was accepted")
			}
			if read(t, owned) != before {
				t.Fatal("failed rollback changed program")
			}
		})
	}
}

func TestAuditInterruptedUpdateRecovery(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	j := journalFor(p)
	for _, b := range j.Entries {
		if err = snapshot(b); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	first := p.Changes[0]
	if err = atomicWrite(first.Path, first.Data, first.Mode); err != nil {
		t.Fatal(err)
	}
	if err = Recover(f.home); err != nil {
		t.Fatal(err)
	}
	if read(t, first.Path) != read(t, f.binary) {
		t.Fatal("interrupted update did not recover its old program")
	}
	if _, err = os.Lstat(filepath.Join(f.home, "last-update.json")); !os.IsNotExist(err) {
		t.Fatal("interrupted update left a success record")
	}
}

func TestAuditRecoveryPreservesNewUserDataInCreatedDirectory(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	data := filepath.Join(f.home, "new-data")
	p = &Plan{Home: f.home}
	if err = p.add(data, "dir", "fixture", nil, "", 0700); err != nil {
		t.Fatal(err)
	}
	m = f.managerFor(t, Options{})
	j := testJournal(t, f.home, m.State.Instance, p.Changes)
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	write(t, filepath.Join(data, "auth.json"), "NEW-USER-DATA", 0600)
	if err = Recover(f.home); err == nil {
		t.Fatal("recovery ignored new user data")
	}
	if read(t, filepath.Join(data, "auth.json")) != "NEW-USER-DATA" {
		t.Fatal("recovery deleted new user data")
	}
}

func TestAuditRelocatedManagerCannotReplayOldRollback(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	newFast := filepath.Join(f.base, "new-fast")
	if err = os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Home: filepath.Join(newFast, ".local", "stk")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Rollback(); err == nil {
		t.Fatal("rollback replayed old physical mount paths")
	}
	if _, err = os.Lstat(filepath.Dir(f.local)); !os.IsNotExist(err) {
		t.Fatal("rollback created old mount")
	}
}

func TestAuditSelectedUpdateIgnoresUnavailableOtherComponent(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary, "proxy": f.proxy}})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	if err = os.Rename(f.config, f.config+"-unmounted"); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	if _, err = os.Lstat(f.config); !os.IsNotExist(err) {
		t.Fatal("unselected proxy config recreated")
	}
}

func TestAuditGlobalEntrypointIsRecordedWithoutChangingHealthyLink(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.local, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.binary, entry); err != nil {
		t.Fatal(err)
	}
	m := f.managerFor(t, Options{Binaries: map[string]string{"codex": entry}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	if target, _ := os.Readlink(entry); target != f.binary {
		t.Fatal("adoption changed a healthy external entry")
	}
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
	newEntry := filepath.Join(newFast, ".local", "bin", "codex")
	if !executable(newEntry) {
		t.Fatal("registered external entry was not repaired")
	}
	if target, _ := os.Readlink(newEntry); filepath.IsAbs(target) {
		t.Fatal("stale external entry was not made relocatable")
	}
}

func TestAuditSetupRelocatesIndividualHomeAndKnownConfigurationPaths(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.codex, "config.toml"), "[mcp_servers.demo]\ncommand='"+f.codex+"/bin/helper'\n", 0600)
	m := f.managerFor(t, Options{ManageConfig: true})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	newHome := filepath.Join(f.local, "codex-new")
	if err = os.Rename(f.codex, newHome); err != nil {
		t.Fatal(err)
	}
	m, err = New(Options{Home: f.home, CodexHome: newHome})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Setup([]string{"codex"})
	apply(t, p, err)
	m, err = New(Options{Home: f.home})
	if err != nil {
		t.Fatal(err)
	}
	program, _ := m.resolve(m.State.Modules["codex"].Binary)
	if !strings.HasPrefix(program, newHome+"/") || strings.Contains(read(t, filepath.Join(newHome, "config.toml")), f.codex+"/bin/helper") {
		t.Fatal("setup mixed old program/config paths with the new data home")
	}
	if read(t, filepath.Join(newHome, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("home relocation changed authentication")
	}
}

func TestAuditCodexHomeEnvironmentIsRespectedAndMissingHomeFails(t *testing.T) {
	f := newFixture(t)
	home := filepath.Join(f.local, "custom-home")
	write(t, filepath.Join(home, "auth.json"), "CUSTOM-AUTH", 0600)
	t.Setenv("CODEX_HOME", home)
	m, err := New(Options{Home: f.home, Roots: map[string]string{"local": f.local}, Binaries: map[string]string{"codex": f.binary}})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m, err = New(Options{Home: f.home})
	if err != nil {
		t.Fatal(err)
	}
	selected, _ := m.resolve(m.State.Modules["codex"].Home)
	if selected != home {
		t.Fatal("ignored CODEX_HOME")
	}
	missing := filepath.Join(f.local, "unavailable-home")
	t.Setenv("CODEX_HOME", missing)
	other := filepath.Join(f.local, "other-manager")
	m, err = New(Options{Home: other, Artifacts: map[string]string{"codex": f.binary}})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	if _, err = m.Install([]string{"codex"}); err == nil {
		t.Fatal("missing CODEX_HOME became a fresh environment")
	}
	if _, err = os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatal("created replacement environment")
	}
}

func TestAuditLifecycleSearchDoesNotRetargetAnExplicitHome(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	before := read(t, filepath.Join(f.home, "state.json"))
	var out, errs strings.Builder
	code := (CLI{Out: &out, Err: &errs}).Run([]string{"--home", filepath.Join(f.base, "wrong-home"), "uninstall", "stk", "--search", f.local})
	if code != 0 || !executable(filepath.Join(f.home, "stk")) || read(t, filepath.Join(f.home, "state.json")) != before {
		t.Fatal("search redirected an uninstall to another home", errs.String())
	}
}

func TestAuditDoctorDryRunNeverExecutesComponent(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.base, "executed")
	write(t, f.binary, "#!/bin/sh\ntouch "+quote(marker)+"\n", 0755)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	var out, errs strings.Builder
	code := (CLI{Out: &out, Err: &errs}).Run([]string{"--home", f.home, "doctor", "--exec-check", "--dry-run"})
	if code == 0 {
		t.Fatal("contradictory dry-run was accepted")
	}
	if _, err = os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("dry-run executed a component")
	}
}

func TestAuditLocalOfficialArchiveAndWrongArchitecture(t *testing.T) {
	f := newFixture(t)
	c := newReleaseClient()
	c.OS, c.Arch = "linux", "amd64"
	archive := filepath.Join(f.base, "codex.tar.gz")
	write(t, archive, string(packed(t, "codex-x86_64-unknown-linux-musl", fakeELF("local"))), 0600)
	data, err := c.artifactBytes(Artifact{Source: archive}, "codex")
	if err != nil || string(data) != string(fakeELF("local")) {
		t.Fatal("offline official archive was not recognized", err)
	}
	c.Arch = "arm64"
	if _, err = c.artifactBytes(Artifact{Source: archive}, "codex"); err == nil {
		t.Fatal("installed a local program for another architecture")
	}
}

func TestAuditLatestCannotSilentlyDowngrade(t *testing.T) {
	f := newFixture(t)
	u := newUpstream(t)
	u.version = "1.2.4"
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": "latest"}})
	m.Releases = u.client
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	u.version = "1.2.3"
	m = f.managerFor(t, Options{})
	m.Releases = u.client
	if _, err = m.Update([]string{"codex"}); err == nil {
		t.Fatal("latest silently downgraded the program")
	}
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": "1.2.3"}})
	m.Releases = u.client
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
}

func TestAuditNestedMappingsAndYamlMigrationStayWithinScope(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	m.State.Roots = map[string]Root{"local": {Path: "/old"}}
	m.Roots = map[string]string{"local": "/old/new"}
	config := []byte("[projects.\"/old/project\"]\ntrust_level='trusted'\n[mcp_servers.demo]\ncommand='/old/new/bin/already-current'\n")
	first, err := m.rewriteConfig("codex", config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.rewriteConfig("codex", first)
	if err != nil || string(first) != string(second) {
		t.Fatal("nested mapping cascaded", err)
	}
	yaml := []byte("proxy-providers:\n  demo:\n    path: /old/cache.yaml\ncredentials:\n  path: /old/SECRET\n")
	after, err := m.rewriteConfig("proxy", yaml)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "/old/new/cache.yaml") || !strings.Contains(string(after), "/old/SECRET") {
		t.Fatal("YAML migration crossed schema boundaries")
	}
	shared := []byte("proxy-providers:\n  demo:\n    path: &shared /old/cache.yaml\ncredentials:\n  password: *shared\n")
	if _, err = m.rewriteConfig("proxy", shared); err == nil {
		t.Fatal("rewrote a shared YAML anchor used by credentials")
	}
}

func TestAuditFailedUpdatePreservesConcurrentUserEditAndRecoveryRecord(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf next\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(f.home, "tools", "codex", "codex")
	p.testHook = func(stage string, index int) error {
		if stage == "change" && index == 0 {
			write(t, owned, "LATER-USER-DATA", 0755)
			return os.ErrPermission
		}
		return nil
	}
	if err = Apply(p); err == nil {
		t.Fatal("injected failure was ignored")
	}
	if read(t, owned) != "LATER-USER-DATA" {
		t.Fatal("automatic recovery overwrote the user edit")
	}
	if !regular(filepath.Join(f.home, "pending.json")) {
		t.Fatal("recovery record was lost")
	}
}

func TestAuditUpdateAfterMountMoveRetiresOldPhysicalHistory(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	artifact := filepath.Join(f.base, "next")
	write(t, artifact, "#!/bin/sh\nprintf two\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	newFast := filepath.Join(f.base, "fast-new")
	if err = os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(newFast, ".local", "stk")
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Repair()
	apply(t, p, err)
	write(t, artifact, "#!/bin/sh\nprintf three\n", 0755)
	m, err = New(Options{Home: home, Artifacts: map[string]string{"codex": artifact}})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Update([]string{"codex"})
	apply(t, p, err)
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.Rollback()
	apply(t, p, err)
	if !strings.Contains(read(t, filepath.Join(home, "tools", "codex", "codex")), "printf two") {
		t.Fatal("new update after relocation lost its usable rollback")
	}
	if _, err = os.Lstat(filepath.Dir(f.local)); !os.IsNotExist(err) {
		t.Fatal("update recreated old mount")
	}
}
