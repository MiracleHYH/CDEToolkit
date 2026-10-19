#!/usr/bin/env bash
set -euo pipefail

# Explicit standalone-module CPU fixture tests; never run a production GDS
# executable or fall back to a host run if namespace setup fails.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行，root 结果不计入此验收\n' >&2; exit 2; }
go_tool="${GO:?set GO to an existing absolute Go executable}"
vendor="${GDS_TEST_VENDOR:?set GDS_TEST_VENDOR to an offline vendor directory for standalone/gds}"
output="${GDS_TEST_OUTPUT:?set GDS_TEST_OUTPUT to a new absolute evidence directory}"
[[ "$go_tool" == /* && -x "$go_tool" ]] || exit 2
[[ "$vendor" == /* && -d "$vendor" && ! -L "$vendor" && -f "$vendor/modules.txt" ]] || exit 2
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
for tool in bwrap file timeout sha256sum tar readlink readelf tee find sort xargs grep bash; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
[[ -x /usr/bin/gcc ]] || { printf '缺少已有 /usr/bin/gcc，不安装工具\n' >&2; exit 2; }
go_tool="$(readlink -f "$go_tool")"
goroot="$(dirname "$(dirname "$go_tool")")"
[[ -d "$goroot/src" && -d "$goroot/pkg" ]] || { printf '需要完整的已有 Go SDK\n' >&2; exit 2; }
description="$(LC_ALL=C file -b "$go_tool")"
[[ "$description" == *'ELF 64-bit'* && "$description" == *'executable'* && "$description" == *'x86-64'* ]] || { printf 'Go 必须是 amd64 原生 ELF\n' >&2; exit 2; }
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
vendor="$(cd "$vendor" && pwd -P)"
output_parent="$(cd "$(dirname "$output")" && pwd -P)"
output="$output_parent/$(basename "$output")"
[[ ! -e "$output" && ! -L "$output" ]] || exit 2
case "$output/" in
  "$root/"*|"$goroot/"*|"$vendor/"*) printf '证据目录不能位于源码、SDK 或 vendor 内\n' >&2; exit 2 ;;
esac
umask 077
mkdir "$output"
output="$(cd "$output" && pwd -P)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/standalone-gds-native.XXXXXX")"
scratch="$(cd "$scratch" && pwd -P)"
cleanup() {
  result=$?
  shopt -s nullglob
  evidence=("$scratch/work/"*.json "$scratch/work/"*.txt)
  if ((${#evidence[@]})); then cp "${evidence[@]}" "$output/" || true; fi
  rm -rf -- "$scratch"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/source" "$scratch/work"
# Copy only public source/test inputs; never copy a checkout's data or dot homes.
inputs=(cmd internal VERSION .go-version go.mod go.sum)
for input in "${inputs[@]}"; do
  [[ -e "$root/$input" && ! -L "$root/$input" ]] || exit 2
  [[ -z "$(find "$root/$input" ! -type d ! -type f -print -quit)" ]] || { printf '拒绝源码输入中的链接或特殊文件\n' >&2; exit 2; }
done
[[ -z "$(find "$vendor" ! -type d ! -type f -print -quit)" ]] || { printf '拒绝 vendor 中的链接或特殊文件\n' >&2; exit 2; }
tar -C "$root" -cf - "${inputs[@]}" | tar -C "$scratch/source" -xf -
cp -R "$vendor" "$scratch/source/vendor"
(cd "$scratch/source" && find cmd internal -type f ! -name '._*' -print0 | sort -z | xargs -0 sha256sum && sha256sum VERSION .go-version go.mod go.sum vendor/modules.txt) > "$output/source-manifest.sha256"
flags=(--unshare-all --die-with-parent --new-session --clearenv --cap-drop ALL
  --ro-bind /usr /usr --ro-bind /lib /lib --ro-bind /lib64 /lib64
  --symlink usr/bin /bin --symlink usr/sbin /sbin
  --ro-bind "$goroot" /toolchain --ro-bind "$scratch/source" /source
  --bind "$scratch/work" /work --proc /proc --dev /dev --tmpfs /tmp
  --dir /test-bin --symlink /usr/bin/gcc /test-bin/cc
  --dir /work/home --dir /work/cache --dir /work/go --chdir /source
  --setenv HOME /work/home --setenv TMPDIR /tmp
  --setenv PATH /test-bin:/toolchain/bin:/usr/bin:/bin --setenv LANG C --setenv LC_ALL C --setenv TZ UTC
  --setenv GOENV off --setenv GOWORK off --setenv GOTOOLCHAIN local --setenv GOFLAGS -mod=vendor
  --setenv GOCACHE /work/cache --setenv GOPATH /work/go --setenv GOMODCACHE /work/mod
  --setenv GOPROXY off --setenv GOSUMDB off --setenv CGO_ENABLED 1 --setenv CC /usr/bin/gcc
  --setenv GOMAXPROCS 4 --setenv GORACE halt_on_error=1)
printf 'host_uid=%s\nhost_kernel=%s\nhost_machine=%s\n' "$(id -u)" "$(uname -r)" "$(uname -m)" > "$output/environment.txt"
# No host HOME, network interface, driver device or service socket is mounted.
timeout --kill-after=5s 15m bwrap "${flags[@]}" /bin/bash -euo pipefail -c '
  [[ "$(id -u)" != 0 && "$(uname -m)" == x86_64 ]]
  [[ ! -e /dev/nvidiactl && ! -e /dev/dri && ! -e /run && ! -e /home && ! -e /root ]]
  [[ "$(readlink /proc/self/exe)" == /usr/bin/readlink ]]
  go version
  gcc -dumpfullversion -dumpversion
  printf "namespace_uid=%s\n" "$(id -u)"
  { id; for ns in mnt pid net user; do printf "%s=" "$ns"; readlink "/proc/self/ns/$ns"; done; ls -la /dev; cat /proc/net/dev; } > /work/namespace.txt
  go test -race -json -count=1 -timeout=3m ./internal/... > /work/gds-race.json
  # Compile CLI tests without running their production metadata code paths.
  go test -race -c -o /work/gds-cli.test ./cmd/gds
  go vet ./...
  go build -trimpath -buildvcs=false -ldflags="-X=main.version=$(cat VERSION)" -o /work/gds ./cmd/gds
  readelf -d /work/gds > /work/gds-dynamic.txt
  sha256sum /work/gds > /work/binary-sha256.txt
' 2>&1 | tee "$output/run.log"
# The EXIT trap preserves partial logs on failure too; no binary is installed.
cp "$scratch/work/"*.json "$scratch/work/"*.txt "$output/"
if grep -Eq '"Action":"(skip|fail)"' "$output/gds-race.json"; then
  printf 'GDS 专项存在失败或跳过，不计为通过\n' >&2
  exit 1
fi
printf '普通用户 Linux amd64 独立模块 CPU 协议、假驱动 race 与静态构建检查通过；未执行生产 GDS；证据：%s\n' "$output"
