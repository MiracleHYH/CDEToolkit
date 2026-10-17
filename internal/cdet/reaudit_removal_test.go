package cdet

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestReauditUpdateDoesNotRecreateStoreLostDuringDownload(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "proxy")
	payload := fakeNative("replacement fixture")
	m := f.manager(t, Options{Artifacts: map[string]string{"proxy": "https://fixture.invalid/mihomo"}, Digests: map[string]string{"proxy": digest(payload)}})
	original := m.Releases.HTTP.Transport
	moved := f.store + "-moved-during-download"
	downloaded := false
	m.Releases.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "fixture.invalid" {
			return original.RoundTrip(r)
		}
		downloaded = true
		if err := os.Rename(f.store, moved); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(payload)), Header: http.Header{}}, nil
	})
	p, err := m.Update([]string{"proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("update accepted disappearing storage")
	}
	if !downloaded {
		t.Fatal("fixture did not reach isolated package download")
	}
	if !absent(f.store) {
		t.Fatal("update recreated the retired storage root after the isolated download")
	}
	if read(t, filepath.Join(moved, "proxy", "config.yaml")) == "" {
		t.Fatal("original configuration was lost")
	}
}

func TestReauditInstallRechecksRetainedHomeBeforeOfficialInstaller(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	first := f.manager(t, Options{})
	first.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "/bin/sh" {
			return nil, errors.New("synthetic initial install failure")
		}
		return runCommand(spec)
	}
	p, err := first.Install([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("synthetic failure not observed")
	}
	second := f.manager(t, Options{})
	installerRan := false
	second.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "/bin/sh" {
			installerRan = true
			return nil, os.WriteFile(filepath.Join(envValue(spec.Env, "CODEX_HOME"), "unintended-installer-file"), []byte("SYNTHETIC-WRITE"), 0600)
		}
		return runCommand(spec)
	}
	p, err = second.Install([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(f.store, "codex")
	if err = os.Rename(data, data+"-retained"); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(f.root, "unmanaged-data")
	write(t, filepath.Join(external, "auth.json"), "EXTERNAL-AUTH-SENTINEL", 0600)
	before := tree(t, external).RawDigest
	if err = os.Symlink(external, data); err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("retained HOME replacement accepted")
	}
	if installerRan || tree(t, external).RawDigest != before {
		t.Fatal("official installer ran with a retained HOME replaced by an external symlink")
	}
}

func TestReauditInstallerCannotRecreateInstallRootLostDuringInvocation(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	m := f.manager(t, Options{})
	moved := f.prefix + "-moved-during-installer"
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "/bin/sh" {
			if err := os.Rename(f.prefix, moved); err != nil {
				return nil, err
			}
		}
		return runCommand(spec)
	}
	p, err := m.Install([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("installer accepted a disappearing install root")
	}
	if !absent(f.prefix) {
		t.Fatal("installer completion recreated the retired install root")
	}
	if !regular(filepath.Join(moved, "bin", "cdet")) {
		t.Fatal("original installation was lost")
	}
}

func TestReauditRemoveNeverFollowsComponentProgramLink(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	f.install(t, "codex")
	external := filepath.Join(f.root, "unmanaged-program")
	write(t, external, "EXTERNAL-PROGRAM-SENTINEL", 0755)
	program := filepath.Join(f.store, programPath("codex"))
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, program); err != nil {
		t.Fatal(err)
	}
	data := tree(t, filepath.Join(f.store, "codex")).RawDigest
	p, err := f.manager(t, Options{}).Remove([]string{"codex"})
	apply(t, p, err)
	if read(t, external) != "EXTERNAL-PROGRAM-SENTINEL" || !absent(program) || tree(t, filepath.Join(f.store, "codex")).RawDigest != data {
		t.Fatal("remove followed an external command link or changed the complete HOME")
	}
}

func TestReauditInstallRetryOnlyRepairsEntryAfterProgramWasSaved(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	first := f.manager(t, Options{})
	entry := filepath.Join(f.prefix, "bin", "codex")
	first.Runner = func(spec CommandSpec) ([]byte, error) {
		out, err := runCommand(spec)
		if err == nil && spec.Path == "/bin/sh" {
			err = os.WriteFile(entry, []byte("SYNTHETIC-ENTRY-CONFLICT"), 0755)
		}
		return out, err
	}
	p, err := first.Install([]string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(p); err == nil {
		t.Fatal("synthetic entry publication failure was ignored")
	}
	if !f.manager(t, Options{}).State.Components["codex"].Installed {
		t.Fatal("fixture did not reach the saved program state")
	}
	data := tree(t, filepath.Join(f.store, "codex")).RawDigest
	program := read(t, filepath.Join(f.store, programPath("codex")))
	curlCalls := read(t, filepath.Join(f.root, "curl-calls"))
	if err = os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	second := f.manager(t, Options{})
	second.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "curl" || spec.Path == "/bin/sh" {
			t.Fatal("retry invoked the official installer for an already saved program")
		}
		return nil, errors.New("no component execution is needed to repair an entry")
	}
	p, err = second.Install([]string{"codex"})
	apply(t, p, err)
	if !regular(entry) || read(t, entry) != string(launcher("codex")) {
		t.Fatal("install retry did not rebuild the missing launcher")
	}
	if tree(t, filepath.Join(f.store, "codex")).RawDigest != data || read(t, filepath.Join(f.store, programPath("codex"))) != program || read(t, filepath.Join(f.root, "curl-calls")) != curlCalls {
		t.Fatal("entry retry changed the saved program or complete HOME")
	}
}

func TestReauditFreshCodexRefusesUnknownFixedProgramBeforeInstaller(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	program := filepath.Join(f.store, programPath("codex"))
	write(t, program, "UNREGISTERED-PROGRAM-SENTINEL", 0755)
	m := f.manager(t, Options{Fresh: true})
	installerRan := false
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		if spec.Path == "curl" || spec.Path == "/bin/sh" {
			installerRan = true
		}
		return runCommand(spec)
	}
	p, err := m.Install([]string{"codex"})
	if err == nil {
		err = Apply(p)
	}
	if err == nil {
		t.Fatal("fresh installation accepted an unregistered fixed program")
	}
	if installerRan || read(t, program) != "UNREGISTERED-PROGRAM-SENTINEL" || !absent(filepath.Join(f.root, "curl-calls")) {
		t.Fatal("fresh installation ran the official installer or overwrote the unregistered program")
	}
}

func TestReauditInstallPreflightRejectsUnknownCommandEntry(t *testing.T) {
	f := newFixture(t)
	f.init(t)
	entry := filepath.Join(f.prefix, "bin", "codex")
	write(t, entry, "UNREGISTERED-ENTRY-SENTINEL", 0755)
	if _, err := f.manager(t, Options{}).Install([]string{"codex"}); err == nil {
		t.Fatal("install preview did not reject an unknown command entry before migration or installation")
	}
	if read(t, entry) != "UNREGISTERED-ENTRY-SENTINEL" || !absent(filepath.Join(f.store, "codex")) || !absent(filepath.Join(f.root, "curl-calls")) {
		t.Fatal("entry preflight changed the unknown file or started installation")
	}
}
