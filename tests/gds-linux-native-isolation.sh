#!/usr/bin/env bash
set -euo pipefail

# CPU fixtures only. Never fall back to a host run if namespace setup fails.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行，root 结果不计入此验收\n' >&2; exit 2; }
go_tool="${GO:?set GO to an existing absolute Go executable}"
vendor="${NIDO_TEST_GDS_VENDOR:?set NIDO_TEST_GDS_VENDOR to an offline vendor directory}"
output="${NIDO_TEST_GDS_OUTPUT:?set NIDO_TEST_GDS_OUTPUT to a new absolute evidence directory}"
export_fixture="${NIDO_TEST_GDS_EXPORT_FIXTURE:-0}"
[[ "$export_fixture" == 0 || "$export_fixture" == 1 ]] || exit 2
[[ "$go_tool" == /* && -x "$go_tool" ]] || exit 2
[[ "$vendor" == /* && -d "$vendor" && ! -L "$vendor" && -f "$vendor/modules.txt" ]] || exit 2
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
for tool in bwrap file timeout sha256sum tar; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
go_tool="$(readlink -f "$go_tool")"
goroot="$(dirname "$(dirname "$go_tool")")"
description="$(LC_ALL=C file -b "$go_tool")"
[[ "$description" == *'ELF 64-bit'* && "$description" == *'executable'* && "$description" == *'x86-64'* ]] || { printf 'Go 必须是 amd64 原生 ELF\n' >&2; exit 2; }
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
umask 077
mkdir "$output"
output="$(cd "$output" && pwd -P)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-gds-native.XXXXXX")"
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
inputs=(cmd internal tests scripts .github Makefile VERSION GDS_VERSION .go-version go.mod go.sum)
for input in "${inputs[@]}"; do
  [[ -e "$root/$input" && ! -L "$root/$input" ]] || exit 2
  [[ -z "$(find "$root/$input" -type l -print -quit)" ]] || { printf '拒绝源码输入中的符号链接\n' >&2; exit 2; }
done
[[ -z "$(find "$vendor" -type l -print -quit)" ]] || { printf '拒绝 vendor 中的符号链接\n' >&2; exit 2; }
tar -C "$root" -cf - "${inputs[@]}" | tar -C "$scratch/source" -xf -
cp -R "$vendor" "$scratch/source/vendor"
(cd "$scratch/source" && find cmd internal tests scripts .github -type f ! -name '._*' -print0 | sort -z | xargs -0 sha256sum; cd "$scratch/source" && sha256sum Makefile VERSION GDS_VERSION .go-version go.mod go.sum) > "$output/source-manifest.sha256"
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
  --setenv GOMAXPROCS 4 --setenv GORACE halt_on_error=1
  --setenv NIDO_TEST_GDS_EXPORT_FIXTURE "$export_fixture")
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
  go test -race -json -count=1 -timeout=3m ./internal/gdsapi ./internal/gdsutil ./internal/gdsruntime/... ./internal/components/gds ./cmd/gds > /work/gds-race.json
  go test -race -json -count=1 -timeout=3m ./internal/nido -run "^TestGDS" > /work/nido-gds-race.json
  go vet ./...
  go build -trimpath -buildvcs=false -ldflags="-X=main.version=$(cat GDS_VERSION)" -o /work/gds ./cmd/gds
  /work/gds --version --json > /work/gds-metadata.json
  if [[ "$NIDO_TEST_GDS_EXPORT_FIXTURE" == 1 ]]; then
    go build -trimpath -buildvcs=false -ldflags="-X=main.version=0.0.0" -o /work/fixture-local-old.gds ./cmd/gds
  fi
  CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=$(cat VERSION)" -o /work/nido ./cmd/nido
  bash tests/nido-cli-isolation.sh /work/nido
  readelf -d /work/gds > /work/gds-dynamic.txt
  sha256sum /work/gds /work/nido > /work/binary-sha256.txt
' 2>&1 | tee "$output/run.log"
# The EXIT trap preserves partial logs on failure too; no binary is installed.
cp "$scratch/work/"*.json "$scratch/work/"*.txt "$output/"
if [[ "$export_fixture" == 1 ]]; then cp "$scratch/work/fixture-local-old.gds" "$output/"; fi
if grep -Eq '"Action":"(skip|fail)"' "$output/gds-race.json" "$output/nido-gds-race.json"; then
  printf 'GDS 专项存在失败或跳过，不计为通过\n' >&2
  exit 1
fi
grep -Fq '"native":true' "$output/gds-metadata.json"
printf '普通用户 Linux amd64 原生 GDS race、假驱动、构建与 CLI 隔离通过；证据：%s\n' "$output"
