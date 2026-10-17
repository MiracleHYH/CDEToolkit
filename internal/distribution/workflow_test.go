package distribution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestCheckWorkflowCoversDeclaredMinimumAndPinnedGo(t *testing.T) {
	module, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var minimum string
	for _, line := range strings.Split(string(module), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			parts := strings.Split(fields[1], ".")
			if len(parts) >= 2 {
				minimum = strings.Join(parts[:2], ".") + ".x"
			}
		}
	}
	pinned, err := os.ReadFile("../../.go-version")
	if err != nil || minimum == "" {
		t.Fatal("missing declared Go versions", err)
	}
	data, err := os.ReadFile("../../.github/workflows/check.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct{ Go string }
				}
			}
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	versions := map[string]bool{}
	for _, item := range workflow.Jobs["go"].Strategy.Matrix.Include {
		versions[item.Go] = true
	}
	if !versions[minimum] || !versions[strings.TrimSpace(string(pinned))] {
		t.Fatal("check workflow does not cover both the declared minimum and release Go toolchain")
	}
}

func TestLinuxWorkflowIsolatesProcessesAndDropsBackToRunner(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/check.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ If, Run string }
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var linux string
	checks := 0
	for _, step := range workflow.Jobs["go"].Steps {
		if !strings.Contains(step.Run, "make check smoke") {
			continue
		}
		checks++
		switch step.If {
		case "runner.os == 'Linux'":
			linux = step.Run
		case "runner.os == 'macOS'":
			if strings.TrimSpace(step.Run) != "make check smoke" {
				t.Fatal("macOS must run the normal check targets")
			}
		default:
			t.Fatal("platform checks must have explicit runner conditions")
		}
	}
	if linux == "" || checks != 2 {
		t.Fatal("missing separate Linux and macOS checks")
	}
	testWorkflowNamespace(t, linux, false)
}

func TestReleaseWorkflowChecksInNamespaceWithExactTag(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string
				Env map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	checks := 0
	for _, step := range workflow.Jobs["release"].Steps {
		if !strings.Contains(step.Run, "make release-check") {
			continue
		}
		checks++
		if step.Env["STK_RELEASE_TAG"] != "${{ github.ref_name }}" {
			t.Fatal("release check must receive the exact ref through its environment")
		}
		start := strings.Index(step.Run, "sudo unshare")
		end := strings.Index(step.Run, "make release-check") + len("make release-check")
		if start < 0 || start >= end || strings.TrimSpace(step.Run[:start]) != "go mod verify" || !strings.HasPrefix(strings.TrimSpace(step.Run[end:]), "go install github.com/zricethezav/gitleaks/") {
			t.Fatal("only release-check belongs in the namespace; dependency and source checks remain on the host")
		}
		testWorkflowNamespace(t, step.Run[start:end], true)
	}
	if checks != 1 {
		t.Fatal("release check must have exactly one isolated command")
	}
}

func testWorkflowNamespace(t *testing.T, script string, release bool) {
	t.Helper()
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "namespace-failure"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			bin, home := filepath.Join(dir, "bin"), filepath.Join(dir, "home")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, text string) {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(text), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write("id", "#!/bin/sh\ncase \"$1\" in -u) echo 1001 ;; -g) echo 1002 ;; *) exit 97 ;; esac\n")
			// Never run sudo, unshare or make here. Capture the exact command
			// boundary to guard PID isolation and privilege drop independently
			// of host OS/capabilities; Actions exercises the real namespace.
			write("sudo", "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CDET_SUDO_ARGS\"\nexit \"$CDET_SUDO_EXIT\"\n")
			write("make", "#!/bin/sh\nexit 98\n")
			path, calls := bin+":/usr/bin:/bin", filepath.Join(dir, "sudo-args")
			exitCode := "0"
			if fail {
				exitCode = "37"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", "set -euo pipefail\n"+script)
			const tag = "v1.2.3-exact tag+$literal"
			cmd.Env = []string{"PATH=" + path, "HOME=" + home, "CDET_SUDO_ARGS=" + calls, "CDET_SUDO_EXIT=" + exitCode, "STK_RELEASE_TAG=" + tag}
			out, err := cmd.CombinedOutput()
			if fail {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 37 {
					t.Fatal("namespace failure must stop checks", err, string(out))
				}
			} else if err != nil {
				t.Fatal("mock namespace command failed", err, string(out))
			}
			args, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"unshare", "--mount", "--pid", "--fork", "--mount-proc", "--kill-child", "--setgid=1002", "--setuid=1001", "--", "env", "HOME=" + home, "PATH=" + path, "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly"}
			if release {
				want = append(want, "STK_RELEASE_TAG="+tag, "make", "release-check")
			} else {
				want = append(want, "make", "check", "smoke")
			}
			if string(args) != strings.Join(want, "\n")+"\n" {
				t.Fatal("Linux checks must isolate /proc and execute as runner, without skipping check targets", string(args))
			}
		})
	}
}

func TestReleaseWorkflowOnlyPublishesVerifiedAllowlist(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct{ Run string }
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["release"].Steps {
		if strings.Contains(step.Run, "gh release create") {
			if script != "" {
				t.Fatal("ambiguous publisher steps")
			}
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("release workflow has no tested publisher")
	}
	for _, failure := range []string{"none", "sign", "verify"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, text string, mode os.FileMode) {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(text), mode); err != nil {
					t.Fatal(err)
				}
			}
			expected := map[string]bool{}
			for _, platform := range Platforms {
				asset, _ := assetName(platform)
				for _, suffix := range []string{"", ".sha256"} {
					path := "dist/" + asset + suffix
					write(path, "fixture asset", 0600)
					expected[path], expected[path+".sigstore.json"] = true, true
				}
			}
			write("dist/install.sh", "fixture reviewed installer", 0600)
			expected["dist/install.sh"], expected["dist/install.sh.sigstore.json"] = true, true
			for _, name := range []string{"dist/legacy-install.sh", "dist/server-toolkit.tar.gz", "dist/private.cfg", "dist/cdet-linux-armv7.tar.gz"} {
				write(name, "must not publish", 0600)
			}
			write("bin/cosign", `#!/bin/sh
set -eu
operation=$1
shift
printf '%s\n' "$operation" >> "$STK_SIGN_CALLS"
if [ "$STK_FAIL" = sign ] && [ "$operation" = sign-blob ]; then exit 23; fi
if [ "$STK_FAIL" = verify ] && [ "$operation" = verify-blob ]; then exit 24; fi
bundle=''
input=''
identity=0
issuer=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --bundle) bundle=$2; shift 2 ;;
    --certificate-identity) [ "$2" = "$CERTIFICATE_IDENTITY" ]; identity=1; shift 2 ;;
    --certificate-oidc-issuer) [ "$2" = https://token.actions.githubusercontent.com ]; issuer=1; shift 2 ;;
    --yes) shift ;;
    *) input=$1; shift ;;
  esac
done
[ -f "$input" ]
[ "$bundle" = "$input.sigstore.json" ]
if [ "$operation" = sign-blob ]; then printf '{}\n' > "$bundle"; else [ -f "$bundle" ]; [ "$identity$issuer" = 11 ]; fi
`, 0755)
			write("bin/gh", "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$STK_GH_CALLS\"\n", 0755)
			write("publish.sh", "set -euo pipefail\n"+script, 0600)
			calls := filepath.Join(dir, "gh-calls")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "publish.sh")
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "HOME=" + filepath.Join(dir, "home"),
				"STK_FAIL=" + failure, "STK_GH_CALLS=" + calls, "STK_SIGN_CALLS=" + filepath.Join(dir, "sign-calls"),
				"CERTIFICATE_IDENTITY=https://github.com/example/stk/.github/workflows/release.yml@refs/tags/v0.1.0", "RELEASE_TAG=v0.1.0"}
			out, err := cmd.CombinedOutput()
			if failure != "none" {
				if err == nil {
					t.Fatal("signing or verification failure did not stop the publisher")
				}
				if _, err = os.Lstat(calls); !os.IsNotExist(err) {
					t.Fatal("gh was called after a signing/verification failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("mock publisher failed: %v: %s", err, out)
			}
			published, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSpace(string(published)), "\n")
			if len(args) != len(expected)+5 || strings.Join(args[:3], " ") != "release create v0.1.0" {
				t.Fatal("unexpected release command", args)
			}
			for _, path := range args[3 : len(args)-2] {
				if !expected[path] {
					t.Fatal("unverified or duplicate asset was published", path)
				}
				delete(expected, path)
			}
			if len(expected) != 0 || args[len(args)-2] != "--verify-tag" || args[len(args)-1] != "--generate-notes" {
				t.Fatal("missing verified files or tag guard")
			}
		})
	}
}
