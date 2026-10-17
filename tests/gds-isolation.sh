#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != '--isolated' ]]; then
  exec env -i PATH=/usr/bin:/bin TMPDIR="${TMPDIR:-/tmp}" /bin/bash --noprofile --norc "$0" --isolated
fi
[[ "$(uname -s)" == Linux ]] || { printf 'GDS isolation requires Linux (no GPU/CUDA required)\n' >&2; exit 2; }
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
FIXTURES="${ROOT}/tests/fixtures"
TEST_DIR="$(mktemp -d "${TMPDIR:-/tmp}/stk-gds-test.XXXXXX")"
TEST_DIR="$(cd "${TEST_DIR}" && pwd -P)"
gds_race_monitor_pid=''
cleanup() {
  local pid
  for pid in "${gds_race_monitor_pid}" "$(cat "${TEST_DIR}/gds-race/gds/workdir/scheduler.pid" 2>/dev/null || true)"; do
    if [[ "${pid}" =~ ^[1-9][0-9]*$ ]] && [[ -r "/proc/${pid}/cmdline" ]] &&
       tr '\0' '\n' <"/proc/${pid}/cmdline" | grep -Fq "${TEST_DIR}/"; then
      kill -TERM "${pid}" 2>/dev/null || true
    fi
  done
  [[ -z "${gds_race_monitor_pid}" ]] || wait "${gds_race_monitor_pid}" 2>/dev/null || true
  rm -rf -- "${TEST_DIR}"
}
trap cleanup EXIT
export HOME="${TEST_DIR}/home" PATH=/usr/bin:/bin
export SERVER_TOOLKIT_CONFIG_DIR="${TEST_DIR}/config" SERVER_TOOLKIT_STATE_DIR="${TEST_DIR}/state"
export STK_GLOBAL_LOCK_DIR="${TEST_DIR}/global.lock"
unset STK_HOME CODEX_HOME BASH_ENV ENV STK_GLOBAL_LOCK_HELD STK_DIRECT_DELEGATED STK_LOG_START_PROTECTED
mkdir -p "${HOME}"

GDS_BUILD_ROOT="${TEST_DIR}/gds-build"
mkdir -p "${GDS_BUILD_ROOT}/gds/src" "${GDS_BUILD_ROOT}/lib"
cp "${ROOT}/gds/gds.sh" "${GDS_BUILD_ROOT}/gds/gds.sh"
cp "${ROOT}/gds/src/scheduler.cu" "${GDS_BUILD_ROOT}/gds/src/scheduler.cu"
cp "${ROOT}/lib/common.sh" "${GDS_BUILD_ROOT}/lib/common.sh"
PATH="${FIXTURES}:${PATH}" "${GDS_BUILD_ROOT}/gds/gds.sh" __ensure_core >/dev/null
gds_fingerprint_before="$(head -n 1 "${GDS_BUILD_ROOT}/gds/core/scheduler.source.sha256")"
printf '\n// force test rebuild\n' >>"${GDS_BUILD_ROOT}/gds/src/scheduler.cu"
PATH="${FIXTURES}:${PATH}" "${GDS_BUILD_ROOT}/gds/gds.sh" __ensure_core >/dev/null
gds_fingerprint_after="$(head -n 1 "${GDS_BUILD_ROOT}/gds/core/scheduler.source.sha256")"
[[ "${gds_fingerprint_before}" != "${gds_fingerprint_after}" ]]

GDS_RACE_ROOT="${TEST_DIR}/gds-race"
GDS_RACE_WORK="${GDS_RACE_ROOT}/gds/workdir"
GDS_RACE_BIN="${GDS_RACE_ROOT}/bin"
mkdir -p "${GDS_RACE_ROOT}/gds/src" "${GDS_RACE_ROOT}/gds/core" \
  "${GDS_RACE_WORK}" "${GDS_RACE_ROOT}/lib" "${GDS_RACE_BIN}"
cp "${ROOT}/gds/gds.sh" "${GDS_RACE_ROOT}/gds/gds.sh"
cp "${ROOT}/gds/src/scheduler.cu" "${GDS_RACE_ROOT}/gds/src/scheduler.cu"
cp "${ROOT}/lib/common.sh" "${GDS_RACE_ROOT}/lib/common.sh"
cp "${FIXTURES}/gds-nvidia-smi" "${GDS_RACE_BIN}/nvidia-smi"
cc -O2 "${FIXTURES}/gds-worker.c" -o "${GDS_RACE_ROOT}/gds/core/scheduler"
gds_race_fingerprint="$({
  sha256sum "${GDS_RACE_ROOT}/gds/src/scheduler.cu"
  printf '%s\n' 'nvcc -O2 -std=c++14 -Xcompiler=-Wall,-Wextra'
} | sha256sum | awk '{print $1}')"
printf '%s\n' "${gds_race_fingerprint}" >"${GDS_RACE_ROOT}/gds/core/scheduler.source.sha256"
printf '%s\n' \
  'probe_interval_seconds=1' \
  'idle_threshold_seconds=1' \
  'gpu_device=0' \
  'memory_percent=25' \
  'process_scope=target' >"${GDS_RACE_ROOT}/gds/config.conf"
STK_TESTING=1 STK_TEST_WORKER_PRE_EXEC_DELAY=1 STK_SERVICE_MODE=direct \
STK_TEST_GDS_WORK_DIR="${GDS_RACE_WORK}" PATH="${GDS_RACE_BIN}:${PATH}" \
  "${GDS_RACE_ROOT}/gds/gds.sh" __run_monitor \
  >"${TEST_DIR}/gds-race.log" 2>&1 &
gds_race_monitor_pid=$!
gds_race_ready=0
for _ in {1..100}; do
  if [[ "$(awk -F= '$1=="mode" {print $2}' "${GDS_RACE_WORK}/state" 2>/dev/null || true)" == 'reserved' ]]; then
    gds_race_ready=1
    break
  fi
  sleep 0.1
done
if (( gds_race_ready == 0 )); then
  gds_race_worker_pid="$(sed -n '1p' "${GDS_RACE_WORK}/scheduler.pid" 2>/dev/null || true)"
  kill -TERM "${gds_race_monitor_pid}" 2>/dev/null || true
  [[ ! "${gds_race_worker_pid}" =~ ^[1-9][0-9]*$ ]] || kill -KILL "${gds_race_worker_pid}" 2>/dev/null || true
  wait "${gds_race_monitor_pid}" 2>/dev/null || true
  printf 'gds monitor deadlocked during delayed worker exec\n' >&2
  exit 1
fi
gds_race_worker_pid="$(sed -n '1p' "${GDS_RACE_WORK}/scheduler.pid")"
kill -0 "${gds_race_monitor_pid}"
kill -0 "${gds_race_worker_pid}"
[[ "$(sed -n '1p' "${GDS_RACE_WORK}/scheduler.gpu-pid")" == "${gds_race_worker_pid}" ]]
kill -TERM "${gds_race_monitor_pid}"
wait "${gds_race_monitor_pid}" 2>/dev/null || true
if kill -0 "${gds_race_worker_pid}" 2>/dev/null; then
  printf 'gds delayed-exec test left its worker running\n' >&2
  kill -KILL "${gds_race_worker_pid}" 2>/dev/null || true
  exit 1
fi

printf 'Optional GDS isolated build and delayed-worker cleanup: PASS\n'
