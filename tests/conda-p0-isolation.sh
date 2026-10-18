#!/usr/bin/env bash
set -euo pipefail

# Run only independently trusted inputs. The host workspace, host HOME and
# Docker socket are never mounted into either acceptance container.
image="${NIDO_P0_IMAGE:-nido-conda-p0:24.1.2-amd64}"
binary="${NIDO_P0_BINARY:?set NIDO_P0_BINARY to a trusted linux-amd64 executable}"
output="${NIDO_P0_OUTPUT:?set NIDO_P0_OUTPUT to a new absolute evidence directory}"
label="${NIDO_P0_LABEL:-p0}"
expected_version="${NIDO_P0_VERSION:-}"
trace="${NIDO_P0_STRACE:-0}"
trace_command="${NIDO_P0_STRACE_COMMAND:-/usr/bin/strace}"
trace_status="${NIDO_P0_TRACE_STATUS:-}"
parallel="${NIDO_P0_PARALLEL:-0}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

die() {
  printf 'conda P0 isolation: %s\n' "$*" >&2
  exit 1
}

for value in "$image" "$binary" "$output" "$label" "$expected_version" "$trace_command" "$trace_status"; do
  case "$value" in
    *$'\n'*|*$'\r'*|*$'\t'*) die 'inputs must not contain line breaks or tabs' ;;
  esac
done
[[ "$binary" = /* && -f "$binary" && -x "$binary" && ! -L "$binary" ]] || die 'binary must be an absolute regular executable, not a symlink'
[[ "$output" = /* && ! -e "$output" && ! -L "$output" ]] || die 'output must be a new absolute directory'
case "$output" in *,*) die 'output path must not contain a comma (Docker mount syntax)' ;; esac
[[ "$trace" = 0 || "$trace" = 1 ]] || die 'NIDO_P0_STRACE must be 0 or 1'
[[ "$parallel" = 0 || "$parallel" = 1 ]] || die 'NIDO_P0_PARALLEL must be 0 or 1'
[[ "$trace_command" = /* ]] || die 'NIDO_P0_STRACE_COMMAND must be an absolute container path'
if [[ "$trace" = 0 ]]; then
  trace_status='not performed; no syscall audit is claimed'
elif [[ -z "$trace_status" && "$trace_command" = /usr/local/bin/nido-p0-strace ]]; then
  trace_status='native-arm64 VM syscall tracing under amd64 emulation'
elif [[ -z "$trace_status" ]]; then
  trace_status="requested syscall tracing through $trace_command; inspect trace and exit status"
fi
command -v docker >/dev/null || die 'docker is required'

for name in conda-p0-acceptance.py conda-source-upstream-isolation.py pip-upstream-isolation.py; do
  [[ -f "$script_dir/$name" && ! -L "$script_dir/$name" ]] || die "missing regular input: $script_dir/$name"
done
output_parent="$(cd "$(dirname "$output")" && pwd -P)"
output="$output_parent/$(basename "$output")"
[[ ! -e "$output" && ! -L "$output" ]] || die 'canonical output already exists'

image_id="$(docker image inspect "$image" --format '{{.Id}}')" || die 'required local image is unavailable; this script never pulls images'
image_platform="$(docker image inspect "$image_id" --format '{{.Os}}/{{.Architecture}}')"
[[ "$image_platform" = linux/amd64 ]] || die "expected linux/amd64 image, received $image_platform"
image_volumes="$(docker image inspect "$image_id" --format '{{json .Config.Volumes}}')"
[[ "$image_volumes" = null || "$image_volumes" = '{}' ]] || die 'image declares extra writable volumes'
host_uname="$(uname -srm)"
engine_architecture="$(docker info --format '{{.Architecture}}')"

umask 077
mkdir "$output"
inputs="$output/inputs"
mkdir "$inputs"
cp "$binary" "$inputs/nido"
for name in conda-p0-acceptance.py conda-source-upstream-isolation.py pip-upstream-isolation.py; do
  cp "$script_dir/$name" "$inputs/$name"
done
cp "${BASH_SOURCE[0]}" "$inputs/conda-p0-isolation.sh"
chmod 0555 "$inputs/nido"
chmod 0444 "$inputs/"*.py "$inputs/conda-p0-isolation.sh"
chmod 0555 "$inputs"

sha256_file() {
  if command -v shasum >/dev/null; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    die 'shasum or sha256sum is required'
  fi
}

[[ "$(sha256_file "$binary")" = "$(sha256_file "$inputs/nido")" ]] || die 'binary changed while copying'
for name in nido conda-p0-acceptance.py conda-source-upstream-isolation.py pip-upstream-isolation.py conda-p0-isolation.sh; do
  printf '%s  %s\n' "$(sha256_file "$inputs/$name")" "$name"
done > "$output/input-sha256.txt"
{
  printf 'label=%s\n' "$label"
  printf 'started_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf 'image_requested=%s\nimage_id=%s\nimage_platform=%s\n' "$image" "$image_id" "$image_platform"
  printf 'host_uname=%s\ndocker_engine_architecture=%s\n' "$host_uname" "$engine_architecture"
  printf 'binary_input=%s\n' "$binary"
  printf 'nido_expected_version=%s\n' "$expected_version"
  printf 'parallel_modes=%s\n' "$parallel"
  printf 'container_user=1000:1000\nrootfs=read-only\nnetwork=none\ncap_drop=ALL\nno_new_privileges=true\npull=never\npids_limit=512\n'
  printf 'input_mount=/inputs:read-only\nevidence_mount=/evidence:read-write\nwork_mount=/work:tmpfs\ntemporary_mount=/tmp:tmpfs\n'
  printf 'normal_runtime=/runtime:tmpfs:2GiB:seed-extracted\nstrict_runtime=/runtime/miniconda:read-only-image\n'
  printf 'syscall_trace_requested=%s\n' "$trace"
  printf 'syscall_trace_status=%s\n' "$trace_status"
  if [[ "$trace" = 1 ]]; then
    printf 'syscall_trace_command=%s\nsyscall_audit=requested; see per-mode trace.log and exitcode.txt\n' "$trace_command"
  else
    printf 'syscall_audit=not-performed\nsyscall_audit_limit=amd64 strace PTRACE_TRACEME is unavailable under the tested OrbStack emulation; no syscall audit is claimed\n'
  fi
  printf 'scope=isolated configuration commands; no real-machine deployment or data migration\n'
} > "$output/inputs.txt"

container_script='set -eu
umask 022
mode=$1
trace=$2
trace_command=$3
trace_status=$4
expected_version=$5
mkdir -p /work/home
if [ "$mode" = normal ]; then
  tar -xf /opt/miniconda-seed.tar -C /runtime
fi
printf "mode=%s\nuid_gid=%s:%s\nruntime=/runtime/miniconda\nsyscall_trace_status=%s\n" "$mode" "$(id -u)" "$(id -g)" "$trace_status" > /evidence/container.txt
set +e
if [ "$trace" = 1 ]; then
  env -i HOME=/work/home TMPDIR=/tmp PATH=/runtime/miniconda/bin:/usr/bin:/bin PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 CONDA_OFFLINE=true BASH_ENV=/dev/null ENV=/dev/null "NIDO_P0_TRACE_STATUS=$trace_status" \
    "$trace_command" -f -qq -s256 -e trace=%file,%network -o /evidence/trace.log \
    /runtime/miniconda/bin/python -B -S /inputs/conda-p0-acceptance.py --binary /inputs/nido --mode "$mode" --output /evidence --expected-version="$expected_version"
else
  env -i HOME=/work/home TMPDIR=/tmp PATH=/runtime/miniconda/bin:/usr/bin:/bin PYTHONDONTWRITEBYTECODE=1 PIP_DISABLE_PIP_VERSION_CHECK=1 CONDA_OFFLINE=true BASH_ENV=/dev/null ENV=/dev/null "NIDO_P0_TRACE_STATUS=$trace_status" \
    /runtime/miniconda/bin/python -B -S /inputs/conda-p0-acceptance.py --binary /inputs/nido --mode "$mode" --output /evidence --expected-version="$expected_version"
fi
result=$?
printf "%s\n" "$result" > /evidence/controller-exitcode.txt
exit "$result"
'

run_mode() {
  local mode="$1" evidence="$output/$1" result
  local -a docker_args
  mkdir "$evidence"
  # A fixed unprivileged container user owns all temporary runtime state. Only
  # this new evidence directory is writable through a host bind mount.
  chmod 0777 "$evidence"
  {
    printf 'mode=%s\nimage_id=%s\nlabel=%s\n' "$mode" "$image_id" "$label"
    printf 'rootfs_read_only=true\nsyscall_trace_requested=%s\n' "$trace"
    printf 'syscall_trace_status=%s\n' "$trace_status"
  } > "$evidence/host-inputs.txt"
  docker_args=(run --rm --pull never --platform linux/amd64 --read-only --network none --cap-drop ALL --security-opt no-new-privileges --pids-limit 512 --user 1000:1000 --workdir /work --entrypoint /bin/sh
    --mount "type=bind,source=$inputs,target=/inputs,readonly"
    --mount "type=bind,source=$evidence,target=/evidence"
    --tmpfs /work:rw,exec,nosuid,nodev,size=2g,uid=1000,gid=1000,mode=0700
    --tmpfs /tmp:rw,exec,nosuid,nodev,size=1g,uid=1000,gid=1000,mode=1777
    --env BASH_ENV=/dev/null --env ENV=/dev/null)
  if [[ "$mode" = normal ]]; then
    docker_args+=(--tmpfs /runtime:rw,exec,nosuid,nodev,size=2g,uid=1000,gid=1000,mode=0700)
  fi
  printf 'conda P0 isolation: %s (%s)\n' "$mode" "$label"
  if docker "${docker_args[@]}" "$image_id" -c "$container_script" nido-p0 "$mode" "$trace" "$trace_command" "$trace_status" "$expected_version" > "$evidence/container.log" 2>&1; then
    result=0
  else
    result=$?
  fi
  printf '%s\n' "$result" > "$evidence/docker-exitcode.txt"
  chmod 0755 "$evidence"
  printf 'conda P0 isolation: %s exit=%s; evidence=%s\n' "$mode" "$result" "$evidence"
  return "$result"
}

failed=0
wait_for_mode() {
  local mode="$1" worker="$2" result evidence="$output/$1"
  if wait "$worker"; then
    result=0
  else
    result=$?
    failed=1
  fi
  # A setup/receipt failure in a worker is an outer failure too. Preserve its
  # status even when Docker never launched or its own status had been zero.
  if [[ ! -d "$evidence" ]]; then
    mkdir "$evidence"
  fi
  printf '%s\n' "$result" > "$evidence/exitcode.txt"
}

if [[ "$parallel" = 1 ]]; then
  run_mode normal &
  normal_worker=$!
  run_mode strict &
  strict_worker=$!
  wait_for_mode normal "$normal_worker"
  wait_for_mode strict "$strict_worker"
else
  for mode in normal strict; do
    # A background worker gives sequential and parallel modes the same
    # errexit and status collection behavior; the sequential path waits now.
    run_mode "$mode" &
    worker=$!
    wait_for_mode "$mode" "$worker"
  done
fi

{
  printf 'finished_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf 'normal_exitcode=%s\nstrict_exitcode=%s\n' "$(cat "$output/normal/exitcode.txt")" "$(cat "$output/strict/exitcode.txt")"
  printf 'result=%s\n' "$([[ "$failed" = 0 ]] && printf PASS || printf FAIL)"
} > "$output/result.txt"
exit "$failed"
