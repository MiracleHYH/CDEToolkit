package distribution

import (
	"archive/tar"
	"bytes"
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

	"github.com/pelletier/go-toml/v2"
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
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("installer execution is supported only in isolated Linux amd64")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "nido")
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(goTool, "build", "-trimpath", "-buildvcs=false", "-ldflags=-X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=1.2.3", "-o", binary, "./cmd/nido")
	cmd.Dir, cmd.Env = repo, append(os.Environ(), "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "GOAMD64=v1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated native CLI: %v: %s", err, out)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	elevatedBaseline := bytes.ReplaceAll(data, []byte("GOAMD64=v1"), []byte("GOAMD64=v3"))
	if bytes.Equal(data, elevatedBaseline) || checkBinary(elevatedBaseline, "1.2.3", platform) == nil {
		t.Fatal("release validation accepted a raised AMD64 CPU baseline")
	}
	asset, _ := assetName(platform)
	packedRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packed := filepath.Join(packedRoot, "release")
	if _, err := Pack(binary, packed, "1.2.3", platform); err != nil {
		t.Fatal(err)
	}
	// Every scenario gets an independent copy of the same reviewed release.
	// Compressing this identical native binary for every fixture adds no
	// coverage and is especially expensive when the test runs under race.
	releaseFiles := map[string][]byte{}
	for _, name := range []string{asset, asset + ".sha256"} {
		data, err := os.ReadFile(filepath.Join(packed, name))
		if err != nil {
			t.Fatal(err)
		}
		releaseFiles[name] = data
	}
	scenarios := []string{"fresh", "repeat", "repeat-bin-link", "pinned", "custom-root", "environment-root", "dry-run", "checksum", "missing-release", "extra-member", "foreign-entry", "foreign-root", "foreign-link", "linked-bin", "version-mismatch", "unsupported-arch", "obsolete-store", "relative-root", "expanded-limit", "listing-failure", "listing-overflow", "listing-extra-newline", "candidate-multiline-version", "github-direct-fails", "github-auto-slow", "github-auto-fallback", "github-auto-second", "github-custom", "github-env", "github-flag-overrides-env", "github-custom-path", "github-policy-preserved"}
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
			if err := os.MkdirAll(assets, 0700); err != nil {
				t.Fatal(err)
			}
			for name, data := range releaseFiles {
				if err := os.WriteFile(filepath.Join(assets, name), data, 0600); err != nil {
					t.Fatal(err)
				}
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
connect=''
maximum=''
literal=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --globoff) literal=1; shift ;;
    --output) output=$2; shift 2 ;;
    --connect-timeout) connect=$2; shift 2 ;;
    --max-time) maximum=$2; shift 2 ;;
    https://*) url=$1; shift ;;
    *) shift ;;
  esac
done
[ "$literal" = 1 ] || exit 95
printf '%s\n' "$url" >> "$NIDO_CURL_CALLS"
printf '%s:%s:%s\n' "$connect" "$maximum" "$url" >> "$NIDO_CURL_LIMITS"
case "$NIDO_SCENARIO:$url" in
  github-auto-fallback:https://github.com/*|github-auto-second:https://github.com/*|github-auto-second:https://gh-proxy.org/*|github-direct-fails:https://github.com/*) exit 28 ;;
  github-auto-slow:https://github.com/*) [ "$maximum" -ge 30 ] || exit 28 ;;
esac
official=$url
case "$url" in
  https://gh-proxy.org/*) official=${url#https://gh-proxy.org/} ;;
  https://ghfast.top/*) official=${url#https://ghfast.top/} ;;
  https://mirror.example/prefix/*) official=${url#https://mirror.example/prefix/} ;;
  https://mirror.example/*) official=${url#https://mirror.example/} ;;
esac
case "$official" in https://github.com/MiracleHYH/CDEToolkit/releases/*/nido-*) ;; *) exit 90 ;; esac
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
			if scenario == "expanded-limit" || strings.HasPrefix(scenario, "listing-") || scenario == "candidate-multiline-version" {
				// Simulate an archive whose sole member expands beyond the test
				// limit, without writing hundreds of MiB to the test filesystem.
				write("bin/tar", `#!/bin/sh
set -eu
case "$1" in
  -tzf)
    case "$NIDO_SCENARIO" in
      listing-failure) printf 'nido\n'; exit 2 ;;
      listing-overflow) dd if=/dev/zero bs=1024 count=64 2>/dev/null ;;
      listing-extra-newline) printf 'nido\n\n' ;;
      *) printf 'nido\n' ;;
    esac ;;
  -xOzf)
    printf 'extracted\n' > "$NIDO_EXTRACT_CALLED"
    if [ "$NIDO_SCENARIO" = expanded-limit ]; then
      printf '#!/bin/sh\nprintf forbidden > "$NIDO_FORBIDDEN"\nexit 0\n'
      dd if=/dev/zero bs=1024 count=64 2>/dev/null
    else
      printf '#!/bin/sh\ncase "$1" in\nversion) printf "nido 1.2.3\\n"; '
      [ "$NIDO_SCENARIO" != candidate-multiline-version ] || printf 'printf "unexpected\\n"; '
      printf ';;\n*) printf forbidden > "$NIDO_FORBIDDEN" ;;\nesac\n'
    fi ;;
  *) exit 94 ;;
esac
`, 0755)
			}
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
			case "github-direct-fails", "github-flag-overrides-env":
				args = []string{"--github-proxy", "direct"}
			case "github-auto-slow", "github-auto-fallback", "github-auto-second":
				args = []string{"--github-proxy", "auto"}
			case "github-custom", "github-policy-preserved", "repeat-bin-link":
				args = []string{"--github-proxy", "https://gh-proxy.org"}
			case "github-custom-path":
				args = []string{"--github-proxy", "https://mirror.example/prefix/"}
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
				"NIDO_CURL_CALLS=" + filepath.Join(dir, "curl-calls"), "NIDO_CURL_LIMITS=" + filepath.Join(dir, "curl-limits"), "NIDO_FORBIDDEN=" + filepath.Join(dir, "forbidden")}
			env = append(env, "NIDO_LIMIT_CALLED="+filepath.Join(dir, "extraction-limit"))
			env = append(env, "NIDO_EXTRACT_CALLED="+filepath.Join(dir, "extraction-called"))
			if scenario == "environment-root" {
				env = append(env, "NIDO_HOME="+managed)
			}
			if scenario == "github-env" || scenario == "github-flag-overrides-env" {
				env = append(env, "NIDO_GITHUB_PROXY=https://mirror.example")
			}
			run := func() ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				shellArgs := append([]string{filepath.Join(repo, "scripts", "install.sh")}, args...)
				if scenario == "expanded-limit" {
					// Keep the production limit assertion while substituting a
					// tiny OS-enforced limit in the extraction subshell only.
					shellArgs = append([]string{"-c", `installer=$1
shift
ulimit() {
  case "$1:$2" in
    '-f:1') command ulimit -f 1 ;;
    '-f:524288') printf 'limited\n' > "$NIDO_LIMIT_CALLED"; command ulimit -f 32 ;;
    *) exit 93 ;;
  esac
}
. "$installer"`, "nido-installer-test"}, shellArgs...)
				}
				cmd := exec.CommandContext(ctx, "/bin/sh", shellArgs...)
				cmd.Dir, cmd.Env = dir, env
				return cmd.CombinedOutput()
			}
			out, err := run()
			success := scenario == "fresh" || scenario == "repeat" || scenario == "repeat-bin-link" || scenario == "pinned" || scenario == "custom-root" || scenario == "environment-root" || scenario == "dry-run" || strings.HasPrefix(scenario, "github-") && scenario != "github-direct-fails"
			if (err == nil) != success {
				t.Fatalf("unexpected installer outcome: %v: %s", err, out)
			}
			if scenario == "expanded-limit" {
				if called, e := os.ReadFile(filepath.Join(dir, "extraction-limit")); e != nil || string(called) != "limited\n" || !strings.Contains(string(out), "extraction failed") {
					t.Fatalf("archive expansion was not bounded: %v %s", e, out)
				}
			}
			if strings.HasPrefix(scenario, "listing-") {
				message := "cannot list release archive"
				if scenario == "listing-extra-newline" {
					message = "release must contain only nido"
				}
				if _, e := os.Lstat(filepath.Join(dir, "extraction-called")); !os.IsNotExist(e) || !strings.Contains(string(out), message) {
					t.Fatalf("failed/oversized tar listing still allowed extraction: %v %s", e, out)
				}
			}
			if scenario == "candidate-multiline-version" && !strings.Contains(string(out), "not a stable release executable") {
				t.Fatalf("multiline candidate version was accepted: %s", out)
			}
			if scenario == "repeat" {
				if out, err := run(); err != nil {
					t.Fatalf("repeat install: %v: %s", err, out)
				}
				before := map[string][]byte{}
				for _, name := range []string{"config.toml", "env.sh", "bin/nido"} {
					var e error
					before[name], e = os.ReadFile(filepath.Join(managed, name))
					if e != nil {
						t.Fatal(e)
					}
				}
				for i, value := range env {
					if strings.HasPrefix(value, "NIDO_SCENARIO=") {
						env[i] = "NIDO_SCENARIO=checksum"
					}
				}
				if output, e := run(); e == nil || !strings.Contains(string(output), "SHA-256 mismatch") {
					t.Fatalf("corrupted repeated install succeeded: %v %s", e, output)
				}
				for name, original := range before {
					current, e := os.ReadFile(filepath.Join(managed, name))
					if e != nil || string(current) != string(original) {
						t.Fatal("failed repeated install changed managed files", name, e)
					}
				}
			}
			if scenario == "repeat-bin-link" {
				before := map[string][]byte{}
				for _, name := range []string{"config.toml", "env.sh", "bin/nido"} {
					var e error
					before[name], e = os.ReadFile(filepath.Join(managed, name))
					if e != nil {
						t.Fatal(e)
					}
				}
				bin, saved := filepath.Join(managed, "bin"), filepath.Join(dir, "outside-saved")
				if err := os.Rename(bin, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(saved, bin); err != nil {
					t.Fatal(err)
				}
				args = []string{"--github-proxy", "direct"}
				if output, e := run(); e == nil {
					t.Fatalf("repeated install through a bin symlink succeeded: %s", output)
				}
				for name, original := range before {
					current, e := os.ReadFile(filepath.Join(managed, name))
					if e != nil || string(current) != string(original) {
						t.Fatal("failed repeat changed program, configuration or environment", name, e)
					}
				}
				if entries, e := os.ReadDir(saved); e != nil || len(entries) != 1 || entries[0].Name() != "nido" {
					t.Fatal("refused repeat wrote into the symlink target", entries, e)
				}
				if info, e := os.Lstat(bin); e != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("refused repeat replaced the user's symlink", e)
				}
				// Restore only this fixture's layout for common read-only checks.
				if err := os.Remove(bin); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(saved, bin); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "github-policy-preserved" {
				before, e := os.ReadFile(filepath.Join(managed, "config.toml"))
				if e != nil {
					t.Fatal(e)
				}
				args = nil // Default bootstrap downloads must not reset saved policy.
				if output, e := run(); e != nil {
					t.Fatalf("repeat without transport override: %v: %s", e, output)
				}
				after, e := os.ReadFile(filepath.Join(managed, "config.toml"))
				if e != nil || string(before) != string(after) {
					t.Fatal("default repeated install overwrote persisted preferences", e)
				}
			}
			if strings.HasPrefix(scenario, "github-") {
				calls, e := os.ReadFile(filepath.Join(dir, "curl-calls"))
				if e != nil {
					t.Fatal(e)
				}
				lines := strings.Fields(string(calls))
				wantPrefix, wantCount := "https://github.com/", 2
				policy := "direct"
				switch scenario {
				case "github-direct-fails":
					wantCount = 1
				case "github-auto-fallback":
					policy, wantCount = "auto", 4
				case "github-auto-second":
					policy, wantCount = "auto", 6
				case "github-auto-slow":
					policy = "auto"
				case "github-custom", "github-policy-preserved":
					policy, wantPrefix = "https://gh-proxy.org", "https://gh-proxy.org/https://github.com/"
					if scenario == "github-policy-preserved" {
						wantCount = 4
					}
				case "github-env":
					policy, wantPrefix = "https://mirror.example", "https://mirror.example/https://github.com/"
				case "github-custom-path":
					policy, wantPrefix = "https://mirror.example/prefix", "https://mirror.example/prefix/https://github.com/"
				}
				if len(lines) != wantCount || !strings.HasPrefix(lines[0], wantPrefix) {
					t.Fatalf("unexpected transport route/count: %q", calls)
				}
				if scenario == "github-auto-fallback" && !strings.HasPrefix(lines[1], "https://gh-proxy.org/https://github.com/") || scenario == "github-auto-second" && !strings.HasPrefix(lines[2], "https://ghfast.top/https://github.com/") {
					t.Fatalf("fallback did not preserve full GitHub URL: %q", calls)
				}
				limits, e := os.ReadFile(filepath.Join(dir, "curl-limits"))
				if e != nil || strings.Contains(scenario, "auto-") && !strings.Contains(string(limits), "4:180:https://github.com/") || (scenario == "github-auto-fallback" || scenario == "github-auto-second") && !strings.Contains(string(limits), "5:90:https://gh-proxy.org/") {
					t.Fatal("unbounded or missing fallback time limits", e, string(limits))
				}
				if success {
					data, e := os.ReadFile(filepath.Join(managed, "config.toml"))
					var config struct {
						Download struct {
							GitHubProxy string `toml:"github_proxy"`
						} `toml:"download"`
					}
					if e != nil || toml.Unmarshal(data, &config) != nil || config.Download.GitHubProxy != policy {
						t.Fatalf("download preference not saved: %q %v", data, e)
					}
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
				if err != nil || json.Unmarshal(out, &rows) != nil || len(rows) != 4 {
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
			if entries, e := os.ReadDir(filepath.Join(dir, "tmp")); e != nil || len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatal("installer leaked temporary data or removed the user's temporary file", entries, e)
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
