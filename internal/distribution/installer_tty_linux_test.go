//go:build linux

package distribution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A real controlling PTY catches curl|sh regressions: stdin contains the
// installer source while answers are available only through /dev/tty.
func installerPTY(t *testing.T, installer string, args, env []string, answers string) ([]byte, error) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Fatal(errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); errno != 0 {
		t.Fatal(errno)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := `installer=$1; shift
if [ -n "${NIDO_TTY_BOOTSTRAP_URL:-}" ]; then
  [ "$#" -eq 0 ] || exit 95
  curl -fsSL "$NIDO_TTY_BOOTSTRAP_URL" | /bin/sh
else
  cat "$installer" | /bin/sh -s -- "$@"
fi
result=$?; printf 'PIPELINE_SCRIPT_REMAINDER\n'; exit "$result"`
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", command, "nido-tty-test", installer}, args...)...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		t.Fatal(err)
	}
	_ = slave.Close()
	var output bytes.Buffer
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(&output, io.LimitReader(master, 1<<20)); close(copied) }()
	if answers != "" {
		if _, err := master.Write([]byte(answers)); err != nil {
			t.Fatal(err)
		}
	}
	err = cmd.Wait()
	select {
	case <-copied:
	case <-time.After(time.Second):
		_ = master.Close()
		<-copied
	}
	if ctx.Err() != nil {
		t.Fatalf("interactive installer blocked: %v %s", ctx.Err(), output.String())
	}
	return output.Bytes(), err
}

func TestInstallerInteractiveControllingTTY(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("installation targets isolated Linux amd64")
	}
	installer, err := filepath.Abs("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{
		"custom-root", "environment-root", "explicit-root", "trailing-slash", "custom-download", "repeat-keeps-direct",
		"bootstrap-direct", "bootstrap-accelerated", "menu-direct", "menu-gh-proxy", "menu-ghfast", "menu-custom", "menu-auto",
		"menu-invalid-retry", "menu-invalid-cancel", "custom-empty-cancel", "custom-eof", "custom-quit", "environment-download",
		"shell-opt-in", "shell-refused", "unknown-shell", "cancel", "quit", "eof", "yes", "dry-run", "invalid-root", "root-directory",
	} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			home, temporary, bin := filepath.Join(dir, "user"), filepath.Join(dir, "tmp"), filepath.Join(dir, "bin")
			for _, path := range []string{home, temporary, bin} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(home, ".bashrc"), "USER BASHRC\n", 0600)
			write(filepath.Join(temporary, "keep"), "KEEP", 0600)
			candidate := filepath.Join(dir, "fixture-nido")
			write(candidate, `#!/bin/sh
set -eu
if [ "$1" = version ]; then printf 'nido 1.2.3\n'; exit 0; fi
[ "$1" = --root ]; root=$2; shift 2
case "$1" in
  init)
    shift
    printf '%s\n' "$@" > "$NIDO_TTY_INIT_CALLS"
    policy=''
    while [ "$#" -gt 0 ]; do
      case "$1" in --github-proxy) policy=$2; shift 2 ;; --yes) shift ;; *) exit 95 ;; esac
    done
    mkdir -p "$root/bin"; cp "$0" "$root/bin/nido"; chmod 0755 "$root/bin/nido"
    if [ -n "$policy" ]; then printf 'github_proxy=%s\n' "$policy" > "$root/config.toml"
    elif [ ! -f "$root/config.toml" ]; then printf 'fixture\n' > "$root/config.toml"; fi
    printf '# fixture env\n' > "$root/env.sh" ;;
  path) CDPATH= cd "$root"; pwd -P ;;
  shell) shift; printf '%s\n' "$*" >> "$NIDO_TTY_SHELL_CALLS"; [ "${NIDO_TTY_REFUSE_SHELL:-0}" != 1 ] ;;
  *) exit 95 ;;
esac
`, 0755)
			archive := []byte("INERT ARCHIVE FIXTURE\n")
			asset := "nido-linux-amd64.tar.gz"
			write(filepath.Join(dir, asset), string(archive), 0600)
			write(filepath.Join(dir, asset+".sha256"), fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), asset), 0600)
			write(filepath.Join(bin, "curl"), `#!/bin/sh
set -eu
url=''; output=''
while [ "$#" -gt 0 ]; do case "$1" in --output) output=$2; shift 2 ;; https://*) url=$1; shift ;; *) shift ;; esac; done
printf '%s\n' "$url" >> "$NIDO_TTY_CURL_CALLS"
if [ -z "$output" ]; then
  case "$url" in */install.sh) cat "$NIDO_TTY_INSTALLER"; exit 0 ;; *) exit 95 ;; esac
fi
cp "$NIDO_TTY_FIXTURES/${url##*/}" "$output"
`, 0755)
			write(filepath.Join(bin, "tar"), `#!/bin/sh
set -eu
case "$1" in -tzf) printf 'nido\n' ;; -xOzf) cat "$NIDO_TTY_CANDIDATE" ;; *) exit 95 ;; esac
`, 0755)
			root := filepath.Join(home, ".nido")
			args, answers, wantSuccess, wantInstall, wantShell := []string{"--github-proxy", "direct"}, "\nn\ny\n", true, true, false
			wantMenu, wantPolicy, wantPrefix, wantShellName, bootstrapURL := false, "direct", "https://github.com/", "zsh", ""
			env := []string{"HOME=" + home, "TMPDIR=" + temporary, "PATH=" + bin + ":/usr/bin:/bin", "SHELL=/bin/bash", "NIDO_TTY_FIXTURES=" + dir, "NIDO_TTY_CANDIDATE=" + candidate, "NIDO_TTY_INSTALLER=" + installer, "NIDO_TTY_CURL_CALLS=" + filepath.Join(dir, "curl-calls"), "NIDO_TTY_SHELL_CALLS=" + filepath.Join(dir, "shell-calls"), "NIDO_TTY_INIT_CALLS=" + filepath.Join(dir, "init-calls")}
			switch scenario {
			case "custom-root":
				root = filepath.Join(home, "custom path ' literal $value")
				answers = root + "\nn\ny\n"
			case "environment-root":
				root = filepath.Join(home, "environment root")
				env = append(env, "NIDO_HOME="+root)
			case "explicit-root":
				root = filepath.Join(home, "explicit root")
				args = append(args, "--root", root)
				answers = "n\ny\n"
			case "trailing-slash":
				root = filepath.Join(home, "trailing slash root")
				answers = root + "///\nn\ny\n"
			case "custom-download":
				args = nil
				answers = "\nhttps://mirror.example\nn\ny\n"
				wantMenu, wantPolicy, wantPrefix = true, "https://mirror.example", "https://mirror.example/https://github.com/"
			case "repeat-keeps-direct":
				args, answers = nil, "\n\nn\ny\n"
				wantMenu, wantPolicy = true, ""
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(root, "config.toml"), "github_proxy=direct\n", 0600)
			case "bootstrap-direct", "bootstrap-accelerated":
				args, wantMenu, wantShell, wantShellName = nil, true, true, "bash"
				root = filepath.Join(home, "custom bootstrap directory")
				bootstrapURL = "https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh"
				selection := "1"
				if scenario == "bootstrap-accelerated" {
					bootstrapURL = "https://gh-proxy.org/" + bootstrapURL
					selection, wantPolicy, wantPrefix = "2", "https://gh-proxy.org", "https://gh-proxy.org/https://github.com/"
				}
				env = append(env, "NIDO_TTY_BOOTSTRAP_URL="+bootstrapURL)
				answers = root + "\n" + selection + "\ny\ny\n"
			case "menu-direct", "menu-gh-proxy", "menu-ghfast", "menu-custom", "menu-auto", "menu-invalid-retry":
				args, wantMenu = nil, true
				selection := "1"
				switch scenario {
				case "menu-gh-proxy":
					selection, wantPolicy, wantPrefix = "2", "https://gh-proxy.org", "https://gh-proxy.org/https://github.com/"
				case "menu-ghfast":
					selection, wantPolicy, wantPrefix = "3", "https://ghfast.top", "https://ghfast.top/https://github.com/"
				case "menu-custom":
					selection, wantPolicy, wantPrefix = "4\nhttps://mirror.example/custom/", "https://mirror.example/custom", "https://mirror.example/custom/https://github.com/"
				case "menu-auto":
					selection, wantPolicy = "5", "auto"
				case "menu-invalid-retry":
					selection = "9\n1"
				}
				answers = "\n" + selection + "\nn\ny\n"
			case "menu-invalid-cancel", "custom-empty-cancel", "custom-eof", "custom-quit":
				args, wantMenu, wantSuccess, wantInstall = nil, true, false, false
				switch scenario {
				case "menu-invalid-cancel":
					answers = "\n9\nq\n"
				case "custom-empty-cancel":
					answers = "\n4\n\nq\n"
				case "custom-eof":
					answers = "\n4\n\x04"
				case "custom-quit":
					answers = "\n4\nq\n"
				}
			case "environment-download":
				args = nil
				env = append(env, "NIDO_GITHUB_PROXY=https://environment.example")
				wantPolicy, wantPrefix = "https://environment.example", "https://environment.example/https://github.com/"
			case "shell-opt-in", "shell-refused":
				args = append(args, "--root", root)
				env = append(env, "SHELL=/bin/zsh", "ZDOTDIR="+filepath.Join(home, "zsh settings"))
				answers = "y\ny\n"
				wantShell = true
				if scenario == "shell-refused" {
					env = append(env, "NIDO_TTY_REFUSE_SHELL=1")
				}
			case "unknown-shell":
				args = append(args, "--root", root)
				env = append(env, "SHELL=/bin/fish")
				answers = "y\n"
			case "cancel":
				answers = "\nn\nn\n"
				wantSuccess, wantInstall = false, false
			case "quit":
				answers = "q\n"
				wantSuccess, wantInstall = false, false
			case "eof":
				answers = "\x04"
				wantSuccess, wantInstall = false, false
			case "yes":
				args = append(args, "--yes")
				answers = ""
			case "dry-run":
				args = append(args, "--dry-run")
				answers = ""
				wantInstall = false
			case "invalid-root":
				answers = "relative\nn\n"
				wantSuccess, wantInstall = false, false
			case "root-directory":
				answers = "/\nn\n"
				wantSuccess, wantInstall = false, false
			}
			out, err := installerPTY(t, installer, args, env, answers)
			if (err == nil) != wantSuccess {
				t.Fatalf("unexpected result %v: %s", err, out)
			}
			if !bytes.Contains(out, []byte("PIPELINE_SCRIPT_REMAINDER")) {
				t.Fatal("installer consumed the piped shell source", string(out))
			}
			if bytes.Contains(out, []byte("GitHub 下载方式：")) != wantMenu {
				t.Fatal("interactive transport menu did not match flags/environment", string(out))
			}
			if wantMenu {
				for _, option := range []string{"1) direct", "2) https://gh-proxy.org", "3) https://ghfast.top", "4) 自定义 HTTPS", "5) auto"} {
					if !bytes.Contains(out, []byte(option)) {
						t.Fatal("transport choice missing", option, string(out))
					}
				}
			}
			if strings.HasPrefix(scenario, "menu-invalid-") && !bytes.Contains(out, []byte("请选择 1–5")) {
				t.Fatal("invalid menu selection was not retried", string(out))
			}
			if scenario == "custom-empty-cancel" && !bytes.Contains(out, []byte("请输入 HTTPS 加速前缀")) {
				t.Fatal("empty custom prefix was accepted", string(out))
			}
			if _, err := os.Lstat(filepath.Join(root, "config.toml")); (err == nil) != wantInstall {
				t.Fatal("unexpected install write", err, string(out))
			}
			calls, callErr := os.ReadFile(filepath.Join(dir, "curl-calls"))
			if bootstrapURL != "" {
				first, rest, found := strings.Cut(string(calls), "\n")
				if !found || first != bootstrapURL {
					t.Fatal("unexpected bootstrap entry point", string(calls))
				}
				calls = []byte(rest)
			}
			if wantInstall {
				if callErr != nil || len(strings.Fields(string(calls))) != 2 {
					t.Fatal("missing package downloads", callErr, string(calls))
				}
				if !bytes.Contains(out, []byte("Installation directory: "+root)) {
					t.Fatal("actual installation path not reported", string(out))
				}
				for _, url := range strings.Fields(string(calls)) {
					if !strings.HasPrefix(url, wantPrefix) {
						t.Fatal("selected transport ignored", wantPrefix, string(calls))
					}
				}
				initArgs, initErr := os.ReadFile(filepath.Join(dir, "init-calls"))
				wantInitArgs := "--yes\n"
				if wantPolicy != "" {
					wantInitArgs += "--github-proxy\n" + wantPolicy + "\n"
				}
				if initErr != nil || string(initArgs) != wantInitArgs {
					t.Fatal("transport persistence did not match explicit choice", initErr, string(initArgs), wantInitArgs)
				}
				if scenario == "repeat-keeps-direct" {
					if got, _ := os.ReadFile(filepath.Join(root, "config.toml")); string(got) != "github_proxy=direct\n" {
						t.Fatal("repeat installation replaced saved direct preference", string(got))
					}
				}
			} else if !os.IsNotExist(callErr) {
				t.Fatal("cancelled/preview install downloaded assets", callErr)
			}
			shellCalls, shellErr := os.ReadFile(filepath.Join(dir, "shell-calls"))
			if (shellErr == nil) != wantShell || wantShell && string(shellCalls) != "setup "+wantShellName+" --yes\n" {
				t.Fatal("unexpected shell registration", shellErr, string(shellCalls), string(out))
			}
			if scenario == "shell-opt-in" && !bytes.Contains(out, []byte(filepath.Join(home, "zsh settings", ".zshrc"))) {
				t.Fatal("real ZDOTDIR profile not displayed", string(out))
			}
			if scenario == "shell-refused" && !bytes.Contains(out, []byte("Shell 接入未完成")) {
				t.Fatal("failed optional registration hid successful installation or manual activation", string(out))
			}
			if data, _ := os.ReadFile(filepath.Join(home, ".bashrc")); string(data) != "USER BASHRC\n" {
				t.Fatal("installer modified profile directly")
			}
			if entries, err := os.ReadDir(temporary); err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatal("temporary fixture changed", entries, err)
			}
			if scenario == "yes" || scenario == "dry-run" {
				if bytes.Contains(out, []byte("开始安装")) || bytes.Contains(out, []byte("是否将")) {
					t.Fatal("noninteractive option still prompted", string(out))
				}
			}
		})
	}
}
