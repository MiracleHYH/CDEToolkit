package nido

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only the entry script runs here. Its platform, SDK and bubblewrap are strict
// inert fixtures; this test never executes a namespace, compiler or service.
func TestProxyQuickStartLinuxEntryRequiresActualEvidence(t *testing.T) {
	for _, test := range []struct {
		mode string
		ok   bool
	}{
		{"pass", true},
		{"namespace-fail", false},
		{"command-fail-after-evidence", false},
		{"missing-log", false},
		{"package-pass-missing", false},
		{"test-skip", false},
		{"ipv6-loopback-skip", true},
		{"ipv6-wildcard-skip", true},
		{"ipv6-both-skips", true},
		{"ipv6-prefix-skip", false},
		{"ipv6-wrong-package", false},
		{"ipv6-nido-skip", false},
		{"ipv6-fail", false},
		{"ipv6-missing-reason", false},
		{"ipv6-unclassified-reason", false},
		{"ipv6-other-test-reason", false},
		{"test-fail", false},
		{"quickstart-pass-missing", false},
		{"node-pass-missing", false},
		{"remediation-pass-missing", false},
		{"cli-pass-missing", false},
		{"new-cli-pass-missing", false},
		{"default-skip-pass-missing", false},
		{"tui-keys-pass-missing", false},
		{"retention-pass-missing", false},
		{"wrong-os", false},
		{"wrong-arch", false},
		{"root-user", false},
		{"wrong-sdk", false},
		{"existing-output", false},
		{"linked-output", false},
		{"linked-source-input", false},
	} {
		t.Run(test.mode, func(t *testing.T) {
			out, state, output, err := runProxyQuickStartLinuxEntryFixture(t, test.mode)
			if (err == nil) != test.ok {
				t.Fatalf("success=%v, want %v: %v\n%s", err == nil, test.ok, err, out)
			}
			if strings.Contains(out, "unexpected fixture command") {
				t.Fatalf("fixture missed a command:\n%s", out)
			}
			if strings.Contains(out, "PASS: 普通用户 Linux amd64") != test.ok {
				t.Fatalf("entry emitted an inaccurate success claim:\n%s", out)
			}
			calls, _ := os.ReadFile(filepath.Join(state, "bwrap-args"))
			if len(calls) != 0 {
				for _, required := range []string{"--unshare-all\n", "--share-net\n", "--clearenv\n", "--cap-drop\nALL\n", "--setenv\nHOME\n/work/home\n", "--setenv\nGOPROXY\noff\n", "--setenv\nGOSUMDB\noff\n", "--setenv\nGOFLAGS\n-mod=vendor\n"} {
					if !strings.Contains(string(calls), required) {
						t.Fatalf("entry omitted isolation flag %q: %s", required, calls)
					}
				}
				if strings.Contains(string(calls), "--setenv\nNIDO_TEST_MIHOMO") || strings.Contains(string(calls), "--share-pid\n") {
					t.Fatalf("entry forwarded a real-core input or shared PID namespace: %s", calls)
				}
				for _, required := range []string{"-o /work/fake-mihomo ./tests/fixtures/proxy-core", "bash tests/proxy-quickstart-cli-isolation.sh /work/nido /work/fake-mihomo"} {
					if !strings.Contains(string(calls), required) {
						t.Fatalf("entry omitted the inert CLI core fixture %q", required)
					}
				}
				work, readErr := os.ReadFile(filepath.Join(state, "work-path"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				scratch := filepath.Dir(strings.TrimSpace(string(work)))
				if !strings.HasPrefix(filepath.Base(scratch), "nido-proxy-quickstart-native.") {
					t.Fatalf("unexpected temporary root: %q", scratch)
				}
				if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
					t.Fatalf("entry did not remove its own temporary root: %v\n%s", err, out)
				}
			}
			if test.ok {
				for _, name := range []string{"source-manifest.sha256", "toolchain-sha256.txt", "environment.txt", "environment-skips.txt", "run.log", "proxy-race.json", "nido-proxy-race.json", "cli-isolation.log", "quickstart-cli-isolation.log", "result.txt"} {
					data, err := os.ReadFile(filepath.Join(output, name))
					if err != nil || len(data) == 0 {
						t.Fatalf("missing complete evidence %s: %v", name, err)
					}
				}
				result, _ := os.ReadFile(filepath.Join(output, "result.txt"))
				if string(result) != "exit_status=0\n" {
					t.Fatalf("wrong final result: %q", result)
				}
				omissions, _ := os.ReadFile(filepath.Join(output, "environment-skips.txt"))
				if strings.HasPrefix(test.mode, "ipv6-") {
					for _, evidenceName := range []string{"environment.txt", "environment-skips.txt", "run.log"} {
						data, err := os.ReadFile(filepath.Join(output, evidenceName))
						if err != nil {
							t.Fatal(err)
						}
						for _, subtest := range []string{"IPv6_loopback", "IPv6_wildcard"} {
							if test.mode == "ipv6-both-skips" || test.mode == "ipv6-loopback-skip" && subtest == "IPv6_loopback" || test.mode == "ipv6-wildcard-skip" && subtest == "IPv6_wildcard" {
								if !strings.Contains(string(data), "TestLinuxKernelIPv6AndWildcardOwnership/"+subtest) {
									t.Fatalf("allowed omission is absent from %s: %s", evidenceName, data)
								}
							}
						}
					}
					if !strings.Contains(out, "ENVIRONMENT SKIP（未计为通过）") || !strings.Contains(out, "BOUNDARY: IPv6") || !strings.Contains(string(omissions), "不计为 PASS") {
						t.Fatalf("allowed omission was counted as ordinary coverage:\n%s\n%s", out, omissions)
					}
					if !strings.Contains(out, "bind: cannot assign requested address") || !strings.Contains(string(omissions), "IPv6 unavailable evidence:") {
						t.Fatalf("entry omitted the precise environmental reason:\n%s\n%s", out, omissions)
					}
				} else if string(omissions) != "environment_skips=none\n" || strings.Contains(out, "ENVIRONMENT SKIP") {
					t.Fatalf("entry invented an IPv6 omission: %q\n%s", omissions, out)
				}
			}
			if test.mode == "existing-output" || test.mode == "linked-output" {
				data, err := os.ReadFile(filepath.Join(state, "preserved", "sentinel"))
				if err != nil || string(data) != "EXISTING OUTPUT\n" {
					t.Fatalf("entry changed an existing output: %v, %q", err, data)
				}
				if len(calls) != 0 {
					t.Fatal("entry executed bubblewrap with an existing output")
				}
			}
		})
	}
}

func runProxyQuickStartLinuxEntryFixture(t *testing.T, mode string) (string, string, string, error) {
	t.Helper()
	for _, path := range []string{"/bin/bash", "/usr/bin/gcc"} {
		if _, err := os.Stat(path); err != nil {
			t.Skip("Linux entry fixture requires existing " + path)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tests", "proxy-quickstart-linux-native.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	state := filepath.Join(root, "state")
	source := filepath.Join(root, "source")
	bin := filepath.Join(root, "bin")
	goTool := filepath.Join(root, "toolchain", "bin", "go")
	output := filepath.Join(root, "evidence")
	for _, path := range []string{state, source, bin, filepath.Join(root, "home"), filepath.Dir(goTool), filepath.Join(root, "toolchain", "src"), filepath.Join(root, "toolchain", "pkg")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cmd", "internal", "tests", "scripts", ".github", "vendor"} {
		if err := os.Mkdir(filepath.Join(source, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Makefile", "VERSION", ".go-version", "go.mod", "go.sum", "vendor/modules.txt", "tests/proxy-quickstart-cli-isolation.sh"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("inert public source fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"uname", "id", "file", "readlink", "sha256sum", "timeout", "bwrap"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(proxyQuickStartLinuxEntryMock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(goTool, []byte("#!/bin/sh\nprintf 'unexpected direct Go execution\\n' >&2\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if mode == "linked-source-input" {
		if err := os.Symlink(root, filepath.Join(source, "internal", "escape")); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "existing-output" || mode == "linked-output" {
		preserved := filepath.Join(state, "preserved")
		if err := os.Mkdir(preserved, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(preserved, "sentinel"), []byte("EXISTING OUTPUT\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "existing-output" {
			output = preserved
		} else if err := os.Symlink(preserved, output); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", script)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + filepath.Join(root, "home"),
		"LANG=C", "LC_ALL=C", "TZ=UTC",
		"NIDO_TEST_QUICKSTART_SOURCE=" + source, "NIDO_TEST_QUICKSTART_GO=" + goTool,
		"NIDO_TEST_QUICKSTART_OUTPUT=" + output,
		"NIDO_QUICKSTART_ENTRY_STATE=" + state, "NIDO_QUICKSTART_ENTRY_MODE=" + mode,
		// A poison input must never reach a namespace's real-core environment.
		"NIDO_TEST_MIHOMO=/nonexistent/never-execute-real-core",
	}
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("entry fixture exceeded its deadline: %v\n%s", ctx.Err(), out)
	}
	return string(out), state, output, runErr
}

const proxyQuickStartLinuxEntryMock = `#!/bin/bash
set -euo pipefail
tool="${0##*/}"
unexpected() { printf 'unexpected fixture command: %s\n' "$tool" >&2; exit 91; }
case "$tool" in
  uname)
    case "$*" in
      -s) if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == wrong-os ]]; then echo Darwin; else echo Linux; fi;;
      -m) if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == wrong-arch ]]; then echo aarch64; else echo x86_64; fi;;
      -r) echo fixture-kernel;;
      *) unexpected;;
    esac
    exit 0;;
  id)
    [[ "$*" == -u ]] || unexpected
    if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == root-user ]]; then echo 0; else echo 1000; fi
    exit 0;;
  file)
    if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == wrong-sdk ]]; then echo 'Mach-O 64-bit executable arm64'
    else echo 'ELF 64-bit LSB executable, x86-64, statically linked'; fi
    exit 0;;
  readlink) [[ "$1" == -f && "$#" == 2 ]] || unexpected; printf '%s\n' "$2"; exit 0;;
  sha256sum)
    for arg in "$@"; do printf '%064d  %s\n' 0 "$arg"; done
    exit 0;;
  timeout) [[ "$1 $2" == '--kill-after=5s 20m' ]] || unexpected; shift 2; exec "$@";;
  bwrap) ;;
  *) unexpected;;
esac
printf '%s\n' "$@" > "$NIDO_QUICKSTART_ENTRY_STATE/bwrap-args"
previous=''
work=''
for arg in "$@"; do
  if [[ "$previous" == --bind ]]; then work="$arg"; break; fi
  previous="$arg"
done
[[ -d "$work" && "${work##*/}" == work ]] || unexpected
printf '%s\n' "$work" > "$NIDO_QUICKSTART_ENTRY_STATE/work-path"
if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == namespace-fail ]]; then printf 'namespace setup refused\n' >&2; exit 73; fi
pass() { printf '{"Action":"pass","Package":"fixture","Test":"%s","Elapsed":0.01}\n' "$1"; }
ipv6_skip() {
  reason_test="$1"
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" != ipv6-other-test-reason ]] || reason_test=unrelated
  reason='cannot assign requested address'
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" != ipv6-unclassified-reason ]] || reason='permission denied'
  if [[ "$NIDO_QUICKSTART_ENTRY_MODE" != ipv6-missing-reason ]]; then
    printf '{"Action":"output","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"TestLinuxKernelIPv6AndWildcardOwnership/%s","Output":"    process_linux_test.go:68: IPv6 loopback unavailable in this network namespace: listen tcp6 [::1]:0: bind: %s\\n"}\n' "$reason_test" "$reason"
  fi
  printf '{"Action":"skip","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"TestLinuxKernelIPv6AndWildcardOwnership/%s","Elapsed":0.01}\n' "$1"
}
package_pass() { [[ "$NIDO_QUICKSTART_ENTRY_MODE" == package-pass-missing ]] || printf '{"Action":"pass","Package":"fixture","Elapsed":0.01}\n'; }
if [[ "$NIDO_QUICKSTART_ENTRY_MODE" != missing-log ]]; then
  {
    [[ "$NIDO_QUICKSTART_ENTRY_MODE" == quickstart-pass-missing ]] || pass TestQuickStartOptionalRoutes
    [[ "$NIDO_QUICKSTART_ENTRY_MODE" == node-pass-missing ]] || pass TestParseNodeURLSSCredentials
    if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == test-skip ]]; then echo '{"Action":"skip","Package":"fixture","Test":"TestOS","Elapsed":0.01}'; fi
    if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == test-fail ]]; then echo '{"Action":"fail","Package":"fixture","Test":"TestOS","Elapsed":0.01}'; fi
    case "$NIDO_QUICKSTART_ENTRY_MODE" in
      ipv6-loopback-skip) ipv6_skip IPv6_loopback;;
      ipv6-wildcard-skip) ipv6_skip IPv6_wildcard;;
      ipv6-both-skips) ipv6_skip IPv6_loopback; ipv6_skip IPv6_wildcard;;
      ipv6-prefix-skip) ipv6_skip IPv6_loopback/unrelated;;
      ipv6-missing-reason|ipv6-unclassified-reason|ipv6-other-test-reason) ipv6_skip IPv6_loopback;;
      ipv6-wrong-package) echo '{"Action":"skip","Package":"fixture","Test":"TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback","Elapsed":0.01}';;
      ipv6-fail) echo '{"Action":"fail","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback","Elapsed":0.01}';;
    esac
    package_pass
  } > "$work/proxy-race.json"
fi
{ pass TestProxyQuickStart; [[ "$NIDO_QUICKSTART_ENTRY_MODE" == remediation-pass-missing ]] || pass TestProxyRemediation; if [[ "$NIDO_QUICKSTART_ENTRY_MODE" == ipv6-nido-skip ]]; then ipv6_skip IPv6_loopback; fi; package_pass; } > "$work/nido-proxy-race.json"
if [[ "$NIDO_QUICKSTART_ENTRY_MODE" != cli-pass-missing ]]; then echo 'Nido 单目录 CLI 隔离验证通过' > "$work/cli-isolation.log"; fi
{
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" == new-cli-pass-missing ]] || echo 'Proxy quick start CLI 隔离验证通过'
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" == default-skip-pass-missing ]] || echo 'Proxy quick start 默认 Skip 与重复配置验证通过'
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" == tui-keys-pass-missing ]] || echo 'Proxy 公共 TUI 方向键、密钥隐藏与 Esc 取消验证通过'
  [[ "$NIDO_QUICKSTART_ENTRY_MODE" == retention-pass-missing ]] || echo 'Proxy 配置、缓存与数据保留及卸载重装隔离验证通过'
} > "$work/quickstart-cli-isolation.log"
[[ "$NIDO_QUICKSTART_ENTRY_MODE" != command-fail-after-evidence ]] || exit 19
printf 'strict bubblewrap fixture completed\n'
`
