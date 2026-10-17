package stk

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellHookBeforeGuardAndAfterMountMove(t *testing.T) {
	f := newFixture(t)
	shell := filepath.Join(f.base, "user", ".bashrc")
	body := "[ -z \"$PS1\" ] && return\n# USER-SENTINEL\nalias custom='true'\n"
	write(t, shell, body, 0644)
	m := f.managerFor(t, Options{ShellFiles: []string{shell}})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	if !strings.HasSuffix(read(t, shell), body) {
		t.Fatal("user shell body changed")
	}
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", ". "+quote(shell)+"; command -v codex")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + filepath.Dir(shell)}
	out, e := cmd.CombinedOutput()
	if e != nil || strings.TrimSpace(string(out)) != filepath.Join(f.home, "bin", "codex") {
		t.Fatal(string(out), e)
	}
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
	if strings.Contains(read(t, shell), f.home) || !strings.HasSuffix(read(t, shell), body) {
		t.Fatal("shell relocation changed body or kept old path")
	}
	m, e = New(Options{Home: m.Home})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	p, e = m.Repair()
	if e != nil || len(p.Changes) != 0 {
		t.Fatal("shell repair not idempotent", e)
	}
}

func TestServiceDefinitionMigrationOnlyTouchesSelectedSection(t *testing.T) {
	f := newFixture(t)
	definition := filepath.Join(f.local, "supervisor", "programs.conf")
	original := "[program:existing-mihomo]\ncommand='" + f.proxy + "' -f '" + f.config + "'\ndirectory=" + f.local + "\nenvironment=TOKEN=\"" + f.local + "/SECRET\"\n[program:sshd]\ncommand=" + f.local + "/do-not-touch\n"
	write(t, definition, original, 0600)
	m := f.managerFor(t, Options{Services: map[string]Service{"proxy": {Backend: "supervisor", Name: "existing-mihomo", Definition: Ref{Path: definition}}}})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
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
	after := read(t, filepath.Join(newFast, ".local", "supervisor", "programs.conf"))
	if !strings.Contains(after, newFast+"/.local/external/mihomo") || !strings.Contains(after, "TOKEN=\""+f.local+"/SECRET") || !strings.Contains(after, "command="+f.local+"/do-not-touch") {
		t.Fatal("service migration crossed scope", after)
	}
}

func TestInterruptedOperationRecoveryAndDriftProtection(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	path := filepath.Join(f.home, "env.sh")
	before := read(t, path)
	p = &Plan{Home: f.home}
	if e = p.add(path, "write", "test", []byte("after"), "", 0600); e != nil {
		t.Fatal(e)
	}
	m = f.managerFor(t, Options{})
	j := testJournal(t, f.home, m.State.Instance, p.Changes)
	raw, _ := json.Marshal(j)
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	write(t, path, "after", 0600)
	if e = Recover(f.home); e != nil {
		t.Fatal(e)
	}
	if read(t, path) != before {
		t.Fatal("did not recover")
	}
	write(t, filepath.Join(f.home, "pending.json"), string(raw), 0600)
	write(t, path, "external edit", 0600)
	if e = Recover(f.home); e == nil {
		t.Fatal("recovery overwrote later user edit")
	}
}

func TestConfiguredRootOverridesOldHintAndDoctorJSONFails(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
	newRoot := filepath.Join(f.base, "new-root")
	if e = os.Rename(f.local, newRoot); e != nil {
		t.Fatal(e)
	}
	newHome := filepath.Join(newRoot, "stk")
	cfgPath := filepath.Join(newHome, "config.toml")
	write(t, cfgPath, strings.ReplaceAll(read(t, cfgPath), f.local, newRoot), 0600)
	m, e = New(Options{Home: newHome})
	if e != nil {
		t.Fatal(e)
	}
	if m.Roots["local"] != newRoot {
		t.Fatal("config override ignored")
	}
	if e = os.Remove(filepath.Join(newRoot, "external", "mihomo")); e != nil {
		t.Fatal(e)
	}
	var out, errs bytes.Buffer
	if code := (CLI{&out, &errs}).Run([]string{"--home", newHome, "doctor", "--json"}); code == 0 {
		t.Fatal("doctor --json hid failure")
	}
}

func TestReadOnlyDryRunDoesNotRunComponent(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.base, "executed")
	write(t, f.binary, "#!/bin/sh\ntouch "+quote(marker)+"\n", 0755)
	m := f.managerFor(t, Options{DryRun: true})
	if _, e := m.Setup([]string{"codex"}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("discovery ran component")
	}
	if _, e := os.Stat(f.home); !os.IsNotExist(e) {
		t.Fatal("dry run created state")
	}
}

func TestRelocateBinaryByIdentity(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"proxy"})
	apply(t, p, e)
	newDir := filepath.Join(f.base, "moved-component")
	if e = os.MkdirAll(newDir, 0700); e != nil {
		t.Fatal(e)
	}
	newBin := filepath.Join(newDir, "mihomo")
	if e = os.Rename(f.proxy, newBin); e != nil {
		t.Fatal(e)
	}
	m, e = New(Options{Home: f.home, Search: []string{newDir}})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	p, e = m.Repair()
	apply(t, p, e)
	m, e = New(Options{Home: f.home})
	if e != nil {
		t.Fatal(e)
	}
	resolved, _ := m.resolve(m.State.Modules["proxy"].Binary)
	if resolved != newBin {
		t.Fatal("binary identity relocation failed")
	}
}

func TestRejectExternalLinkEdit(t *testing.T) {
	f := newFixture(t)
	entry := filepath.Join(f.codex, "bin", "codex")
	if e := os.MkdirAll(filepath.Dir(entry), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("/old/.codex/packages/standalone/current/bin/codex", entry); e != nil {
		t.Fatal(e)
	}
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	if e = os.Remove(entry); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("/foreign/other", entry); e != nil {
		t.Fatal(e)
	}
	m = f.managerFor(t, Options{})
	if _, e = m.Repair(); e == nil {
		t.Fatal("overwrote user-modified component link")
	}
}

func TestIndividualCodexHomeCanBeExplicitlyRelocated(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	newHome := filepath.Join(f.local, "moved-codex")
	if e = os.Rename(f.codex, newHome); e != nil {
		t.Fatal(e)
	}
	m, e = New(Options{Home: f.home, CodexHome: newHome})
	if e != nil {
		t.Fatal(e)
	}
	m.Executable = f.manager
	p, e = m.Repair()
	apply(t, p, e)
	m, e = New(Options{Home: f.home})
	if e != nil {
		t.Fatal(e)
	}
	home, _ := m.resolve(m.State.Modules["codex"].Home)
	if home != newHome || read(t, filepath.Join(home, "auth.json")) != "AUTH-SENTINEL" {
		t.Fatal("individual home relocation failed")
	}
}

func TestFindHomeRefusesDuplicateInstances(t *testing.T) {
	f := newFixture(t)
	m := f.managerFor(t, Options{})
	p, e := m.Setup([]string{"codex"})
	apply(t, p, e)
	other := filepath.Join(f.base, "other", ".local", "stk")
	write(t, filepath.Join(other, "state.json"), read(t, filepath.Join(f.home, "state.json")), 0600)
	if _, e = FindHome("", "", []string{filepath.Dir(f.local), filepath.Join(f.base, "other")}); e == nil {
		t.Fatal("duplicate instances selected silently")
	}
}

func TestGDSIsExplicitlyOptionalAndNeverStartedBySetup(t *testing.T) {
	f := newFixture(t)
	program := filepath.Join(f.local, "optional", "gds.sh")
	marker := filepath.Join(f.base, "gds-started")
	write(t, program, "#!/bin/sh\ntouch "+quote(marker)+"\n", 0755)
	m := f.managerFor(t, Options{Binaries: map[string]string{"gds": program}})
	p, e := m.Setup([]string{"gds"})
	apply(t, p, e)
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("GDS was executed")
	}
	m, e = New(Options{Home: f.home})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := m.State.Modules["gds"]; !ok {
		t.Fatal("explicit GDS selection not recorded")
	}
}
