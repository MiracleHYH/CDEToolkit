package stk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReauditRecoveryBeforeInitialWrites(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	// A process may exit after persisting pending, before creating bin/tools.
	write(t, filepath.Join(f.home, "lock"), "", 0600)
	j := testJournal(t, f.home, p.Instance, p.Changes)
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	if err = Recover(f.home); err != nil {
		t.Fatal("untouched absent targets must not prevent recovery:", err)
	}
	for _, c := range p.Changes {
		fp, err := fingerprint(c.Path)
		if err != nil || fp != c.Before {
			t.Fatal("recovery changed a target that was never installed", c.Path, err)
		}
	}
	if regular(filepath.Join(f.home, "pending.json")) {
		t.Fatal("recovered transaction still pending")
	}
	if read(t, filepath.Join(f.codex, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("recovery changed external credentials")
	}
}

func TestReauditUpdateBackupRetentionIsBounded(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	unknown := filepath.Join(f.home, "backups", "user-note")
	write(t, unknown, "KEEP-UNKNOWN", 0600)
	artifact := filepath.Join(f.base, "codex-next")
	for version := 2; version <= 5; version++ {
		write(t, artifact, fmt.Sprintf("#!/bin/sh\nprintf version-%d\n", version), 0755)
		m = f.managerFor(t, Options{Artifacts: map[string]string{"codex": artifact}})
		p, err = m.Update([]string{"codex"})
		apply(t, p, err)
		j, err := readJournal(filepath.Join(f.home, "last-update.json"), f.home)
		if err != nil {
			t.Fatal(err)
		}
		allowed := map[string]bool{unknown: true}
		for _, b := range j.Entries {
			if b.File != "" {
				allowed[b.File] = true
			}
		}
		assertBackupFiles(t, f.home, allowed)
	}
	m = f.managerFor(t, Options{})
	p, err = m.Rollback()
	apply(t, p, err)
	assertBackupFiles(t, f.home, map[string]bool{unknown: true})
	if read(t, unknown) != "KEEP-UNKNOWN" {
		t.Fatal("cleanup removed unknown content")
	}
}

func assertBackupFiles(t *testing.T, home string, allowed map[string]bool) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(home, "backups"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !allowed[path] {
			t.Errorf("unreferenced backup remained: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReauditRelocationRequiresRepairBeforeExecution(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.base, "controller-called")
	controller := filepath.Join(f.base, "fake-controller")
	write(t, controller, "#!/bin/sh\n: > "+quote(marker)+"\n", 0755)
	m := f.managerFor(t, Options{Services: map[string]Service{"proxy": {
		Backend: "supervisor", Name: "fixture-proxy", Controller: Ref{Path: controller},
	}}})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	newFast := filepath.Join(f.base, "moved-fast")
	if err = os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(newFast, ".local", "stk")
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	if _, _, _, err = m.Command("codex", nil); err == nil {
		t.Error("execution was allowed before rebuilding configuration paths")
	}
	if _, err = m.ServiceAction("proxy", "start"); err == nil {
		t.Error("service start was allowed before repair")
	}
	if _, err = os.Lstat(marker); !os.IsNotExist(err) {
		t.Error("service controller was executed before repair")
	}
	p, err = m.Repair()
	apply(t, p, err)
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = m.Command("codex", nil); err != nil {
		t.Fatal("repaired component is not usable:", err)
	}
}

func TestReauditDisabledProxyDoesNotRouteCodex(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"proxy", "codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	cfg := m.Config.Modules["proxy"]
	cfg.Enabled = false
	m.Config.Modules["proxy"] = cfg
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9001")
	_, _, env, err := m.Command("codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(env, "\n"), "HTTP_PROXY=http://127.0.0.1:9001\n") {
		t.Fatal("disabled managed proxy overrode the inherited proxy")
	}
	m.Config.Modules["codex"] = ModuleConfig{Enabled: true, Endpoint: "http://127.0.0.1:9002"}
	_, _, env, err = m.Command("codex", nil)
	if err != nil || !strings.Contains(strings.Join(env, "\n"), "HTTP_PROXY=http://127.0.0.1:9002\n") {
		t.Fatal("explicit Codex endpoint was lost", err)
	}
}

func TestReauditRollbackConstructionRejectsPostValidationDrift(t *testing.T) {
	for _, corruptBackup := range []bool{false, true} {
		t.Run(map[bool]string{false: "target-edit", true: "backup-edit"}[corruptBackup], func(t *testing.T) {
			f := newFixture(t)
			m := f.managerFor(t, Options{})
			p, err := m.Setup([]string{"codex"})
			apply(t, p, err)
			m = f.managerFor(t, Options{})
			target := filepath.Join(f.home, "env.sh")
			p = &Plan{Home: f.home, Instance: m.State.Instance}
			if err = p.add(target, "write", "fixture update", []byte("updated"), "", 0600); err != nil {
				t.Fatal(err)
			}
			j := testJournal(t, f.home, m.State.Instance, p.Changes)
			write(t, target, "updated", 0600)
			if err = validateRestore(j, f.home, true); err != nil {
				t.Fatal(err)
			}
			// Simulate an edit between the whole-journal preflight and the
			// later per-entry read that constructs the rollback plan.
			path := target
			if corruptBackup {
				path = j.Entries[0].File
			}
			write(t, path, "LATER-USER-CONTENT", 0600)
			reverse := &Plan{Home: f.home, Instance: m.State.Instance}
			if err = appendRollbackChange(reverse, j.Entries[0]); err == nil {
				t.Fatal("later edit was accepted as authorization or restoration data")
			}
			if read(t, path) != "LATER-USER-CONTENT" {
				t.Fatal("planning changed the user's edit")
			}
		})
	}
}

func TestReauditRecoveryDoesNotRecreateMissingOriginalParent(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	target := filepath.Join(f.base, "original-mount", "config")
	write(t, target, "PRESERVE-CONFIG", 0600)
	p = &Plan{Home: f.home}
	if err = p.add(target, "remove", "fixture", nil, "", 0); err != nil {
		t.Fatal(err)
	}
	j := testJournal(t, f.home, m.State.Instance, p.Changes)
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Dir(target)); err != nil {
		t.Fatal(err)
	}
	if err = Recover(f.home); err == nil {
		t.Fatal("recovery recreated a missing original mount")
	}
	if directory(filepath.Dir(target)) || !regular(filepath.Join(f.home, "pending.json")) {
		t.Fatal("missing parent was recreated or recovery record was lost")
	}
	if read(t, j.Entries[0].File) != "PRESERVE-CONFIG" {
		t.Fatal("recovery discarded the only preserved copy")
	}
}

func TestReauditUninstallRejectsLaterEditAndPreservesUnknownData(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{Artifacts: map[string]string{"codex": f.binary}})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	unknown := filepath.Join(f.home, "tools", "codex", "user-data", "experiment")
	write(t, unknown, "EXPERIMENT-SENTINEL", 0600)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"stk"})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(f.home, "bin", "codex")
	original := read(t, wrapper)
	write(t, wrapper, "USER-EDIT", 0755)
	if err = Apply(p); err == nil {
		t.Fatal("uninstall removed a later user edit")
	}
	if read(t, wrapper) != "USER-EDIT" || read(t, unknown) != "EXPERIMENT-SENTINEL" {
		t.Fatal("failed uninstall changed user files")
	}
	write(t, wrapper, original, 0755)
	m = f.managerFor(t, Options{})
	p, err = m.Uninstall([]string{"stk"})
	apply(t, p, err)
	if read(t, unknown) != "EXPERIMENT-SENTINEL" || read(t, filepath.Join(f.codex, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("uninstall changed unknown data or credentials")
	}
}

func TestReauditApplyRejectsForeignInstanceBeforeWriting(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	target := filepath.Join(f.home, "env.sh")
	before := read(t, target)
	id, err := instanceID()
	if err != nil {
		t.Fatal(err)
	}
	p = &Plan{Home: f.home, Instance: id}
	if err = p.add(target, "write", "foreign fixture", []byte("foreign"), "", 0600); err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("plan for a different instance was applied")
	}
	if read(t, target) != before || regular(filepath.Join(f.home, "pending.json")) {
		t.Fatal("foreign plan changed the instance")
	}
}

func TestReauditYAMLSharedMappingsDoNotCrossConfigurationScope(t *testing.T) {
	m := &Manager{State: State{Roots: map[string]Root{"local": {Path: "/old"}}}, Roots: map[string]string{"local": "/new"}}
	for name, source := range map[string]string{
		"provider-table":  "proxy-providers:\n  demo: &shared\n    path: /old/cache/demo\nunknown: *shared\n",
		"providers-table": "proxy-providers: &shared\n  demo:\n    path: /old/cache/demo\nunknown: *shared\n",
		"provider-alias":  "defaults: &shared\n  path: /old/cache/demo\nproxy-providers:\n  demo: *shared\n",
		"providers-alias": "defaults: &shared\n  demo:\n    path: /old/cache/demo\nproxy-providers: *shared\n",
		"path-alias":      "secret: &shared /old/cache/demo\nproxy-providers:\n  demo:\n    path: *shared\n",
		"provider-merge":  "defaults: &shared\n  path: /old/cache/demo\nproxy-providers:\n  demo:\n    <<: *shared\n",
		"providers-merge": "defaults: &shared\n  demo:\n    path: /old/cache/demo\nproxy-providers:\n  <<: *shared\n",
		"root-merge":      "defaults: &shared\n  proxy-providers:\n    demo:\n      path: /old/cache/demo\n<<: *shared\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := m.rewriteConfig("proxy", []byte(source)); err == nil {
				t.Fatal("shared paths were changed or silently omitted instead of requiring explicit migration")
			}
			unchanged := strings.ReplaceAll(source, "/old/cache/demo", "/already-current/cache/demo")
			after, err := m.rewriteConfig("proxy", []byte(unchanged))
			if err != nil || string(after) != unchanged {
				t.Fatal("unrelated shared configuration should remain untouched", err)
			}
		})
	}
}
