package nido

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

func quickStartScriptSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "proxy-quickstart-cli-isolation.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestProxyQuickStartScriptRequiresProxyOnlyLifecycleEvidence(t *testing.T) {
	source := quickStartScriptSource(t)
	for _, required := range []string{
		`prompted_nido_plan() {`,
		`local fixture_plan=true`,
		`while IFS=$'\t' read -r label keys`,
		`printf '%b' "$keys" >&3`,
		`'安装 Proxy'`,
		`'是否启用独立下载入口' '\033[B\r'`,
		`'日常代理端口' '\0001\001317890\r'`,
		`'回环管理地址' '\r'`,
		`'保存 Proxy 配置（保持停止）'`,
		`root="$scratch/default skip root"`,
		`grep -Fq 'Skip（默认）'`,
		`! grep -Fq 'Provider URL'`,
		`! -e "$root/proxy/config.yaml.back"`,
		`proxy configure`,
		`nido proxy remove`,
		`proxy install --artifact`,
		`providers/`,
		`cmp "$HOME/.gitconfig" "$scratch/before/.gitconfig"`,
		`cmp "$codex_home/.env" "$scratch/before/codex/.env"`,
		`Proxy quick start 默认 Skip 与重复配置验证通过`,
		`Proxy 公共 TUI 方向键、密钥隐藏与 Esc 取消验证通过`,
		`Proxy 配置、缓存与数据保留及卸载重装隔离验证通过`,
		`Proxy quick start CLI 隔离验证通过`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("quick start entry omitted required evidence %q", required)
		}
	}
	for _, obsolete := range []string{
		`nido proxy bind`, `--tool`, `nido codex`, `nido download`, `nido network set`,
		`proxy configure --file`, `quickstart_plan 'Proxy 配置'`,
		`Proxy 主 TOML Mode/Listener 与下载绑定隔离验证通过`,
		`"api_listener"`, `grep -Fq nido-api-proxy`, `stat -c %a`,
	} {
		if strings.Contains(source, obsolete) {
			t.Fatalf("Proxy-only entry retained removed behavior: %s", obsolete)
		}
	}
}

// Reuse the exact shell bridge with an inert public UI session. This observes
// submitted values, including editing a nonempty fallback; no Manager or core
// is constructed. The child is this test binary, reached through a private shim.
func TestProxyQuickStartScriptUIEditsFallbackThroughPTY(t *testing.T) {
	if os.Getenv("NIDO_FIXTURE_UI_FIELD_CHILD") == "1" {
		s, err := terminalui.New(os.Stdin, os.Stderr)
		if err != nil {
			os.Exit(91)
		}
		var result struct {
			Port, Target string
			SecretOK     bool
		}
		code, err := s.RunTask(func() int {
			var err error
			result.Port, err = s.Input("惰性端口字段", "7890")
			if err != nil {
				return 92
			}
			result.Target, err = s.Input("惰性地址字段", "https://default.invalid/check")
			if err != nil {
				return 93
			}
			secret, err := s.InputSecret("惰性秘密字段", "")
			if err != nil {
				return 94
			}
			result.SecretOK = secret == "synthetic-ui-private-value"
			return 0
		})
		if err != nil || code != 0 {
			os.Exit(95)
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			os.Exit(96)
		}
		os.Exit(0)
	}
	source := quickStartScriptSource(t)
	start := strings.Index(source, "prompted_nido() {\n")
	if start < 0 {
		t.Fatal("production private PTY wrapper missing")
	}
	end := strings.Index(source[start:], "\n}\n\n")
	if end < 0 {
		t.Fatal("production private PTY wrapper incomplete")
	}
	wrapper := source[start : start+end+3]
	scratch := t.TempDir()
	binary := filepath.Join(scratch, "inert ui cli")
	if err := os.WriteFile(binary, []byte(`#!/bin/bash
set -euo pipefail
shift 2
exec "$NIDO_FIXTURE_TEST_EXECUTABLE" -test.run '^TestProxyQuickStartScriptUIEditsFallbackThroughPTY$'
`), 0700); err != nil {
		t.Fatal(err)
	}
	program := `set -euo pipefail
umask 077
binary=$1; scratch=$2; root="$scratch/software root"; quickstart_mode=current; fixture_plan=true
` + wrapper + `
printf '%s\t%s\n' \
  '惰性端口字段' '\0001\001317890\r' \
  '惰性地址字段' '\0001\0013https://replacement.invalid/check\r' \
  '惰性秘密字段' '\0001\0013synthetic-ui-private-value\r' |
  prompted_nido "$scratch/output" "$scratch/diagnostic" fixture
`
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/bash", "-c", program, "private-ui-field-contract", binary, scratch)
	command.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "NIDO_FIXTURE_UI_FIELD_CHILD=1", "NIDO_FIXTURE_TEST_EXECUTABLE=" + os.Args[0]}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	diagnostic, _ := os.ReadFile(filepath.Join(scratch, "diagnostic"))
	if err != nil {
		t.Fatalf("public UI fallback PTY failed: %v\n%s\n%s", err, output, diagnostic)
	}
	data, err := os.ReadFile(filepath.Join(scratch, "output"))
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		Port, Target string
		SecretOK     bool
	}
	if err := json.Unmarshal(data, &actual); err != nil {
		t.Fatalf("inert UI output was not a result: %v", err)
	}
	if actual.Port != "17890" || actual.Target != "https://replacement.invalid/check" || !actual.SecretOK {
		t.Fatalf("submitted fallback edits differ: port=%q target=%q secret_matches=%v", actual.Port, actual.Target, actual.SecretOK)
	}
	if strings.Contains(string(output)+string(diagnostic), "synthetic-ui-private-value") {
		t.Fatal("public UI fallback PTY exposed a secret")
	}
}

// The fixture waits for rendered prompts and sends actual terminal keys;
// production input interpretation remains entirely inside terminalui.
func TestProxyQuickStartScriptPTYPlanUsesRenderedPromptsAndRealKeys(t *testing.T) {
	source := quickStartScriptSource(t)
	start := strings.Index(source, "prompted_nido() {\n")
	if start < 0 {
		t.Fatal("production private PTY wrapper missing")
	}
	end := strings.Index(source[start:], "\n}\n\n")
	if end < 0 {
		t.Fatal("production private PTY wrapper incomplete")
	}
	wrapper := source[start : start+end+3]
	scratch := t.TempDir()
	binary := filepath.Join(scratch, "inert plan cli")
	child := `#!/bin/bash
set -euo pipefail
[[ -t 0 ]] || exit 91
stty raw -echo
printf '请选择方向键\n' >&2
IFS= read -r -n 4 value
[[ "$value" == $'\033[B\r' || "$value" == $'\033[B' ]] || exit 92
printf '私密节点链接\n' >&2
IFS= read -r -n 22 value
[[ "$value" == $'synthetic-plan-secret\r' || "$value" == synthetic-plan-secret ]] || exit 93
printf 'PLAN_KEYS_ACCEPTED\n'
exit 0
`
	if err := os.WriteFile(binary, []byte(child), 0700); err != nil {
		t.Fatal(err)
	}
	program := `set -euo pipefail
umask 077
binary=$1; scratch=$2; root="$scratch/software root"; quickstart_mode=current; fixture_plan=true
` + wrapper + `
printf '%s\t%s\n' '请选择方向键' '\033[B\r' '私密节点链接' 'synthetic-plan-secret\r' |
  prompted_nido "$scratch/output" "$scratch/diagnostic" fixture
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/bash", "-c", program, "private-pty-plan-contract", binary, scratch)
	command.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	diagnostic, _ := os.ReadFile(filepath.Join(scratch, "diagnostic"))
	if err != nil {
		t.Fatalf("planned private PTY failed: %v\n%s\n%s", err, output, diagnostic)
	}
	stdout, err := os.ReadFile(filepath.Join(scratch, "output"))
	if err != nil || string(stdout) != "PLAN_KEYS_ACCEPTED\n" {
		t.Fatalf("planned child did not receive real keys: %q %v", stdout, err)
	}
	if strings.Contains(string(output)+string(stdout)+string(diagnostic), "synthetic-plan-secret") {
		t.Fatal("planned PTY echoed a private input")
	}
}

// Exercise the production PTY wrapper using only an inert shell child. This
// catches BSD script's different argument/status behavior and input echo races;
// it never constructs a Manager, executes a core, or makes a network request.
func TestProxyQuickStartScriptPTYProtectsInputAndPropagatesChildStatus(t *testing.T) {
	source := quickStartScriptSource(t)
	start := strings.Index(source, "prompted_nido() {\n")
	if start < 0 {
		t.Fatal("production private PTY wrapper missing")
	}
	end := strings.Index(source[start:], "\n}\n\n")
	if end < 0 {
		t.Fatal("production private PTY wrapper is incomplete")
	}
	wrapper := source[start : start+end+3]
	for _, exitCode := range []int{0, 7} {
		t.Run(fmt.Sprintf("child_status_%d", exitCode), func(t *testing.T) {
			scratch := t.TempDir()
			binary := filepath.Join(scratch, "inert cli")
			child := `#!/bin/bash
set -euo pipefail
[[ -t 0 ]] || { printf 'child lacks a real PTY\n' >&2; exit 91; }
IFS= read -r value
[[ "$value" == synthetic-private-pty-input ]] || exit 92
printf 'PRIVATE_INPUT_ACCEPTED\n'
printf 'CHILD_DIAGNOSTIC\n' >&2
printf '中文摘要哨兵\n' >&2
exit "$4"
`
			if err := os.WriteFile(binary, []byte(child), 0700); err != nil {
				t.Fatal(err)
			}
			program := `set -euo pipefail
umask 077
binary=$1; scratch=$2; root="$scratch/software root"; quickstart_mode=current
` + wrapper + `
set +e
printf 'synthetic-private-pty-input\n' | prompted_nido "$scratch/output" "$scratch/diagnostic" fixture "$3"
actual=$?
set -e
[[ "$actual" == "$3" ]] || { printf 'wrong child status: %s\n' "$actual" >&2; exit 93; }
`
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/bash", "-c", program, "private-pty-contract", binary, scratch, fmt.Sprint(exitCode))
			command.Env = []string{"HOME=" + scratch, "TMPDIR=" + scratch, "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
			command.WaitDelay = time.Second
			output, err := command.CombinedOutput()
			if err != nil {
				diagnostic, _ := os.ReadFile(filepath.Join(scratch, "diagnostic"))
				t.Fatalf("private PTY wrapper failed: %v\n%s\n%s", err, output, diagnostic)
			}
			stdout, err := os.ReadFile(filepath.Join(scratch, "output"))
			if err != nil || string(stdout) != "PRIVATE_INPUT_ACCEPTED\n" {
				t.Fatalf("inert child did not receive its private input: %q %v", stdout, err)
			}
			diagnostic, err := os.ReadFile(filepath.Join(scratch, "diagnostic"))
			if err != nil || !strings.Contains(string(diagnostic), "NIDO_PRIVATE_PTY_READY") || !strings.Contains(string(diagnostic), "CHILD_DIAGNOSTIC") || !strings.Contains(string(diagnostic), "中文摘要哨兵") {
				t.Fatalf("missing child PTY evidence: %q %v", diagnostic, err)
			}
			if strings.Contains(string(output)+string(stdout)+string(diagnostic), "synthetic-private-pty-input") {
				t.Fatal("production PTY wrapper echoed its private input")
			}
		})
	}
}
