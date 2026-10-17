package stk

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitOnlyOwnsManagerAndLeavesComponentsAlone(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Init()
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	if len(m.State.Modules) != 0 || m.selfPath() != "cdet" || read(t, filepath.Join(f.home, "cdet")) != read(t, f.manager) {
		t.Fatal("init adopted a component or did not own the cdet executable")
	}
	if read(t, filepath.Join(f.codex, "auth.json")) != "AUTH-SENTINEL" || read(t, filepath.Join(f.codex, "sessions", "session.jsonl")) != "SESSION-SENTINEL" {
		t.Fatal("init touched application data")
	}
	if _, err := m.Init(); err == nil {
		t.Fatal("init accepted an existing installation instead of requiring update")
	}
}

func TestLegacyManagerKeepsOwnedNameAcrossRepairUpdateAndRollback(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Setup([]string{"codex"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	if err := os.Rename(filepath.Join(f.home, "cdet"), filepath.Join(f.home, "stk")); err != nil {
		t.Fatal(err)
	}
	m.State.Generated["stk"] = m.State.Generated["cdet"]
	delete(m.State.Generated, "cdet")
	metadata, err := m.metadata()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range metadata {
		write(t, filepath.Join(f.home, name), string(data), 0600)
	}
	m = f.managerFor(t, Options{})
	p, err = m.Repair()
	apply(t, p, err)
	if !strings.Contains(read(t, filepath.Join(f.home, "bin", "codex")), "$STK_HOME/stk") {
		t.Fatal("repair lost the legacy owned executable")
	}
	next := filepath.Join(f.base, "next-manager")
	write(t, next, "#!/bin/sh\nprintf cdet-next\\n\n", 0755)
	m = f.managerFor(t, Options{Artifacts: map[string]string{"stk": next}})
	p, err = m.Update([]string{"stk"})
	apply(t, p, err)
	if read(t, filepath.Join(f.home, "stk")) != read(t, next) || regular(filepath.Join(f.home, "cdet")) {
		t.Fatal("update created a second manager instead of respecting legacy ownership")
	}
	m = f.managerFor(t, Options{})
	p, err = m.Rollback()
	apply(t, p, err)
	if read(t, filepath.Join(f.home, "stk")) != read(t, f.manager) || read(t, filepath.Join(f.codex, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("legacy self rollback or authentication preservation failed")
	}
}

func TestCLIUsesCDetAliasesAndHomePrecedence(t *testing.T) {
	f := newFixture(t)
	t.Setenv("CDET_HOME", f.home)
	t.Setenv("STK_HOME", filepath.Join(f.base, "wrong-legacy-home"))
	var out, errors bytes.Buffer
	cli := CLI{Out: &out, Err: &errors}
	if cli.Run([]string{"init"}) != 0 {
		t.Fatal(errors.String())
	}
	next := filepath.Join(f.base, "next")
	write(t, next, "#!/bin/sh\nprintf cdet-next\\n\n", 0755)
	if cli.Run([]string{"update", "cdet", "--install", "cdet=" + next}) != 0 {
		t.Fatal(errors.String())
	}
	if read(t, filepath.Join(f.home, "cdet")) != read(t, next) {
		t.Fatal("cdet self-update alias did not use CDET_HOME")
	}
	if cli.Run([]string{"uninstall", "cdet"}) != 0 || regular(filepath.Join(f.home, "cdet")) {
		t.Fatal("cdet uninstall alias failed", errors.String())
	}
	if read(t, filepath.Join(f.codex, "state.sqlite")) != "DATABASE-SENTINEL" {
		t.Fatal("self uninstall touched component database")
	}
}

func TestCLIConventionalHomesIncludeLegacyAndRelocatedCDToolkit(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CDET_HOME", "")
	t.Setenv("STK_HOME", "")
	opts, _, _, _, err := parse(nil)
	if err != nil || opts.Home != filepath.Join(home, ".local", "cdetoolkit") {
		t.Fatal("unexpected default home", opts.Home, err)
	}
	legacy := filepath.Join(home, ".local", "stk")
	write(t, filepath.Join(legacy, "state.json"), "legacy metadata must be validated by New", 0600)
	opts, _, _, _, err = parse(nil)
	if err != nil || opts.Home != legacy {
		t.Fatal("existing legacy state was silently bypassed", opts.Home, err)
	}
	current := filepath.Join(home, ".local", "cdetoolkit")
	write(t, filepath.Join(current, "config.toml"), "incomplete new installation", 0600)
	opts, _, _, _, err = parse(nil)
	if err != nil || opts.Home != current {
		t.Fatal("incomplete current metadata was silently bypassed", opts.Home, err)
	}
	f := newFixture(t)
	m := f.managerFor(t, Options{Home: filepath.Join(f.local, "cdetoolkit")})
	p, err := m.Init()
	apply(t, p, err)
	located, err := FindHome("", "", []string{f.local})
	if err != nil || located != m.Home {
		t.Fatal("new conventional home was not located", located, err)
	}
}

func TestManagedCommandEntryRelocatesAndUninstallsWithoutTouchingData(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.local, "bin", "cdet")
	m := f.managerFor(t, Options{Entry: entry})
	p, err := m.Init()
	apply(t, p, err)
	newFast := filepath.Join(f.base, "fast-new")
	if err := os.Rename(filepath.Dir(f.local), newFast); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(newFast, ".local", "stk")
	entry = filepath.Join(newFast, ".local", "bin", "cdet")
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	m.Executable = f.manager
	p, err = m.Repair()
	apply(t, p, err)
	if target, err := filepath.EvalSymlinks(entry); err != nil || target != filepath.Join(home, "cdet") {
		t.Fatal("public command did not survive mount relocation", target, err)
	}
	m, err = New(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	p, err = m.Uninstall([]string{"stk"})
	apply(t, p, err)
	if _, err := os.Lstat(entry); !os.IsNotExist(err) {
		t.Fatal("uninstall left the owned public command")
	}
	if read(t, filepath.Join(newFast, ".local", ".codex", "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("entry lifecycle changed authentication")
	}
}

func TestSelfUpdateWithNewPublicEntryCanRetirePreviousUpdate(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, err := m.Init()
	apply(t, p, err)
	next := filepath.Join(f.base, "next")
	write(t, next, "#!/bin/sh\nprintf first\\n\n", 0755)
	entry := filepath.Join(f.local, "bin", "cdet")
	m = f.managerFor(t, Options{Entry: entry, Artifacts: map[string]string{"stk": next}})
	p, err = m.Update([]string{"stk"})
	apply(t, p, err)
	write(t, next, "#!/bin/sh\nprintf second\\n\n", 0755)
	m = f.managerFor(t, Options{Entry: entry, Artifacts: map[string]string{"stk": next}})
	p, err = m.Update([]string{"stk"})
	apply(t, p, err)
	m = f.managerFor(t, Options{})
	p, err = m.Rollback()
	apply(t, p, err)
	if read(t, filepath.Join(f.home, "cdet")) != "#!/bin/sh\nprintf first\\n\n" {
		t.Fatal("self rollback lost the preceding manager")
	}
}

func TestModifiedPublicEntryStopsUninstallBeforeAnyRemoval(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.base, "user", "bin", "cdet")
	m := f.managerFor(t, Options{Entry: entry})
	p, err := m.Init()
	apply(t, p, err)
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	write(t, entry, "USER-REPLACEMENT", 0755)
	m = f.managerFor(t, Options{})
	if _, err := m.Uninstall([]string{"stk"}); err == nil {
		t.Fatal("uninstall accepted a user replacement entry")
	}
	if read(t, entry) != "USER-REPLACEMENT" || !executable(filepath.Join(f.home, "cdet")) {
		t.Fatal("failed uninstall changed the entry or manager")
	}
}

func TestPublicEntryOutsideRelocatedMountIsRebuilt(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.base, "user", "bin", "cdet")
	m := f.managerFor(t, Options{Entry: entry})
	p, err := m.Init()
	apply(t, p, err)
	newFast := filepath.Join(f.base, "new-fast")
	if err := os.Rename(filepath.Dir(f.local), newFast); err != nil {
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
	if actual, err := filepath.EvalSymlinks(entry); err != nil || actual != filepath.Join(home, "cdet") {
		t.Fatal("external public entry was not rebuilt", actual, err)
	}
}
