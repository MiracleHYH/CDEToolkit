package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChangeProxyCompensatesFailedConfigSave(t *testing.T) {
	for _, kind := range []string{"new-file", "existing-file", "replace-block", "clear-block"} {
		t.Run(kind, func(t *testing.T) {
			s, home := fixture(t)
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, ".env")
			c := Config{Home: home}
			original := []byte("# user bytes\r\nTOKEN='private $(literal)'")
			if kind != "new-file" {
				write(t, path, original, 0640)
			}
			if kind == "replace-block" || kind == "clear-block" {
				var err error
				c, err = s.SetProxy(c, "http://127.0.0.1:7890")
				if err != nil {
					t.Fatal(err)
				}
				original = read(t, path)
			}
			auth := filepath.Join(home, "auth.json")
			write(t, auth, []byte("auth unchanged"), 0600)
			endpoint := "http://127.0.0.1:7891"
			if kind == "clear-block" {
				endpoint = "off"
			}
			next, undo, err := s.ChangeProxy(c, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if undo == nil || next.Home != c.Home {
				t.Fatal("missing compensation")
			}
			// Simulate the caller's first config commit failing. The receipt was
			// never committed, so only this invocation's .env edit is restored.
			persist := func(Config) error { return fmt.Errorf("isolated save failure") }
			if persist(next) == nil {
				t.Fatal("fixture did not fail")
			}
			if err = undo(); err != nil {
				t.Fatal(err)
			}
			if err = undo(); err != nil {
				t.Fatalf("compensation not idempotent: %v", err)
			}
			if kind == "new-file" {
				if _, err = os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("new .env not removed")
				}
			} else {
				if !bytes.Equal(read(t, path), original) {
					t.Fatal("did not restore exact original bytes")
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0640 {
					t.Fatal("did not preserve original permissions")
				}
			}
			if string(read(t, auth)) != "auth unchanged" {
				t.Fatal("compensation touched another file")
			}
		})
	}
}

func TestChangeProxyUndoRefusesConcurrentUserChanges(t *testing.T) {
	for _, change := range []string{"append", "replace-identical", "permission", "mtime", "remove", "symlink"} {
		t.Run(change, func(t *testing.T) {
			s, home := fixture(t)
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, ".env")
			_, undo, err := s.ChangeProxy(Config{Home: home}, "http://127.0.0.1:7890")
			if err != nil {
				t.Fatal(err)
			}
			after := read(t, path)
			switch change {
			case "append":
				write(t, path, append(after, []byte("USER=concurrent\n")...), 0600)
			case "replace-identical":
				write(t, path+".user", after, 0600)
				if err = os.Rename(path+".user", path); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if err = os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
			case "mtime":
				future := time.Now().Add(time.Hour)
				if err = os.Chtimes(path, future, future); err != nil {
					t.Fatal(err)
				}
			case "remove":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(s.UserHome, "user-env"), after, 0600)
				if err = os.Symlink(filepath.Join(s.UserHome, "user-env"), path); err != nil {
					t.Fatal(err)
				}
			}
			if err = undo(); err == nil {
				t.Fatal("overrode concurrent user change")
			}
			if change == "remove" {
				if _, err = os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("recreated user-deleted .env")
				}
			} else {
				if _, err = os.Lstat(path); err != nil {
					t.Fatal("removed user file")
				}
				if change == "append" && !bytes.HasSuffix(read(t, path), []byte("USER=concurrent\n")) {
					t.Fatal("lost concurrent bytes")
				}
				if change == "symlink" {
					if info, _ := os.Lstat(path); info.Mode()&os.ModeSymlink == 0 {
						t.Fatal("replaced user symlink")
					}
				}
			}
		})
	}
}

func TestChangeProxyNoOpUndoDoesNotTouchLaterUserFile(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	_, undo, err := s.ChangeProxy(Config{Home: home}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".env")
	write(t, path, []byte("USER=created-later\n"), 0600)
	if err = undo(); err != nil {
		t.Fatal(err)
	}
	if string(read(t, path)) != "USER=created-later\n" {
		t.Fatal("no-op compensation removed later user file")
	}
}

func TestChangeProxyPersistedReceiptStillDetaches(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	c, _, err := s.ChangeProxy(Config{Home: home}, "http://127.0.0.1:7890")
	if err != nil {
		t.Fatal(err)
	}
	// A successfully saved receipt means the caller discards undo, including
	// when refreshing Nido's separate env.sh subsequently fails.
	if c.ProxyBlock == "" {
		t.Fatal("missing durable receipt")
	}
	if err = s.Detach(c); err != nil {
		t.Fatal(err)
	}
	if len(read(t, filepath.Join(home, ".env"))) != 0 {
		t.Fatal("persisted receipt did not clean owned block")
	}
}

func TestProxyWriteRefusesConcurrentMetadataAndOversizedResult(t *testing.T) {
	s, home := fixture(t)
	path := filepath.Join(home, ".env")
	write(t, path, []byte("USER=content\n"), 0600)
	before, err := readEnv(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if err = writeEnv(home, before, []byte("replacement")); err == nil {
		t.Fatal("overwrote concurrent file permission change")
	}
	if string(read(t, path)) != "USER=content\n" {
		t.Fatal("changed original bytes")
	}
	large := bytes.Repeat([]byte("#"), maxEnvSize)
	write(t, path, large, 0600)
	if _, _, err = s.ChangeProxy(Config{Home: home}, "http://127.0.0.1:7890"); err == nil {
		t.Fatal("wrote an unreadably oversized .env")
	}
	if !bytes.Equal(read(t, path), large) {
		t.Fatal("changed oversized input")
	}
}
