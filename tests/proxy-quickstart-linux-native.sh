#!/usr/bin/env bash
set -euo pipefail

# Offline, ordinary-user Linux amd64 fixtures only. There is no host fallback,
# real Mihomo input, tool installation, downloader or service-manager command.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行\n' >&2; exit 2; }
source_dir="${NIDO_TEST_QUICKSTART_SOURCE:?set NIDO_TEST_QUICKSTART_SOURCE to an offline vendored source tree}"
go_tool="${NIDO_TEST_QUICKSTART_GO:?set NIDO_TEST_QUICKSTART_GO to an existing absolute Go executable}"
output="${NIDO_TEST_QUICKSTART_OUTPUT:?set NIDO_TEST_QUICKSTART_OUTPUT to a new absolute evidence directory}"
[[ "$source_dir" == /* && -d "$source_dir" && ! -L "$source_dir" ]] || { printf '源码必须为普通绝对目录\n' >&2; exit 2; }
[[ "$go_tool" == /* && -x "$go_tool" ]] || { printf '需要已有 Go SDK 的绝对路径\n' >&2; exit 2; }
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
for tool in bwrap file timeout sha256sum tar readlink find sort xargs tee grep bash; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
[[ -x /usr/bin/gcc ]] || { printf '缺少已有 /usr/bin/gcc，不安装工具\n' >&2; exit 2; }
source_dir="$(cd "$source_dir" && pwd -P)"
go_tool="$(readlink -f "$go_tool")"
goroot="$(dirname "$(dirname "$go_tool")")"
[[ -d "$goroot/src" && -d "$goroot/pkg" ]] || { printf '需要完整的已有 Go SDK\n' >&2; exit 2; }
description="$(LC_ALL=C file -b "$go_tool")"
[[ "$description" == *'ELF 64-bit'* && "$description" == *'executable'* && "$description" == *'x86-64'* ]] || { printf 'Go 必须为 Linux amd64 原生 ELF\n' >&2; exit 2; }
output_parent="$(cd "$(dirname "$output")" && pwd -P)"
output="$output_parent/$(basename "$output")"
[[ ! -e "$output" && ! -L "$output" ]] || { printf '证据目录已存在\n' >&2; exit 2; }
case "$output/" in
  "$source_dir/"*|"$goroot/"*) printf '证据目录不能位于输入源码或 SDK 内\n' >&2; exit 2 ;;
esac
inputs=(cmd internal tests scripts .github Makefile VERSION GDS_VERSION .go-version go.mod go.sum vendor)
for input in "${inputs[@]}"; do
  [[ -e "$source_dir/$input" && ! -L "$source_dir/$input" ]] || { printf '缺少普通源码输入：%s\n' "$input" >&2; exit 2; }
  [[ -z "$(find "$source_dir/$input" ! -type d ! -type f -print -quit)" ]] || { printf '拒绝源码中的链接或特殊文件：%s\n' "$input" >&2; exit 2; }
done
[[ -f "$source_dir/vendor/modules.txt" && -f "$source_dir/tests/proxy-quickstart-cli-isolation.sh" ]] || { printf '需要离线 vendor 和新版 CLI 隔离夹具\n' >&2; exit 2; }
umask 077
mkdir "$output"
scratch="$(mktemp -d /tmp/nido-proxy-quickstart-native.XXXXXX)"
scratch="$(cd "$scratch" && pwd -P)"
cleanup() {
  result=$?
  trap - EXIT
  shopt -s nullglob
  evidence=("$scratch/work/"*.json "$scratch/work/"*.txt "$scratch/work/"*.log)
  if ((${#evidence[@]})); then
    for evidence_file in "${evidence[@]}"; do
      [[ -f "$evidence_file" && ! -L "$evidence_file" ]] || continue
      cp -- "$evidence_file" "$output/" || true
    done
  fi
  printf 'exit_status=%s\n' "$result" > "$output/result.txt"
  rm -rf -- "$scratch"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/source" "$scratch/work"
tar -C "$source_dir" -cf - "${inputs[@]}" | tar -C "$scratch/source" -xf -
(
  cd "$scratch/source"
  find "${inputs[@]}" -type f -print0 | sort -z | xargs -0 sha256sum
) > "$output/source-manifest.sha256"
printf 'host_uid=%s\nhost_kernel=%s\nhost_machine=%s\nnetwork=shared-host; tests use temporary loopback ports only\nsource_snapshot=read-only\nreal_mihomo=not-used\n' \
  "$(id -u)" "$(uname -r)" "$(uname -m)" > "$output/environment.txt"
sha256sum "$go_tool" > "$output/toolchain-sha256.txt"
flags=(--unshare-all --share-net --die-with-parent --new-session --clearenv --cap-drop ALL
  --ro-bind /usr /usr --ro-bind /lib /lib --ro-bind /lib64 /lib64
  --symlink usr/bin /bin --symlink usr/sbin /sbin
  --ro-bind "$goroot" /toolchain --ro-bind "$scratch/source" /source
  --bind "$scratch/work" /work --proc /proc --dev /dev --tmpfs /tmp
  --dir /test-bin --symlink /usr/bin/gcc /test-bin/cc
  --dir /work/home --dir /work/cache --dir /work/go --dir /work/tmp --chdir /source
  --setenv HOME /work/home --setenv TMPDIR /work/tmp
  --setenv PATH /test-bin:/toolchain/bin:/usr/bin:/bin --setenv LANG C --setenv LC_ALL C --setenv TZ UTC
  --setenv XDG_CONFIG_HOME /work/home/.config --setenv XDG_CACHE_HOME /work/home/.cache
  --setenv GOENV off --setenv GOWORK off --setenv GOTOOLCHAIN local --setenv GOFLAGS -mod=vendor
  --setenv GOCACHE /work/cache --setenv GOPATH /work/go --setenv GOMODCACHE /work/mod
  --setenv GOPROXY off --setenv GOSUMDB off --setenv CGO_ENABLED 1 --setenv CC /usr/bin/gcc
  --setenv GOMAXPROCS 4 --setenv GORACE halt_on_error=1
  --setenv NIDO_TEST_RACE_REPORT_DIR /work/tmp/nido-proxy-race-quickstart)
if [[ -d /etc/alternatives ]]; then flags+=(--ro-bind /etc/alternatives /etc/alternatives); fi
# No host HOME, actual credentials, user configuration, session/database,
# service socket or GPU device is mounted. The inherited environment is empty.
timeout --kill-after=5s 20m bwrap "${flags[@]}" /bin/bash -euo pipefail -c '
  [[ "$(id -u)" != 0 && "$(uname -m)" == x86_64 ]]
  [[ ! -e /run && ! -e /home && ! -e /root && ! -e /dev/nvidiactl ]]
  [[ -z "${NIDO_TEST_MIHOMO:-}${NIDO_TEST_MIHOMO_UPDATE:-}${NIDO_TEST_NIDO:-}" ]]
  go version | tee /work/go-version.txt
  gcc -dumpfullversion -dumpversion | tee /work/cc-version.txt
  { id; for ns in mnt pid net user; do printf "%s=" "$ns"; readlink "/proc/self/ns/$ns"; done; cat /proc/net/dev; } > /work/namespace.txt
  go test -race -json -count=1 -timeout=5m ./internal/components/proxy > /work/proxy-race.json
  go test -race -json -count=1 -timeout=3m ./internal/integrations/gitproxy > /work/gitproxy-race.json
  go test -race -json -count=1 -timeout=5m ./internal/nido \
    -run "^Test(Proxy|CodexBuiltin|QuickStart|Node|ParseNode)" \
    -skip "^TestProxyRealMihomo(CrossVersion|QuickStart)?$" > /work/nido-proxy-race.json
  go build -race -trimpath -buildvcs=false \
    -ldflags="-X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=$(cat VERSION)" \
    -o /work/nido ./cmd/nido
  go version -m /work/nido > /work/nido-build.txt
  grep -Fq GOOS=linux /work/nido-build.txt
  grep -Fq GOARCH=amd64 /work/nido-build.txt
  grep -Fq CGO_ENABLED=1 /work/nido-build.txt
  grep -Fq -- -race=true /work/nido-build.txt
  /work/nido version | tee /work/nido-version.txt
  grep -Fxq "nido $(cat VERSION)" /work/nido-version.txt
  CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /work/fake-mihomo ./tests/fixtures/proxy-core
  go version -m /work/fake-mihomo > /work/fake-core-build.txt
  bash tests/nido-cli-isolation.sh /work/nido > /work/cli-isolation.log 2>&1
  bash tests/proxy-quickstart-cli-isolation.sh /work/nido /work/fake-mihomo > /work/quickstart-cli-isolation.log 2>&1
  sha256sum /work/nido /work/fake-mihomo > /work/binary-sha256.txt
' 2>&1 | tee "$output/run.log"
environment_skip_count=0
for log in proxy-race.json gitproxy-race.json nido-proxy-race.json; do
  [[ -s "$scratch/work/$log" ]] || { printf '缺少实际测试日志：%s\n' "$log" >&2; exit 1; }
  if grep -Eq '"Action":"fail"' "$scratch/work/$log"; then
    printf '专项测试出现失败：%s\n' "$log" >&2
    exit 1
  fi
  while IFS= read -r skip_line; do
    ipv6_test=''
    # These two kernel subtests explicitly skip when the host has no IPv6
    # loopback. Do not change its interfaces or count their omission as PASS.
    # Match the exact package, complete test name and proxy log only.
    if [[ "$log" == proxy-race.json ]]; then
      case "$skip_line" in
        *'"Action":"skip","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback","Elapsed":'*) ipv6_test='TestLinuxKernelIPv6AndWildcardOwnership/IPv6_loopback' ;;
        *'"Action":"skip","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"TestLinuxKernelIPv6AndWildcardOwnership/IPv6_wildcard","Elapsed":'*) ipv6_test='TestLinuxKernelIPv6AndWildcardOwnership/IPv6_wildcard' ;;
      esac
    fi
    if [[ -z "$ipv6_test" ]]; then
      printf '专项测试出现未允许的跳过：%s\n%s\n' "$log" "$skip_line" >&2
      exit 1
    fi
    reason_prefix='"Action":"output","Package":"github.com/MiracleHYH/CDEToolkit/internal/components/proxy","Test":"'"$ipv6_test"'","Output":'
    # The kernel fixture skips only for its five classified IPv6-unavailable
    # errors. A familiar test name alone cannot excuse an arbitrary skip.
    if ! reason_lines="$(grep -F "$reason_prefix" "$scratch/work/$log" | grep -E 'IPv6( loopback( route)?)? unavailable in this network namespace:.*(address family not supported by protocol|protocol not supported|cannot assign requested address|no such device|network is unreachable)')"; then
      printf 'IPv6 跳过缺少同用例的明确环境不可用证据：%s\n%s\n' "$log" "$skip_line" >&2
      exit 1
    fi
    environment_skip_count=$((environment_skip_count + 1))
    printf 'ENVIRONMENT SKIP（未计为通过）：%s\n' "$skip_line" | tee -a "$output/run.log" "$scratch/work/environment-skips.txt"
    printf 'IPv6 unavailable evidence: %s\n' "$reason_lines" | tee -a "$output/run.log" "$scratch/work/environment-skips.txt"
    printf 'ipv6_environment_skip=%s\n' "$skip_line" >> "$output/environment.txt"
    printf 'ipv6_environment_reason=%s\n' "$reason_lines" >> "$output/environment.txt"
  done < <(grep -E '"Action":"skip"' "$scratch/work/$log" || true)
  grep -Eq '"Action":"pass","Package":"[^\"]+","Elapsed":' "$scratch/work/$log" || { printf '缺少测试包成功证据：%s\n' "$log" >&2; exit 1; }
done
if ((environment_skip_count)); then
  printf 'BOUNDARY: IPv6 回环/通配归属专项有 %s 项因环境缺少 IPv6 而未覆盖，不计为 PASS；未修改宿主网络。\n' "$environment_skip_count" | tee -a "$output/run.log" "$scratch/work/environment-skips.txt"
else
  printf 'environment_skips=none\n' > "$scratch/work/environment-skips.txt"
fi
for test in TestQuickStartOptionalRoutes TestParseNodeURLSSCredentials; do
  grep -Eq '"Action":"pass".*"Test":"'"$test"'"' "$scratch/work/proxy-race.json" || { printf '缺少实际通过：%s\n' "$test" >&2; exit 1; }
done
grep -Eq '"Action":"pass".*"Test":"TestProxyPreservesOtherBytesAndUsesOnlyHTTPProxy"' "$scratch/work/gitproxy-race.json" || { printf '缺少 Git 代理配置实际通过\n' >&2; exit 1; }
for test in TestProxyQuickStart TestProxyBind; do
  grep -Eq '"Action":"pass".*"Test":"'"$test"'[^"/]*"' "$scratch/work/nido-proxy-race.json" || { printf '缺少实际通过：%s\n' "$test" >&2; exit 1; }
done
grep -Fxq 'Nido 单目录 CLI 隔离验证通过' "$scratch/work/cli-isolation.log" || { printf '缺少既有 CLI 隔离成功证据\n' >&2; exit 1; }
grep -Fxq 'Proxy quick start CLI 隔离验证通过' "$scratch/work/quickstart-cli-isolation.log" || { printf '缺少新版 CLI 隔离成功证据\n' >&2; exit 1; }
printf 'PASS: 普通用户 Linux amd64 Proxy quick start、节点解析、Git/Codex 配置 race 与 CLI 隔离验证通过；证据：%s\n' "$output"
