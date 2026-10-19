package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectBroadAndControlCharacterHomes(t *testing.T) {
	s, home := fixture(t)
	for _, path := range []string{"/", s.UserHome, filepath.Dir(s.UserHome), home + "\nnext", home + "\x00next", home + "\tpart", home + "\x7fpart"} {
		if _, err := s.Associate(path); err == nil {
			t.Fatalf("associated unsafe path %q", path)
		}
		s.Run = func(string, []string, []string, []byte) ([]byte, error) {
			t.Fatal("unsafe path ran installer")
			return nil, nil
		}
		if _, err := s.Install(Config{Home: path}, nil); err == nil {
			t.Fatalf("installed unsafe path %q", path)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("created an external directory during rejection")
	}
}

func TestInstallRechecksLocationsAfterDownload(t *testing.T) {
	for _, target := range []string{"home-link", "home-replaced", "bin-link", "entry-link"} {
		t.Run(target, func(t *testing.T) {
			s, home := fixture(t)
			write(t, filepath.Join(home, "auth.json"), []byte("untouched"), 0600)
			bin := filepath.Join(s.UserHome, ".local", "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			c := Config{Home: home}
			if target == "entry-link" {
				command := filepath.Join(bin, "codex")
				mockStandalone(t, home, command, []byte("original binary"))
				c.Command, c.Channel = command, "standalone"
			}
			calls := 0
			s.Run = func(path string, _ []string, _ []string, _ []byte) ([]byte, error) {
				calls++
				if path != "curl" {
					t.Fatal("ran script after target changed")
				}
				switch target {
				case "home-link":
					if err := os.Rename(home, home+"-saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(s.Root, home); err != nil {
						t.Fatal(err)
					}
				case "home-replaced":
					if err := os.Rename(home, home+"-saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(home, 0700); err != nil {
						t.Fatal(err)
					}
				case "bin-link":
					if err := os.Rename(bin, bin+"-saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(s.Root, bin); err != nil {
						t.Fatal(err)
					}
				case "entry-link":
					if err := os.Rename(c.Command, c.Command+"-saved"); err != nil {
						t.Fatal(err)
					}
					write(t, filepath.Join(s.Root, "private-binary"), []byte("private"), 0700)
					if err := os.Symlink(filepath.Join(s.Root, "private-binary"), c.Command); err != nil {
						t.Fatal(err)
					}
				}
				return []byte("mock script"), nil
			}
			var err error
			if target == "entry-link" {
				_, err = s.Update(c, nil)
			} else {
				_, err = s.Install(c, nil)
			}
			if err == nil {
				t.Fatal("accepted changed install target")
			}
			if calls != 1 {
				t.Fatal("unexpected operations")
			}
			authHome := home
			if strings.HasPrefix(target, "home-") {
				authHome = home + "-saved"
			}
			if string(read(t, filepath.Join(authHome, "auth.json"))) != "untouched" {
				t.Fatal("changed existing auth")
			}
		})
	}
}

func TestStatusRediscoversCommandWithoutSavingIt(t *testing.T) {
	s, home := fixture(t)
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	c, err := s.Associate(home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Command != "" {
		t.Fatal("fixture unexpectedly has Codex")
	}
	bin := filepath.Join(s.UserHome, "later-bin")
	command := filepath.Join(bin, "codex")
	write(t, command, []byte("mock"), 0700)
	s.Env = []string{"PATH=" + bin}
	s.Run = func(path string, args []string, _ []string, _ []byte) ([]byte, error) {
		if path != command || len(args) != 1 || args[0] != "--version" {
			t.Fatal("wrong command rediscovery")
		}
		return []byte("codex 1.2.3\n"), nil
	}
	st := s.Status(c)
	if st.Command != command || st.Version != "1.2.3" || c.Command != "" {
		t.Fatalf("bad dynamic state: %+v %+v", st, c)
	}
}
