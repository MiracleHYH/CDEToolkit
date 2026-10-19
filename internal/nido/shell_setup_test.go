package nido

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func shellFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("shell integration runs only with isolated Linux amd64 fixtures")
	}
	m, home := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	m.Env = append(m.Env, "SHELL=/bin/bash", "ZDOTDIR=")
	return m, home
}

func shellCLI(m *Manager, o cliOptions, input string, args ...string) (int, string, string) {
	var out, stderr bytes.Buffer
	reader := strings.NewReader(input)
	// Unit fixtures explicitly provide a scripted UI. Production continues to
	// require a real terminal for prompts; a string reader is not a terminal.
	c := CLI{UI: terminalui.NewScriptDriver(reader, &stderr), In: reader, Out: &out, Err: &stderr}
	code := c.shell(m, o, args)
	return code, out.String(), stderr.String()
}

func TestShellSetupPreservesProfilesAndIsIdempotent(t *testing.T) {
	for _, initial := range []string{"", "# USER\nexport KEEP=literal", "# USER\nexport KEEP=literal\n"} {
		t.Run(initial, func(t *testing.T) {
			m, home := shellFixture(t)
			path := filepath.Join(home, ".bashrc")
			if initial != "" {
				if err := os.WriteFile(path, []byte(initial), 0640); err != nil {
					t.Fatal(err)
				}
			}
			original, _ := os.Lstat(path)
			config, _ := os.ReadFile(filepath.Join(m.Root, "config.toml"))
			env, _ := os.ReadFile(filepath.Join(m.Root, "env.sh"))
			code, out, stderr := shellCLI(m, cliOptions{yes: true, json: true}, "", "setup", "bash")
			if code != 0 || !strings.Contains(out, path) || !strings.Contains(out, "activate") || stderr != "" {
				t.Fatalf("code=%d out=%s err=%s", code, out, stderr)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.HasPrefix(data, []byte(initial)) || bytes.Count(data, []byte(shellBlockStart)) != 1 || !bytes.Contains(data, []byte(". "+shellQuote(filepath.Join(m.Root, "env.sh")))) {
				t.Fatal("invalid shell block or changed user prefix", err, string(data))
			}
			before, _ := os.Stat(path)
			if initial != "" && before.Mode().Perm() != 0640 {
				t.Fatal("existing profile mode changed")
			}
			if original != nil && !sameFileOwner(original, before) {
				t.Fatal("existing profile owner or group changed")
			}
			if stat := before.Sys().(*syscall.Stat_t); stat.Uid != uint32(os.Geteuid()) {
				t.Fatal("new profile is not owned by the current user")
			}
			if code, _, stderr = shellCLI(m, cliOptions{yes: true}, "", "setup", "bash"); code != 0 {
				t.Fatal(stderr)
			}
			after, _ := os.Stat(path)
			got, _ := os.ReadFile(path)
			if !os.SameFile(before, after) || !bytes.Equal(got, data) {
				t.Fatal("repeated registration rewrote the profile")
			}
			for name, want := range map[string][]byte{"config.toml": config, "env.sh": env} {
				if got, _ := os.ReadFile(filepath.Join(m.Root, name)); !bytes.Equal(got, want) {
					t.Fatal("shell setup rewrote Nido configuration", name)
				}
			}
		})
	}
}

func shellAlternateGroup(t *testing.T, info os.FileInfo) int {
	t.Helper()
	original := info.Sys().(*syscall.Stat_t).Gid
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range append(groups, os.Getegid()) {
		if uint32(group) != original {
			return group
		}
	}
	if os.Geteuid() == 0 {
		return int(original) + 1
	}
	t.Skip("a supplementary group is required for chgrp isolation")
	return -1
}

func TestShellSetupPreservesSupplementaryGroup(t *testing.T) {
	m, home := shellFixture(t)
	path := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(path, []byte("USER\n"), 0640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, -1, shellAlternateGroup(t, info)); err != nil {
		t.Fatal(err)
	}
	original, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := shellCLI(m, cliOptions{yes: true}, "", "setup", "bash"); code != 0 {
		t.Fatal(stderr)
	}
	after, err := os.Lstat(path)
	if err != nil || !sameFileOwner(original, after) || after.Mode() != original.Mode() {
		t.Fatal("shell registration lost original metadata", err)
	}
}

func TestShellSetupRejectsForeignProfileOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing a fixture's owner requires root")
	}
	m, home := shellFixture(t)
	path := filepath.Join(home, ".bashrc")
	original := []byte("USER\n")
	if err := os.WriteFile(path, original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 1, -1); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := shellCLI(m, cliOptions{yes: true}, "", "setup", "bash"); code == 0 || !strings.Contains(stderr, "当前用户") {
		t.Fatal("foreign profile owner was accepted", code, stderr)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || !sameFileOwner(before, after) {
		t.Fatal("foreign profile metadata changed", err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
		t.Fatal("foreign profile content changed", err)
	}
}

func TestShellSetupRejectsConfirmationOwnershipChanges(t *testing.T) {
	for _, target := range []string{"profile-group", "parent-group", "profile-owner", "parent-owner"} {
		t.Run(target, func(t *testing.T) {
			changeOwner := strings.HasSuffix(target, "-owner")
			if changeOwner && os.Geteuid() != 0 {
				t.Skip("changing a fixture's owner requires root")
			}
			m, home := shellFixture(t)
			dir := filepath.Join(home, "shell-settings")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			m.Env = append(m.Env, "ZDOTDIR="+dir)
			path := filepath.Join(dir, ".zshrc")
			original := []byte("USER\n")
			if err := os.WriteFile(path, original, 0640); err != nil {
				t.Fatal(err)
			}
			plan, err := m.prepareShell("zsh")
			if err != nil {
				t.Fatal(err)
			}
			changed, info := path, plan.info
			if strings.HasPrefix(target, "parent-") {
				changed, info = dir, plan.parentInfo
			}
			uid, gid := -1, -1
			if changeOwner {
				uid = int(info.Sys().(*syscall.Stat_t).Uid) + 1
				originalUID := int(info.Sys().(*syscall.Stat_t).Uid)
				t.Cleanup(func() {
					if err := os.Chown(changed, originalUID, -1); err != nil {
						t.Error(err)
					}
				})
			} else {
				gid = shellAlternateGroup(t, info)
			}
			if err := os.Chown(changed, uid, gid); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.applyShell(plan); err == nil {
				t.Fatal("confirmation-time ownership change was ignored")
			}
			after, err := os.Lstat(changed)
			if err != nil || !sameFileOwner(before, after) {
				t.Fatal("concurrent ownership change was overwritten", err)
			}
			if changeOwner && strings.HasPrefix(target, "parent-") {
				// A root test with only CAP_CHOWN cannot traverse a foreign
				// 0700 directory. Restore this fixture after checking refusal.
				if err := os.Chown(changed, int(info.Sys().(*syscall.Stat_t).Uid), -1); err != nil {
					t.Fatal(err)
				}
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
				t.Fatal("profile changed after ownership check failed", err)
			}
		})
	}
}

// Alter only the captured metadata to exercise failure without requiring root
// or changing process credentials. A gid of -1 cannot be assigned by chown.
type shellOwnershipInfo struct {
	os.FileInfo
	uid, gid uint32
}

func (s shellOwnershipInfo) Sys() any {
	stat := *s.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid, stat.Gid = s.uid, s.gid
	return &stat
}

func TestShellReplacementCannotDiscardUnpreservableOwnership(t *testing.T) {
	for _, foreignOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "unassignable-group", true: "foreign-owner"}[foreignOwner], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".bashrc")
			original := []byte("USER\n")
			if err := os.WriteFile(path, original, 0640); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			stat := info.Sys().(*syscall.Stat_t)
			owner := shellOwnershipInfo{FileInfo: info, uid: stat.Uid, gid: ^uint32(0)}
			if foreignOwner {
				owner.uid, owner.gid = stat.Uid+1, stat.Gid
			}
			if err := replaceFileOwned(path, []byte("REPLACEMENT\n"), 0640, nil, owner); err == nil {
				t.Fatal("replacement silently changed captured ownership")
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(info, after) || !sameFileOwner(info, after) || info.Mode() != after.Mode() {
				t.Fatal("failed ownership restoration replaced the original profile", err)
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
				t.Fatal("failed ownership restoration changed profile contents", err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatal("failed ownership restoration leaked a candidate file", err)
			}
		})
	}
}

func TestShellSetupSelectionPreviewAndCancellation(t *testing.T) {
	for _, scenario := range []string{"default-shell", "zsh-dotdir", "yes-needs-shell", "unsupported-shell", "preview", "cancel", "eof", "confirm"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := shellFixture(t)
			args, o, input := []string{"setup", "bash"}, cliOptions{}, ""
			path, wantSuccess, wantWrite := filepath.Join(home, ".bashrc"), false, false
			switch scenario {
			case "default-shell":
				args, input, wantSuccess, wantWrite = []string{"setup"}, "y\n", true, true
			case "zsh-dotdir":
				dir := filepath.Join(home, "zsh settings")
				m.Env = append(m.Env, "SHELL=/bin/zsh", "ZDOTDIR="+dir)
				args, input, path, wantSuccess, wantWrite = []string{"setup"}, "y\n", filepath.Join(dir, ".zshrc"), true, true
			case "yes-needs-shell":
				args, o.yes = []string{"setup"}, true
			case "unsupported-shell":
				args, o.yes = []string{"setup", "fish"}, true
			case "preview":
				o.dry, o.json, wantSuccess = true, true, true
			case "cancel":
				input = "n\n"
			case "confirm":
				input, wantSuccess, wantWrite = "y\n", true, true
			}
			code, out, stderr := shellCLI(m, o, input, args...)
			if (code == 0) != wantSuccess {
				t.Fatalf("unexpected result %d %s %s", code, out, stderr)
			}
			if _, err := os.Lstat(path); (err == nil) != wantWrite {
				t.Fatal("unexpected profile write", err)
			}
			if scenario == "preview" && (!strings.Contains(out, path) || stderr != "") {
				t.Fatal("preview prompted or omitted actual profile", out, stderr)
			}
		})
	}
}

func TestShellSetupRejectsUnsafeOrEditedProfiles(t *testing.T) {
	for _, scenario := range []string{"symlink", "hardlink", "directory", "parent-link", "partial-block", "different-block", "duplicate-block", "relative-dotdir", "unclean-dotdir", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := shellFixture(t)
			path := filepath.Join(home, ".zshrc")
			target := filepath.Join(home, "user-target")
			if err := os.WriteFile(target, []byte("USER TARGET\n"), 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "parent-link":
				dir, link := filepath.Join(home, "settings"), filepath.Join(home, "linked")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				m.Env = append(m.Env, "ZDOTDIR="+link)
			case "partial-block":
				if err := os.WriteFile(path, []byte(shellBlockStart+"\nUSER\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "different-block":
				if err := os.WriteFile(path, []byte(shellBlockStart+"\nUSER\n"+shellBlockEnd+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "duplicate-block":
				if err := os.WriteFile(path, []byte(shellBlockStart+"\n"+shellBlockEnd+"\n"+shellBlockStart+"\n"+shellBlockEnd+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "relative-dotdir":
				m.Env = append(m.Env, "ZDOTDIR=relative")
			case "unclean-dotdir":
				m.Env = append(m.Env, "ZDOTDIR="+home+"/../other")
			case "oversize":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			if code, _, stderr := shellCLI(m, cliOptions{yes: true}, "", "setup", "zsh"); code == 0 {
				t.Fatal("unsafe profile accepted", stderr)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("unsafe profile changed")
			}
			if got, _ := os.ReadFile(target); string(got) != "USER TARGET\n" {
				t.Fatal("linked target changed")
			}
		})
	}
}

func TestShellSetupRetainsIdentityAcrossConfirmation(t *testing.T) {
	for _, scenario := range []string{"content", "inode", "mode", "link", "parent", "created"} {
		t.Run(scenario, func(t *testing.T) {
			m, home := shellFixture(t)
			dir := filepath.Join(home, "settings")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			m.Env = append(m.Env, "ZDOTDIR="+dir)
			path := filepath.Join(dir, ".zshrc")
			if scenario != "created" {
				if err := os.WriteFile(path, []byte("USER\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := m.prepareShell("zsh")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "content", "created":
				err = os.WriteFile(path, []byte("EDITED\n"), 0600)
			case "inode":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.WriteFile(path, []byte("USER\n"), 0600)
				}
			case "mode":
				err = os.Chmod(path, 0644)
			case "link":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.Symlink(path+".original", path)
				}
			case "parent":
				err = os.Rename(dir, dir+".original")
				if err == nil {
					err = os.Mkdir(dir, 0700)
				}
				if err == nil {
					err = os.WriteFile(path, []byte("USER\n"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if err := m.applyShell(plan); err == nil {
				t.Fatal("confirmation-time replacement accepted")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, before) {
				t.Fatal("concurrent user edit overwritten")
			}
		})
	}
}

func TestShellSetupQuotesActualEnvironmentPath(t *testing.T) {
	m, home := shellFixture(t)
	root := filepath.Join(filepath.Dir(m.Root), "nido path ' literal $value")
	next, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Init(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(path, []byte("export USER_KEEP=KEEP\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := shellCLI(next, cliOptions{yes: true}, "", "setup", "bash"); code != 0 {
		t.Fatal(stderr)
	}
	// Source only fixture-generated shell data. command -v does not execute Nido.
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", `. "$1"; command -v nido; printf '%s\n' "$USER_KEEP"`, "shell-activation-test", path)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BASH_ENV=/dev/null"}
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != filepath.Join(root, "bin", "nido")+"\nKEEP\n" {
		t.Fatal("quoted profile failed to activate the actual path", err, string(out))
	}
}
