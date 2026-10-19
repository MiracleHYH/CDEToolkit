package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func TestPublicPipelineUsesSystemCurlWithoutInstallerArguments(t *testing.T) {
	cmd := pipelineCommand(context.Background(), officialInstaller)
	if cmd.Path != "/bin/bash" || strings.Join(cmd.Args[1:], "\n") != "-o\npipefail\n-c\n/usr/bin/curl -fsSL \"$1\" | /bin/sh\nnido-public-install\n"+officialInstaller {
		t.Fatal("public pipeline must use the real unwrapped curl and no installer flags", cmd.Args)
	}
}

func TestPublicInstallationStepsKeepThreeRoutesAndDefaultCancellation(t *testing.T) {
	for _, route := range []string{"none", "prefix", "proxy"} {
		steps, err := installationSteps(route, "https://gh-proxy.org", "http://127.0.0.1:7890", "/tmp/临时 Home/nido", true)
		if err != nil || len(steps) < 5 || steps[len(steps)-1].keys != "\r" {
			t.Fatal("cancellation must retain the default choice", route, err)
		}
		all := fmt.Sprint(steps)
		if (route == "prefix") != strings.Contains(all, "GitHub 加速 HTTPS 前缀") || (route == "proxy") != strings.Contains(all, "Proxy URL（") {
			t.Fatal("route prompts overlap", route, all)
		}
		if !strings.Contains(all, "\x1b[B") {
			t.Fatal("fixture must use direction keys")
		}
	}
	if _, err := installationSteps("auto", "", "", "", false); err == nil {
		t.Fatal("unexpected route accepted")
	}
}

func TestPublicCancellationDoesNotAcceptNetworkFailuresOrTimeouts(t *testing.T) {
	exitOne := exec.Command("/bin/sh", "-c", "exit 1").Run()
	for _, item := range []struct {
		err error
		out string
	}{
		{nil, "nido: 操作已取消"},
		{context.DeadlineExceeded, "nido: 操作已取消"},
		{errors.New("network failure"), "nido: 操作已取消"},
		{exitOne, "curl failed"},
	} {
		if verifyCancellation(item.err, []byte(item.out)) == nil {
			t.Fatal("non-cancellation accepted", item.err, item.out)
		}
	}
	if err := verifyCancellation(exitOne, []byte("nido: 操作已取消\r\n")); err != nil {
		t.Fatal(err)
	}
}

func TestPublicInstallerHarnessKeepsSignaturesAndTemporaryContainerBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../nido-public-install-isolation.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, required := range []string{"cosign verify-blob", "--certificate-github-workflow-sha", "--certificate-github-workflow-ref", "--pull never", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--user 10001:10001", "source=$output/inputs,target=/inputs,readonly", "docker rm -f", "second_pipeline_response=not-verified-before-execution"} {
		if !strings.Contains(source, required) {
			t.Fatal("missing public fixture safety boundary", required)
		}
	}
	for _, forbidden := range []string{"/var/run/docker.sock", "source=$HOME", "go build", "go run", "gh release create", "git push"} {
		if strings.Contains(source, forbidden) {
			t.Fatal("public installer acceptance escaped its scope", forbidden)
		}
	}
}

// Explicit local smoke uses the same steps and PTY driver but a separate test
// command. It cannot replace the system-curl command in public acceptance.
func TestPublicInstallerLocalCandidatePTYSmoke(t *testing.T) {
	installer, expected := os.Getenv("NIDO_LOCAL_INSTALLER"), os.Getenv("NIDO_LOCAL_BINARY")
	if installer == "" || expected == "" {
		t.Skip("opt-in smoke requires a local installer and its release binary")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("local candidate smoke requires isolated Linux amd64")
	}
	want, err := os.ReadFile(expected)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"none", "prefix", "proxy", "cancel"} {
		t.Run(route, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "云端 Work Area", "nido manager")
			profile := filepath.Join(home, ".bashrc")
			if err := os.WriteFile(profile, []byte("# USER BASHRC\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cancelled, selection := route == "cancel", route
			if cancelled {
				selection = "none"
			}
			steps, err := installationSteps(selection, "https://gh-proxy.org", "http://127.0.0.1:7890", root, cancelled)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-o", "pipefail", "-c", `cat "$1" | /bin/sh`, "local-candidate-smoke", installer)
			cmd.Env = []string{"HOME=" + home, "TMPDIR=" + home, "SHELL=/bin/bash", "TERM=xterm-256color", "PATH=/usr/bin:/bin", "BASH_ENV=/dev/null", "ENV=/dev/null"}
			output, installErr := runPTY(ctx, cmd, steps)
			if cancelled {
				if err := verifyCancellation(installErr, output); err != nil {
					t.Fatal(err, string(output))
				}
				after, err := os.ReadFile(profile)
				if err != nil || string(after) != "# USER BASHRC\n" {
					t.Fatal("cancel changed the shell profile", err)
				}
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 1 || entries[0].Name() != ".bashrc" {
					t.Fatal("cancel wrote to temporary HOME", entries, err)
				}
				return
			}
			if installErr != nil {
				t.Fatal(installErr, string(output))
			}
			got, err := os.ReadFile(filepath.Join(root, "bin", "nido"))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("local installed binary mismatch", err)
			}
			if _, err := savedSettings(ctx, root, route, "https://gh-proxy.org", "http://127.0.0.1:7890"); err != nil {
				t.Fatal(err)
			}
			check := shellCommand(ctx, home, root, os.Getenv("NIDO_LOCAL_VERSION"))
			check.Env = cmd.Env
			activation, err := runPTY(ctx, check, nil)
			if err != nil || !bytes.Contains(activation, []byte("NIDO_PUBLIC_SHELL_REGISTERED")) {
				t.Fatal("new shell registration failed", err, string(activation))
			}
		})
	}
}

func TestPublicPTYDriverWaitsForPromptAndUsesACompletePrivateTerminal(t *testing.T) {
	const marker, secret = "NIDO_PUBLIC_PTY_CHILD", "private-pty-unit-value"
	if os.Getenv(marker) == "1" {
		if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stderr.Fd()) {
			t.Fatal("incomplete controlling terminal")
		}
		rows, cols, err := pty.Getsize(os.Stdin)
		if err != nil || rows != 40 || cols != 120 {
			t.Fatal("unexpected terminal size", rows, cols, err)
		}
		state, err := term.MakeRaw(os.Stdin.Fd())
		if err != nil {
			t.Fatal(err)
		}
		defer term.Restore(os.Stdin.Fd(), state)
		fmt.Print("PRIVATE_FIXTURE_PROMPT")
		var value bytes.Buffer
		for {
			one := make([]byte, 1)
			if _, err := os.Stdin.Read(one); err != nil {
				t.Fatal(err)
			}
			if one[0] == '\r' {
				break
			}
			value.WriteByte(one[0])
		}
		if value.String() != secret {
			t.Fatal("unexpected fixture input")
		}
		fmt.Println("PRIVATE_FIXTURE_ACCEPTED")
		return
	}
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "-test.run=^TestPublicPTYDriverWaitsForPromptAndUsesACompletePrivateTerminal$", "-test.count=1")
	cmd.Env = []string{marker + "=1", "HOME=" + t.TempDir(), "TERM=xterm-256color", "PATH=/nonexistent"}
	output, err := runPTY(ctx, cmd, []keyStep{{"PRIVATE_FIXTURE_PROMPT", secret + "\r"}})
	if err != nil || !bytes.Contains(output, []byte("PRIVATE_FIXTURE_ACCEPTED")) || bytes.Contains(output, []byte(secret)) {
		t.Fatalf("private real PTY fixture failed: %v %s", err, output)
	}
}
