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
			Steps []struct{ Run string }
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
		t.Fatal("check workflow must cover both minimum and release Go toolchains")
	}
	checks := 0
	for _, step := range workflow.Jobs["go"].Steps {
		if strings.Contains(step.Run, "make check smoke") {
			checks++
			if strings.TrimSpace(step.Run) != "make check smoke" {
				t.Fatal("fixture checks must run without privileged process namespaces")
			}
		}
	}
	if checks != 1 {
		t.Fatal("missing common native checks")
	}
}

func TestReleaseWorkflowUsesExactTagAndNoPrivilegedRunner(t *testing.T) {
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
		if step.Env["NIDO_RELEASE_TAG"] != "${{ github.ref_name }}" {
			t.Fatal("release check must receive the exact ref through its environment")
		}
		if strings.Contains(step.Run, "sudo") || strings.Contains(step.Run, "unshare") || strings.Contains(step.Run, "${{ github.ref_name }}") {
			t.Fatal("release checks must neither interpolate tags into shell nor elevate privileges")
		}
		if !strings.Contains(step.Run, "go mod verify\nmake release-check\n") {
			t.Fatal("release checks must verify dependencies before testing packages")
		}
	}
	if checks != 1 {
		t.Fatal("release workflow must check exactly once before signing")
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
			for _, name := range []string{"dist/legacy-install.sh", "dist/server-toolkit.tar.gz", "dist/private.cfg", "dist/nido-linux-armv7.tar.gz"} {
				write(name, "must not publish", 0600)
			}
			write("bin/cosign", `#!/bin/sh
set -eu
operation=$1
shift
printf '%s\n' "$operation" >> "$NIDO_SIGN_CALLS"
if [ "$NIDO_FAIL" = sign ] && [ "$operation" = sign-blob ]; then exit 23; fi
if [ "$NIDO_FAIL" = verify ] && [ "$operation" = verify-blob ]; then exit 24; fi
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
			write("bin/gh", "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$@\" > \"$NIDO_GH_CALLS\"\n", 0755)
			write("publish.sh", "set -euo pipefail\n"+script, 0600)
			calls := filepath.Join(dir, "gh-calls")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "publish.sh")
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + filepath.Join(dir, "bin") + ":/usr/bin:/bin", "HOME=" + filepath.Join(dir, "home"),
				"NIDO_FAIL=" + failure, "NIDO_GH_CALLS=" + calls, "NIDO_SIGN_CALLS=" + filepath.Join(dir, "sign-calls"),
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
