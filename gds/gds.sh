#!/usr/bin/env bash

set -uo pipefail
umask 077

SCRIPT_PATH="${BASH_SOURCE[0]}"
if command -v readlink >/dev/null 2>&1; then
  RESOLVED_PATH="$(readlink -f -- "${SCRIPT_PATH}" 2>/dev/null || true)"
  if [[ -n "${RESOLVED_PATH}" ]]; then
    SCRIPT_PATH="${RESOLVED_PATH}"
  fi
fi
PROJECT_DIR="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"
TOOLKIT_DIR="$(dirname -- "${PROJECT_DIR}")"
# shellcheck disable=SC2034 # Consumed while sourcing lib/common.sh.
STK_ROOT="${TOOLKIT_DIR}"
# shellcheck source=../lib/common.sh
source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
CORE_DIR="${PROJECT_DIR}/core"
CORE_SOURCE="${PROJECT_DIR}/src/scheduler.cu"
CORE_BIN="${CORE_DIR}/scheduler"
CORE_BUILD_STATE="${CORE_DIR}/scheduler.source.sha256"
WORK_DIR="${PROJECT_DIR}/workdir"
CONFIG_FILE="${PROJECT_DIR}/config.conf"
LOG_FILE="${WORK_DIR}/gds.log"
STK_GDS_LOG_FD_OPEN=0
PID_FILE="${WORK_DIR}/gds.pid"
STATE_FILE="${WORK_DIR}/state"
WORKER_PID_FILE="${WORK_DIR}/scheduler.pid"
WORKER_GPU_PID_FILE="${WORK_DIR}/scheduler.gpu-pid"
WORKER_STATE_FILE="${WORK_DIR}/scheduler.state"
LOCK_DIR="${WORK_DIR}/.gds.lock"
SESSION_NAME="${GDS_TMUX_SESSION:-gpu-dynamic-scheduler}"
SESSION_OWNER_OPTION='@gds_project'
DEFAULT_PROBE_INTERVAL=10
DEFAULT_IDLE_THRESHOLD=1800
DEFAULT_GPU_DEVICE=0
DEFAULT_MEMORY_PERCENT=25
DEFAULT_PROCESS_SCOPE='target'
DASHBOARD_REFRESH_SECONDS=10

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || printf '0')" -ge 8 ]]; then
  RESET=$'\033[0m'
  BOLD=$'\033[1m'
  DIM=$'\033[2m'
  GREEN=$'\033[32m'
  YELLOW=$'\033[33m'
  RED=$'\033[31m'
  CYAN=$'\033[36m'
else
  RESET=''
  BOLD=''
  DIM=''
  GREEN=''
  YELLOW=''
  RED=''
  CYAN=''
fi

info() { printf '%s\n' "${CYAN}›${RESET} $*"; }
ok() { printf '%s\n' "${GREEN}✓${RESET} $*"; }
warn() { printf '%s\n' "${YELLOW}!${RESET} $*" >&2; }
fail() { printf '%s\n' "${RED}✗${RESET} $*" >&2; return 1; }

has_command() {
  command -v "$1" >/dev/null 2>&1
}

require_command() {
  if ! has_command "$1"; then
    fail "缺少依赖：$1"
    return 1
  fi
}

secure_runtime_paths() {
  if [[ -L "${CONFIG_FILE}" || ( -e "${CONFIG_FILE}" && ! -f "${CONFIG_FILE}" ) ]]; then
    fail "GDS 配置必须是普通文件且不能是符号链接：${CONFIG_FILE}"
    return 1
  fi
  [[ ! -e "${CONFIG_FILE}" ]] || chmod 0600 "${CONFIG_FILE}" || return 1
  if [[ -L "${WORK_DIR}" ]]; then
    fail "GDS 运行目录不能是符号链接：${WORK_DIR}"
    return 1
  fi
  mkdir -p -- "${WORK_DIR}"
  chmod 0700 "${WORK_DIR}" || return 1
  local runtime_file
  for runtime_file in "${LOG_FILE}" "${PID_FILE}" "${STATE_FILE}" \
    "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" "${WORKER_STATE_FILE}"; do
    if [[ -L "${runtime_file}" ]]; then
      fail "GDS 运行文件不能是符号链接：${runtime_file}"
      return 1
    fi
    [[ ! -e "${runtime_file}" ]] || chmod 0600 "${runtime_file}" || return 1
  done
}

raw_config_value() {
  local key="$1" value=''
  if [[ -r "${CONFIG_FILE}" ]]; then
    value="$(awk -F= -v key="${key}" '
      /^[[:space:]]*#/ { next }
      {
        name=$1
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
        if (name == key) {
          $1=""
          sub(/^=/, "")
          gsub(/^[[:space:]]+|[[:space:]]+$/, "")
          print
          exit
        }
      }
    ' "${CONFIG_FILE}" 2>/dev/null || true)"
  fi
  printf '%s\n' "${value}"
}

config_value() {
  local key="$1" fallback="$2" value
  value="$(raw_config_value "${key}")"
  if [[ "${value}" =~ ^[1-9][0-9]*$ && ${#value} -le 9 ]]; then
    printf '%s\n' "${value}"
  else
    printf '%s\n' "${fallback}"
  fi
}

config_nonnegative_value() {
  local key="$1" fallback="$2" value
  value="$(raw_config_value "${key}")"
  if [[ "${value}" =~ ^[0-9]+$ && ${#value} -le 9 ]]; then
    printf '%s\n' "${value}"
  else
    printf '%s\n' "${fallback}"
  fi
}

probe_interval() {
  config_value 'probe_interval_seconds' "${DEFAULT_PROBE_INTERVAL}"
}

idle_threshold() {
  config_value 'idle_threshold_seconds' "${DEFAULT_IDLE_THRESHOLD}"
}

gpu_device() {
  config_nonnegative_value 'gpu_device' "${DEFAULT_GPU_DEVICE}"
}

memory_percent() {
  local value
  value="$(config_value 'memory_percent' "${DEFAULT_MEMORY_PERCENT}")"
  if (( value > 90 )); then
    value="${DEFAULT_MEMORY_PERCENT}"
  fi
  printf '%s\n' "${value}"
}

process_scope() {
  local value
  value="$(raw_config_value 'process_scope')"
  case "${value}" in
    target|all) printf '%s\n' "${value}" ;;
    *) printf '%s\n' "${DEFAULT_PROCESS_SCOPE}" ;;
  esac
}

target_gpu_uuid() {
  nvidia-smi --id="$(gpu_device)" --query-gpu=uuid \
    --format=csv,noheader,nounits 2>/dev/null | awk 'NF { gsub(/^[[:space:]]+|[[:space:]]+$/, ""); print; exit }'
}

format_duration() {
  local total="${1:-0}" days hours minutes seconds
  [[ "${total}" =~ ^[0-9]+$ ]] || total=0
  days=$((total / 86400))
  hours=$(((total % 86400) / 3600))
  minutes=$(((total % 3600) / 60))
  seconds=$((total % 60))
  if (( days > 0 )); then
    printf '%dd %02dh %02dm %02ds' "${days}" "${hours}" "${minutes}" "${seconds}"
  elif (( hours > 0 )); then
    printf '%dh %02dm %02ds' "${hours}" "${minutes}" "${seconds}"
  elif (( minutes > 0 )); then
    printf '%dm %02ds' "${minutes}" "${seconds}"
  else
    printf '%ds' "${seconds}"
  fi
}

format_bytes() {
  local bytes="${1:-0}"
  if [[ ! "${bytes}" =~ ^[0-9]+$ ]]; then
    bytes=0
  fi
  awk -v bytes="${bytes}" 'BEGIN { printf "%.1f GiB", bytes / 1073741824 }'
}

run_module_locked() {
  local owner_file owner_pid='' owner_start='' process_start='' result
  mkdir -p "${WORK_DIR}"
  owner_file="${LOCK_DIR}/owner"
  if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
    if [[ -r "${owner_file}" ]]; then
      owner_pid="$(stk_state_value "${owner_file}" pid)"
      owner_start="$(stk_state_value "${owner_file}" process_start)"
      if [[ -z "${owner_pid}" ]]; then
        read -r owner_pid <"${owner_file}" || true
      fi
    fi
    if [[ -n "${owner_start}" ]] && stk_lock_owner_is_live "${owner_pid}" "${owner_start}"; then
      fail '另一项 gds 操作正在进行，请稍后重试'
      return 75
    fi
    warn '检测到异常遗留的操作锁，正在自动恢复'
    rm -f -- "${owner_file}"
    rmdir -- "${LOCK_DIR}" 2>/dev/null || {
      fail "无法清理操作锁：${LOCK_DIR}"
      return 1
    }
    if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
      fail '另一项 gds 操作刚刚开始，请稍后重试'
      return 75
    fi
  fi
  process_start="$(stk_process_start_marker "$$" 2>/dev/null || true)"
  if ! printf 'pid=%s\nprocess_start=%s\n' "$$" "${process_start}" >"${owner_file}"; then
    rm -f -- "${owner_file}"
    rmdir -- "${LOCK_DIR}" 2>/dev/null || true
    fail "无法写入操作锁：${owner_file}"
    return 1
  fi
  "$@"
  result=$?
  rm -f -- "${owner_file}"
  rmdir -- "${LOCK_DIR}" 2>/dev/null || true
  return "${result}"
}

run_global_operation() {
  if [[ "${STK_GLOBAL_LOCK_HELD:-0}" == '1' ]]; then
    "$@"
  else
    stk_with_global_lock "$@"
  fi
}

run_locked_after_global() {
  [[ -d "${TOOLKIT_DIR}" && -f "${SCRIPT_PATH}" ]] || {
    fail 'server-toolkit 已在等待锁期间被移除'
    return 1
  }
  run_module_locked "$@"
}

run_locked() {
  run_global_operation run_locked_after_global "$@"
}

core_needs_build() {
  local expected recorded=''
  [[ -x "${CORE_BIN}" && -f "${CORE_BUILD_STATE}" && ! -L "${CORE_BUILD_STATE}" ]] || return 0
  has_command sha256sum || return 0
  expected="$( { sha256sum "${CORE_SOURCE}"; printf '%s\n' 'nvcc -O2 -std=c++14 -Xcompiler=-Wall,-Wextra'; } | sha256sum | awk '{print $1}' )" || return 0
  read -r recorded <"${CORE_BUILD_STATE}" || true
  [[ "${recorded}" != "${expected}" ]]
}

build_core_unlocked() {
  local temporary state_temporary fingerprint
  require_command nvcc || return 1
  require_command sha256sum || return 1
  if [[ ! -r "${CORE_SOURCE}" ]]; then
    fail "找不到 scheduler 源码：${CORE_SOURCE}"
    return 1
  fi
  mkdir -p "${CORE_DIR}"
  temporary="$(mktemp "${CORE_DIR}/.scheduler.XXXXXX")" || return 1
  info '正在编译透明 CUDA worker：scheduler'
  if ! nvcc -O2 -std=c++14 -Xcompiler=-Wall,-Wextra \
    "${CORE_SOURCE}" -o "${temporary}"; then
    rm -f -- "${temporary}"
    fail 'scheduler 编译失败'
    return 1
  fi
  chmod 0755 "${temporary}"
  if ! "${temporary}" --help >/dev/null 2>&1; then
    rm -f -- "${temporary}"
    fail '编译后的 scheduler 无法运行'
    return 1
  fi
  fingerprint="$( { sha256sum "${CORE_SOURCE}"; printf '%s\n' 'nvcc -O2 -std=c++14 -Xcompiler=-Wall,-Wextra'; } | sha256sum | awk '{print $1}' )" || {
    rm -f -- "${temporary}"
    return 1
  }
  state_temporary="$(mktemp "${CORE_DIR}/.scheduler-source.XXXXXX")" || {
    rm -f -- "${temporary}"
    return 1
  }
  printf '%s\n' "${fingerprint}" >"${state_temporary}"
  chmod 0600 "${state_temporary}"
  mv -f -- "${temporary}" "${CORE_BIN}" || { rm -f -- "${state_temporary}"; return 1; }
  mv -f -- "${state_temporary}" "${CORE_BUILD_STATE}" || return 1
  ok "scheduler 已编译：${CORE_BIN}"
}

ensure_core() {
  if core_needs_build; then
    build_core_unlocked
  fi
}

ensure_core_locked() {
  run_locked ensure_core
}

session_exists() {
  has_command tmux && tmux has-session -t "=${SESSION_NAME}" 2>/dev/null
}

session_pid() {
  tmux list-panes -t "=${SESSION_NAME}" -F '#{pane_pid}' 2>/dev/null | head -n 1
}

pid_is_monitor() {
  local pid="$1" argument has_script=1 has_marker=1
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null || return 1
  [[ -r "/proc/${pid}/cmdline" ]] || return 1
  while IFS= read -r -d '' argument; do
    if [[ "${argument}" == "${PROJECT_DIR}/gds.sh" || "${argument}" == "${SCRIPT_PATH}" ]]; then
      has_script=0
    elif [[ "${argument}" == '__run_monitor' ]]; then
      has_marker=0
    fi
  done <"/proc/${pid}/cmdline"
  (( has_script == 0 && has_marker == 0 ))
}

managed_monitor_pid() {
  local pid='' process
  if [[ -r "${PID_FILE}" ]]; then
    read -r pid <"${PID_FILE}" || true
    if pid_is_monitor "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi
  if session_exists; then
    pid="$(session_pid)"
    if pid_is_monitor "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi
  for process in /proc/[0-9]*; do
    pid="${process##*/}"
    if pid_is_monitor "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  done
  return 1
}

session_owner() {
  tmux show-options -qv -t "${SESSION_NAME}" "${SESSION_OWNER_OPTION}" 2>/dev/null || true
}

session_is_managed() {
  local owner pid
  session_exists || return 1
  owner="$(session_owner)"
  if [[ "${owner}" == "${PROJECT_DIR}" ]]; then
    return 0
  fi
  if [[ -z "${owner}" ]]; then
    pid="$(session_pid)"
    pid_is_monitor "${pid}"
    return
  fi
  return 1
}

session_pane_alive() {
  local pane_dead pid
  session_exists || return 1
  pane_dead="$(tmux list-panes -t "=${SESSION_NAME}" -F '#{pane_dead}' 2>/dev/null | head -n 1)"
  [[ "${pane_dead}" == '0' ]] || return 1
  pid="$(session_pid)"
  kill -0 "${pid}" 2>/dev/null
}

is_running() {
  local pid
  if session_is_managed && session_pane_alive; then
    pid="$(session_pid)"
    pid_is_monitor "${pid}"
    return
  fi
  pid="$(managed_monitor_pid 2>/dev/null || true)"
  [[ -n "${pid}" ]] && pid_is_direct_service "${pid}"
}

pid_is_direct_service() {
  local pid="$1"
  pid_is_monitor "${pid}" || return 1
  tr '\0' '\n' <"/proc/${pid}/environ" 2>/dev/null | grep -Fqx 'STK_SERVICE_MODE=direct'
}

delegate_direct_service_action() {
  local action="$1"
  if [[ "${STK_DIRECT_DELEGATED:-0}" == '1' || ! -x "${TOOLKIT_DIR}/toolkit.sh" ]]; then
    fail "gds monitor 由 supervisord 直接监管；请使用 stk module ${action} gds"
    return 1
  fi
  info "gds monitor 由 supervisord 直接监管，交由 stk module ${action} gds"
  STK_DIRECT_DELEGATED=1 "${TOOLKIT_DIR}/toolkit.sh" module "${action}" gds
}

worker_pid_is_managed() {
  local pid="$1" executable
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null || return 1
  executable="$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
  executable="${executable% (deleted)}"
  [[ "${executable}" == "${CORE_BIN}" ]]
}

managed_worker_pid() {
  local pid=''
  if [[ -r "${WORKER_PID_FILE}" ]]; then
    read -r pid <"${WORKER_PID_FILE}" || true
    if worker_pid_is_managed "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi
  return 1
}

managed_worker_gpu_pid() {
  local worker_pid gpu_pid=''
  worker_pid="$(managed_worker_pid 2>/dev/null || true)"
  [[ -n "${worker_pid}" && -r "${WORKER_GPU_PID_FILE}" ]] || return 1
  read -r gpu_pid <"${WORKER_GPU_PID_FILE}" || true
  [[ "${gpu_pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  printf '%s\n' "${gpu_pid}"
}

remove_stale_worker_files() {
  local pid=''
  if [[ -r "${WORKER_PID_FILE}" ]]; then
    read -r pid <"${WORKER_PID_FILE}" || true
    if worker_pid_is_managed "${pid}"; then
      return 0
    fi
  fi
  rm -f -- "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" \
    "${WORKER_STATE_FILE}"
}

terminate_worker_pid() {
  local pid="$1"
  if ! worker_pid_is_managed "${pid}"; then
    remove_stale_worker_files
    return 0
  fi
  kill -TERM "${pid}" 2>/dev/null || true
  for _ in {1..50}; do
    if ! worker_pid_is_managed "${pid}"; then
      wait "${pid}" 2>/dev/null || true
      rm -f -- "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" \
        "${WORKER_STATE_FILE}"
      return 0
    fi
    sleep 0.1
  done
  warn "scheduler PID ${pid} 未响应 TERM，正在强制结束"
  kill -KILL "${pid}" 2>/dev/null || true
  for _ in {1..20}; do
    if ! worker_pid_is_managed "${pid}"; then
      wait "${pid}" 2>/dev/null || true
      rm -f -- "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" \
        "${WORKER_STATE_FILE}"
      return 0
    fi
    sleep 0.1
  done
  fail "无法结束 scheduler PID ${pid}"
}

stop_worker() {
  local reason="${1:-requested}" pid=''
  pid="$(managed_worker_pid 2>/dev/null || true)"
  if [[ -z "${pid}" ]]; then
    remove_stale_worker_files
    return 0
  fi
  log_event "stopping scheduler PID ${pid}: ${reason}"
  terminate_worker_pid "${pid}"
}

started_worker_is_ours() {
  local pid="$1" parent=''
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null || return 1
  parent="$(awk '/^PPid:/ { print $2; exit }' "/proc/${pid}/status" 2>/dev/null || true)"
  [[ "${parent}" == "$$" ]]
}

started_worker_has_exited() {
  local pid="$1" state=''
  if ! kill -0 "${pid}" 2>/dev/null; then
    return 0
  fi
  state="$(sed -n 's/^.*) \([^ ]\).*/\1/p' "/proc/${pid}/stat" 2>/dev/null || true)"
  [[ "${state}" == 'Z' || "${state}" == 'X' ]]
}

terminate_started_worker_pid() {
  local pid="$1"
  if started_worker_has_exited "${pid}"; then
    wait "${pid}" 2>/dev/null || true
    remove_stale_worker_files
    return 0
  fi
  if ! started_worker_is_ours "${pid}" && ! worker_pid_is_managed "${pid}"; then
    fail "启动中的 scheduler PID ${pid} 已失去父子关系且身份不匹配，拒绝终止"
    return 1
  fi
  kill -TERM "${pid}" 2>/dev/null || true
  for _ in {1..50}; do
    if started_worker_has_exited "${pid}"; then
      wait "${pid}" 2>/dev/null || true
      rm -f -- "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" \
        "${WORKER_STATE_FILE}"
      return 0
    fi
    sleep 0.1
  done
  warn "启动中的 scheduler PID ${pid} 未响应 TERM，正在强制结束"
  kill -KILL "${pid}" 2>/dev/null || true
  for _ in {1..20}; do
    if started_worker_has_exited "${pid}"; then
      wait "${pid}" 2>/dev/null || true
      rm -f -- "${WORKER_PID_FILE}" "${WORKER_GPU_PID_FILE}" \
        "${WORKER_STATE_FILE}"
      return 0
    fi
    sleep 0.1
  done
  fail "无法结束启动中的 scheduler PID ${pid}"
}

start_worker() {
  local device percent pid pid_file_tmp status_pid
  local visible_processes visible_count gpu_pid gpu_pid_file_tmp
  if managed_worker_pid >/dev/null 2>&1; then
    return 0
  fi
  remove_stale_worker_files
  ensure_core || return 1
  device="$(gpu_device)"
  percent="$(memory_percent)"
  rm -f -- "${WORKER_STATE_FILE}"
  log_event "starting scheduler (device=${device}, memory=${percent}%)"
  if [[ "${STK_TESTING:-0}" == '1' && \
    "${STK_TEST_WORKER_PRE_EXEC_DELAY:-}" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
    bash -c 'sleep "$1"; shift; exec "$@"' _ \
      "${STK_TEST_WORKER_PRE_EXEC_DELAY}" "${CORE_BIN}" \
      --device "${device}" \
      --memory-percent "${percent}" \
      --status-file "${WORKER_STATE_FILE}" &
  elif [[ "${STK_SERVICE_MODE:-}" == 'direct' ]]; then
    "${CORE_BIN}" \
      --device "${device}" \
      --memory-percent "${percent}" \
      --status-file "${WORKER_STATE_FILE}" &
  else
    "${CORE_BIN}" \
      --device "${device}" \
      --memory-percent "${percent}" \
      --status-file "${WORKER_STATE_FILE}" >>"${LOG_FILE}" 2>&1 &
  fi
  pid=$!
  pid_file_tmp="${WORKER_PID_FILE}.$$"
  printf '%s\n' "${pid}" >"${pid_file_tmp}"
  mv -f -- "${pid_file_tmp}" "${WORKER_PID_FILE}"

  for _ in {1..50}; do
    if ! kill -0 "${pid}" 2>/dev/null; then
      wait "${pid}" 2>/dev/null || true
      remove_stale_worker_files
      log_event 'scheduler failed before becoming ready'
      return 1
    fi
    status_pid="$(file_value "${WORKER_STATE_FILE}" pid)"
    if [[ "${status_pid}" == "${pid}" ]]; then
      if worker_pid_is_managed "${pid}"; then
        break
      fi
      log_event 'scheduler published readiness with an unexpected executable identity'
      terminate_started_worker_pid "${pid}" || true
      return 1
    fi
    sleep 0.1
  done

  if [[ "${status_pid:-}" != "${pid}" ]]; then
    log_event 'scheduler readiness timed out'
    terminate_started_worker_pid "${pid}" || true
    return 1
  fi

  for _ in {1..50}; do
    if ! worker_pid_is_managed "${pid}"; then
      terminate_started_worker_pid "${pid}" || true
      log_event 'scheduler stopped before its GPU PID was visible'
      return 1
    fi
    if visible_processes="$(query_compute_processes)"; then
      visible_count="$(printf '%s\n' "${visible_processes}" | awk 'NF { count++ } END { print count+0 }')"
      if (( visible_count == 1 )); then
        gpu_pid="$(printf '%s\n' "${visible_processes}" | awk -F, 'NF { gsub(/[[:space:]]/, "", $1); print $1; exit }')"
        if [[ "${gpu_pid}" =~ ^[1-9][0-9]*$ ]]; then
          gpu_pid_file_tmp="${WORKER_GPU_PID_FILE}.$$"
          printf '%s\n' "${gpu_pid}" >"${gpu_pid_file_tmp}"
          mv -f -- "${gpu_pid_file_tmp}" "${WORKER_GPU_PID_FILE}"
          log_event "scheduler ready (PID ${pid}, GPU PID ${gpu_pid})"
          return 0
        fi
      elif (( visible_count > 1 )); then
        log_event 'another GPU process appeared while scheduler was starting'
        terminate_started_worker_pid "${pid}" || true
        return 1
      fi
    fi
    sleep 0.1
  done

  log_event 'scheduler GPU PID discovery timed out'
  terminate_started_worker_pid "${pid}" || true
  return 1
}

monitor_state_is_fresh() {
  local pid="$1" timestamp now interval max_age mode state_worker actual_worker gpu_pid
  pid_is_monitor "${pid}" || return 1
  timestamp="$(state_value timestamp)"
  [[ "${timestamp}" =~ ^[1-9][0-9]*$ ]] || return 1
  now="$(date +%s)"
  (( timestamp <= now )) || return 1
  interval="$(probe_interval)"
  max_age=$((interval * 3 + 5))
  (( now - timestamp <= max_age )) || return 1

  mode="$(state_value mode)"
  case "${mode}" in
    busy|idle|reserved|worker-error|unavailable) ;;
    *) return 1 ;;
  esac
  if [[ "${mode}" == 'reserved' ]]; then
    state_worker="$(state_value worker_pid)"
    actual_worker="$(managed_worker_pid 2>/dev/null || true)"
    gpu_pid="$(managed_worker_gpu_pid 2>/dev/null || true)"
    [[ -n "${actual_worker}" && "${state_worker}" == "${actual_worker}" && \
      -n "${gpu_pid}" ]] || return 1
  fi
}

show_status() {
  local quiet="${1:-}" state pid=''
  if session_exists; then
    if ! session_is_managed; then
      state='conflict'
    elif is_running; then
      pid="$(session_pid)"
      if monitor_state_is_fresh "${pid}"; then state='running'; else state='unhealthy'; fi
    else
      state='unhealthy'
    fi
  elif pid="$(managed_monitor_pid 2>/dev/null || true)"; [[ -n "${pid}" ]]; then
    if pid_is_direct_service "${pid}"; then
      if monitor_state_is_fresh "${pid}"; then state='running'; else state='unhealthy'; fi
    else
      state='orphan'
    fi
  else
    state='stopped'
  fi
  if [[ "${quiet}" != '--quiet' ]]; then
    printf '%s\n' "${state}"
  fi
  [[ "${state}" == 'running' ]]
}

file_value() {
  local file="$1" key="$2"
  awk -F= -v key="${key}" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' \
    "${file}" 2>/dev/null || true
}

state_value() {
  file_value "${STATE_FILE}" "$1"
}

worker_state_value() {
  file_value "${WORKER_STATE_FILE}" "$1"
}

write_state() {
  local mode="$1" timestamp="$2" idle_since="$3" idle_seconds="$4" process_count="$5"
  local worker_pid="${6:-0}"
  local temp_file="${STATE_FILE}.$$"
  {
    printf 'mode=%s\n' "${mode}"
    printf 'timestamp=%s\n' "${timestamp}"
    printf 'idle_since=%s\n' "${idle_since}"
    printf 'idle_seconds=%s\n' "${idle_seconds}"
    printf 'process_count=%s\n' "${process_count}"
    printf 'worker_pid=%s\n' "${worker_pid}"
  } >"${temp_file}"
  mv -f -- "${temp_file}" "${STATE_FILE}"
}

log_event() {
  if [[ "${STK_SERVICE_MODE:-}" == 'direct' ]]; then
    printf '[%s] %s\n' "$(date '+%F %T')" "$*"
  elif (( STK_GDS_LOG_FD_OPEN )); then
    printf '[%s] %s\n' "$(date '+%F %T')" "$*" >&9
  else
    printf '[%s] %s\n' "$(date '+%F %T')" "$*" >>"${LOG_FILE}"
  fi
}

query_compute_processes() {
  local processes scope uuid
  processes="$(nvidia-smi \
    --query-compute-apps=pid,gpu_uuid,process_name,used_memory \
    --format=csv,noheader,nounits 2>/dev/null)" || return 1
  scope="$(process_scope)"
  if [[ "${scope}" == 'all' ]]; then
    printf '%s\n' "${processes}"
    return 0
  fi
  uuid="$(target_gpu_uuid)" || return 1
  [[ -n "${uuid}" ]] || return 1
  filter_processes_for_gpu "${processes}" "${uuid}"
}

filter_processes_for_gpu() {
  local processes="$1" uuid="$2"
  awk -F, -v expected="${uuid}" '
    {
      current=$2
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", current)
      if (current == expected) print
    }
  ' <<<"${processes}"
}

filter_external_processes() {
  local processes="$1" worker_pid='' gpu_pid='' line pid
  worker_pid="$(managed_worker_pid 2>/dev/null || true)"
  if [[ -n "${worker_pid}" ]]; then
    gpu_pid="$(managed_worker_gpu_pid 2>/dev/null || true)"
  fi
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    pid="${line%%,*}"
    pid="${pid#"${pid%%[![:space:]]*}"}"
    pid="${pid%"${pid##*[![:space:]]}"}"
    if [[ -n "${worker_pid}" && \
      ( "${pid}" == "${worker_pid}" || "${pid}" == "${gpu_pid}" ) ]]; then
      continue
    fi
    printf '%s\n' "${line}"
  done <<<"${processes}"
}

query_gpu_summary() {
  nvidia-smi \
    --query-gpu=index,name,utilization.gpu,memory.used,memory.total \
    --format=csv,noheader,nounits 2>/dev/null
}

run_monitor() {
  local pid_file_tmp="${PID_FILE}.$$"
  local acquired_global_lock=0
  local interval threshold now idle_since=0 idle_seconds=0 process_count=0
  local processes='' external_processes='' mode='' previous_mode='' cleaned=0
  local worker_pid='' saved_mode saved_idle_since

  secure_runtime_paths || return 1
  interval="$(probe_interval)"
  threshold="$(idle_threshold)"
  saved_mode="$(state_value mode)"
  saved_idle_since="$(state_value idle_since)"
  now="$(date +%s)"
  case "${saved_mode}" in
    idle|idle-threshold|reserved|worker-error)
      if [[ "${saved_idle_since}" =~ ^[1-9][0-9]*$ ]] && \
        (( saved_idle_since <= now )); then
        idle_since="${saved_idle_since}"
      fi
      ;;
  esac
  if [[ "${STK_SERVICE_MODE:-}" != 'direct' ]]; then
    if [[ "${STK_LOG_START_PROTECTED:-0}" != '1' && "${STK_GLOBAL_LOCK_HELD:-0}" != '1' ]]; then
      stk_lock_acquire || return 1
      acquired_global_lock=1
    fi
    if ! exec 9>>"${LOG_FILE}"; then
      (( acquired_global_lock == 0 )) || stk_lock_release
      return 1
    fi
    STK_GDS_LOG_FD_OPEN=1
  fi
  if managed_worker_pid >/dev/null 2>&1; then
    if ! stop_worker 'monitor startup cleanup'; then
      (( acquired_global_lock == 0 )) || stk_lock_release
      return 1
    fi
  else
    remove_stale_worker_files
  fi
  printf '%s\n' "$$" >"${pid_file_tmp}"
  mv -f -- "${pid_file_tmp}" "${PID_FILE}"
  (( acquired_global_lock == 0 )) || stk_lock_release

  cleanup_monitor() {
    if (( cleaned == 0 )); then
      cleaned=1
      stop_worker 'monitor stopped' || true
      rm -f -- "${PID_FILE}"
      write_state 'stopped' "$(date +%s)" 0 0 0 0
      log_event 'monitor stopped'
    fi
  }
  trap cleanup_monitor EXIT
  trap 'exit 0' HUP INT TERM

  log_event "monitor started (probe=${interval}s, idle-threshold=${threshold}s, device=$(gpu_device), scope=$(process_scope))"
  while true; do
    now="$(date +%s)"
    if processes="$(query_compute_processes)"; then
      external_processes="$(filter_external_processes "${processes}")"
      process_count="$(printf '%s\n' "${external_processes}" | awk 'NF { count++ } END { print count+0 }')"
      if (( process_count > 0 )); then
        if managed_worker_pid >/dev/null 2>&1; then
          stop_worker 'other GPU compute process detected' || true
        fi
        mode='busy'
        idle_since=0
        idle_seconds=0
      else
        worker_pid="$(managed_worker_pid 2>/dev/null || true)"
        if [[ -n "${worker_pid}" ]]; then
          mode='reserved'
        else
          remove_stale_worker_files
          if (( idle_since == 0 )); then
            idle_since="${now}"
          fi
          idle_seconds=$((now - idle_since))
          if (( idle_seconds >= threshold )); then
            if start_worker; then
              worker_pid="$(managed_worker_pid 2>/dev/null || true)"
              mode='reserved'
            else
              worker_pid=''
              mode='worker-error'
            fi
          else
            mode='idle'
          fi
        fi
      fi
    else
      stop_worker 'GPU probe unavailable' || true
      mode='unavailable'
      idle_since=0
      idle_seconds=0
      process_count=0
    fi

    worker_pid="$(managed_worker_pid 2>/dev/null || true)"
    write_state "${mode}" "${now}" "${idle_since}" "${idle_seconds}" \
      "${process_count}" "${worker_pid:-0}"
    if [[ "${mode}" != "${previous_mode}" ]]; then
      case "${mode}" in
        busy) log_event "GPU activity detected (${process_count} compute process(es))" ;;
        idle) log_event 'GPU became idle; countdown started' ;;
        reserved) log_event "GPU idle threshold reached; scheduler is active (PID ${worker_pid})" ;;
        worker-error) log_event 'GPU idle threshold reached but scheduler failed to start' ;;
        unavailable) log_event 'nvidia-smi probe failed' ;;
      esac
      previous_mode="${mode}"
    fi
    sleep "${interval}" &
    wait $!
  done
}

remove_stale_files() {
  local pid=''
  if [[ -r "${PID_FILE}" ]]; then
    read -r pid <"${PID_FILE}" || true
    if ! pid_is_monitor "${pid}"; then
      rm -f -- "${PID_FILE}"
    fi
  fi
}

terminate_managed_pid() {
  local pid="$1"
  if ! pid_is_monitor "${pid}"; then
    remove_stale_files
    return 0
  fi
  kill -TERM "${pid}" 2>/dev/null || true
  for _ in {1..30}; do
    if ! pid_is_monitor "${pid}"; then
      rm -f -- "${PID_FILE}"
      return 0
    fi
    sleep 0.1
  done
  warn "monitor PID ${pid} 未响应 TERM，正在强制结束"
  kill -KILL "${pid}" 2>/dev/null || true
  for _ in {1..10}; do
    if ! pid_is_monitor "${pid}"; then
      rm -f -- "${PID_FILE}"
      return 0
    fi
    sleep 0.1
  done
  fail "无法结束 monitor PID ${pid}"
}

start_monitor_unlocked() {
  local tmux_command orphan_pid orphan_worker_pid
  require_command tmux || return 1
  require_command nvidia-smi || return 1
  secure_runtime_paths || return 1

  if ! nvidia-smi -L >/dev/null 2>&1; then
    fail 'nvidia-smi 无法访问 GPU，请检查驱动和设备权限'
    return 1
  fi
  if [[ -z "$(target_gpu_uuid)" ]]; then
    fail "配置的 GPU 设备 $(gpu_device) 不存在或无法访问"
    return 1
  fi
  ensure_core || return 1

  if session_exists; then
    if ! session_is_managed; then
      fail "tmux 会话 ${SESSION_NAME} 已被其他程序占用，未执行任何操作"
      return 1
    fi
    if is_running; then
      warn 'gds monitor 已经在运行，未重复创建 tmux 会话'
      return 0
    fi
    warn '检测到本项目的残留 tmux 会话，正在清理'
    tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null || true
  fi

  orphan_pid="$(managed_monitor_pid 2>/dev/null || true)"
  if [[ -n "${orphan_pid}" ]]; then
    if pid_is_direct_service "${orphan_pid}"; then
      warn "gds monitor 已由 supervisord 直接监管（PID ${orphan_pid}）"
      return 0
    fi
    fail "检测到脱离 tmux 的 monitor PID ${orphan_pid}；请先执行停止"
    return 1
  fi
  orphan_worker_pid="$(managed_worker_pid 2>/dev/null || true)"
  if [[ -n "${orphan_worker_pid}" ]]; then
    warn "检测到脱离 monitor 的 scheduler PID ${orphan_worker_pid}，正在恢复性停止"
    stop_worker 'orphan cleanup before monitor start' || return 1
  fi
  remove_stale_files
  mkdir -p "${WORK_DIR}"

  printf -v tmux_command 'env STK_LOG_START_PROTECTED=1 %q %q' "${PROJECT_DIR}/gds.sh" '__run_monitor'
  if ! tmux new-session -d -s "${SESSION_NAME}" "${tmux_command}" \; \
    set-option -t "${SESSION_NAME}" "${SESSION_OWNER_OPTION}" "${PROJECT_DIR}"; then
    if session_exists; then
      fail "tmux 会话 ${SESSION_NAME} 已由另一项操作创建"
    else
      fail '无法创建 tmux 会话'
    fi
    return 1
  fi

  for _ in {1..50}; do
    if ! session_exists || ! session_pane_alive; then
      break
    fi
    if is_running && [[ -r "${STATE_FILE}" ]]; then
      ok "gds monitor 已启动，tmux 会话：${SESSION_NAME}"
      return 0
    fi
    sleep 0.1
  done

  if session_exists && session_is_managed; then
    tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null || true
  fi
  remove_stale_files
  fail "gds monitor 启动后立即退出，诊断文件：${LOG_FILE}"
  tail -n 20 "${LOG_FILE}" 2>/dev/null || true
  return 1
}

start_monitor() {
  run_locked start_monitor_unlocked
}

stop_monitor_unlocked() {
  local pid='' worker_pid=''
  if session_exists; then
    if ! session_is_managed; then
      fail "tmux 会话 ${SESSION_NAME} 不属于本项目，拒绝停止"
      return 1
    fi
    pid="$(managed_monitor_pid 2>/dev/null || true)"
    worker_pid="$(managed_worker_pid 2>/dev/null || true)"
    if ! tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null; then
      fail '无法停止 tmux 会话'
      return 1
    fi
    if [[ -n "${pid}" ]]; then
      terminate_managed_pid "${pid}" || return 1
    else
      remove_stale_files
    fi
    if [[ -n "${worker_pid}" ]] && worker_pid_is_managed "${worker_pid}"; then
      terminate_worker_pid "${worker_pid}" || return 1
    else
      remove_stale_worker_files
    fi
    write_state 'stopped' "$(date +%s)" 0 0 0 0
    log_event 'monitor stopped by manager'
    ok 'gds monitor 已停止'
    return 0
  fi

  pid="$(managed_monitor_pid 2>/dev/null || true)"
  if [[ -n "${pid}" ]]; then
    if pid_is_direct_service "${pid}"; then
      delegate_direct_service_action stop
      return
    fi
    warn "检测到脱离 tmux 的 monitor PID ${pid}，正在恢复性停止"
    terminate_managed_pid "${pid}" || return 1
    stop_worker 'orphan monitor stopped' || return 1
    write_state 'stopped' "$(date +%s)" 0 0 0 0
    log_event 'orphan monitor stopped by manager'
    ok 'gds monitor 已停止'
    return 0
  fi
  remove_stale_files
  if managed_worker_pid >/dev/null 2>&1; then
    warn '检测到脱离 monitor 的 scheduler，正在恢复性停止'
    stop_worker 'monitor is not running' || return 1
  else
    remove_stale_worker_files
  fi
  write_state 'stopped' "$(date +%s)" 0 0 0 0
  warn 'gds monitor 当前未运行'
  return 0
}

stop_monitor() {
  run_locked stop_monitor_unlocked
}

restart_monitor_unlocked() {
  local saved_mode saved_idle_since saved_idle_seconds preserve_idle=0
  local direct_pid
  direct_pid="$(managed_monitor_pid 2>/dev/null || true)"
  if [[ -n "${direct_pid}" ]] && pid_is_direct_service "${direct_pid}"; then
    delegate_direct_service_action restart
    return
  fi
  saved_mode="$(state_value mode)"
  saved_idle_since="$(state_value idle_since)"
  saved_idle_seconds="$(state_value idle_seconds)"
  case "${saved_mode}" in
    idle|idle-threshold|reserved|worker-error)
      if [[ "${saved_idle_since}" =~ ^[1-9][0-9]*$ && \
        "${saved_idle_seconds}" =~ ^[0-9]+$ ]]; then
        preserve_idle=1
      fi
      ;;
  esac
  if session_exists || managed_monitor_pid >/dev/null 2>&1; then
    stop_monitor_unlocked || return 1
  fi
  if (( preserve_idle )); then
    write_state 'idle-threshold' "$(date +%s)" "${saved_idle_since}" \
      "${saved_idle_seconds}" 0 0
  fi
  start_monitor_unlocked
}

restart_monitor() {
  run_locked restart_monitor_unlocked
}

monitor_state_label() {
  case "${1:-unknown}" in
    busy) printf '检测到 GPU 任务' ;;
    idle) printf 'GPU 空闲，正在计时' ;;
    idle-threshold) printf '已达到空闲阈值' ;;
    reserved) printf 'scheduler 正在保留 GPU' ;;
    worker-error) printf 'scheduler 启动失败' ;;
    unavailable) printf 'GPU 探针不可用' ;;
    stopped) printf '监听已停止' ;;
    *) printf '等待首次探测' ;;
  esac
}

print_gpu_lines() {
  local summaries line index name utilization used total
  if ! summaries="$(query_gpu_summary)"; then
    printf '  %-16s %s\n' 'GPU' '无法读取 nvidia-smi'
    return
  fi
  if [[ -z "${summaries}" ]]; then
    printf '  %-16s %s\n' 'GPU' '未发现设备'
    return
  fi
  while IFS= read -r line; do
    IFS=',' read -r index name utilization used total <<<"${line}"
    index="${index# }"; index="${index% }"
    name="${name# }"; name="${name% }"
    utilization="${utilization# }"; utilization="${utilization% }"
    used="${used# }"; used="${used% }"
    total="${total# }"; total="${total% }"
    printf '  %-16s #%s %s · util %s%% · memory %s/%s MiB\n' \
      'GPU' "${index}" "${name}" "${utilization}" "${used}" "${total}"
  done <<<"${summaries}"
}

pause_menu() {
  if [[ -t 0 ]]; then
    printf '\n%s' "${DIM}按 Enter 返回菜单...${RESET}"
    read -r _ || true
  fi
}

draw_menu() {
  local selected="$1" state_color state_detail monitor idle_seconds process_count
  local index marker pid='' worker_pid='' worker_device worker_percent allocated_bytes
  local -a labels=(
    '启动监听'
    '停止监听'
    '重启监听'
    '查看最近日志'
    '退出'
  )

  if session_exists; then
    if ! session_is_managed; then
      state_color="${YELLOW}"
      state_detail='会话名冲突 · 未接管该会话'
    elif is_running; then
      pid="$(session_pid)"
      if monitor_state_is_fresh "${pid}"; then
        state_color="${GREEN}"
        state_detail="运行中 · PID ${pid}"
      else
        state_color="${YELLOW}"
        state_detail="异常 · monitor 状态已停止更新 · PID ${pid}"
      fi
    else
      state_color="${YELLOW}"
      state_detail='异常 · 残留 tmux 会话'
    fi
  else
    pid="$(managed_monitor_pid 2>/dev/null || true)"
    if [[ -n "${pid}" ]]; then
      if pid_is_direct_service "${pid}"; then
        if monitor_state_is_fresh "${pid}"; then
          state_color="${GREEN}"
          state_detail="运行中 · supervisord direct · PID ${pid}"
        else
          state_color="${YELLOW}"
          state_detail="异常 · monitor 状态已停止更新 · PID ${pid}"
        fi
      else
        state_color="${YELLOW}"
        state_detail="异常 · monitor 脱离 tmux · PID ${pid}"
      fi
    else
      state_color="${RED}"
      state_detail='已停止'
    fi
  fi
  monitor="$(state_value mode)"
  idle_seconds="$(state_value idle_seconds)"
  process_count="$(state_value process_count)"
  worker_pid="$(managed_worker_pid 2>/dev/null || true)"

  if [[ -t 1 ]]; then
    clear
  fi
  printf '%s%sGPU Dynamic Scheduler%s\n\n' "${BOLD}" "${CYAN}" "${RESET}"
  printf '  %-16s %s● %s%s\n' '服务状态' "${state_color}" "${state_detail}" "${RESET}"
  printf '  %-16s %s\n' '探测状态' "$(monitor_state_label "${monitor}")"
  printf '  %-16s %s\n' '连续空闲' "$(format_duration "${idle_seconds:-0}") / $(format_duration "$(idle_threshold)")"
  printf '  %-16s %s\n' '计算进程' "${process_count:-0}"
  if [[ -n "${worker_pid}" ]]; then
    worker_device="$(worker_state_value device)"
    worker_percent="$(worker_state_value memory_percent)"
    allocated_bytes="$(worker_state_value allocated_bytes)"
    printf '  %-16s %s● 运行中 · PID %s · GPU %s · %s (%s%%)%s\n' \
      'scheduler' "${GREEN}" "${worker_pid}" "${worker_device:-?}" \
      "$(format_bytes "${allocated_bytes:-0}")" "${worker_percent:-?}" "${RESET}"
  else
    printf '  %-16s %s · GPU %s · 目标显存 %s%%\n' \
      'scheduler' '未运行' "$(gpu_device)" "$(memory_percent)"
  fi
  printf '  %-16s %ss\n' '探测间隔' "$(probe_interval)"
  printf '  %-16s %s\n' 'tmux 会话' "${SESSION_NAME}"
  print_gpu_lines
  printf '  %-16s %s%s%s\n\n' '项目目录' "${DIM}" "${PROJECT_DIR}" "${RESET}"

  for index in "${!labels[@]}"; do
    if (( index == selected )); then
      marker="${CYAN}›${RESET}"
      printf '  %s %s%s%s\n' "${marker}" "${BOLD}" "${labels[index]}" "${RESET}"
    else
      printf '    %s\n' "${labels[index]}"
    fi
  done
  printf '\n%s每 10s 自动刷新  •  ↑/↓ 选择  •  Enter 确认  •  r 立即刷新  •  q / Esc 退出%s\n' "${DIM}" "${RESET}"
}

show_recent_logs() {
  if [[ -r "${LOG_FILE}" ]]; then
    tail -n 30 "${LOG_FILE}"
  else
    warn '暂无日志'
  fi
}

interactive_menu() {
  local selected=0 item_count=5 key next_key final_key read_status
  if [[ ! -t 0 || ! -t 1 ]]; then
    fail '交互管理面板需要在终端中运行；自动化场景请使用命令模式'
    return 1
  fi
  while true; do
    draw_menu "${selected}"
    key=''
    if IFS= read -rsn1 -t "${DASHBOARD_REFRESH_SECONDS}" key; then
      :
    else
      read_status=$?
      if (( read_status > 128 )); then
        continue
      fi
      return 0
    fi
    case "${key}" in
      $'\x1b')
        next_key=''
        final_key=''
        IFS= read -rsn1 -t 0.15 next_key || true
        if [[ "${next_key}" == '[' ]]; then
          IFS= read -rsn1 -t 0.15 final_key || true
          case "${final_key}" in
            A) selected=$(( (selected - 1 + item_count) % item_count )) ;;
            B) selected=$(( (selected + 1) % item_count )) ;;
          esac
        elif [[ -z "${next_key}" ]]; then
          return 0
        fi
        ;;
      k) selected=$(( (selected - 1 + item_count) % item_count )) ;;
      j) selected=$(( (selected + 1) % item_count )) ;;
      r|R) ;;
      q|Q) return 0 ;;
      '')
        case "${selected}" in
          0) start_monitor; pause_menu ;;
          1) stop_monitor; pause_menu ;;
          2) restart_monitor; pause_menu ;;
          3) show_recent_logs; pause_menu ;;
          4) return 0 ;;
        esac
        ;;
    esac
  done
}

usage() {
  cat <<'EOF'
用法：gds [命令]

不带命令             打开交互管理面板
start                 启动 GPU 空闲监听
stop                  停止 GPU 空闲监听
restart               重启 GPU 空闲监听
logs                   显示最近日志
status [--quiet]       输出运行状态；--quiet 仅使用退出码
help                   显示帮助

达到空闲阈值后会启动透明的 scheduler CUDA worker；发现其他 GPU
计算任务时会立即释放显存并停止 worker，但 monitor 会继续监听。
EOF
}

prepare_service() {
  require_command nvidia-smi || return 1
  secure_runtime_paths || return 1
  nvidia-smi -L >/dev/null 2>&1 || { fail 'nvidia-smi 无法访问 GPU'; return 1; }
  [[ -n "$(target_gpu_uuid)" ]] || { fail "配置的 GPU 设备 $(gpu_device) 不存在"; return 1; }
  ensure_core
}

main() {
  case "${1:-menu}" in
    menu) interactive_menu ;;
    start) start_monitor ;;
    stop) stop_monitor ;;
    restart) restart_monitor ;;
    logs) show_recent_logs ;;
    status) show_status "${2:-}" ;;
    help|-h|--help) usage ;;
    __ensure_core) ensure_core_locked ;;
    __run_monitor) run_monitor ;;
    __prepare_service) run_global_operation prepare_service ;;
    __filter_gpu)
      [[ "${STK_TESTING:-0}" == '1' && -n "${2:-}" ]] || return 2
      filter_processes_for_gpu "$(cat)" "$2"
      ;;
    __config_value)
      [[ "${STK_TESTING:-0}" == '1' && -n "${2:-}" && -n "${3:-}" ]] || return 2
      STK_GDS_LOG_FD_OPEN="${4:-0}"
      config_value "$2" "$3"
      ;;
    *) fail "未知命令：$1"; usage; return 2 ;;
  esac
}

main "$@"
