package nido

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReinstallStagesProgramBeforeSavingDownloadPreference(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directory needs an ordinary user")
	}
	m, _ := fixture(t)
	if err := m.InitWithDownload("direct"); err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, name := range []string{"config.toml", "env.sh", "bin/nido"} {
		path := filepath.Join(m.Root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		protected[path] = data
	}
	bin := filepath.Join(m.Root, "bin")
	if err := os.Chmod(bin, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bin, 0700) })
	if err := m.InitWithDownload("https://mirror.invalid"); err == nil {
		t.Fatal("reinstall unexpectedly wrote into a read-only bin")
	}
	if m.Config.Download.GitHubProxy != "direct" {
		t.Fatal("failed staging changed the in-memory preference")
	}
	for path, expected := range protected {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, expected) {
			t.Fatalf("failed staging modified %s: %v", path, err)
		}
	}
}

func TestAtomicWritePreservesReplacedDirectoryOrTemporaryFile(t *testing.T) {
	for _, replaceDirectory := range []bool{false, true} {
		name := "temporary-file"
		if replaceDirectory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "owned")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "target")
			old, user := []byte("OLD ENTRY"), []byte("USER REPLACEMENT")
			if err := os.WriteFile(path, old, 0600); err != nil {
				t.Fatal(err)
			}
			var staged string
			err = replaceFile(path, []byte("CANDIDATE"), 0600, func() error {
				stages, err := filepath.Glob(filepath.Join(dir, ".nido-write-*"))
				if err != nil || len(stages) != 1 {
					t.Fatal("candidate was not prepared before the check", stages, err)
				}
				staged = stages[0]
				if replaceDirectory {
					if err := os.Rename(dir, dir+"-original"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, old, 0600); err != nil {
						t.Fatal(err)
					}
				}
				replacement := filepath.Join(dir, "replacement")
				if err := os.WriteFile(replacement, user, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, staged); err != nil {
					t.Fatal(err)
				}
				return nil
			})
			if err == nil {
				t.Fatal("accepted a replaced directory or temporary file")
			}
			if got, err := os.ReadFile(staged); err != nil || !bytes.Equal(got, user) {
				t.Fatal("failed write cleanup removed an unknown replacement", err)
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, old) {
				t.Fatal("failed write changed the target", err)
			}
		})
	}
}
