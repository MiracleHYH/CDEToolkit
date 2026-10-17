package cdet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlatLayoutRejectsOldNestedStateWithoutChangingTheStore(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	state := f.manager(t, Options{}).State
	state.Schema = 2
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.store, ".cdet", "state.json"), string(raw), 0600)
	write(t, filepath.Join(f.store, "codex", "data", "auth.json"), "OLD-NESTED-HOME", 0600)
	before := tree(t, f.store).RawDigest
	if _, err = New(Options{Prefix: f.prefix, Store: f.store}); err == nil {
		t.Fatal("old nested layout silently became a flat HOME")
	}
	if tree(t, f.store).RawDigest != before {
		t.Fatal("rejecting the old layout changed existing state or data")
	}
}

func TestRepairRejectsIdleDaemonStillUsingThePreviousHome(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex")
	oldHome := filepath.Join(f.store, dataPath("codex"))
	child := startInactiveChild(t, oldHome, "env-daemon")
	next := filepath.Join(f.root, "moved-store")
	if err := os.Rename(f.store, next); err != nil {
		t.Fatal(err)
	}
	f.store = next
	config := read(t, filepath.Join(f.prefix, "config.toml"))
	m := f.manager(t, Options{})
	if !m.Relocated {
		t.Fatal("fixture store did not relocate")
	}
	if _, err := m.Repair(); err == nil || !strings.Contains(err.Error(), "daemon/app-server") {
		t.Fatal("repair ignored an idle daemon with the old HOME", child.Process.Pid, err)
	}
	if read(t, filepath.Join(f.prefix, "config.toml")) != config || !absent(oldHome) {
		t.Fatal("blocked repair rebound configuration or recreated the old HOME")
	}
}
