package distribution

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallerPublishesOnlyReviewedPOSIXSource(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, out := filepath.Join(dir, "source.sh"), filepath.Join(dir, "dist")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nprintf fixture\\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Installer(source, out, false); err != nil {
		t.Fatal(err)
	}
	if err := Installer(source, out, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "install.sh"), []byte("#!/bin/sh\nchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Installer(source, out, true); err == nil {
		t.Fatal("accepted installer differing from audited source")
	}
}

func TestPOSIXInstallerWithIsolatedReleaseAndHomes(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "nido")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goTool, "build", "-trimpath", "-buildvcs=false", "-ldflags=-X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=1.2.3", "-o", binary, "./cmd/nido")
	cmd.Dir, cmd.Env = repo, append(os.Environ(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated native CLI: %v: %s", err, out)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	asset, _ := assetName(platform)
	scenarios := []string{"fresh", "repeat", "pinned", "custom-root", "environment-root", "dry-run", "checksum", "missing-release", "extra-member", "foreign-entry", "foreign-root", "foreign-link", "linked-bin", "version-mismatch", "unsupported-arch", "obsolete-store", "relative-root"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			write := func(relative, text string, mode os.FileMode) {
				t.Helper()
				path := filepath.Join(dir, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), mode); err != nil {
					t.Fatal(err)
				}
			}
			home := filepath.Join(dir, "user")
			managed := filepath.Join(home, ".nido")
			if scenario == "custom-root" || scenario == "environment-root" {
				managed = filepath.Join(home, "custom path", "nido software")
			}
			entry, assets := filepath.Join(managed, "bin", "nido"), filepath.Join(dir, "assets")
			if _, err := Pack(binary, assets, "1.2.3", platform); err != nil {
				t.Fatal(err)
			}
			protected := map[string]string{
				"user/.codex/auth.json": "AUTH-SENTINEL", "user/.codex/config.toml": "CONFIG-SENTINEL",
				"user/.codex/sessions/one.jsonl": "SESSION-SENTINEL", "user/.codex/state.sqlite": "DATABASE-SENTINEL",
				"user/.codex/.env":                "HTTPS_PROXY=http://user-proxy.invalid:7890\nUSER_KEY=KEEP\n",
				"user/.config/mihomo/config.yaml": "EXTERNAL-PROXY-SENTINEL", "user/.bashrc": "USER-BASHRC", "user/.zshrc": "USER-ZSHRC",
			}
			for path, text := range protected {
				write(path, text, 0600)
			}
			write("tmp/keep", "fixture temporary directory", 0600)
			write("bin/curl", `#!/bin/sh
set -eu
output=''
url=''
while [ "$#" -gt 0 ]; do
  case "$1" in --output) output=$2; shift 2 ;; https://*) url=$1; shift ;; *) shift ;; esac
done
case "$url" in https://github.com/MiracleHYH/CDEToolkit/releases/*/nido-*) ;; *) exit 90 ;; esac
printf '%s\n' "$url" >> "$NIDO_CURL_CALLS"
[ "$NIDO_SCENARIO" != missing-release ] || exit 22
cp "$NIDO_ASSETS/${url##*/}" "$output"
case "$NIDO_SCENARIO:$url" in checksum:*.tar.gz) printf corrupted >> "$output" ;; esac
`, 0755)
			osName, archName := "Linux", "x86_64"
			if runtime.GOOS == "darwin" {
				osName = "Darwin"
			}
			if runtime.GOARCH == "arm64" {
				archName = "arm64"
			}
			if scenario == "unsupported-arch" {
				archName = "riscv64"
			}
			write("bin/uname", "#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' "+osName+" ;; -m) printf '%s\\n' "+archName+" ;; *) exit 91 ;; esac\n", 0755)
			for _, name := range []string{"systemctl", "supervisorctl", "sudo", "codex", "mihomo", "gds"} {
				write("bin/"+name, "#!/bin/sh\nprintf forbidden >> \"$NIDO_FORBIDDEN\"\nexit 92\n", 0755)
			}
			var args []string
			switch scenario {
			case "pinned":
				args = []string{"--version", "1.2.3"}
			case "custom-root":
				args = []string{"--root", managed}
			case "dry-run":
				args = []string{"--dry-run"}
			case "version-mismatch":
				args = []string{"--version", "1.2.4"}
			case "obsolete-store":
				args = []string{"--store", filepath.Join(dir, "obsolete")}
			case "relative-root":
				args = []string{"--root", "relative"}
			case "foreign-entry":
				write("user/.nido/bin/nido", "FOREIGN-ENTRY", 0755)
			case "foreign-root":
				write("user/.nido/keep", "FOREIGN-ROOT", 0600)
			case "foreign-link", "linked-bin":
				write("foreign/keep", "FOREIGN-LINK-TARGET", 0600)
				if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "foreign-link" {
					err = os.Symlink(filepath.Join(dir, "foreign", "keep"), entry)
				} else if err = os.Remove(filepath.Dir(entry)); err == nil {
					err = os.Symlink(filepath.Join(dir, "foreign"), filepath.Dir(entry))
				}
				if err != nil {
					t.Fatal(err)
				}
			case "extra-member":
				data := tarFixture(t, []*tar.Header{{Name: "nido", Typeflag: tar.TypeReg, Mode: 0755, Size: 1, Format: tar.FormatUSTAR}, {Name: "../outside", Typeflag: tar.TypeReg, Mode: 0600, Size: 1, Format: tar.FormatUSTAR}}, nil)
				if err := os.WriteFile(filepath.Join(assets, asset), data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(assets, asset+".sha256"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := []string{"HOME=" + home, "PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "TMPDIR=" + filepath.Join(dir, "tmp"),
				"CODEX_HOME=" + filepath.Join(home, ".codex"), "NIDO_SCENARIO=" + scenario, "NIDO_ASSETS=" + assets,
				"NIDO_CURL_CALLS=" + filepath.Join(dir, "curl-calls"), "NIDO_FORBIDDEN=" + filepath.Join(dir, "forbidden")}
			if scenario == "environment-root" {
				env = append(env, "NIDO_HOME="+managed)
			}
			run := func() ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{filepath.Join(repo, "scripts", "install.sh")}, args...)...)
				cmd.Dir, cmd.Env = dir, env
				return cmd.CombinedOutput()
			}
			out, err := run()
			success := scenario == "fresh" || scenario == "repeat" || scenario == "pinned" || scenario == "custom-root" || scenario == "environment-root" || scenario == "dry-run"
			if (err == nil) != success {
				t.Fatalf("unexpected installer outcome: %v: %s", err, out)
			}
			if scenario == "repeat" {
				if out, err := run(); err != nil {
					t.Fatalf("repeat install: %v: %s", err, out)
				}
			}
			if success && scenario != "dry-run" {
				installed, err := os.ReadFile(entry)
				original, oe := os.ReadFile(binary)
				if err != nil || oe != nil || sha256.Sum256(installed) != sha256.Sum256(original) {
					t.Fatal("incorrect installed CLI", err)
				}
				if _, err := os.Stat(filepath.Join(managed, "env.sh")); err != nil {
					t.Fatal("missing generated environment", err)
				}
				status := exec.Command(entry, "--root", managed, "status", "--json")
				status.Env = env
				out, err := status.Output()
				var rows []struct {
					Configured bool `json:"configured"`
				}
				if err != nil || json.Unmarshal(out, &rows) != nil || len(rows) != 3 {
					t.Fatal("invalid fresh status", err, string(out))
				}
				for _, row := range rows {
					if row.Configured {
						t.Fatal("installer configured a component")
					}
				}
				for _, name := range []string{"codex", "proxy", "gds", ".cdet"} {
					if _, err := os.Lstat(filepath.Join(managed, name)); !os.IsNotExist(err) {
						t.Fatal("installer created component data or legacy metadata", name)
					}
				}
			} else if _, err := os.Lstat(filepath.Join(managed, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("failed or dry-run install wrote configuration")
			}
			if scenario == "foreign-entry" || scenario == "foreign-root" {
				name, want := entry, "FOREIGN-ENTRY"
				if scenario == "foreign-root" {
					name, want = filepath.Join(managed, "keep"), "FOREIGN-ROOT"
				}
				data, err := os.ReadFile(name)
				if err != nil || string(data) != want {
					t.Fatal("installer overwrote an unowned file")
				}
				for _, name := range []string{".lock", "env.sh"} {
					if _, err := os.Lstat(filepath.Join(managed, name)); !os.IsNotExist(err) {
						t.Fatal("refused initialization still wrote into an unowned root", name)
					}
				}
			}
			for relative, want := range protected {
				data, err := os.ReadFile(filepath.Join(dir, relative))
				if err != nil || string(data) != want {
					t.Fatal("installer changed existing user data:", relative)
				}
			}
			for _, path := range []string{"forbidden", "outside"} {
				if _, err := os.Lstat(filepath.Join(dir, path)); !os.IsNotExist(err) {
					t.Fatal("installer executed a component or extracted unsafe paths")
				}
			}
			if scenario == "dry-run" {
				if _, err := os.Lstat(filepath.Join(dir, "curl-calls")); !os.IsNotExist(err) {
					t.Fatal("dry-run downloaded release assets")
				}
				if !strings.Contains(string(out), "nido-") || strings.Contains(string(out), "Components:") {
					t.Fatal("dry-run reports obsolete layout", string(out))
				}
			}
		})
	}
}
