// nido-public-install drives a real public curl|sh installation through a PTY.
// It is test infrastructure, never a Nido release or a replacement HTTP client.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const officialInstaller = "https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh"

type keyStep struct{ screen, keys string }

func installationSteps(route, prefix, proxy, root string, cancel bool) ([]keyStep, error) {
	const down = "\x1b[B"
	steps := []keyStep{{"GitHub Accelerate（", "\r"}}
	switch route {
	case "none":
	case "prefix":
		steps[0].keys = down + "\r"
		steps = append(steps, keyStep{"GitHub 加速 HTTPS 前缀", "\x01\x0b" + prefix + "\r"})
	case "proxy":
		steps[0].keys = down + down + "\r"
	default:
		return nil, fmt.Errorf("unsupported route %q", route)
	}
	proxyKeys := "\r"
	if route == "proxy" {
		proxyKeys = down + "\r"
	}
	steps = append(steps, keyStep{"Proxy（Nido 的 API", proxyKeys})
	if route == "proxy" {
		steps = append(steps, keyStep{"Proxy URL（", "\x01\x0b" + proxy + "\r"})
	}
	steps = append(steps, keyStep{"Nido 安装目录", "\x01\x0b" + root + "\r"}, keyStep{"终端自动加载", down + "\r"})
	confirm := down + "\r"
	if cancel {
		confirm = "\r" // Default cancellation must not write either root or profile.
	}
	return append(steps, keyStep{"安装 Nido（不安装或启动组件）", confirm}), nil
}

// No PATH lookup, curl shim, cached response, verifier filter, or installer
// arguments: these are the same two curl -fsSL URL | sh commands as README.
func pipelineCommand(ctx context.Context, address string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/bash", "-o", "pipefail", "-c", `/usr/bin/curl -fsSL "$1" | /bin/sh`, "nido-public-install", address)
}

func shellCommand(ctx context.Context, home, root, version string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--rcfile", filepath.Join(home, ".bashrc"), "-ic", `set -eu
[ "$(command -v nido)" = "$1/bin/nido" ]
[ "$(nido version)" = "nido $2" ]
printf 'NIDO_PUBLIC_SHELL_REGISTERED\n'`, "nido-public-shell", root, version)
}

func verifyCancellation(installErr error, terminal []byte) error {
	var exit *exec.ExitError
	if !errors.As(installErr, &exit) || exit.ExitCode() != 1 || !bytes.Contains(terminal, []byte("nido: 操作已取消")) {
		return fmt.Errorf("expected explicit Nido cancellation with exit 1, got %v", installErr)
	}
	return nil
}

func savedSettings(ctx context.Context, root, route, prefix, proxy string) (map[string]string, error) {
	command := exec.CommandContext(ctx, filepath.Join(root, "bin", "nido"), "--root", root, "download", "show", "--json")
	data, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read installed settings: %w", err)
	}
	var saved map[string]string
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil, err
	}
	wantAccelerate, wantProxy := route, "None"
	if route == "prefix" {
		wantAccelerate = prefix
	}
	if route == "proxy" {
		wantProxy = proxy
	}
	if saved["github_accelerate"] != wantAccelerate || saved["proxy"] != wantProxy {
		return nil, fmt.Errorf("saved network route differs from the interactive selection: %v", saved)
	}
	return saved, nil
}

type terminalOutput struct {
	sync.Mutex
	data bytes.Buffer
}

func (o *terminalOutput) Write(p []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	if o.data.Len()+len(p) > 4<<20 {
		return 0, fmt.Errorf("PTY transcript exceeds 4 MiB")
	}
	return o.data.Write(p)
}

func (o *terminalOutput) bytes() []byte {
	o.Lock()
	defer o.Unlock()
	return append([]byte(nil), o.data.Bytes()...)
}

func runPTY(ctx context.Context, cmd *exec.Cmd, steps []keyStep) ([]byte, error) {
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		return nil, err
	}
	defer master.Close()
	var output terminalOutput
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(&output, master)
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
			err = nil // Normal PTY close on Linux is wrapped EIO.
		}
		readDone <- err
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	stop := func() {
		// pty starts a new session, so timeout cleanup includes curl and sh.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	var sessionErr error
	for _, step := range steps {
		for !bytes.Contains(output.bytes(), []byte(step.screen)) {
			select {
			case err := <-waitDone:
				waitDone <- err
				sessionErr = fmt.Errorf("process exited before screen %q: %v", step.screen, err)
			case <-ctx.Done():
				sessionErr = fmt.Errorf("missing screen %q: %w", step.screen, ctx.Err())
			case <-time.After(15 * time.Millisecond):
			}
			if sessionErr != nil {
				break
			}
		}
		if sessionErr != nil {
			break
		}
		if _, err = master.Write([]byte(step.keys)); err != nil {
			sessionErr = err
			break
		}
	}
	if sessionErr != nil {
		stop()
	}
	select {
	case err = <-waitDone:
	case <-ctx.Done():
		stop()
		err = <-waitDone
		if sessionErr == nil {
			sessionErr = ctx.Err()
		}
	}
	select {
	case readErr := <-readDone:
		if sessionErr == nil && readErr != nil {
			sessionErr = readErr
		}
	case <-time.After(time.Second):
		_ = master.Close()
		<-readDone
		if sessionErr == nil {
			sessionErr = fmt.Errorf("PTY did not close after the command")
		}
	}
	if sessionErr != nil {
		return output.bytes(), sessionErr
	}
	return output.bytes(), err
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address, root := os.Getenv("NIDO_PUBLIC_INSTALL_URL"), os.Getenv("NIDO_PUBLIC_INSTALL_ROOT")
	prefix, proxy := os.Getenv("NIDO_PUBLIC_INSTALL_PREFIX"), "http://127.0.0.1:7890"
	route, version := os.Getenv("NIDO_PUBLIC_INSTALL_ROUTE"), os.Getenv("NIDO_PUBLIC_INSTALL_VERSION")
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasSuffix(parsed.Path, "/releases/latest/download/install.sh") {
		return fmt.Errorf("expected a public HTTPS latest installer URL")
	}
	home := os.Getenv("HOME")
	relative, err := filepath.Rel(home, root)
	if err != nil || !filepath.IsAbs(home) || !filepath.IsAbs(root) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("custom root must stay inside this fixture's temporary HOME")
	}
	cancel := os.Getenv("NIDO_PUBLIC_INSTALL_CANCEL") == "true"
	steps, err := installationSteps(route, prefix, proxy, root, cancel)
	if err != nil {
		return err
	}
	ctx, done := context.WithTimeout(context.Background(), 6*time.Minute)
	defer done()
	terminal, installErr := runPTY(ctx, pipelineCommand(ctx, address), steps)
	if err = os.WriteFile(os.Getenv("NIDO_PUBLIC_INSTALL_TRANSCRIPT"), terminal, 0600); err != nil {
		return err
	}
	if cancel {
		if err := verifyCancellation(installErr, terminal); err != nil {
			return err
		}
		if _, err := os.Lstat(root); !os.IsNotExist(err) {
			return fmt.Errorf("cancellation created the installation directory")
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"route": route, "cancelled": true, "real_public_pipeline": true})
	}
	if installErr != nil {
		return installErr
	}
	expected, err := os.ReadFile(os.Getenv("NIDO_PUBLIC_INSTALL_SIGNED_BINARY"))
	if err != nil {
		return err
	}
	installed, err := os.ReadFile(filepath.Join(root, "bin", "nido"))
	if err != nil || !bytes.Equal(expected, installed) {
		return fmt.Errorf("installed executable differs from the independently signed release")
	}
	settings, err := savedSettings(ctx, root, route, prefix, proxy)
	if err != nil {
		return err
	}
	for _, component := range []string{"codex", "proxy", "gds", "conda"} {
		if _, err := os.Lstat(filepath.Join(root, component)); !os.IsNotExist(err) {
			return fmt.Errorf("installer unexpectedly created component %s", component)
		}
	}
	// Start a new interactive shell through its real rc file. No manual source
	// or inherited NIDO_HOME/PATH may substitute for registration by the installer.
	check := shellCommand(ctx, home, root, version)
	activation, err := runPTY(ctx, check, nil)
	if writeErr := os.WriteFile(os.Getenv("NIDO_PUBLIC_INSTALL_TRANSCRIPT")+".shell", activation, 0600); writeErr != nil {
		return writeErr
	}
	if err != nil || !bytes.Contains(activation, []byte("NIDO_PUBLIC_SHELL_REGISTERED")) {
		return fmt.Errorf("new interactive Bash did not register nido: %v", err)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"route": route, "version": version, "custom_root": root, "actual_settings": settings, "signed_binary_match": true, "shell_registration": true, "real_public_pipeline": true, "second_pipeline_response_verified_before_execution": false})
}
