package cdet

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// This fake runner models the upstream package layout, including its absolute
// public symlinks. It never executes a fetched script or a Codex program.
func integratedCodexManager(t *testing.T, f fixture, options Options, installs *int) *Manager {
	t.Helper()
	m := f.manager(t, options)
	m.Runner = func(spec CommandSpec) ([]byte, error) {
		switch spec.Path {
		case "curl":
			return []byte("# SYNTHETIC-OFFICIAL-PACKAGE-INSTALLER"), nil
		case "/bin/sh":
			*installs++
			data, bin := envValue(spec.Env, "CODEX_HOME"), envValue(spec.Env, "CODEX_INSTALL_DIR")
			if data != filepath.Join(m.Store, dataPath("codex")) || bin != filepath.Join(m.Store, ".cdet", "bin") {
				t.Fatal("fake upstream installer escaped the managed roots")
			}
			packages := filepath.Join(data, "packages", "standalone")
			release := filepath.Join(packages, "releases", "1.2.3-isolated-target")
			write(t, filepath.Join(release, "bin", "codex"), string(fakeNative("UPSTREAM-PACKAGED-CODEX")), 0755)
			write(t, filepath.Join(release, "bin", "codex-code-mode-host"), string(fakeNative("UPSTREAM-PACKAGED-HOST")), 0755)
			write(t, filepath.Join(release, "codex-resources", "helper.bin"), "COMPLETE-UPSTREAM-PACKAGE", 0600)
			write(t, filepath.Join(release, "codex-package.json"), "{opaque upstream metadata}", 0600)
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			for path, target := range map[string]string{
				filepath.Join(packages, "current"):         release,
				filepath.Join(bin, "codex"):                filepath.Join(packages, "current", "bin", "codex"),
				filepath.Join(bin, "codex-code-mode-host"): filepath.Join(packages, "current", "bin", "codex-code-mode-host"),
			} {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			return nil, nil
		}
		if len(spec.Args) == 1 && spec.Args[0] == "--version" {
			return []byte("codex-cli 1.2.3\n"), nil
		}
		t.Fatal("integration test attempted an unexpected external command")
		return nil, nil
	}
	return m
}

// Preserve link text exactly when modelling an existing duplicate mount. Using
// the production import copier here would already normalize the links for us.
func copyIntegratedFixtureRaw(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(source, path)
		to := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Mkdir(to, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, to)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegratedCodexPackageLinksSurviveMountChangesAndRemoval(t *testing.T) {
	for _, keepOld := range []bool{false, true} {
		name := "old-mount-absent"
		if keepOld {
			name = "explicit-new-copy-with-old-mount-present"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			installs := 0
			m := integratedCodexManager(t, f, Options{}, &installs)
			p, err := m.Init()
			apply(t, p, err)
			source := tree(t, f.codex).RawDigest
			p, err = m.Install([]string{"codex"})
			apply(t, p, err)
			if installs != 1 || tree(t, f.codex).RawDigest != source {
				t.Fatal("install did not delegate exactly once or changed the original HOME")
			}
			// Model a later official self-update restoring absolute symlinks.
			packages := filepath.Join(f.store, dataPath("codex"), "packages", "standalone")
			current := filepath.Join(packages, "current")
			for path, target := range map[string]string{
				current: filepath.Join(packages, "releases", "1.2.3-isolated-target"),
				filepath.Join(f.store, programPath("codex")):                   filepath.Join(current, "bin", "codex"),
				filepath.Join(f.store, ".cdet", "bin", "codex-code-mode-host"): filepath.Join(current, "bin", "codex-code-mode-host"),
			} {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			oldStore := f.store
			oldDigest := tree(t, oldStore).RawDigest
			f.store = filepath.Join(f.root, "new-mount-components")
			if keepOld {
				copyIntegratedFixtureRaw(t, oldStore, f.store)
			} else if err := os.Rename(oldStore, f.store); err != nil {
				t.Fatal(err)
			}
			m = integratedCodexManager(t, f, Options{Store: f.store}, &installs)
			if !m.Relocated {
				t.Fatal("explicitly selected new storage was not recognized as moved")
			}
			p, err = m.Repair()
			apply(t, p, err)
			if installs != 1 || m.Relocated {
				t.Fatal("repair ran an installer or failed to update the two-root locator")
			}
			for _, visible := range []string{filepath.Join(f.store, programPath("codex")), filepath.Join(f.store, ".cdet", "bin", "codex-code-mode-host")} {
				real, err := filepath.EvalSymlinks(visible)
				if err != nil || !within(filepath.Join(f.store, dataPath("codex"), "packages", "standalone"), real) || within(oldStore, real) {
					t.Fatal("repair still executes from the old mount", visible, real, err)
				}
			}
			data := filepath.Join(f.store, dataPath("codex"))
			beforeRemoval := tree(t, data).RawDigest
			p, err = m.Remove([]string{"codex"})
			apply(t, p, err)
			if tree(t, data).RawDigest != beforeRemoval || !absent(filepath.Join(f.store, programPath("codex"))) || !absent(filepath.Join(f.store, ".cdet", "bin", "codex-code-mode-host")) || m.State.Components["codex"].Installed {
				t.Fatal("uninstall followed package links or changed complete HOME data")
			}
			if keepOld {
				if tree(t, oldStore).RawDigest != oldDigest {
					t.Fatal("explicit new-copy repair/removal touched the old store copy")
				}
			} else if !absent(oldStore) {
				t.Fatal("path rebuilding recreated the retired mount")
			}
			if tree(t, f.codex).RawDigest != source {
				t.Fatal("installation, relocation, or uninstall modified the original HOME")
			}
		})
	}
}
