//go:build linux

package distribution

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type installerKeyStep struct{ screen, keys string }
type installerPTYOutput struct {
	sync.Mutex
	data bytes.Buffer
}

func (o *installerPTYOutput) Write(p []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	return o.data.Write(p)
}
func (o *installerPTYOutput) String() string { o.Lock(); defer o.Unlock(); return o.data.String() }

// Use a real controlling terminal. A curl|sh pipe contains shell source only;
// direction keys are sent after the corresponding Go screen has appeared.
func installerPTY(t *testing.T, installer string, args, env []string, steps []installerKeyStep) ([]byte, error) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Fatal(e)
	}
	var number uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&number))); e != 0 {
		t.Fatal(e)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	winsize := [4]uint16{32, 110, 0, 0}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, slave.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&winsize))); e != 0 {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := `installer=$1; shift
if [ -n "${NIDO_TTY_BOOTSTRAP_URL:-}" ]; then
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
	var output installerPTYOutput
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(&output, io.LimitReader(master, 2<<20)); close(copied) }()
	started := time.Now()
	for stepIndex, step := range steps {
		for !strings.Contains(output.String(), step.screen) {
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("missing Go screen %q: %s", step.screen, output.String())
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		if stepIndex == 0 {
			elapsed := time.Since(started)
			t.Logf("first Go screen after bootstrap, no OSC/DSR responses: %s", elapsed)
			if elapsed >= 4*time.Second {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatal("bootstrap blocked on unsupported terminal queries", elapsed)
			}
		}
		if _, err := master.Write([]byte(step.keys)); err != nil {
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
		t.Fatalf("Go installer blocked: %v %s", ctx.Err(), output.String())
	}
	return []byte(output.String()), err
}

func TestInstallerInteractiveControllingTTY(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("installation targets isolated Linux amd64")
	}
	_, installer, _ := installerCandidate(t)
	const down = "\x1b[B"
	for _, scenario := range []string{"direct", "github-preset", "github-custom", "general-proxy", "general-proxy-auth", "general-proxy-token", "api-proxy-only", "prefix-and-proxy", "missing-reference", "bootstrap-direct", "bootstrap-accelerated", "shell-opt-in", "environment-root", "explicit-root", "cancel-network", "cancel-root", "cancel-final", "invalid-root", "unknown-shell"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			home, tmp, bin := filepath.Join(dir, "user"), filepath.Join(dir, "tmp"), filepath.Join(dir, "bin")
			for _, p := range []string{home, tmp, bin} {
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			profile := filepath.Join(home, ".bashrc")
			_ = os.WriteFile(profile, []byte("# USER BASHRC\n"), 0600)
			_ = os.MkdirAll(filepath.Join(home, ".codex", "sessions"), 0700)
			_ = os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("AUTH"), 0600)
			_ = os.WriteFile(filepath.Join(home, ".codex", "sessions", "one.jsonl"), []byte("SESSION"), 0600)
			_ = os.WriteFile(filepath.Join(tmp, "keep"), []byte("KEEP"), 0600)
			for _, tool := range []string{"codex", "mihomo", "gds", "sudo", "systemctl"} {
				_ = os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nprintf forbidden > \"$NIDO_TTY_FORBIDDEN\"\nexit 99\n"), 0755)
			}
			_ = os.WriteFile(filepath.Join(bin, "curl"), []byte(`#!/bin/sh
set -eu
[ "$#" = 2 ] && [ "$1" = -fsSL ] || exit 95
printf '%s\n' "$2" >> "$NIDO_TTY_CURL_CALLS"
case "$2" in */install.sh) cat "$NIDO_TTY_INSTALLER" ;; *) exit 95 ;; esac
`), 0755)
			root := filepath.Join(home, "自定义 nido ' $value")
			args := []string(nil)
			accelerate, proxy := "none", ""
			steps := []installerKeyStep{{"GitHub Accelerate（", "\r"}, {"Proxy（Nido 的 API", "\r"}}
			success, shell := true, false
			env := []string{"HOME=" + home, "TMPDIR=" + tmp, "PATH=" + bin + ":/usr/bin:/bin", "SHELL=/bin/bash", "TERM=xterm-256color", "NIDO_TTY_INSTALLER=" + installer, "NIDO_TTY_CURL_CALLS=" + filepath.Join(dir, "curl-calls"), "NIDO_TTY_FORBIDDEN=" + filepath.Join(dir, "forbidden"), "HTTPS_PROXY=http://unwanted.invalid:8000", "ALL_PROXY=http://unwanted.invalid:8001", "NO_PROXY=*"}
			switch scenario {
			case "github-preset", "bootstrap-accelerated":
				accelerate = "https://gh-proxy.org"
				steps = []installerKeyStep{{"GitHub Accelerate（", down + "\r"}, {"GitHub 加速 HTTPS 前缀", "\r"}, {"Proxy（Nido 的 API", "\r"}}
			case "github-custom":
				accelerate = "https://mirror.example/prefix"
				steps = []installerKeyStep{{"GitHub Accelerate（", down + "\r"}, {"GitHub 加速 HTTPS 前缀", "\x01\x0b" + accelerate + "\r"}, {"Proxy（Nido 的 API", "\r"}}
			case "general-proxy", "general-proxy-auth", "general-proxy-token", "api-proxy-only", "prefix-and-proxy":
				accelerate, proxy = "proxy", "http://127.0.0.1:7890"
				if scenario == "general-proxy-auth" {
					proxy = "http://installer-user:installer-secret@127.0.0.1:7890"
				}
				if scenario == "general-proxy-token" {
					proxy = "http://installer-private-token@127.0.0.1:7890"
				}
				steps = []installerKeyStep{{"GitHub Accelerate（", down + down + "\r"}}
				if scenario == "api-proxy-only" {
					accelerate = "none"
					steps = []installerKeyStep{{"GitHub Accelerate（", "\r"}}
				}
				if scenario == "prefix-and-proxy" {
					accelerate = "https://gh-proxy.org"
					steps = []installerKeyStep{{"GitHub Accelerate（", down + "\r"}, {"GitHub 加速 HTTPS 前缀", "\r"}}
				}
				proxyKeys := "\r"
				if proxy != "http://127.0.0.1:7890" {
					proxyKeys = "\x01\x0b" + proxy + "\r"
				}
				steps = append(steps, installerKeyStep{"Proxy（Nido 的 API", down + "\r"}, installerKeyStep{"Proxy URL（", proxyKeys})
			case "missing-reference":
				steps = []installerKeyStep{{"GitHub Accelerate（", down + down + "\r"}, {"Proxy（Nido 的 API", "\r"}}
				success = false
			case "explicit-root":
				args = []string{"--root", root}
			case "environment-root":
				env = append(env, "NIDO_HOME="+root)
			case "shell-opt-in":
				shell = true
			case "cancel-network":
				steps = []installerKeyStep{{"GitHub Accelerate（", "\x1b"}}
				success = false
			case "cancel-root":
				steps = append(steps, installerKeyStep{"Nido 安装目录", "\x1b"})
				success = false
			case "invalid-root":
				steps = append(steps, installerKeyStep{"Nido 安装目录", "\x01\x0brelative\r"})
				success = false
			case "unknown-shell":
				env = append(env, "SHELL=/bin/fish")
			}
			if success || scenario == "cancel-final" {
				if scenario != "explicit-root" {
					keys := "\x01\x0b" + root + "\r"
					if scenario == "environment-root" {
						keys = "\r"
					}
					steps = append(steps, installerKeyStep{"Nido 安装目录", keys})
				}
				if scenario != "unknown-shell" {
					keys := "\r"
					if shell {
						keys = down + "\r"
					}
					steps = append(steps, installerKeyStep{"终端自动加载", keys})
				}
				keys := down + "\r"
				if scenario == "cancel-final" {
					keys = "\r"
					success = false
				}
				steps = append(steps, installerKeyStep{"安装 Nido（不安装或启动组件）", keys})
			}
			bootstrapURL := ""
			if scenario == "bootstrap-direct" || scenario == "bootstrap-accelerated" {
				bootstrapURL = "https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh"
				if scenario == "bootstrap-accelerated" {
					bootstrapURL = "https://gh-proxy.org/" + bootstrapURL
				}
				env = append(env, "NIDO_TTY_BOOTSTRAP_URL="+bootstrapURL)
			}
			out, err := installerPTY(t, installer, args, env, steps)
			if (err == nil) != success {
				t.Fatalf("unexpected result: %v %s", err, out)
			}
			for _, private := range []string{"installer-user", "installer-secret", "installer-private-token"} {
				if bytes.Contains(out, []byte(private)) {
					t.Fatal("proxy credentials appeared in input or restored scrollback")
				}
			}
			if !bytes.Contains(out, []byte("PIPELINE_SCRIPT_REMAINDER")) {
				t.Fatal("Go consumed pipeline source", string(out))
			}
			if bytes.Contains(out, []byte("选择编号")) || bytes.Contains(out, []byte("y/N")) {
				t.Fatal("legacy character prompt", string(out))
			}
			if _, err := os.Lstat(filepath.Join(root, "config.toml")); (err == nil) != success {
				t.Fatal("unexpected installation write", err, string(out))
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "curl-calls"))
			if string(calls) != bootstrapURL+"\n" && !(bootstrapURL == "" && len(calls) == 0) {
				t.Fatal("bootstrap performed extra downloads", string(calls))
			}
			if success {
				afterTerminal := out
				if pos := bytes.LastIndex(out, []byte("\x1b[?1049l")); pos >= 0 {
					afterTerminal = out[pos:]
				}
				activationLine := ". '" + strings.ReplaceAll(filepath.Join(root, "env.sh"), "'", "'\"'\"'") + "'"
				if !bytes.Contains(afterTerminal, []byte(activationLine)) {
					t.Fatal("activation instruction disappeared after terminal restoration", string(out))
				}
				info, err := os.Stat(filepath.Join(root, "config.toml"))
				if err != nil || info.Mode().Perm()&0077 != 0 {
					t.Fatal("download credentials saved to a public configuration", err)
				}
				config, err := os.ReadFile(filepath.Join(root, "config.toml"))
				if err != nil {
					t.Fatal(err)
				}
				for _, legacy := range []string{"mode =", "github_proxy =", "listener ="} {
					if bytes.Contains(config, []byte(legacy)) {
						t.Fatal("installation retained legacy mixed network policy", legacy, string(config))
					}
				}
				for _, value := range []string{accelerate, proxy} {
					if value != "" && !bytes.Contains(config, []byte(value)) {
						t.Fatal("selected policy not saved", value, string(config))
					}
				}
				activate := `set -eu; . "$1/env.sh"; [ "$(command -v nido)" = "$1/bin/nido" ]; [ "$(nido version)" = 'nido 1.2.3' ]; previous=$PATH; . "$1/env.sh"; [ "$PATH" = "$previous" ]; [ "$HTTPS_PROXY" = 'http://unwanted.invalid:8000' ]; [ "$ALL_PROXY" = 'http://unwanted.invalid:8001' ]; [ "$NO_PROXY" = '*' ]`
				if shell {
					activate = `set -eu; . "$HOME/.bashrc"; [ "$(command -v nido)" = "$1/bin/nido" ]; ` + activate
				}
				cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", activate, "installer-activation", root)
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatal("command registration", err, string(out))
				}
			}
			profileBytes, err := os.ReadFile(profile)
			if err != nil || (!shell && string(profileBytes) != "# USER BASHRC\n") {
				t.Fatal("unrequested Shell modification", err, string(profileBytes))
			}
			for p, want := range map[string]string{filepath.Join(home, ".codex", "auth.json"): "AUTH", filepath.Join(home, ".codex", "sessions", "one.jsonl"): "SESSION"} {
				got, err := os.ReadFile(p)
				if err != nil || string(got) != want {
					t.Fatal("external data changed", p, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
				t.Fatal("installer executed component")
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
				t.Fatal("temporary cleanup", entries, err)
			}
		})
	}
}
