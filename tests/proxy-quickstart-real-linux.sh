#!/usr/bin/env bash
set -euo pipefail

# Existing, verified artifacts only: no downloads, build, installer or daemon.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行\n' >&2; exit 2; }
go_tool="${GO:-go}"
prebuilt="${NIDO_TEST_LINUX_BIN_DIR:?set NIDO_TEST_LINUX_BIN_DIR to existing static race CLI and test artifacts}"
core="${NIDO_TEST_MIHOMO:?set NIDO_TEST_MIHOMO to a previously verified local artifact}"
output="${NIDO_TEST_QUICKSTART_REAL_OUTPUT:?set NIDO_TEST_QUICKSTART_REAL_OUTPUT to a new absolute evidence directory}"
[[ "$prebuilt" == /* && -d "$prebuilt" && ! -L "$prebuilt" ]] || { printf '需要普通绝对产物目录\n' >&2; exit 2; }
[[ "$core" == /* && -f "$core" && ! -L "$core" && -r "$core" ]] || { printf '需要已核验 Mihomo 普通文件绝对路径\n' >&2; exit 2; }
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
for tool in "$go_tool" bwrap file timeout sha256sum tee grep awk find; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
prebuilt="$(cd "$prebuilt" && pwd -P)"
output="$(cd "$(dirname "$output")" && pwd -P)/$(basename "$output")"
[[ ! -e "$output" && ! -L "$output" ]] || { printf '证据目录已存在\n' >&2; exit 2; }
case "$output/" in "$prebuilt/"*) printf '证据目录不能位于输入产物目录内\n' >&2; exit 2;; esac
umask 077
mkdir "$output"
scratch="$(mktemp -d /tmp/nido-proxy-quickstart-real.XXXXXX)"
scratch="$(cd "$scratch" && pwd -P)"
cleanup() {
  result=$?
  trap - EXIT
  if [[ -d "$scratch/tmp/nido-proxy-race-quickstart" && ! -L "$scratch/tmp/nido-proxy-race-quickstart" ]]; then
    mkdir "$output/race-reports" || true
    shopt -s nullglob
    for report in "$scratch/tmp/nido-proxy-race-quickstart/"*; do
      [[ -f "$report" && ! -L "$report" ]] || continue
      cp -- "$report" "$output/race-reports/" || true
    done
  fi
  printf 'exit_status=%s\n' "$result" > "$output/result.txt"
  rm -rf -- "$scratch"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/inputs" "$scratch/tmp"
export GOENV=off GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOEXPERIMENT='' GOPROXY=off GOSUMDB=off
for name in nido nido.test mihomo; do
  input="$prebuilt/$name"
  [[ "$name" != mihomo ]] || input="$core"
  [[ -f "$input" && ! -L "$input" && -r "$input" ]] || { printf '缺少普通程序：%s\n' "$name" >&2; exit 2; }
  cp -- "$input" "$scratch/inputs/$name"
  input="$scratch/inputs/$name"
  description="$(LC_ALL=C file -b "$input")"
  printf '%s: %s\n' "$name" "$description" >> "$output/artifact-types.txt"
  [[ "$description" == *'ELF 64-bit'* && "$description" == *'x86-64'* && "$description" == *'statically linked'* ]] || { printf '输入必须为静态 Linux amd64 ELF：%s\n' "$name" >&2; exit 2; }
  chmod 0555 "$input"
  if [[ "$name" == nido ]]; then
    "$go_tool" version -m "$input" > "$output/nido-build.txt"
    awk '
      $1 == "path" && $2 == "github.com/MiracleHYH/CDEToolkit/cmd/nido" { path++ }
      $1 == "build" && $2 == "GOOS=linux" { os++ }
      $1 == "build" && $2 == "GOARCH=amd64" { arch++ }
      $1 == "build" && $2 == "CGO_ENABLED=1" { cgo++ }
      $1 == "build" && $2 == "-race=true" { race++ }
      END { exit !(path == 1 && os == 1 && arch == 1 && cgo == 1 && race == 1) }
    ' "$output/nido-build.txt" || { printf 'CLI package、架构或 race 元数据不符\n' >&2; exit 2; }
  elif [[ "$name" == nido.test ]]; then
    "$go_tool" tool nm "$input" > "$output/nido-test-symbols.txt"
    awk '
      $2 ~ /^[Tt]$/ && $3 == "github.com/MiracleHYH/CDEToolkit/internal/nido.TestProxyRealMihomoQuickStart" { entry++ }
      $2 ~ /^[Tt]$/ && $3 ~ /^runtime\.raceinit(\.abi0)?$/ { race++ }
      $2 ~ /^[Tt]$/ && $3 == "__tsan_init" { native++ }
      END { exit !(entry == 1 && race >= 1 && native == 1) }
    ' "$output/nido-test-symbols.txt" || { printf 'test 入口或 race 运行时符号不符\n' >&2; exit 2; }
  fi
done
(cd "$scratch/inputs" && sha256sum nido nido.test mihomo) > "$output/input-sha256.txt"
"$go_tool" version > "$output/go-version.txt"
printf 'host_uid=%s\nhost_kernel=%s\nhost_machine=%s\nrace=required\nnetwork=private-namespace-loopback-only\ninputs=three-read-only-static-artifacts\nsource=not-mounted\nhost_home=not-mounted\n' \
  "$(id -u)" "$(uname -r)" "$(uname -m)" > "$output/environment.txt"
# Only these three programs, an empty writable temporary root, proc and private
# devices enter the new filesystem. No host source, libraries or HOME is mounted.
flags=(--unshare-all --die-with-parent --new-session --clearenv --cap-drop ALL
  --ro-bind "$scratch/inputs" /inputs --proc /proc --dev /dev --bind "$scratch/tmp" /tmp
  --dir /tmp/home --chdir /tmp --setenv HOME /tmp/home --setenv TMPDIR /tmp
  --setenv XDG_CONFIG_HOME /tmp/home/.config --setenv XDG_CACHE_HOME /tmp/home/.cache
  --setenv PATH /nonexistent --setenv LANG C --setenv LC_ALL C --setenv TZ UTC --setenv GOMAXPROCS 2
  --setenv NIDO_TEST_RACE_REPORT_DIR /tmp/nido-proxy-race-quickstart --setenv GORACE halt_on_error=1)
timeout --kill-after=5s 1m bwrap "${flags[@]}" /inputs/mihomo -v 2>&1 | tee "$output/mihomo-version.log"
grep -Eq '^Mihomo Meta [^[:space:]]+' "$output/mihomo-version.log" || { printf '缺少真实 Mihomo 版本输出\n' >&2; exit 1; }
timeout --kill-after=5s 15m bwrap "${flags[@]}" \
  --setenv NIDO_TEST_NIDO /inputs/nido --setenv NIDO_TEST_MIHOMO /inputs/mihomo \
  /inputs/nido.test -test.v -test.count=1 -test.timeout=10m -test.run '^TestProxyRealMihomoQuickStart$' 2>&1 | tee "$output/quickstart-real.log"
if grep -Eq -- '--- (SKIP|FAIL):|^FAIL([[:space:]]|$)|WARNING: DATA RACE|^fatal error:|^panic:' "$output/quickstart-real.log"; then
  printf '真实 quick start 出现跳过、失败或 race，拒绝计为通过\n' >&2
  exit 1
fi
grep -Eq '^--- PASS: TestProxyRealMihomoQuickStart[[:space:]]' "$output/quickstart-real.log" || { printf '缺少新真实 quick start 用例的 PASS\n' >&2; exit 1; }
# The parent symbol/PASS can also exist in older prebuilt tests without the
# node matrix. Require every exact leaf name so those artifacts cannot claim
# configuration acceptance for protocols they never exercised.
node_cases=(ss-plain-percent ss-base64-userinfo ss-legacy-base64 ss-2022 ss-obfs ss-v2ray-plugin
  vmess-tcp vmess-ws-tls-early-data vless-tls-ws vless-tls-grpc vless-reality-tcp
  trojan-ws-tls hy2-salamander-pin http-auth https-auth-tls socks5-auth socks5h-anonymous)
for node_case in "${node_cases[@]}"; do
  grep -Eq '^[[:space:]]*--- PASS: TestProxyRealMihomoQuickStart/nodes/'"$node_case"'[[:space:]]' "$output/quickstart-real.log" || {
    printf '缺少真实节点配置子项的精确 PASS：%s\n' "$node_case" >&2
    exit 1
  }
done
grep -Fxq PASS "$output/quickstart-real.log" || { printf '缺少测试进程完整 PASS\n' >&2; exit 1; }
[[ -d "$scratch/tmp/nido-proxy-race-quickstart" && ! -L "$scratch/tmp/nido-proxy-race-quickstart" ]] || { printf '缺少私有子进程 race 报告目录\n' >&2; exit 1; }
if [[ -n "$(find "$scratch/tmp/nido-proxy-race-quickstart" -mindepth 1 -print -quit)" ]]; then
  printf '存在子进程 race 报告，拒绝计为通过\n' >&2
  exit 1
fi
printf 'PASS: 普通用户 Linux amd64 真实 Mihomo quick start 与 CLI race 隔离验证通过；证据：%s\n' "$output"
