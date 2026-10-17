package distribution

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/cdet"
)

func TestInstallerPublishesOnlyReviewedPOSIXSource(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.sh")
	out := filepath.Join(dir, "dist")
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
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	build := t.TempDir()
	binary := filepath.Join(build, "cdet")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goTool, "build", "-trimpath", "-buildvcs=false", "-ldflags=-X=github.com/MiracleHYH/CDEToolkit/internal/cdet.Version=1.2.3", "-o", binary, "./cmd/cdet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated native CLI: %v: %s", err, out)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	asset, _ := assetName(platform)
	scenarios := []string{"fresh", "repeat", "pinned", "custom-roots", "dry-run", "checksum", "missing-release", "extra-member", "foreign-entry", "foreign-home", "foreign-link", "linked-bin", "version-mismatch", "unsupported-arch"}
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
			managed := filepath.Join(home, ".cdet")
			store := filepath.Join(home, ".cdet-store")
			if scenario == "custom-roots" {
				managed = filepath.Join(home, "custom", "manager")
				store = filepath.Join(home, "custom", "components")
			}
			entry := filepath.Join(managed, "bin", "cdet")
			assets := filepath.Join(dir, "assets")
			if _, err := Pack(binary, assets, "1.2.3", platform); err != nil {
				t.Fatal(err)
			}
			protected := map[string]string{"user/.codex/auth.json": "AUTH-SENTINEL", "user/.codex/config.toml": "CONFIG-SENTINEL", "user/.codex/sessions/one.jsonl": "SESSION-SENTINEL", "user/.codex/state.sqlite": "DATABASE-SENTINEL", "user/.bashrc": "USER-BASHRC", "user/.zshrc": "USER-ZSHRC"}
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
case "$url" in https://github.com/MiracleHYH/CDEToolkit/releases/*/cdet-*) ;; *) exit 90 ;; esac
printf '%s\n' "$url" >> "$CDET_CURL_CALLS"
[ "$CDET_SCENARIO" != missing-release ] || exit 22
cp "$CDET_ASSETS/${url##*/}" "$output"
case "$CDET_SCENARIO:$url" in checksum:*.tar.gz) printf corrupted >> "$output" ;; esac
`, 0755)
			osName := "Linux"
			if runtime.GOOS == "darwin" {
				osName = "Darwin"
			}
			archName := "x86_64"
			if runtime.GOARCH == "arm64" {
				archName = "arm64"
			}
			if scenario == "unsupported-arch" {
				archName = "riscv64"
			}
			write("bin/uname", "#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' "+osName+" ;; -m) printf '%s\\n' "+archName+" ;; *) exit 91 ;; esac\n", 0755)
			for _, name := range []string{"systemctl", "supervisorctl", "sudo", "codex", "mihomo", "gds"} {
				write("bin/"+name, "#!/bin/sh\nprintf forbidden >> \"$CDET_FORBIDDEN\"\nexit 92\n", 0755)
			}
			args := []string{}
			switch scenario {
			case "pinned":
				args = []string{"--version", "1.2.3"}
			case "dry-run":
				args = []string{"--dry-run"}
			case "version-mismatch":
				args = []string{"--version", "1.2.4"}
			case "foreign-entry":
				write("user/.cdet/bin/cdet", "FOREIGN-ENTRY", 0755)
			case "foreign-home":
				write("user/.cdet/keep", "FOREIGN-HOME", 0755)
			case "foreign-link", "linked-bin":
				write("foreign/keep", "FOREIGN-LINK-TARGET", 0600)
				if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "foreign-link" {
					err = os.Symlink(filepath.Join(dir, "foreign", "keep"), entry)
				} else {
					if err = os.Remove(filepath.Dir(entry)); err == nil {
						err = os.Symlink(filepath.Join(dir, "foreign"), filepath.Dir(entry))
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			case "extra-member":
				data := tarFixture(t, []*tar.Header{{Name: "cdet", Typeflag: tar.TypeReg, Mode: 0755, Size: 1, Format: tar.FormatUSTAR}, {Name: "../outside", Typeflag: tar.TypeReg, Mode: 0600, Size: 1, Format: tar.FormatUSTAR}}, nil)
				if err := os.WriteFile(filepath.Join(assets, asset), data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(assets, asset+".sha256"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), asset)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := []string{"HOME=" + home, "CDET_PREFIX=" + managed, "CDET_STORE=" + store, "PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "TMPDIR=" + filepath.Join(dir, "tmp"), "CODEX_HOME=" + filepath.Join(home, ".codex"), "CDET_SCENARIO=" + scenario, "CDET_ASSETS=" + assets, "CDET_CURL_CALLS=" + filepath.Join(dir, "curl-calls"), "CDET_FORBIDDEN=" + filepath.Join(dir, "forbidden")}
			if scenario == "custom-roots" {
				if err := os.MkdirAll(filepath.Dir(managed), 0700); err != nil {
					t.Fatal(err)
				}
				prepare := exec.Command(binary, "--prefix", managed, "--store", store, "init")
				prepare.Env, prepare.Dir = env, dir
				if out, err := prepare.CombinedOutput(); err != nil {
					t.Fatalf("prepare existing custom home: %v: %s", err, out)
				}
			}
			run := func() ([]byte, error) {
				cmd := exec.Command("/bin/sh", append([]string{filepath.Join(root, "scripts", "install.sh")}, args...)...)
				cmd.Dir, cmd.Env = dir, env
				return cmd.CombinedOutput()
			}
			out, err := run()
			success := scenario == "fresh" || scenario == "repeat" || scenario == "pinned" || scenario == "custom-roots" || scenario == "dry-run"
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
				state, err := os.ReadFile(filepath.Join(store, ".cdet", "state.json"))
				var manifest cdet.State
				if err != nil || json.Unmarshal(state, &manifest) != nil || manifest.Schema != cdet.Schema || manifest.Components == nil || len(manifest.Components) != 0 {
					t.Fatal("installer adopted or downloaded a component", string(state), err)
				}
			} else if _, err := os.Lstat(filepath.Join(store, ".cdet", "state.json")); !os.IsNotExist(err) {
				t.Fatal("failed or dry-run install wrote managed metadata")
			}
			if scenario == "foreign-entry" || scenario == "foreign-home" {
				name, want := entry, "FOREIGN-ENTRY"
				if scenario == "foreign-home" {
					name, want = filepath.Join(managed, "keep"), "FOREIGN-HOME"
				}
				data, err := os.ReadFile(name)
				if err != nil || string(data) != want {
					t.Fatal("installer overwrote an unowned file")
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
					t.Fatal("installer started a component/service or extracted an unsafe path")
				}
			}
		})
	}
}
