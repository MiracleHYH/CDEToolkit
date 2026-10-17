#!/usr/bin/env bash
set -euo pipefail

# Native Linux only: no emulator, toolchain installation, host fallback or daemon.
[[ "$(uname -s)" == Linux ]] || { printf '此入口仅在原生 Linux 上执行\n' >&2; exit 2; }
case "$(uname -m)" in
  x86_64|amd64) arch=amd64; machine='x86-64' ;;
  aarch64|arm64) arch=arm64; machine='ARM aarch64' ;;
  *) printf '仅支持原生 Linux amd64/arm64\n' >&2; exit 2 ;;
esac
go_tool="${GO:-go}"
core="${NIDO_TEST_MIHOMO:-}"
core_update="${NIDO_TEST_MIHOMO_UPDATE:-}"
prebuilt="${NIDO_TEST_LINUX_BIN_DIR:-}"
race="${NIDO_TEST_LINUX_RACE:-0}"
[[ "$race" == 0 || "$race" == 1 ]] || { printf 'NIDO_TEST_LINUX_RACE 只能为 0 或 1\n' >&2; exit 2; }
[[ "$prebuilt" == /* && -d "$prebuilt" && ! -L "$prebuilt" ]] || { printf '需要 NIDO_TEST_LINUX_BIN_DIR 绝对目录\n' >&2; exit 2; }
[[ "$core" == /* && -f "$core" && ! -L "$core" && -r "$core" ]] || { printf '需要 NIDO_TEST_MIHOMO 已核验普通程序绝对路径\n' >&2; exit 2; }
if [[ -n "$core_update" && ( "$core_update" != /* || ! -f "$core_update" || -L "$core_update" || ! -r "$core_update" ) ]]; then
  printf '需要 NIDO_TEST_MIHOMO_UPDATE 已核验普通程序绝对路径\n' >&2
  exit 2
fi
for tool in "$go_tool" bwrap file; do
  command -v "$tool" >/dev/null || { printf '找不到已有命令：%s\n' "$tool" >&2; exit 2; }
done
umask 077
scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-proxy-linux-native.XXXXXX")"
scratch="$(cd "$scratch" && pwd -P)"
trap 'rm -rf -- "$scratch"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/inputs"
export GOENV=off GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOEXPERIMENT='' GOPROXY=off GOSUMDB=off
inputs=(nido nido.test proxy.test mihomo)
[[ -z "$core_update" ]] || inputs+=(mihomo-update)
for name in "${inputs[@]}"; do
  input="$prebuilt/$name"
  [[ "$name" != mihomo ]] || input="$core"
  [[ "$name" != mihomo-update ]] || input="$core_update"
  [[ -f "$input" && ! -L "$input" && -r "$input" ]] || { printf '缺少普通程序：%s\n' "$input" >&2; exit 2; }
  cp "$input" "$scratch/inputs/$name"
  input="$scratch/inputs/$name"
  description="$(LC_ALL=C file -b "$input")"
  if [[ "$description" != *"ELF 64-bit"* || "$description" != *"$machine"* || "$description" != *"statically linked"* ]]; then
    printf '输入必须为当前架构的静态 Linux ELF：%s（%s）\n' "$name" "$description" >&2
    exit 2
  fi
  chmod 0555 "$input"
  [[ "$name" != mihomo && "$name" != mihomo-update ]] || continue
  if [[ "$name" == nido ]]; then
    metadata="$("$go_tool" version -m "$input")"
    if ! awk -v arch="$arch" -v race="$race" '
      $1 == "path" && $2 == "github.com/MiracleHYH/CDEToolkit/cmd/nido" { path++ }
      $1 == "build" && $2 == "GOOS=linux" { os++ }
      $1 == "build" && $2 == "GOARCH=" arch { target++ }
      $1 == "build" && $2 == "CGO_ENABLED=1" { cgo++ }
      $1 == "build" && $2 == "-race=true" { detector++ }
      END { exit !(path == 1 && os == 1 && target == 1 && (race == 1 ? detector == 1 && cgo == 1 : detector == 0)) }
    ' <<< "$metadata"; then
      printf 'CLI 的 package、架构或 race 元数据不符\n' >&2
      exit 2
    fi
  else
    # Go 1.23 omits test build settings; inspect linked code instead.
    symbol='github.com/MiracleHYH/CDEToolkit/internal/nido.TestProxyRealMihomo'
    [[ "$name" != proxy.test ]] || symbol='github.com/MiracleHYH/CDEToolkit/internal/components/proxy.TestMain'
    symbols="$("$go_tool" tool nm "$input")"
    if ! awk -v test="$symbol" -v race="$race" '
      $2 ~ /^[Tt]$/ && $3 == test { entry++ }
      $2 ~ /^[Tt]$/ && $3 ~ /^runtime\.raceinit(\.abi0)?$/ { detector++ }
      $2 ~ /^[Tt]$/ && $3 == "__tsan_init" { native++ }
      END { exit !(entry == 1 && (race == 1 ? detector >= 1 && native == 1 : detector == 0 && native == 0)) }
    ' <<< "$symbols"; then
      printf 'test 程序入口或 race 运行时符号不符：%s\n' "$name" >&2
      exit 2
    fi
  fi
done
printf '原生 Linux/%s，kernel=%s，race=%s；仅 %s 个产物、私有 PID/网络与临时 HOME。\n' "$arch" "$(uname -r)" "$race" "${#inputs[@]}"
"$go_tool" version
# A new namespace root exposes no host home, source, credentials or libraries.
# Bubblewrap's PID namespace init also reaps descendants when each run ends.
flags=(--unshare-all --die-with-parent --new-session --clearenv --cap-drop ALL
  --ro-bind "$scratch/inputs" /inputs --proc /proc --dev /dev --tmpfs /tmp
  --dir /tmp/home --chdir /tmp --setenv HOME /tmp/home --setenv TMPDIR /tmp
  --setenv PATH /nonexistent --setenv LANG C --setenv LC_ALL C --setenv TZ UTC --setenv GOMAXPROCS 2)
timeout=3m
if [[ "$race" == 1 ]]; then
  timeout=10m
  flags+=(--setenv NIDO_TEST_RACE_REPORT_DIR /tmp/nido-proxy-race-reports
    --setenv GORACE halt_on_error=1)
fi
bwrap "${flags[@]}" /inputs/proxy.test -test.v -test.count=1 -test.timeout="$timeout" | tee "$scratch/proxy.log"
if grep -E -- '--- SKIP: TestOS' "$scratch/proxy.log" || ! grep -E -- '^--- PASS: TestOS' "$scratch/proxy.log" >/dev/null; then
  printf 'TestOS 必须实际通过，拒绝将跳过计为原生验证\n' >&2
  exit 1
fi
if grep -Ei -- '--- SKIP:.*ipv6' "$scratch/proxy.log"; then
  printf '本次 namespace 未提供 IPv6 回环，以上专项跳过，不计入 IPv6 验证。\n'
fi
bwrap "${flags[@]}" /inputs/mihomo -v | tee "$scratch/version.log"
grep -E -- '^Mihomo Meta [^[:space:]]+' "$scratch/version.log" >/dev/null || { printf '缺少真实 Mihomo 版本输出\n' >&2; exit 1; }
real_flags=(--setenv NIDO_TEST_NIDO /inputs/nido --setenv NIDO_TEST_MIHOMO /inputs/mihomo)
real_timeout="$timeout"
if [[ -n "$core_update" ]]; then
  real_flags+=(--setenv NIDO_TEST_MIHOMO_UPDATE /inputs/mihomo-update)
  # The ordinary and upgrade E2Es each retain their original time budget.
  real_timeout=6m
  [[ "$race" != 1 ]] || real_timeout=20m
fi
bwrap "${flags[@]}" "${real_flags[@]}" \
  /inputs/nido.test -test.v -test.count=1 -test.timeout="$real_timeout" -test.run '^TestProxyRealMihomo(CrossVersion)?$' | tee "$scratch/real.log"
if grep -E -- '--- SKIP: TestProxyRealMihomo([[:space:]/]|$)' "$scratch/real.log" || ! grep -E -- '^--- PASS: TestProxyRealMihomo([[:space:]]|$)' "$scratch/real.log" >/dev/null; then
  printf '真实 Mihomo 测试必须实际通过\n' >&2
  exit 1
fi
if [[ -n "$core_update" ]] && {
  grep -E -- '--- SKIP: TestProxyRealMihomoCrossVersion([[:space:]/]|$)' "$scratch/real.log" ||
  ! grep -E -- '^--- PASS: TestProxyRealMihomoCrossVersion([[:space:]]|$)' "$scratch/real.log" >/dev/null;
}; then
  printf '显式跨版本 Mihomo 测试必须实际通过\n' >&2
  exit 1
fi
printf 'Linux %s 原生 bwrap Proxy 全包与真实 Mihomo 隔离验证通过（race=%s）。\n' "$arch" "$race"
