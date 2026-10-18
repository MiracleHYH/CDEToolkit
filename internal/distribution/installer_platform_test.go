package distribution

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerRejectsUnsupportedPlatformsBeforeSideEffects(t *testing.T) {
	installer, err := filepath.Abs("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range []struct{ system, architecture string }{
		{"Linux", "arm64"}, {"Linux", "aarch64"}, {"Linux", "riscv64"},
		{"Darwin", "x86_64"}, {"Darwin", "arm64"}, {"FreeBSD", "amd64"},
	} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/dry=%v", platform.system, platform.architecture, dry), func(t *testing.T) {
				dir := t.TempDir()
				for _, name := range []string{"home", "tmp", "bin"} {
					if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
						t.Fatal(err)
					}
				}
				uname := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; *) exit 99 ;; esac\n", platform.system, platform.architecture)
				if err := os.WriteFile(filepath.Join(dir, "bin", "uname"), []byte(uname), 0755); err != nil {
					t.Fatal(err)
				}
				for _, command := range []string{"curl", "tar", "sha256sum", "shasum"} {
					if err := os.WriteFile(filepath.Join(dir, "bin", command), []byte("#!/bin/sh\nprintf forbidden > \"$NIDO_PLATFORM_FORBIDDEN\"\nexit 99\n"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{installer}
				if dry {
					args = append(args, "--dry-run")
				}
				cmd := exec.Command("/bin/sh", args...)
				forbidden := filepath.Join(dir, "forbidden")
				cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp"), "PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "NIDO_PLATFORM_FORBIDDEN=" + forbidden}
				output, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(output), "supported platform: Linux amd64 only") {
					t.Fatalf("unsupported platform accepted: %v %s", err, output)
				}
				if _, err := os.Lstat(forbidden); !os.IsNotExist(err) {
					t.Fatal("unsupported platform reached download or archive tools", err)
				}
				for _, name := range []string{"home", "tmp"} {
					if entries, err := os.ReadDir(filepath.Join(dir, name)); err != nil || len(entries) != 0 {
						t.Fatal("unsupported platform changed destination or temporary files", name, entries, err)
					}
				}
			})
		}
	}
}
