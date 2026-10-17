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

// Exercise the actual entry scripts, while every service, kernel/platform and
// build command is a strict local fixture. None of the input files is runnable.
func TestProxyLinuxEntriesRequireActualPassEvidence(t *testing.T) {
	for _, entry := range []string{"proxy-linux-isolation.sh", "proxy-linux-bwrap.sh"} {
		t.Run(entry, func(t *testing.T) {
			for _, test := range []struct {
				name, mode string
				update, ok bool
			}{
				{"default-baseline-cross-skip", "default", false, true},
				{"explicit-cross-pass", "pass", true, true},
				{"explicit-cross-missing", "cross-missing", true, false},
				{"explicit-cross-skip", "cross-skip", true, false},
				{"baseline-missing", "baseline-missing", true, false},
				{"baseline-skip", "baseline-skip", false, false},
				{"os-missing", "os-missing", false, false},
				{"os-skip", "os-skip", false, false},
				{"proxy-exit-after-pass", "proxy-exit", false, false},
				{"version-exit-after-output", "version-exit", false, false},
				{"real-exit-after-pass", "real-exit", true, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					output, calls, err := runProxyLinuxEntryFixture(t, entry, test.mode, test.update)
					if (err == nil) != test.ok {
						t.Fatalf("success=%v, want %v: %v\n%s", err == nil, test.ok, err, output)
					}
					if test.ok && !strings.Contains(output, "隔离验证通过") {
						t.Fatalf("successful command omitted final evidence: %s", output)
					}
					if test.ok {
						for _, program := range []string{"proxy.test", "mihomo", "nido.test"} {
							option := "-test.v\t"
							if program == "mihomo" {
								option = "-v\n"
							}
							if strings.Count(calls, "\t/inputs/"+program+"\t"+option) != 1 {
								t.Fatalf("entry did not invoke exactly one isolated %s: %s", program, calls)
							}
						}
					}
					if test.ok && entry == "proxy-linux-isolation.sh" {
						assertProxyLinuxFixtureCleanup(t, calls, true)
					}
				})
			}
		})
	}
}

func TestProxyLinuxDockerCleanupRetainsChangedImageOwner(t *testing.T) {
	output, calls, err := runProxyLinuxEntryFixture(t, "proxy-linux-isolation.sh", "foreign-owner", false)
	if err == nil || !strings.Contains(output, "归属发生变化，未删除") || !strings.Contains(output, "隔离清理未完成") {
		t.Fatalf("changed image owner did not fail closed: %v\n%s", err, output)
	}
	assertProxyLinuxFixtureCleanup(t, calls, false)
}

func assertProxyLinuxFixtureCleanup(t *testing.T, calls string, removeImage bool) {
	t.Helper()
	removed := 0
	images := 0
	for _, call := range strings.Split(calls, "\n") {
		fields := strings.Split(call, "\t")
		if len(fields) >= 3 && fields[0] == "docker" && fields[1] == "container" && fields[2] == "rm" {
			if len(fields) != 5 || fields[3] != "-f" || fields[4] != strings.Repeat("a", 64) && fields[4] != strings.Repeat("b", 64) && fields[4] != strings.Repeat("c", 64) {
				t.Fatalf("cleanup removed a non-fixture container: %q", call)
			}
			removed++
		}
		if len(fields) >= 3 && fields[0] == "docker" && fields[1] == "image" && fields[2] == "rm" {
			if len(fields) != 4 || !strings.HasPrefix(fields[3], "nido-proxy-linux-e2e:nido-proxy-linux-") {
				t.Fatalf("cleanup did not use this run's unique image tag: %q", call)
			}
			images++
		}
		if strings.Contains(call, "prune") {
			t.Fatalf("cleanup used a global operation: %q", call)
		}
	}
	wantImages := 0
	if removeImage {
		wantImages = 1
	}
	if removed != 3 || images != wantImages {
		t.Fatalf("unexpected cleanup: containers=%d images=%d\n%s", removed, images, calls)
	}
}

func runProxyLinuxEntryFixture(t *testing.T, entry, mode string, update bool) (string, string, error) {
	t.Helper()
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("Linux entry fixtures require the existing /bin/bash")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tests", entry))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"bin", "inputs", "tmp", "home", "state"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"nido", "nido.test", "proxy.test", "mihomo", "mihomo-update"} {
		if err := os.WriteFile(filepath.Join(root, "inputs", name), []byte("inert entry fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"docker", "bwrap", "file", "go", "uname"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(proxyLinuxEntryMock), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", script)
	cmd.WaitDelay = time.Second
	// Start from an empty environment: host NIDO_TEST_*, Docker settings and
	// inherited build/proxy overrides must never influence this fixture.
	cmd.Env = []string{
		"PATH=" + filepath.Join(root, "bin") + ":/usr/bin:/bin",
		"HOME=" + filepath.Join(root, "home"), "TMPDIR=" + filepath.Join(root, "tmp"),
		"LANG=C", "LC_ALL=C", "TZ=UTC",
		"GO=" + filepath.Join(root, "bin", "go"),
		"GOCACHE=" + filepath.Join(root, "go-cache"),
		"GOMODCACHE=" + filepath.Join(root, "go-mod"), "GOPATH=" + filepath.Join(root, "go-path"),
		"NIDO_TEST_LINUX_RACE=1", "NIDO_TEST_LINUX_BIN_DIR=" + filepath.Join(root, "inputs"),
		"NIDO_TEST_MIHOMO=" + filepath.Join(root, "inputs", "mihomo"),
		"NIDO_ENTRY_FIXTURE_STATE=" + filepath.Join(root, "state"), "NIDO_ENTRY_FIXTURE_MODE=" + mode,
	}
	if update {
		cmd.Env = append(cmd.Env, "NIDO_TEST_MIHOMO_UPDATE="+filepath.Join(root, "inputs", "mihomo-update"))
	}
	out, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("entry fixture exceeded its deadline: %v\n%s", ctx.Err(), out)
	}
	calls, err := os.ReadFile(filepath.Join(root, "state", "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "unexpected fixture command") {
		t.Fatalf("fixture did not handle a command: %s", out)
	}
	return string(out), string(calls), runErr
}

const proxyLinuxEntryMock = `#!/bin/bash
set -euo pipefail
tool="${0##*/}"
printf '%s' "$tool" >> "$NIDO_ENTRY_FIXTURE_STATE/calls"
printf '\t%s' "$@" >> "$NIDO_ENTRY_FIXTURE_STATE/calls"
printf '\n' >> "$NIDO_ENTRY_FIXTURE_STATE/calls"
unexpected() { printf 'unexpected fixture command: %s\n' "$tool" >&2; exit 91; }
case "$tool" in
  uname)
    case "$*" in -s) echo Linux;; -m) echo x86_64;; -r) echo fixture-kernel;; *) unexpected;; esac
    exit 0;;
  file) echo 'ELF 64-bit LSB executable, x86-64, statically linked'; exit 0;;
  go)
    if [[ "$*" == version ]]; then echo 'go version fixture linux/amd64'; exit 0; fi
    if [[ "$1 $2" == 'version -m' ]]; then
      printf '\tpath\tgithub.com/MiracleHYH/CDEToolkit/cmd/nido\n'
      printf '\tbuild\t%s\n' GOOS=linux GOARCH=amd64 CGO_ENABLED=1 -race=true
      exit 0
    fi
    if [[ "$1 $2" == 'tool nm' ]]; then
      case "${3##*/}" in
        nido.test) symbol=github.com/MiracleHYH/CDEToolkit/internal/nido.TestProxyRealMihomo;;
        proxy.test) symbol=github.com/MiracleHYH/CDEToolkit/internal/components/proxy.TestMain;;
        *) unexpected;;
      esac
      printf '100 T %s\n' "$symbol" runtime.raceinit __tsan_init
      exit 0
    fi
    unexpected;;
  docker)
    case "${1:-} ${2:-}" in
      'info --format') echo 'linux|amd64|fixture-kernel'; exit 0;;
      'image inspect')
        [[ -f "$NIDO_ENTRY_FIXTURE_STATE/built" ]] || exit 1
        [[ "${3:-}" == --format ]] || unexpected
        if [[ "$4" == '{{.Os}}/{{.Architecture}}' ]]; then echo linux/amd64
        elif [[ "$NIDO_ENTRY_FIXTURE_MODE" == foreign-owner ]]; then echo unrelated-run
        else cat "$NIDO_ENTRY_FIXTURE_STATE/owner"; fi
        exit 0;;
      'image ls') echo sha256:fixture-image; exit 0;;
      'image rm') exit 0;;
      'container ls')
        for arg in "$@"; do [[ "$arg" != id=* ]] || echo "${arg#id=}"; done
        exit 0;;
      'container rm') exit 0;;
    esac
    if [[ "${1:-}" == build ]]; then
      for arg in "$@"; do
        [[ "$arg" != NIDO_PROXY_RUN=* ]] || printf '%s\n' "${arg#NIDO_PROXY_RUN=}" > "$NIDO_ENTRY_FIXTURE_STATE/owner"
      done
      : > "$NIDO_ENTRY_FIXTURE_STATE/built"
      exit 0
    fi
    [[ "${1:-}" == run ]] || unexpected;;
  bwrap) ;;
  *) unexpected;;
esac

program=''
cidfile=''
previous=''
has_update=0
for arg in "$@"; do
  [[ "$previous" != --cidfile ]] || cidfile="$arg"
  case "$arg" in
    /inputs/proxy.test|/inputs/mihomo|/inputs/nido.test) program="$arg";;
    NIDO_TEST_MIHOMO_UPDATE|NIDO_TEST_MIHOMO_UPDATE=*) has_update=1;;
  esac
  previous="$arg"
done
case "$program" in
  /inputs/proxy.test) cid=a;; /inputs/mihomo) cid=b;; /inputs/nido.test) cid=c;; *) unexpected;;
esac
if [[ -n "$cidfile" ]]; then
  printf '%64s\n' '' | tr ' ' "$cid" > "$cidfile"
fi
case "$program" in
  /inputs/proxy.test)
    case "$NIDO_ENTRY_FIXTURE_MODE" in
      os-missing) echo 'PASS';;
      os-skip) echo '--- SKIP: TestOSFixture (0.00s)';;
      *) echo '--- PASS: TestOSFixture (0.00s)';;
    esac
    [[ "$NIDO_ENTRY_FIXTURE_MODE" != proxy-exit ]] || exit 17;;
  /inputs/mihomo)
    echo 'Mihomo Meta v1.2.3 linux amd64'
    [[ "$NIDO_ENTRY_FIXTURE_MODE" != version-exit ]] || exit 17;;
  /inputs/nido.test)
    case "$NIDO_ENTRY_FIXTURE_MODE" in
      baseline-missing) ;;
      baseline-skip) echo '--- SKIP: TestProxyRealMihomo (0.00s)';;
      *) echo '--- PASS: TestProxyRealMihomo (0.00s)';;
    esac
    case "$NIDO_ENTRY_FIXTURE_MODE" in
      default|cross-skip) echo '--- SKIP: TestProxyRealMihomoCrossVersion (0.00s)';;
      cross-missing) ;;
      *)
        if (( has_update )); then echo '--- PASS: TestProxyRealMihomoCrossVersion (0.00s)'
        else echo '--- SKIP: TestProxyRealMihomoCrossVersion (0.00s)'; fi;;
    esac
    [[ "$NIDO_ENTRY_FIXTURE_MODE" != real-exit ]] || exit 17;;
esac
`
