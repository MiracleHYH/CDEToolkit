package cdet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundStoreCannotBeClaimedFromAnotherInstallRoot(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex", "proxy")
	before := tree(t, f.store).RawDigest
	other := filepath.Join(f.root, "other-install")
	if _, e := New(Options{Prefix: other, Store: f.store}); e == nil {
		t.Fatal("fresh installation claimed another installation's store")
	}
	if !absent(other) || tree(t, f.store).RawDigest != before {
		t.Fatal("rejected binding modified installation or component data")
	}
}

func TestBootstrapRequiresMatchingLocalInstallIdentity(t *testing.T) {
	for _, mode := range []string{"missing", "different", "permissions", "old-state"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			marker := filepath.Join(f.prefix, ".instance")
			switch mode {
			case "missing":
				if e := os.Remove(marker); e != nil {
					t.Fatal(e)
				}
			case "different":
				write(t, marker, strings.Repeat("c", 32)+"\n", 0600)
			case "permissions":
				if e := os.Chmod(marker, 0644); e != nil {
					t.Fatal(e)
				}
			case "old-state":
				state, _, e := readState(f.store)
				if e != nil {
					t.Fatal(e)
				}
				state.InstallID = ""
				b, e := json.Marshal(state)
				if e != nil {
					t.Fatal(e)
				}
				write(t, filepath.Join(f.store, ".cdet", "state.json"), string(b), 0600)
			}
			before := tree(t, f.store).RawDigest
			if _, e := New(Options{Prefix: f.prefix, Store: f.store}); e == nil {
				t.Fatal("unproven installation identity accepted", mode)
			}
			if tree(t, f.store).RawDigest != before {
				t.Fatal("identity rejection modified store")
			}
		})
	}
}

func TestCopiedMountRequiresExplicitStoreChoice(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex", "proxy")
	old := tree(t, filepath.Join(f.root, "mount")).RawDigest
	newMount := filepath.Join(f.root, "copied-mount")
	if e := copyTree(tree(t, filepath.Join(f.root, "mount")), newMount); e != nil {
		t.Fatal(e)
	}
	prefix, store := filepath.Join(newMount, "cli"), filepath.Join(newMount, "components")
	if _, e := New(Options{Prefix: prefix}); e == nil {
		t.Fatal("silently selected one of two same-identity stores")
	}
	m, e := New(Options{Prefix: prefix, Store: store})
	if e != nil || !m.Relocated {
		t.Fatal("explicit same-identity copy cannot be relocated", e)
	}
	before := tree(t, filepath.Join(store, dataPath("codex"))).RawDigest
	p, e := m.Repair()
	apply(t, p, e)
	m, e = New(Options{Prefix: prefix})
	if e != nil || m.Relocated {
		t.Fatal("new roots not persisted", e)
	}
	path, _, env, e := m.Command("codex", nil)
	if e != nil || !within(store, path) || !contains(env, "CODEX_HOME="+filepath.Join(store, dataPath("codex"))) {
		t.Fatal("relocated runtime depends on original store", e)
	}
	if tree(t, filepath.Join(store, dataPath("codex"))).RawDigest != before || tree(t, filepath.Join(f.root, "mount")).RawDigest != old {
		t.Fatal("relocation changed data or original mount")
	}
}

func TestCodexDatabaseEnvironmentStaysInsideManagedStore(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex")
	external := filepath.Join(f.root, "retired-database")
	write(t, filepath.Join(external, "auth.json"), "EXTERNAL-SENTINEL", 0600)
	before := tree(t, external).RawDigest
	t.Setenv("CODEX_HOME", external)
	t.Setenv("CODEX_SQLITE_HOME", external)
	m := f.manager(t, Options{})
	_, _, env, e := m.Command("codex", nil)
	if e != nil {
		t.Fatal(e)
	}
	data := filepath.Join(f.store, dataPath("codex"))
	if !contains(env, "CODEX_HOME="+data) || !contains(env, "CODEX_SQLITE_HOME="+data) {
		t.Fatal("external Codex data or SQLite environment escaped into managed execution")
	}
	for _, value := range env {
		if value == "CODEX_HOME="+external || value == "CODEX_SQLITE_HOME="+external {
			t.Fatal("stale data environment retained")
		}
	}
	if tree(t, external).RawDigest != before {
		t.Fatal("command construction modified external data")
	}
}

func TestStatusDetectsMissingOrModifiedManagedLaunchers(t *testing.T) {
	for _, mode := range []string{"missing", "modified"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			f.install(t, "codex")
			entry := filepath.Join(f.prefix, "bin", "codex")
			if mode == "missing" {
				if e := os.Remove(entry); e != nil {
					t.Fatal(e)
				}
			} else {
				write(t, entry, "#!/bin/sh\n# user modification\n", 0755)
			}
			before := tree(t, filepath.Join(f.store, dataPath("codex"))).RawDigest
			m := f.manager(t, Options{})
			if m.Status()[0].State == "已安装" {
				t.Fatal("broken command entry reported healthy")
			}
			p, e := m.Repair()
			if mode == "missing" {
				apply(t, p, e)
				if f.manager(t, Options{}).Status()[0].State != "已安装" {
					t.Fatal("missing command entry not repaired")
				}
			} else if e == nil {
				t.Fatal("modified launcher would be replaced")
			}
			if tree(t, filepath.Join(f.store, dataPath("codex"))).RawDigest != before {
				t.Fatal("entry inspection or repair changed data")
			}
		})
	}
}

func TestCodexRuntimeRejectsConfiguredExternalDatabaseWithoutChanges(t *testing.T) {
	for _, setting := range []string{"/retired/mount/.codex", "../outside", ""} {
		t.Run(setting, func(t *testing.T) {
			f := newFixture(t)
			f.init(t)
			f.install(t, "codex")
			data := filepath.Join(f.store, dataPath("codex"))
			write(t, filepath.Join(data, "config.toml"), "sqlite_home = "+quote(setting)+"\n", 0600)
			before := tree(t, data).RawDigest
			m := f.manager(t, Options{})
			if _, _, _, e := m.Command("codex", nil); e == nil {
				t.Fatal("configured external database was accepted")
			}
			if tree(t, data).RawDigest != before {
				t.Fatal("runtime validation modified persistent data")
			}
			write(t, filepath.Join(data, "config.toml"), "sqlite_home = "+quote(data)+"\n", 0600)
			if _, _, _, e := m.Command("codex", nil); e != nil {
				t.Fatal("current managed database location rejected", e)
			}
		})
	}
}
