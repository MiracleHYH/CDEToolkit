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

// The entry itself is real shell; every platform, artifact and namespace
// command is inert. No test input is executable and no core is ever started.
func TestProxyQuickStartRealLinuxEntryRequiresActualEvidence(t *testing.T) {
	for _, test := range []struct {
		mode string
		ok   bool
	}{
		{"pass", true}, {"namespace-fail", false}, {"version-fail", false},
		{"version-missing", false}, {"test-command-fail", false},
		{"test-pass-missing", false}, {"old-test-pass", false}, {"overall-pass-missing", false},
		{"node-matrix-missing", false}, {"node-case-missing", false}, {"node-parent-pass", false},
		{"node-prefix-pass", false}, {"node-suffix-pass", false},
		{"test-skip", false}, {"test-fail", false}, {"race-output", false}, {"child-race-report", false}, {"missing-race-directory", false},
		{"wrong-os", false}, {"wrong-arch", false}, {"root-user", false},
		{"dynamic-cli", false}, {"dynamic-test", false}, {"wrong-core", false},
		{"cli-no-race", false}, {"cli-wrong-package", false}, {"test-no-symbol", false},
		{"test-no-race", false}, {"test-no-tsan", false}, {"missing-cli", false},
		{"linked-core", false}, {"existing-output", false}, {"linked-output", false},
	} {
		t.Run(test.mode, func(t *testing.T) {
			out, state, evidence, err := runProxyQuickStartRealLinuxEntryFixture(t, test.mode)
			if (err == nil) != test.ok {
				t.Fatalf("success=%v, want %v: %v\n%s", err == nil, test.ok, err, out)
			}
			if strings.Contains(out, "unexpected fixture command") {
				t.Fatalf("fixture missed a command:\n%s", out)
			}
			if strings.Contains(out, "PASS: 普通用户 Linux amd64") != test.ok {
				t.Fatalf("entry emitted an inaccurate success claim:\n%s", out)
			}
			calls, _ := os.ReadFile(filepath.Join(state, "calls"))
			if test.ok {
				if strings.Count(string(calls), "bwrap\n") != 2 || strings.Count(string(calls), "--ro-bind\n") != 2 {
					t.Fatalf("entry must run only core version and new E2E with one artifact mount: %s", calls)
				}
				for _, required := range []string{"--unshare-all\n", "--clearenv\n", "--cap-drop\nALL\n", "--setenv\nHOME\n/tmp/home\n", "--setenv\nPATH\n/nonexistent\n", "--setenv\nNIDO_TEST_NIDO\n/inputs/nido\n", "--setenv\nNIDO_TEST_MIHOMO\n/inputs/mihomo\n", "--setenv\nNIDO_TEST_RACE_REPORT_DIR\n/tmp/nido-proxy-race-quickstart\n", "-test.run\n^TestProxyRealMihomoQuickStart$\n"} {
					if !strings.Contains(string(calls), required) {
						t.Fatalf("missing isolation flag %q: %s", required, calls)
					}
				}
				for _, forbidden := range []string{"--share-net\n", "--share-pid\n", "/source\n", "/lib\n", "/usr\n", "NIDO_TEST_MIHOMO_UPDATE\n", "--setenv\nHTTP_PROXY\n"} {
					if strings.Contains(string(calls), forbidden) {
						t.Fatalf("entry exposed forbidden surface %q: %s", forbidden, calls)
					}
				}
				for _, name := range []string{"artifact-types.txt", "nido-build.txt", "nido-test-symbols.txt", "input-sha256.txt", "go-version.txt", "environment.txt", "mihomo-version.log", "quickstart-real.log", "result.txt"} {
					data, err := os.ReadFile(filepath.Join(evidence, name))
					if err != nil || len(data) == 0 {
						t.Fatalf("missing evidence %s: %v", name, err)
					}
				}
				result, _ := os.ReadFile(filepath.Join(evidence, "result.txt"))
				if string(result) != "exit_status=0\n" {
					t.Fatalf("wrong status: %q", result)
				}
			}
			if data, err := os.ReadFile(filepath.Join(state, "scratch-path")); err == nil {
				scratch := strings.TrimSpace(string(data))
				if !strings.HasPrefix(filepath.Base(scratch), "nido-proxy-quickstart-real.") {
					t.Fatalf("unexpected scratch %q", scratch)
				}
				if _, err := os.Lstat(scratch); !os.IsNotExist(err) {
					t.Fatalf("entry did not clean its scratch: %v", err)
				}
			}
			if test.mode == "existing-output" || test.mode == "linked-output" {
				data, err := os.ReadFile(filepath.Join(state, "preserved", "sentinel"))
				if err != nil || string(data) != "EXISTING OUTPUT\n" || len(calls) != 0 {
					t.Fatalf("existing output changed or namespace ran: %v %q %s", err, data, calls)
				}
			}
			if test.mode == "child-race-report" {
				data, err := os.ReadFile(filepath.Join(evidence, "race-reports", "race.123"))
				if err != nil || !strings.Contains(string(data), "DATA RACE") {
					t.Fatalf("failure did not preserve child report: %v %q", err, data)
				}
			}
		})
	}
}

func runProxyQuickStartRealLinuxEntryFixture(t *testing.T, mode string) (string, string, string, error) {
	t.Helper()
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("entry fixture requires existing /bin/bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tests", "proxy-quickstart-real-linux.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	state, bin, inputs := filepath.Join(root, "state"), filepath.Join(root, "bin"), filepath.Join(root, "inputs")
	for _, dir := range []string{state, bin, inputs, filepath.Join(root, "home")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"nido", "nido.test", "mihomo"} {
		if err := os.WriteFile(filepath.Join(inputs, name), []byte("inert artifact fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"uname", "id", "go", "file", "sha256sum", "timeout", "bwrap"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(proxyQuickStartRealLinuxEntryMock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "missing-cli" {
		if err := os.Remove(filepath.Join(inputs, "nido")); err != nil {
			t.Fatal(err)
		}
	}
	core := filepath.Join(inputs, "mihomo")
	if mode == "linked-core" {
		core = filepath.Join(inputs, "mihomo-link")
		if err := os.Symlink(filepath.Join(inputs, "mihomo"), core); err != nil {
			t.Fatal(err)
		}
	}
	evidence := filepath.Join(root, "evidence")
	if mode == "existing-output" || mode == "linked-output" {
		preserved := filepath.Join(state, "preserved")
		if err := os.Mkdir(preserved, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(preserved, "sentinel"), []byte("EXISTING OUTPUT\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if mode == "existing-output" {
			evidence = preserved
		} else if err := os.Symlink(preserved, evidence); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", script)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + filepath.Join(root, "home"),
		"LANG=C", "LC_ALL=C", "TZ=UTC", "GO=" + filepath.Join(bin, "go"),
		"NIDO_TEST_LINUX_BIN_DIR=" + inputs, "NIDO_TEST_MIHOMO=" + core,
		"NIDO_TEST_QUICKSTART_REAL_OUTPUT=" + evidence,
		"NIDO_REAL_ENTRY_STATE=" + state, "NIDO_REAL_ENTRY_MODE=" + mode,
		"NIDO_TEST_MIHOMO_UPDATE=/poison/never-forward", "HTTP_PROXY=http://127.0.0.1:1",
	}
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("fixture exceeded deadline: %v\n%s", ctx.Err(), out)
	}
	return string(out), state, evidence, runErr
}

const proxyQuickStartRealLinuxEntryMock = `#!/bin/bash
set -euo pipefail
tool="${0##*/}"
mode="$NIDO_REAL_ENTRY_MODE"
unexpected() { printf 'unexpected fixture command: %s\n' "$tool" >&2; exit 91; }
case "$tool" in
  uname)
    case "$*" in
      -s) if [[ "$mode" == wrong-os ]]; then echo Darwin; else echo Linux; fi;;
      -m) if [[ "$mode" == wrong-arch ]]; then echo aarch64; else echo x86_64; fi;;
      -r) echo fixture-kernel;; *) unexpected;;
    esac; exit 0;;
  id) [[ "$*" == -u ]] || unexpected; if [[ "$mode" == root-user ]]; then echo 0; else echo 1000; fi; exit 0;;
  file)
    name="${!#}"
    if [[ "$mode" == dynamic-cli && "${name##*/}" == nido || "$mode" == dynamic-test && "${name##*/}" == nido.test ]]; then
      echo 'ELF 64-bit LSB executable, x86-64, dynamically linked'
    elif [[ "$mode" == wrong-core && "${name##*/}" == mihomo ]]; then echo 'ELF 64-bit LSB executable, ARM aarch64, statically linked'
    else echo 'ELF 64-bit LSB executable, x86-64, statically linked'; fi
    exit 0;;
  sha256sum) for arg in "$@"; do printf '%064d  %s\n' 0 "$arg"; done; exit 0;;
  go)
    if [[ "$*" == version ]]; then echo 'go version fixture linux/amd64'; exit 0; fi
    if [[ "$1 $2" == 'version -m' ]]; then
      if [[ "$mode" == cli-wrong-package ]]; then printf '\tpath\twrong/package\n'; else printf '\tpath\tgithub.com/MiracleHYH/CDEToolkit/cmd/nido\n'; fi
      printf '\tbuild\t%s\n' GOOS=linux GOARCH=amd64 CGO_ENABLED=1
      [[ "$mode" == cli-no-race ]] || printf '\tbuild\t-race=true\n'
      exit 0
    fi
    if [[ "$1 $2" == 'tool nm' ]]; then
      [[ "$mode" == test-no-symbol ]] || echo '001 T github.com/MiracleHYH/CDEToolkit/internal/nido.TestProxyRealMihomoQuickStart'
      [[ "$mode" == test-no-race ]] || echo '002 T runtime.raceinit'
      [[ "$mode" == test-no-tsan ]] || echo '003 T __tsan_init'
      exit 0
    fi
    unexpected;;
  timeout)
    [[ "$1" == --kill-after=5s && ( "$2" == 1m || "$2" == 15m ) ]] || unexpected
    shift 2; exec "$@";;
  bwrap) ;;
  *) unexpected;;
esac
printf 'bwrap\n' >> "$NIDO_REAL_ENTRY_STATE/calls"
printf '%s\n' "$@" >> "$NIDO_REAL_ENTRY_STATE/calls"
previous=''
tmp=''
for arg in "$@"; do
  if [[ "$previous" == --bind ]]; then tmp="$arg"; break; fi
  previous="$arg"
done
[[ -d "$tmp" && "${tmp##*/}" == tmp ]] || unexpected
printf '%s\n' "${tmp%/tmp}" > "$NIDO_REAL_ENTRY_STATE/scratch-path"
[[ "$mode" != namespace-fail ]] || { echo 'namespace setup refused' >&2; exit 73; }
if [[ "${!#}" == -v ]]; then
  [[ "$mode" == version-missing ]] || echo 'Mihomo Meta v1.19.32 linux amd64 fixture'
  [[ "$mode" != version-fail ]] || exit 19
  exit 0
fi
[[ "$mode" == missing-race-directory ]] || mkdir -p "$tmp/nido-proxy-race-quickstart"
[[ "$mode" != child-race-report ]] || echo 'WARNING: DATA RACE' > "$tmp/nido-proxy-race-quickstart/race.123"
echo '=== RUN   TestProxyRealMihomoQuickStart'
case "$mode" in
  test-pass-missing) ;;
  old-test-pass) echo '--- PASS: TestProxyRealMihomo (0.10s)';;
  test-skip) echo '--- SKIP: TestProxyRealMihomoQuickStart (0.10s)';;
  test-fail) echo '--- FAIL: TestProxyRealMihomoQuickStart (0.10s)';;
  *) echo '--- PASS: TestProxyRealMihomoQuickStart (0.10s)';;
esac
if [[ "$mode" != node-matrix-missing ]]; then
  for node_case in ss-plain-percent ss-base64-userinfo ss-legacy-base64 ss-2022 ss-obfs ss-v2ray-plugin \
    vmess-tcp vmess-ws-tls-early-data vless-tls-ws vless-tls-grpc vless-reality-tcp \
    trojan-ws-tls hy2-salamander-pin http-auth https-auth-tls socks5-auth socks5h-anonymous; do
    if [[ "$node_case" == ss-plain-percent ]]; then
      case "$mode" in
        node-case-missing) continue;;
        node-parent-pass) echo '    --- PASS: TestProxyRealMihomoQuickStart/nodes (0.01s)'; continue;;
        node-prefix-pass) echo '    --- PASS: ExtraTestProxyRealMihomoQuickStart/nodes/ss-plain-percent (0.01s)'; continue;;
        node-suffix-pass) echo '    --- PASS: TestProxyRealMihomoQuickStart/nodes/ss-plain-percent-extra (0.01s)'; continue;;
      esac
    fi
    printf '    --- PASS: TestProxyRealMihomoQuickStart/nodes/%s (0.01s)\n' "$node_case"
  done
fi
[[ "$mode" != race-output ]] || echo 'WARNING: DATA RACE'
[[ "$mode" == overall-pass-missing ]] || echo PASS
[[ "$mode" != test-command-fail ]] || exit 23
`
