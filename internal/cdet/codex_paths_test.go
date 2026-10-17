package cdet

import (
	"os"
	"path/filepath"
	"testing"
)

func officialCodexLinkFixture(t *testing.T) (*Manager, []string) {
	t.Helper()
	m := networkTestManager(t)
	m.Config.Store = m.Store
	packages := filepath.Join(m.Store, dataPath("codex"), "packages", "standalone")
	release := filepath.Join(packages, "releases", "1.2.3-linux-x64")
	write(t, filepath.Join(release, "bin", "codex"), "FAKE-NATIVE-CODEX", 0755)
	write(t, filepath.Join(release, "bin", "codex-code-mode-host"), "FAKE-NATIVE-CODE-MODE", 0755)
	write(t, filepath.Join(m.Store, dataPath("codex"), "state_99999.sqlite"), "OPAQUE-DATABASE", 0600)
	write(t, filepath.Join(m.Store, dataPath("codex"), "auth.json"), "PRIVATE-AUTH-SENTINEL", 0600)
	current := filepath.Join(packages, "current")
	visible := filepath.Join(m.Store, programPath("codex"))
	host := filepath.Join(filepath.Dir(visible), "codex-code-mode-host")
	if err := os.MkdirAll(filepath.Dir(visible), 0700); err != nil {
		t.Fatal(err)
	}
	for path, target := range map[string]string{current: release, visible: filepath.Join(current, "bin", "codex"), host: filepath.Join(current, "bin", "codex-code-mode-host")} {
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	return m, []string{current, visible, host}
}

func TestCodexKnownPackageLinksRelocateWithoutAnInstallerOrDataRewrite(t *testing.T) {
	m, links := officialCodexLinkFixture(t)
	old := m.Store
	original := map[string]string{}
	for _, link := range links {
		value, err := os.Readlink(link)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(old, link)
		original[rel] = value
	}
	m.Store = filepath.Join(filepath.Dir(old), "moved-store")
	if err := os.Rename(old, m.Store); err != nil {
		t.Fatal(err)
	}
	m.Runner = func(CommandSpec) ([]byte, error) {
		t.Fatal("path repair ran an installer or component")
		return nil, nil
	}
	if err := m.codexLinks(false); err != nil {
		t.Fatal("read-only validation could not map broken old absolute links", err)
	}
	for rel, value := range original {
		if got, err := os.Readlink(filepath.Join(m.Store, rel)); err != nil || got != value {
			t.Fatal("read-only validation changed a link", err)
		}
	}
	if err := m.codexLinks(true); err != nil {
		t.Fatal(err)
	}
	for rel := range original {
		path := filepath.Join(m.Store, rel)
		if got, err := os.Readlink(path); err != nil || filepath.IsAbs(got) {
			t.Fatal("known package link was not made relative", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("rebuilt package link still points at the retired mount", err)
		}
	}
	if !absent(old) || read(t, filepath.Join(m.Store, dataPath("codex"), "state_99999.sqlite")) != "OPAQUE-DATABASE" || read(t, filepath.Join(m.Store, dataPath("codex"), "auth.json")) != "PRIVATE-AUTH-SENTINEL" {
		t.Fatal("known-link repair recreated old roots or changed private HOME data")
	}
	if err := m.codexLinks(false); err != nil {
		t.Fatal("relative upstream links no longer validate", err)
	}
}

func TestCodexKnownLinkRepairValidatesEveryTargetBeforeChangingAnyLink(t *testing.T) {
	for _, kind := range []string{"outside", "cycle", "missing", "package-escape"} {
		t.Run(kind, func(t *testing.T) {
			m, links := officialCodexLinkFixture(t)
			visible := filepath.Join(m.Store, programPath("codex"))
			current := links[0]
			target := filepath.Join(filepath.Dir(m.Store), "outside")
			write(t, target, "OUTSIDE-PROGRAM", 0755)
			if kind == "cycle" {
				target = current
				visible = current
			} else if kind == "missing" {
				target = filepath.Join(m.Store, dataPath("codex"), "packages", "standalone", "missing")
			} else if kind == "package-escape" {
				payload := filepath.Join(m.Store, dataPath("codex"), "packages", "standalone", "releases", "1.2.3-linux-x64", "bin", "codex")
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, payload); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(current, "bin", "codex")
			}
			if err := os.Remove(visible); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, visible); err != nil {
				t.Fatal(err)
			}
			before := map[string]string{}
			for _, path := range links {
				before[path], _ = os.Readlink(path)
			}
			if err := m.codexLinks(true); err == nil {
				t.Fatal("invalid official package link was accepted", kind)
			}
			for path, target := range before {
				if got, err := os.Readlink(path); err != nil || got != target {
					t.Fatal("repair changed links before validating the full known set", path, err)
				}
			}
		})
	}
}

func TestCodexKnownLinksPreserveOrdinaryProgramFilesAndUnknownPackageContents(t *testing.T) {
	m := networkTestManager(t)
	program := filepath.Join(m.Store, programPath("codex"))
	write(t, program, "ORDINARY-LOCAL-CODEX", 0755)
	unknown := filepath.Join(m.Store, dataPath("codex"), "packages", "standalone", "current")
	write(t, unknown, "UNUSED-OPAQUE-FILE", 0600)
	for _, repair := range []bool{false, true} {
		if err := m.codexLinks(repair); err != nil {
			t.Fatal(err)
		}
		if read(t, program) != "ORDINARY-LOCAL-CODEX" || read(t, unknown) != "UNUSED-OPAQUE-FILE" {
			t.Fatal("repair rewrote an ordinary file")
		}
	}
}
