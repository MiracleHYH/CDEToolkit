#!/usr/bin/env bash
set -euo pipefail

# This entry point only uses an already running Docker engine and an already
# verified local core. It never downloads a core, pulls a base image, starts a
# daemon, or mounts host directories into a container.
nido_linux_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
nido_linux_go="${GO:-go}"
nido_linux_core="${NIDO_TEST_MIHOMO:-}"
nido_linux_core_update="${NIDO_TEST_MIHOMO_UPDATE:-}"
nido_linux_race="${NIDO_TEST_LINUX_RACE:-0}"
nido_linux_prebuilt="${NIDO_TEST_LINUX_BIN_DIR:-}"
case "$nido_linux_race" in
  0|1) ;;
  *) printf 'NIDO_TEST_LINUX_RACE 只能为 0 或 1\n' >&2; exit 2 ;;
esac
if [[ "$nido_linux_race" == 1 ]]; then
  if [[ "$nido_linux_prebuilt" != /* || ! -d "$nido_linux_prebuilt" || -L "$nido_linux_prebuilt" ]]; then
    printf 'race 模式需要 NIDO_TEST_LINUX_BIN_DIR 指向已构建静态 race 程序的绝对目录\n' >&2
    exit 2
  fi
elif [[ -n "$nido_linux_prebuilt" ]]; then
  printf 'NIDO_TEST_LINUX_BIN_DIR 仅用于显式 NIDO_TEST_LINUX_RACE=1，避免跳过默认源码构建\n' >&2
  exit 2
fi
if [[ "$nido_linux_core" != /* || ! -f "$nido_linux_core" || -L "$nido_linux_core" || ! -r "$nido_linux_core" ]]; then
  printf 'NIDO_TEST_MIHOMO 必须是已核验、可读取的原生 Linux 普通程序绝对路径\n' >&2
  exit 2
fi
if [[ -n "$nido_linux_core_update" && ( "$nido_linux_core_update" != /* || ! -f "$nido_linux_core_update" || -L "$nido_linux_core_update" || ! -r "$nido_linux_core_update" ) ]]; then
  printf 'NIDO_TEST_MIHOMO_UPDATE 必须是已核验、可读取的原生 Linux 普通程序绝对路径\n' >&2
  exit 2
fi
command -v "$nido_linux_go" >/dev/null || { printf '找不到指定的现有 Go：%s\n' "$nido_linux_go" >&2; exit 2; }
command -v docker >/dev/null || { printf '需要已有 Docker CLI 与正在运行的 Linux engine；脚本不会启动 daemon\n' >&2; exit 2; }
command -v file >/dev/null || { printf '需要已有 file 命令核验原生静态 ELF\n' >&2; exit 2; }

if ! nido_linux_engine="$(docker info --format '{{.OSType}}|{{.Architecture}}|{{.KernelVersion}}')"; then
  printf '无法读取现有 Docker engine；脚本不会安装或启动 Docker\n' >&2
  exit 2
fi
IFS='|' read -r nido_linux_os nido_linux_machine nido_linux_kernel <<< "$nido_linux_engine"
case "$nido_linux_os/$nido_linux_machine" in
  linux/amd64|linux/x86_64) nido_linux_arch=amd64; nido_linux_elf_machine='x86-64' ;;
  linux/arm64|linux/aarch64) nido_linux_arch=arm64; nido_linux_elf_machine='ARM aarch64' ;;
  *) printf '仅支持原生 Linux amd64/arm64 engine，收到：%s\n' "$nido_linux_engine" >&2; exit 2 ;;
esac
nido_linux_platform="linux/$nido_linux_arch"

nido_linux_scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-proxy-linux.XXXXXX")"
nido_linux_scratch="$(cd "$nido_linux_scratch" && pwd -P)"
nido_linux_context="$nido_linux_scratch/context"
nido_linux_meta="$nido_linux_scratch/meta"
nido_linux_run="nido-proxy-linux-$$-${nido_linux_scratch##*.}"
nido_linux_image="nido-proxy-linux-e2e:$nido_linux_run"
nido_linux_label='io.github.miraclehyh.nido.proxy-linux-run'
nido_linux_image_started=0

cleanup() {
  local nido_linux_result=$? nido_linux_cidfile nido_linux_cid nido_linux_owner nido_linux_existing
  local nido_linux_cleanup_failed=0
  trap - EXIT INT TERM
  for nido_linux_cidfile in "$nido_linux_meta/proxy.cid" "$nido_linux_meta/version.cid" "$nido_linux_meta/real.cid"; do
    if [[ -f "$nido_linux_cidfile" ]]; then
      nido_linux_cid=''
      IFS= read -r nido_linux_cid < "$nido_linux_cidfile" || true
      # A failed name collision never authorizes deleting that named container.
      # Only Docker's CID file for a container created by this run is accepted.
      if [[ "$nido_linux_cid" =~ ^[a-f0-9]{64}$ ]]; then
        if ! nido_linux_existing="$(docker container ls --all --quiet --no-trunc --filter "id=$nido_linux_cid" 2>/dev/null)"; then
          printf '无法确认本次容器是否已清理：%s\n' "$nido_linux_cid" >&2
          nido_linux_cleanup_failed=1
        elif [[ "$nido_linux_existing" == "$nido_linux_cid" ]]; then
          if ! docker container rm -f "$nido_linux_cid" >/dev/null 2>&1; then
            printf '未能清理本次容器：%s\n' "$nido_linux_cid" >&2
            nido_linux_cleanup_failed=1
          fi
        elif [[ -n "$nido_linux_existing" ]]; then
          printf '容器查询返回未知身份，未清理：%s\n' "$nido_linux_cid" >&2
          nido_linux_cleanup_failed=1
        fi
      fi
    fi
  done
  if (( nido_linux_image_started )); then
    if ! nido_linux_existing="$(docker image ls --quiet --no-trunc --filter "reference=$nido_linux_image" 2>/dev/null)"; then
      printf '无法确认本次 image tag 是否已清理：%s\n' "$nido_linux_image" >&2
      nido_linux_cleanup_failed=1
    elif [[ -n "$nido_linux_existing" ]]; then
      if ! nido_linux_owner="$(docker image inspect --format "{{index .Config.Labels \"$nido_linux_label\"}}" "$nido_linux_image" 2>/dev/null)"; then
        printf '无法核验本次 image tag 归属：%s\n' "$nido_linux_image" >&2
        nido_linux_cleanup_failed=1
      elif [[ "$nido_linux_owner" == "$nido_linux_run" ]]; then
        # Remove only our unique tag. Never force-remove a shared image ID/prune.
        if ! docker image rm "$nido_linux_image" >/dev/null 2>&1; then
          printf '未能清理本次临时 image tag：%s\n' "$nido_linux_image" >&2
          nido_linux_cleanup_failed=1
        fi
      else
        printf '临时 image tag 归属发生变化，未删除：%s\n' "$nido_linux_image" >&2
        nido_linux_cleanup_failed=1
      fi
    fi
  fi
  if (( nido_linux_cleanup_failed )); then
    printf '隔离清理未完成，保留本次 CID、日志及构建状态：%s\n' "$nido_linux_meta" >&2
    (( nido_linux_result != 0 )) || nido_linux_result=1
  elif ! rm -rf -- "$nido_linux_scratch"; then
    printf '未能移除本次临时目录：%s\n' "$nido_linux_scratch" >&2
    (( nido_linux_result != 0 )) || nido_linux_result=1
  fi
  return "$nido_linux_result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$nido_linux_context" "$nido_linux_meta"
# Buildx may need mutable builder-selection state even for a scratch build.
# Keep that state here; the user's existing Docker connection config is read-only.
export BUILDX_CONFIG="$nido_linux_meta/buildx"
mkdir "$BUILDX_CONFIG"

if docker image inspect "$nido_linux_image" >/dev/null 2>&1; then
  printf '临时 image tag 已存在，拒绝覆盖：%s\n' "$nido_linux_image" >&2
  exit 2
fi

# Validate private copies before compiling or accepting any test programs.
for nido_linux_name in mihomo mihomo-update; do
  nido_linux_input="$nido_linux_core"
  if [[ "$nido_linux_name" == mihomo-update ]]; then
    [[ -n "$nido_linux_core_update" ]] || continue
    nido_linux_input="$nido_linux_core_update"
  fi
  cp "$nido_linux_input" "$nido_linux_context/$nido_linux_name"
  nido_linux_file="$(LC_ALL=C file -b "$nido_linux_context/$nido_linux_name")"
  if [[ "$nido_linux_file" != *"ELF 64-bit"* || "$nido_linux_file" != *"$nido_linux_elf_machine"* || "$nido_linux_file" != *"statically linked"* ]]; then
    printf 'Mihomo 输入必须是当前 engine 架构的静态 Linux ELF：%s（%s）\n' "$nido_linux_name" "$nido_linux_file" >&2
    exit 2
  fi
  chmod 0555 "$nido_linux_context/$nido_linux_name"
done

# Compile on the host without installing a Linux Go toolchain or fetching
# modules. Race builds require a Linux C toolchain, so that explicit mode accepts
# three previously built static programs and verifies their build evidence.
export GOENV=off GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOEXPERIMENT=''
export GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH="$nido_linux_arch" GOAMD64=v1
export GOCACHE="${GOCACHE:-$nido_linux_root/build/go-cache}"
export GOMODCACHE="${GOMODCACHE:-$nido_linux_root/build/_go-mod}"
export GOPATH="${GOPATH:-$nido_linux_root/build/go-path}"
printf 'Linux engine：%s；kernel：%s\n' "$nido_linux_platform" "$nido_linux_kernel"
"$nido_linux_go" version
cd "$nido_linux_root"
if [[ "$nido_linux_race" == 1 ]]; then
  for nido_linux_name in nido nido.test proxy.test; do
    nido_linux_input="$nido_linux_prebuilt/$nido_linux_name"
    if [[ ! -f "$nido_linux_input" || -L "$nido_linux_input" || ! -r "$nido_linux_input" ]]; then
      printf '缺少可读取的 race 普通程序：%s\n' "$nido_linux_input" >&2
      exit 2
    fi
    cp "$nido_linux_input" "$nido_linux_context/$nido_linux_name"
    nido_linux_input="$nido_linux_context/$nido_linux_name"
    # A dynamic loader or libc would violate this scratch container's input
    # boundary. Nothing from the host's library directories is copied in.
    nido_linux_file="$(LC_ALL=C file -b "$nido_linux_input")"
    if [[ "$nido_linux_file" != *"ELF 64-bit"* || "$nido_linux_file" != *"$nido_linux_elf_machine"* || "$nido_linux_file" != *"statically linked"* ]]; then
      printf 'race 输入必须是当前 engine 架构的静态 Linux ELF：%s（%s）\n' "$nido_linux_input" "$nido_linux_file" >&2
      exit 2
    fi
    if [[ "$nido_linux_name" == nido ]]; then
      nido_linux_buildinfo="$("$nido_linux_go" version -m "$nido_linux_input")"
      if ! awk -v arch="$nido_linux_arch" '
        $1 == "path" && $2 == "github.com/MiracleHYH/CDEToolkit/cmd/nido" { path++ }
        $1 == "build" && $2 == "GOOS=linux" { os++ }
        $1 == "build" && $2 == "GOARCH=" arch { target++ }
        $1 == "build" && $2 == "CGO_ENABLED=1" { cgo++ }
        $1 == "build" && $2 == "-race=true" { race++ }
        END { exit !(path == 1 && os == 1 && target == 1 && cgo == 1 && race == 1) }
      ' <<< "$nido_linux_buildinfo"; then
        printf 'CLI 的 package、架构、CGO 或 -race 元数据不符\n' >&2
        exit 2
      fi
    else
      # Go 1.23 test executables omit build settings. Require the actual test
      # entry and both the Go race runtime and native ThreadSanitizer symbols.
      nido_linux_symbol='github.com/MiracleHYH/CDEToolkit/internal/nido.TestProxyRealMihomo'
      [[ "$nido_linux_name" != proxy.test ]] || nido_linux_symbol='github.com/MiracleHYH/CDEToolkit/internal/components/proxy.TestMain'
      nido_linux_symbols="$("$nido_linux_go" tool nm "$nido_linux_input")"
      if ! awk -v test="$nido_linux_symbol" '
        $2 ~ /^[Tt]$/ && $3 == test { entry++ }
        $2 ~ /^[Tt]$/ && $3 ~ /^runtime\.raceinit(\.abi0)?$/ { detector++ }
        $2 ~ /^[Tt]$/ && $3 == "__tsan_init" { native++ }
        END { exit !(entry == 1 && detector >= 1 && native == 1) }
      ' <<< "$nido_linux_symbols"; then
        printf 'test 程序缺少指定测试入口或真实 race 运行时符号：%s\n' "$nido_linux_name" >&2
        exit 2
      fi
    fi
    printf '已核验原生静态 race 输入：%s\n' "$nido_linux_name"
  done
  nido_linux_mode='race'
  nido_linux_memory=2g
  nido_linux_timeout=10m
else
  printf '构建目标与 engine 架构一致；执行原生非 race 测试，不请求跨架构模拟。\n'
  "$nido_linux_go" build -mod=readonly -trimpath -buildvcs=false -o "$nido_linux_context/nido" ./cmd/nido
  "$nido_linux_go" test -c -mod=readonly -trimpath -buildvcs=false -o "$nido_linux_context/nido.test" ./internal/nido
  "$nido_linux_go" test -c -mod=readonly -trimpath -buildvcs=false -o "$nido_linux_context/proxy.test" ./internal/components/proxy
  nido_linux_mode='非 race'
  nido_linux_memory=1g
  nido_linux_timeout=3m
fi
chmod 0555 "$nido_linux_context/nido" "$nido_linux_context/nido.test" "$nido_linux_context/proxy.test"
cat > "$nido_linux_context/Dockerfile" <<'DOCKERFILE'
FROM scratch
ARG NIDO_PROXY_RUN
LABEL io.github.miraclehyh.nido.proxy-linux-run="${NIDO_PROXY_RUN}"
COPY nido nido.test proxy.test mihomo /inputs/
DOCKERFILE
if [[ -n "$nido_linux_core_update" ]]; then
  printf 'COPY mihomo-update /inputs/\n' >> "$nido_linux_context/Dockerfile"
fi
cat >> "$nido_linux_context/Dockerfile" <<'DOCKERFILE'
USER 65532:65532
WORKDIR /tmp
DOCKERFILE
nido_linux_image_started=1
docker build --pull=false --network=none --platform "$nido_linux_platform" \
  --build-arg "NIDO_PROXY_RUN=$nido_linux_run" --iidfile "$nido_linux_meta/image.id" \
  --tag "$nido_linux_image" "$nido_linux_context"
nido_linux_image_platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$nido_linux_image")"
if [[ "$nido_linux_image_platform" != "$nido_linux_platform" ]]; then
  printf '临时镜像架构与 engine 不一致：%s != %s\n' "$nido_linux_image_platform" "$nido_linux_platform" >&2
  exit 1
fi
printf '原生执行证据：engine=%s，image=%s；TestOS 不允许跳过。\n' "$nido_linux_platform" "$nido_linux_image_platform"

# --init keeps the test away from PID 1 (which the identity API rejects) and
# reaps the one-shot launcher's descendants. Private PID/network namespaces
# also ensure that a remaining test core cannot outlive the container.
nido_linux_flags=(--init --rm --platform "$nido_linux_platform" --network none --read-only
  --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges
  --memory "$nido_linux_memory" --pids-limit 256 --tmpfs /tmp:rw,exec,nosuid,nodev,mode=1777,size=512m
  --label "$nido_linux_label=$nido_linux_run" --env HOME=/tmp --env TMPDIR=/tmp
  --env LANG=C --env LC_ALL=C --env TZ=UTC --env GOMAXPROCS=2)
if [[ "$nido_linux_race" == 1 ]]; then
  # The test creates its private report directory before giving log_path to
  # helpers. Its own detector reports to stderr, including before TestMain.
  nido_linux_flags+=(--env NIDO_TEST_RACE_REPORT_DIR=/tmp/nido-proxy-race-reports
    --env GORACE=halt_on_error=1)
fi
docker run "${nido_linux_flags[@]}" --name "$nido_linux_run-proxy" --cidfile "$nido_linux_meta/proxy.cid" \
  "$nido_linux_image" /inputs/proxy.test -test.v -test.count=1 -test.timeout="$nido_linux_timeout" | tee "$nido_linux_meta/proxy.log"
if grep -E -- '--- SKIP: TestOS' "$nido_linux_meta/proxy.log"; then
  printf 'Linux OS 身份/启停测试被跳过，不能报告原生验证通过\n' >&2
  exit 1
fi
if ! grep -E -- '^--- PASS: TestOS' "$nido_linux_meta/proxy.log" >/dev/null; then
  printf '输出中缺少实际通过的 TestOS 证据\n' >&2
  exit 1
fi
if grep -Ei -- '--- SKIP:.*ipv6' "$nido_linux_meta/proxy.log"; then
  printf '本次 none 网络未提供所需 IPv6 回环；以上专项明确跳过，不计入 IPv6 验证。\n'
fi
docker run "${nido_linux_flags[@]}" --name "$nido_linux_run-version" --cidfile "$nido_linux_meta/version.cid" \
  "$nido_linux_image" /inputs/mihomo -v | tee "$nido_linux_meta/version.log"
if ! grep -E -- '^Mihomo Meta [^[:space:]]+' "$nido_linux_meta/version.log" >/dev/null; then
  printf '缺少 Linux 原生 Mihomo 版本输出\n' >&2
  exit 1
fi
nido_linux_real_flags=(--env NIDO_TEST_NIDO=/inputs/nido --env NIDO_TEST_MIHOMO=/inputs/mihomo)
nido_linux_real_timeout="$nido_linux_timeout"
if [[ -n "$nido_linux_core_update" ]]; then
  nido_linux_real_flags+=(--env NIDO_TEST_MIHOMO_UPDATE=/inputs/mihomo-update)
  # Two independent real E2Es share this one test process. Keep each suite's
  # existing budget rather than halving it when the upgrade is requested.
  nido_linux_real_timeout=6m
  [[ "$nido_linux_race" != 1 ]] || nido_linux_real_timeout=20m
fi
docker run "${nido_linux_flags[@]}" --name "$nido_linux_run-real" --cidfile "$nido_linux_meta/real.cid" \
  "${nido_linux_real_flags[@]}" \
  "$nido_linux_image" /inputs/nido.test -test.v -test.count=1 -test.timeout="$nido_linux_real_timeout" \
  -test.run '^TestProxyRealMihomo(CrossVersion)?$' | tee "$nido_linux_meta/real.log"
if grep -E -- '--- SKIP: TestProxyRealMihomo([[:space:]/]|$)' "$nido_linux_meta/real.log" ||
   ! grep -E -- '^--- PASS: TestProxyRealMihomo([[:space:]]|$)' "$nido_linux_meta/real.log" >/dev/null; then
  printf '真实 Mihomo 测试未实际通过\n' >&2
  exit 1
fi
if [[ -n "$nido_linux_core_update" ]] && {
  grep -E -- '--- SKIP: TestProxyRealMihomoCrossVersion([[:space:]/]|$)' "$nido_linux_meta/real.log" ||
  ! grep -E -- '^--- PASS: TestProxyRealMihomoCrossVersion([[:space:]]|$)' "$nido_linux_meta/real.log" >/dev/null;
}; then
  printf '显式跨版本 Mihomo 测试未实际通过\n' >&2
  exit 1
fi
printf 'Linux %s 原生 Proxy 全包与真实 Mihomo 回环隔离验证通过（%s）。\n' "$nido_linux_arch" "$nido_linux_mode"
