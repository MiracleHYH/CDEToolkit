package nido

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/integrations/codex"
)

func TestConfigurationRequiresIdentityAndValidAssociation(t *testing.T) {
	for _, config := range []string{
		"",
		"schema = 1\n",
		"product = 'nido'\n",
		"schema = 1\nproduct = 'nido'\nenv_digest = 'wrong'\n",
		"schema = 1\nproduct = 'nido'\n[codex]\nhome = 'relative'\n",
		"schema = 1\nproduct = 'nido'\n[codex]\ncommand = '/usr/bin/codex'\n",
	} {
		t.Run(config, func(t *testing.T) {
			m, _ := fixture(t)
			if err := os.Mkdir(m.Root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(m.Root, "config.toml")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := New(m.Root); err == nil {
				t.Fatal("unidentified or malformed configuration accepted")
			}
			got, _ := os.ReadFile(path)
			if string(got) != config {
				t.Fatal("read-only validation changed configuration")
			}
		})
	}
}

func TestRootRejectsPATHSeparatorAndControlCharacters(t *testing.T) {
	m, _ := fixture(t)
	for _, suffix := range []string{":second", "\tname", "\x1bname", "\x7fname"} {
		if _, err := New(m.Root + suffix); err == nil {
			t.Fatalf("invalid software directory accepted: %q", suffix)
		}
	}
	aliased := m.Root + "-alias"
	if err := os.Mkdir(m.Root+":target", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(m.Root+":target", aliased); err != nil {
		t.Fatal(err)
	}
	if _, err := New(aliased); err == nil {
		t.Fatal("alias resolved to a PATH separator")
	}
	rootAlias := m.Root + "-filesystem-root"
	if err := os.Symlink(string(filepath.Separator), rootAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := New(rootAlias); err == nil {
		t.Fatal("alias resolved to filesystem root")
	}
}

func TestInitFailureKeepsStableRetryableLock(t *testing.T) {
	m, _ := fixture(t)
	realExecutable := m.Executable
	m.Executable = filepath.Join(m.UserHome, "missing-binary")
	if err := m.Init(); err == nil {
		t.Fatal("missing source binary accepted")
	}
	lockPath := filepath.Join(m.Root, ".lock")
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal("failed init removed stable lock", err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	m.Executable = realExecutable
	if err := m.Init(); err == nil {
		t.Fatal("initialization ignored active lock")
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("failed contender replaced or removed active lock")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := m.Init(); err != nil {
		t.Fatal("failed first init is not retryable", err)
	}
}

func TestManagementLockRejectsSpecialAndLinkedFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "hardlink", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(m.Root, ".lock")
			if err := os.Remove(lock); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(home, "keep")
			if err := os.WriteFile(outside, []byte(lockContents), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "fifo":
				err = syscall.Mkfifo(lock, 0600)
			case "symlink":
				err = os.Symlink(outside, lock)
			case "hardlink":
				err = os.Link(outside, lock)
			case "foreign":
				err = os.WriteFile(lock, []byte("USER"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = m.ConfigureCodex(func(c codex.Config) (codex.Config, error) { called = true; return c, nil })
			if err == nil || called {
				t.Fatalf("unsafe management lock accepted: called=%v err=%v", called, err)
			}
			got, _ := os.ReadFile(outside)
			if string(got) != lockContents {
				t.Fatal("outside lock target changed")
			}
		})
	}
}

func TestManagementRejectsReplacedSoftwareDirectory(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(m.Root, m.Root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(m.Root, 0700); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := m.ConfigureCodex(func(c codex.Config) (codex.Config, error) { called = true; return c, nil }); err == nil || called {
		t.Fatal("replaced root was modified")
	}
	entries, _ := os.ReadDir(m.Root)
	if len(entries) != 0 {
		t.Fatal("replacement root received management files")
	}
}

func TestConfiguredResultValidatedBeforePersistence(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), m.configBytes...)
	if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) {
		return codex.Config{Home: filepath.Join(m.Root, "codex")}, nil
	}); err == nil {
		t.Fatal("external integration saved inside the software root")
	}
	got, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
	if !bytes.Equal(before, got) || m.Config.Codex.Home != "" {
		t.Fatal("rejected association changed intent")
	}
}

func TestAssociationCannotTransferOrDiscardProxyOwnership(t *testing.T) {
	for _, operation := range []string{"discard", "change-home", "detach", "invent"} {
		t.Run(operation, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(home, "codex")
			other := filepath.Join(home, "other-codex")
			for _, path := range []string{external, other} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); err != nil {
				t.Fatal(err)
			}
			if operation != "invent" {
				if err := m.ConfigureProxy("http://127.0.0.1:7890", false); err != nil {
					t.Fatal(err)
				}
			}
			configBefore := append([]byte(nil), m.configBytes...)
			intentBefore := m.Config
			envPath := filepath.Join(external, ".env")
			envBefore, _ := os.ReadFile(envPath)
			err := m.ConfigureCodex(func(c codex.Config) (codex.Config, error) {
				switch operation {
				case "discard":
					c.ProxyBlock = ""
				case "change-home":
					c.Home = other
				case "detach":
					c = codex.Config{}
				case "invent":
					c.ProxyBlock = "unrecorded ownership"
				}
				return c, nil
			})
			if err == nil {
				t.Fatal("association callback changed proxy ownership")
			}
			configAfter, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			envAfter, _ := os.ReadFile(envPath)
			if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(envBefore, envAfter) || m.Config != intentBefore {
				t.Fatal("rejected ownership change modified integration state")
			}
		})
	}
}

func TestDetachWithoutProxyReceiptDoesNotVisitExternalData(t *testing.T) {
	for _, state := range []string{"foreign-env", "removed-home"} {
		t.Run(state, func(t *testing.T) {
			m, home := fixture(t)
			if err := m.Init(); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(home, "codex")
			if err := os.Mkdir(external, 0700); err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureCodex(func(codex.Config) (codex.Config, error) { return m.Codex.Associate(external) }); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(external, ".env")
			want := []byte("# >>> nido proxy >>>\nUSER_PROXY=keep\n")
			if state == "foreign-env" {
				if err := os.WriteFile(path, want, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(external); err != nil {
				t.Fatal(err)
			}
			// Reconstruct the manager as real CLI commands do.
			m, err := New(m.Root)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.ConfigureProxy("", true); err != nil {
				t.Fatal("pure detachment visited external data", err)
			}
			if m.Config.Codex != (codex.Config{}) {
				t.Fatal("association remained")
			}
			if state == "foreign-env" {
				got, _ := os.ReadFile(path)
				if !bytes.Equal(got, want) {
					t.Fatal("detachment changed unowned .env")
				}
			} else if _, err := os.Lstat(external); !os.IsNotExist(err) {
				t.Fatal("detachment recreated missing Codex directory")
			}
		})
	}
}

func TestEnvironmentPrefersAssociatedCommandAndIsIdempotent(t *testing.T) {
	m, home := fixture(t)
	m.Root += " '$() [*]"
	selected := filepath.Join(home, "selected 'bin")
	m.Config.Codex = codex.Config{Home: filepath.Join(home, "codex"), Command: filepath.Join(selected, "codex")}
	script := m.Environment() + m.Environment() + "printf '%s\\n%s\\n' \"$PATH\" \"$CODEX_HOME\""
	cmd := exec.Command("/bin/sh", "-c", script)
	original := "/old/codex:" + selected + ":/usr/bin:/bin"
	cmd.Env = []string{"HOME=" + home, "PATH=" + original}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(m.Root, "bin") + ":" + selected + ":" + original + "\n" + m.Config.Codex.Home + "\n"
	if string(out) != want {
		t.Fatalf("unexpected shell environment:\ngot %q\nwant %q", out, want)
	}
	if strings.Contains(m.Environment(), "CODEX_SQLITE_HOME") {
		t.Fatal("Nido configured private Codex storage")
	}
}

func TestPreparedReplacementPreservesConcurrentEdit(t *testing.T) {
	m, _ := fixture(t)
	if err := os.Mkdir(m.Root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Root, "config.toml")
	old := []byte("ORIGINAL")
	edited := []byte("USER EDIT")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	err := replaceFile(path, []byte("NIDO"), 0600, func() error {
		if err := os.WriteFile(path, edited, 0600); err != nil {
			return err
		}
		return unchanged(path, old)
	})
	if err == nil {
		t.Fatal("edit during write preparation was overwritten")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, edited) {
		t.Fatal("concurrent edit lost")
	}
	entries, _ := os.ReadDir(m.Root)
	if len(entries) != 1 {
		t.Fatal("failed replacement left temporary files")
	}
}
