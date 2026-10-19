package distribution

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Build only Nido from this checkout. Neither the generator nor the test may
// resolve or execute a user's components, package manager, or service manager.
func installerCandidate(t *testing.T) (string, string, []byte) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "nido")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	modMode := "-mod=readonly"
	if _, err := os.Stat(filepath.Join(repo, "vendor", "modules.txt")); err == nil {
		modMode = "-mod=vendor"
	}
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", modMode, "-trimpath", "-buildvcs=false", "-ldflags=-s -w -X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=1.2.3", "-o", binary, "./cmd/nido")
	cmd.Dir, cmd.Env = repo, append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOAMD64=v1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build isolated Nido: %v: %s", err, out)
	}
	output := filepath.Join(dir, "release")
	if err := InstallerWithBinary(filepath.Join(repo, "scripts", "install.sh"), binary, output, "1.2.3", false); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(output, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return binary, filepath.Join(output, "install.sh"), script
}

func TestInstallerEmbedsOnlyVerifiedReleaseProgram(t *testing.T) {
	binary, script, data := installerCandidate(t)
	repo, _ := filepath.Abs("../..")
	source := filepath.Join(repo, "scripts", "install.sh")
	output := filepath.Dir(script)
	candidateData, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	highBaseline := bytes.ReplaceAll(candidateData, []byte("GOAMD64=v1"), []byte("GOAMD64=v3"))
	if bytes.Equal(candidateData, highBaseline) || checkBinary(highBaseline, "1.2.3", "linux/amd64") == nil {
		t.Fatal("accepted raised CPU baseline")
	}
	if err := InstallerWithBinary(source, binary, output, "1.2.3", true); err != nil {
		t.Fatal(err)
	}
	if len(data) <= 64<<10 || bytes.Contains(data, []byte("@NIDO_PAYLOAD@")) {
		t.Fatal("missing embedded release")
	}
	if bytes.Contains(data, []byte("curl ")) || bytes.Contains(data, []byte("选择编号")) {
		t.Fatal("bootstrap contains network or Shell interaction business")
	}
	if err := os.WriteFile(script, append(data, []byte("# changed\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InstallerWithBinary(source, binary, output, "1.2.3", true); err == nil {
		t.Fatal("accepted altered release installer")
	}
	for _, scenario := range []string{"foreign", "bad-template", "missing-token", "version"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			input, template, version := binary, source, "1.2.3"
			switch scenario {
			case "foreign":
				input = filepath.Join(dir, "foreign")
				_ = os.WriteFile(input, []byte("#!/bin/sh\nexit 0\n"), 0600)
			case "bad-template":
				template = filepath.Join(dir, "bad.sh")
				_ = os.WriteFile(template, []byte("#!/bin/bash\r\n"), 0600)
			case "missing-token":
				template = filepath.Join(dir, "bad.sh")
				_ = os.WriteFile(template, []byte("#!/bin/sh\nexit 0\n"), 0600)
			case "version":
				version = "1.2.3\nextra"
			}
			out := filepath.Join(dir, "out")
			if err := InstallerWithBinary(template, input, out, version, false); err == nil {
				t.Fatal("unsafe generation accepted")
			}
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatal("failed generation created output", err)
			}
		})
	}
	template, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"@NIDO_VERSION@", "@NIDO_SHA256@", "@NIDO_SIZE@", "@NIDO_GZIP_SIZE@", "@NIDO_PAYLOAD@"} {
		for _, mutation := range []string{"missing", "duplicate"} {
			t.Run(mutation+"-"+strings.Trim(token, "@"), func(t *testing.T) {
				dir := t.TempDir()
				data := append([]byte(nil), template...)
				if mutation == "missing" {
					data = bytes.Replace(data, []byte(token), nil, 1)
				} else {
					data = append(data, []byte("\n# "+token+"\n")...)
				}
				input, output := filepath.Join(dir, "invalid.sh"), filepath.Join(dir, "output")
				if err := os.WriteFile(input, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := InstallerWithBinary(input, binary, output, "1.2.3", false); err == nil {
					t.Fatal("non-unique template token accepted", token)
				}
				if _, err := os.Lstat(output); !os.IsNotExist(err) {
					t.Fatal("invalid template created output", err)
				}
			})
		}
	}
	// A checked package is the canonical source for the normal release target.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "scripts", "install.sh"), template, 0600)
	_ = os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.2.3\n"), 0600)
	if _, err := Pack(binary, filepath.Join(root, "dist"), "1.2.3", "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	if err := Installer(filepath.Join(root, "scripts", "install.sh"), filepath.Join(root, "dist"), false); err != nil {
		t.Fatal(err)
	}
	if err := Installer(filepath.Join(root, "scripts", "install.sh"), filepath.Join(root, "dist"), true); err != nil {
		t.Fatal(err)
	}
	archiveInfo, _ := os.Stat(filepath.Join(root, "dist", "nido-linux-amd64.tar.gz"))
	binaryInfo, _ := os.Stat(binary)
	t.Logf("release size: raw=%d archive=%d embedded-installer=%d; installer/archive=%.3f", binaryInfo.Size(), archiveInfo.Size(), len(data), float64(len(data))/float64(archiveInfo.Size()))
}

func TestPOSIXInstallerWithIsolatedReleaseAndHomes(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("execution requires isolated Linux amd64")
	}
	_, _, base := installerCandidate(t)
	for _, scenario := range []string{"fresh", "repeat", "custom-root", "environment-root", "dry-run", "no-tty", "missing-policy", "missing-reference", "prefix-and-proxy", "proxy-only", "version", "payload-version", "auto", "proxy", "github", "proxy-token", "checksum", "gzip", "compressed-size", "raw-size", "raised-limit", "foreign-root", "foreign-entry", "root-link"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			home, tmp, bin := filepath.Join(dir, "home"), filepath.Join(dir, "tmp"), filepath.Join(dir, "bin")
			for _, p := range []string{home, tmp, bin} {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			root := filepath.Join(home, "nido literal ' $value")
			for _, tool := range []string{"curl", "codex", "mihomo", "gds", "sudo", "systemctl", "conda", "pip"} {
				_ = os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nprintf forbidden > \"$NIDO_FORBIDDEN\"\nexit 99\n"), 0755)
			}
			_ = os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700)
			_ = os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("AUTH"), 0600)
			_ = os.WriteFile(filepath.Join(home, ".codex", "sessions", "one.jsonl"), []byte("SESSION"), 0600)
			_ = os.WriteFile(filepath.Join(home, ".bashrc"), []byte("PROFILE"), 0600)
			_ = os.WriteFile(filepath.Join(tmp, "keep"), []byte("KEEP"), 0600)
			data := append([]byte(nil), base...)
			args := []string{"--root", root, "--github-accelerate", "none", "--yes"}
			success, writes := true, true
			env := []string{"HOME=" + home, "TMPDIR=" + tmp, "PATH=" + bin + ":/usr/bin:/bin", "SHELL=/bin/bash", "TERM=xterm-256color", "NIDO_FORBIDDEN=" + filepath.Join(dir, "forbidden")}
			switch scenario {
			case "custom-root":
				root = filepath.Join(home, "another location")
				args[1] = root
			case "environment-root":
				env = append(env, "NIDO_HOME="+root)
				args = args[2:]
			case "dry-run":
				args = append(args, "--dry-run", "--json")
				writes = false
			case "no-tty":
				args = nil
				success, writes = false, false
			case "missing-policy":
				args = []string{"--root", root, "--yes"}
				success, writes = false, false
			case "version":
				args = append(args, "--version", "9.9.9")
				success, writes = false, false
			case "payload-version":
				data = bytes.Replace(data, []byte("release_version='1.2.3'"), []byte("release_version='1.2.4'"), 1)
				success, writes = false, false
			case "proxy":
				args = []string{"--root", root, "--github-accelerate", "proxy", "--proxy", "http://127.0.0.1:7890", "--yes"}
			case "github":
				args = []string{"--root", root, "--github-accelerate", "https://mirror.example/prefix", "--yes"}
			case "prefix-and-proxy":
				args = []string{"--root", root, "--github-accelerate", "https://mirror.example/prefix", "--proxy", "http://127.0.0.1:7890", "--yes"}
			case "proxy-only":
				args = []string{"--root", root, "--proxy", "http://127.0.0.1:7890", "--yes"}
			case "missing-reference":
				args = []string{"--root", root, "--github-accelerate", "proxy", "--yes"}
				success, writes = false, false
			case "auto":
				args = []string{"--root", root, "--network-mode", "AUTO", "--yes"}
				success, writes = false, false
			case "proxy-token":
				args = []string{"--root", root, "--network-mode", "PROXY", "--proxy", "http://private-token@127.0.0.1:7890", "--dry-run", "--json"}
				writes = false
			case "checksum":
				data = bytes.Replace(data, []byte("release_sha256='"), []byte("release_sha256='0"), 1)
				success, writes = false, false
			case "gzip":
				data = bytes.Replace(data, []byte("H4sI"), []byte("AAAA"), 1)
				success, writes = false, false
			case "compressed-size":
				data = bytes.Replace(data, []byte("compressed_size='"), []byte("compressed_size='1"), 1)
				success, writes = false, false
			case "raw-size":
				data = bytes.Replace(data, []byte("release_size='"), []byte("release_size='1"), 1)
				success, writes = false, false
			case "raised-limit":
				data = bytes.Replace(data, []byte("ulimit -f 262144"), []byte("ulimit -f 1"), 1)
				success, writes = false, false
			case "foreign-root":
				_ = os.MkdirAll(filepath.Join(root, "custom"), 0700)
				_ = os.WriteFile(filepath.Join(root, "custom", "keep"), []byte("KEEP"), 0600)
				success, writes = false, false
			case "foreign-entry":
				_ = os.MkdirAll(filepath.Join(root, "bin"), 0700)
				_ = os.WriteFile(filepath.Join(root, "bin", "nido"), []byte("UNOWNED"), 0755)
				success, writes = false, false
			case "root-link":
				target := filepath.Join(home, "target")
				_ = os.Mkdir(target, 0700)
				_ = os.WriteFile(filepath.Join(target, "keep"), []byte("KEEP"), 0600)
				_ = os.Symlink(target, root)
				success, writes = false, false
			}
			script := filepath.Join(dir, "install.sh")
			_ = os.WriteFile(script, data, 0600)
			run := func() ([]byte, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{script}, args...)...)
				cmd.Env = env
				return cmd.CombinedOutput()
			}
			out, err := run()
			if (err == nil) != success {
				t.Fatalf("unexpected outcome: %v %s", err, out)
			}
			if strings.Contains(string(out), "private-token") {
				t.Fatal("proxy credential leaked")
			}
			if _, err := os.Lstat(filepath.Join(root, "config.toml")); (err == nil) != writes {
				t.Fatal("unexpected target write", err, string(out))
			}
			for scenarioName, sentinel := range map[string]string{"foreign-root": filepath.Join(root, "custom", "keep"), "foreign-entry": filepath.Join(root, "bin", "nido"), "root-link": filepath.Join(home, "target", "keep")} {
				if scenario != scenarioName {
					continue
				}
				want := "KEEP"
				if scenario == "foreign-entry" {
					want = "UNOWNED"
				}
				got, err := os.ReadFile(sentinel)
				if err != nil || string(got) != want {
					t.Fatal("unmanaged target modified", scenario, err, string(got))
				}
			}
			if scenario == "repeat" && success {
				before, _ := os.ReadFile(filepath.Join(root, "config.toml"))
				if out, err := run(); err != nil {
					t.Fatal(err, string(out))
				}
				after, _ := os.ReadFile(filepath.Join(root, "config.toml"))
				if !bytes.Equal(before, after) {
					t.Fatal("repeat changed config")
				}
			}
			if writes {
				activation := exec.Command("/bin/sh", "-c", `. "$1/env.sh"; [ "$(command -v nido)" = "$1/bin/nido" ] && [ "$(nido version)" = 'nido 1.2.3' ]`, "installer-activation", root)
				activation.Env = env
				if out, err := activation.CombinedOutput(); err != nil {
					t.Fatal("command registration", err, string(out))
				}
			}
			for p, want := range map[string]string{filepath.Join(home, ".bashrc"): "PROFILE", filepath.Join(home, ".codex", "auth.json"): "AUTH", filepath.Join(home, ".codex", "sessions", "one.jsonl"): "SESSION"} {
				got, err := os.ReadFile(p)
				if err != nil || string(got) != want {
					t.Fatal("protected data changed", p, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
				t.Fatal("bootstrap contacted network or component")
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatal("temporary cleanup", entries, err)
			}
		})
	}
}
