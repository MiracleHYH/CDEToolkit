package distribution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerRejectsUnsafeGitHubPrefixesBeforeDownload(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "http://mirror.example", "https://user:secret@mirror.example", "https://mirror.example/?secret=value", "https://mirror.example/#fragment", "https://mirror.example/\nnext", "https://mirror.example/with space", "https://mirror.example\\path", "https://mirror.example:0", "https://mirror.example:65536", "https://mirror.example:", "https://", "https:///mirror.example", "socks5://localhost:7890"} {
		t.Run(value, func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", filepath.Join(repo, "scripts", "install.sh"), "--github-proxy", value, "--dry-run")
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "nido:") {
				t.Fatalf("unsafe prefix accepted: %v %s", err, out)
			}
			if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
				t.Fatal("invalid prefix created installation files", err)
			}
		})
	}
}

func TestInstallerGitHubTransportDryRunPriority(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, environment, flag, want string
	}{
		{"default", "", "", "auto"},
		{"environment", "https://mirror.example/", "", "https://mirror.example"},
		{"explicit", "https://unwanted.example", "direct", "direct"},
		{"custom-prefix", "", "https://mirror.example:8443/prefix/", "https://mirror.example:8443/prefix"},
		{"literal-prefix", "", "https://mirror.example/{one,two}/[item]/", "https://mirror.example/{one,two}/[item]"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			args := []string{filepath.Join(repo, "scripts", "install.sh"), "--dry-run"}
			if scenario.flag != "" {
				args = append(args, "--github-proxy", scenario.flag)
			}
			cmd := exec.Command("/bin/sh", args...)
			cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
			if scenario.environment != "" {
				cmd.Env = append(cmd.Env, "NIDO_GITHUB_PROXY="+scenario.environment)
			}
			out, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(out), "GitHub transport: "+scenario.want+"\n") {
				t.Fatalf("wrong transport priority: %v %s", err, out)
			}
		})
	}
}

func TestInstallerRejectsMultilineVersionsAsOneValue(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.2.3\nextra", "extra\n1.2.3", "1.2.3\n1.2.4", "1.2.3\n", "1.2.3\r"} {
		for _, source := range []string{"flag", "environment"} {
			t.Run(source+":"+version, func(t *testing.T) {
				home := t.TempDir()
				args := []string{filepath.Join(repo, "scripts", "install.sh"), "--dry-run"}
				env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
				if source == "flag" {
					args = append(args, "--version", version)
				} else {
					env = append(env, "NIDO_VERSION="+version)
				}
				cmd := exec.Command("/bin/sh", args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "version must be X.Y.Z or latest") {
					t.Fatalf("multiline version accepted: %v %s", err, out)
				}
				if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
					t.Fatal("invalid version created installation data", err)
				}
			})
		}
	}
}

func TestInstallerCleansTempWhenCanonicalizationFails(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user", "tmp", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tmp", "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "curl"), []byte("#!/bin/sh\nprintf forbidden > \"$NIDO_FORBIDDEN\"\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// Fail after mktemp has returned, without changing filesystem permissions
	// or relying on platform-specific access rules for privileged test users.
	cmd := exec.Command("/bin/sh", "-c", `installer=$1
shift
pwd() { return 1; }
. "$installer"`, "nido-canonicalization-test", filepath.Join(repo, "scripts", "install.sh"))
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "user"), "TMPDIR=" + filepath.Join(dir, "tmp"), "PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "NIDO_FORBIDDEN=" + filepath.Join(dir, "forbidden")}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot resolve the installer temporary directory") {
		t.Fatalf("unexpected canonicalization failure: %v %s", err, out)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "tmp")); err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatal("canonicalization failure leaked or removed temporary data", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("canonicalization failure still downloaded a release")
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "user")); err != nil || len(entries) != 0 {
		t.Fatal("canonicalization failure modified the destination", entries, err)
	}
}
