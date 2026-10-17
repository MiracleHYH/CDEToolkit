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
# shellcheck disable=SC2034 # Read by lib/common.sh while it is sourced.
STK_ROOT="${TOOLKIT_DIR}"
# shellcheck source=../lib/common.sh
source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
# shellcheck source=../lib/python-runtime.sh
source "${TOOLKIT_DIR}/lib/python-runtime.sh"
CORE_DIR="${PROJECT_DIR}/core"
WORK_DIR="${PROJECT_DIR}/workdir"
CORE_BIN="${CORE_DIR}/mihomo"
CENTRAL_CONFIG="${SERVER_TOOLKIT_CONFIG:-${XDG_CONFIG_HOME:-${HOME}/.config}/server-toolkit/config.conf}"
CONFIGURED_PROXY_CONFIG="$(awk -F= '$1=="proxy_config_path" {sub(/^[^=]*=/,""); print; exit}' "${CENTRAL_CONFIG}" 2>/dev/null || true)"
CONFIG_FILE="${PROXY_CONFIG_FILE:-${CONFIGURED_PROXY_CONFIG:-${PROJECT_DIR}/config.yaml}}"
LOG_FILE="${WORK_DIR}/mihomo.log"
PID_FILE="${WORK_DIR}/mihomo.pid"
LOCK_DIR="${WORK_DIR}/.pm.lock"
SESSION_NAME="${PROXY_TMUX_SESSION:-proxy-mihomo}"
SESSION_OWNER_OPTION='@pm_project'
PROVIDER_NAME="proxy_provider"
CONFIGURED_RELEASE_PROXY="$(awk -F= '$1=="proxy_release_proxy" {sub(/^[^=]*=/,""); print; exit}' "${CENTRAL_CONFIG}" 2>/dev/null || true)"
GH_PROXY="${GH_PROXY:-${CONFIGURED_RELEASE_PROXY}}"
RELEASE_ASSET_PARSER="${PROJECT_DIR}/release_asset.py"
CORE_DECOMPRESSOR="${PROJECT_DIR}/decompress_core.py"

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
    fail "敏感配置必须是普通文件且不能是符号链接：${CONFIG_FILE}"
    return 1
  elif [[ -e "${CONFIG_FILE}" ]]; then
    chmod 0600 "${CONFIG_FILE}" || {
      fail "无法收紧配置文件权限：${CONFIG_FILE}"
      return 1
    }
  fi
  if [[ -L "${WORK_DIR}" || -L "${WORK_DIR}/providers" ]]; then
    fail 'proxy 运行目录和 providers 目录不能是符号链接'
    return 1
  fi
  mkdir -p -- "${WORK_DIR}" "${WORK_DIR}/providers"
  chmod 0700 "${WORK_DIR}" "${WORK_DIR}/providers" || {
    fail '无法收紧 proxy 运行目录权限'
    return 1
  }
  local sensitive
  for sensitive in "${LOG_FILE}" "${PID_FILE}" \
    "${WORK_DIR}/cache.db" "${WORK_DIR}/providers/${PROVIDER_NAME}.yaml"; do
    if [[ -L "${sensitive}" ]]; then
      fail "proxy 敏感运行文件不能是符号链接：${sensitive}"
      return 1
    fi
    [[ ! -e "${sensitive}" ]] || chmod 0600 "${sensitive}" || return 1
  done
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
      fail '另一项 pm 操作正在进行，请稍后重试'
      return 75
    fi
    warn '检测到异常遗留的操作锁，正在自动恢复'
    rm -f -- "${owner_file}"
    rmdir -- "${LOCK_DIR}" 2>/dev/null || {
      fail "无法清理操作锁：${LOCK_DIR}"
      return 1
    }
    if ! mkdir "${LOCK_DIR}" 2>/dev/null; then
      fail '另一项 pm 操作刚刚开始，请稍后重试'
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

session_exists() {
  has_command tmux && tmux has-session -t "=${SESSION_NAME}" 2>/dev/null
}

pid_is_managed_core() {
  local pid="$1"
  local executable argument has_config=1
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null || return 1
  executable="$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
  executable="${executable% (deleted)}"
  [[ "${executable}" == "${CORE_BIN}" || "${executable}" == "${CORE_BIN}.previous" ]] || return 1
  while IFS= read -r -d '' argument; do
    if [[ "${argument}" == "${CONFIG_FILE}" ]]; then
      has_config=0
      break
    fi
  done <"/proc/${pid}/cmdline"
  return "${has_config}"
}

session_pid() {
  tmux list-panes -t "=${SESSION_NAME}" -F '#{pane_pid}' 2>/dev/null | head -n 1
}

managed_core_pid() {
  local pid='' process
  if [[ -r "${PID_FILE}" ]]; then
    read -r pid <"${PID_FILE}" || true
    if pid_is_managed_core "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi
  if session_exists; then
    pid="$(session_pid)"
    if pid_is_managed_core "${pid}"; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi
  for process in /proc/[0-9]*; do
    pid="${process##*/}"
    if pid_is_managed_core "${pid}"; then
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
    pid_is_managed_core "${pid}"
    return
  fi
  return 1
}

mark_session_managed() {
  tmux set-option -t "${SESSION_NAME}" "${SESSION_OWNER_OPTION}" "${PROJECT_DIR}" >/dev/null
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
    pid_is_managed_core "${pid}"
    return
  fi
  pid="$(managed_core_pid 2>/dev/null || true)"
  [[ -n "${pid}" ]] && pid_is_direct_service "${pid}"
}

pid_is_direct_service() {
  local pid="$1"
  pid_is_managed_core "${pid}" || return 1
  tr '\0' '\n' <"/proc/${pid}/environ" 2>/dev/null | grep -Fqx 'STK_SERVICE_MODE=direct'
}

delegate_direct_service_action() {
  local action="$1"
  if [[ "${STK_DIRECT_DELEGATED:-0}" == '1' || ! -x "${TOOLKIT_DIR}/toolkit.sh" ]]; then
    fail "mihomo 由 supervisord 直接监管；请使用 stk module ${action} proxy"
    return 1
  fi
  info "mihomo 由 supervisord 直接监管，交由 stk module ${action} proxy"
  STK_DIRECT_DELEGATED=1 "${TOOLKIT_DIR}/toolkit.sh" module "${action}" proxy
}

proxy_state() {
  local pid
  if session_exists; then
    if ! session_is_managed; then
      printf 'conflict\n'
    elif is_running; then
      printf 'running\n'
    else
      printf 'unhealthy\n'
    fi
    return
  fi
  pid="$(managed_core_pid 2>/dev/null || true)"
  if [[ -n "${pid}" ]]; then
    if pid_is_direct_service "${pid}"; then printf 'running\n'; else printf 'orphan\n'; fi
  else
    printf 'stopped\n'
  fi
}

show_status() {
  local quiet="${1:-}" state
  state="$(proxy_state)"
  if [[ "${quiet}" != '--quiet' ]]; then
    printf '%s\n' "${state}"
  fi
  [[ "${state}" == 'running' ]]
}

show_endpoint() {
  local listener="${1:-}" port
  if [[ -z "${listener}" ]]; then
    fail '请指定 listener，例如：pm endpoint common'
    return 2
  fi
  port="$(listener_port "${listener}")"
  if [[ ! "${port}" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
    fail "listener ${listener} 不存在或端口无效"
    return 1
  fi
  printf '127.0.0.1:%s\n' "${port}"
}

provider_url() {
  awk -v provider="${PROVIDER_NAME}" '
    $0 ~ "^  " provider ":" { in_provider=1; next }
    in_provider && $0 ~ /^    url:[[:space:]]*/ {
      sub(/^    url:[[:space:]]*/, "")
      sub(/[[:space:]]+#.*$/, "")
      gsub(/^[[:space:]\047\042]+|[[:space:]\047\042]+$/, "")
      print
      exit
    }
    in_provider && $0 !~ /^    / && NF { exit }
  ' "${CONFIG_FILE}" 2>/dev/null
}

listener_port() {
  local listener="$1"
  awk -v listener="${listener}" '
    /^listeners:[[:space:]]*$/ { in_listeners=1; next }
    in_listeners && /^[^[:space:]]/ { exit }
    in_listeners && /^  - name:[[:space:]]*/ {
      name=$0
      sub(/^  - name:[[:space:]]*/, "", name)
      gsub(/^[[:space:]\047\042]+|[[:space:]\047\042]+$/, "", name)
      in_listener=(name == listener)
      next
    }
    in_listener && /^    port:[[:space:]]*/ {
      port=$0
      sub(/^    port:[[:space:]]*/, "", port)
      sub(/[[:space:]]+#.*$/, "", port)
      print port
      exit
    }
  ' "${CONFIG_FILE}" 2>/dev/null
}

installed_version() {
  if [[ -x "${CORE_BIN}" ]]; then
    "${CORE_BIN}" -v 2>/dev/null | head -n 1 || printf '未知版本'
  else
    printf '未安装'
  fi
}

check_provider_url() {
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    fail "找不到配置文件：${CONFIG_FILE}"
    return 1
  fi
  if [[ -z "$(provider_url)" ]]; then
    fail "proxy_provider 的 url 为空，请先编辑 ${CONFIG_FILE}"
    return 1
  fi
}

release_asset_info() {
  local release_json machine
  require_command curl || return 1

  info '获取 mihomo 最新稳定版本信息...' >&2
  release_json="$(curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 10 --max-time 30 --max-filesize 1048576 \
    -H 'Accept: application/vnd.github+json' \
    -H 'User-Agent: pm' \
    'https://api.github.com/repos/MetaCubeX/mihomo/releases/latest')" || {
      fail '无法获取 mihomo 版本信息，请检查到 api.github.com 的连接'
      return 1
    }

  machine="$(uname -m)"
  if ! printf '%s' "${release_json}" | "${STK_PYTHON}" "${RELEASE_ASSET_PARSER}" "${machine}"; then
    fail "无法从 mihomo 发布信息中选择适用于 ${machine} 的可信资源"
    return 1
  fi
}

update_core_unlocked() {
  local asset_info asset_url asset_digest expected_digest actual_digest
  local download_url archive_file binary_file backup_file
  require_command curl || return 1
  require_command sha256sum || return 1
  stk_python_runtime_resolve || {
    fail 'STK Python 私有环境不可用；请重新运行 STK 安装器修复'
    return 1
  }
  mkdir -p "${CORE_DIR}"

  asset_info="$(release_asset_info)" || return 1
  asset_url="$(printf '%s\n' "${asset_info}" | sed -n '1p')"
  asset_digest="$(printf '%s\n' "${asset_info}" | sed -n '2p')"
  expected_digest="${asset_digest#sha256:}"
  archive_file="$(mktemp "${CORE_DIR}/.mihomo.XXXXXX.gz")" || return 1
  binary_file="$(mktemp "${CORE_DIR}/.mihomo.XXXXXX")" || {
    rm -f -- "${archive_file}"
    return 1
  }

  if [[ -n "${GH_PROXY}" ]]; then
    download_url="${GH_PROXY%/}/${asset_url}"
    info "通过 ${GH_PROXY} 下载 core..."
    if ! curl -fL --proto '=https' --proto-redir '=https' --tlsv1.2 \
      --retry 3 --connect-timeout 15 --max-time 300 --max-filesize 134217728 --progress-bar \
      "${download_url}" -o "${archive_file}"; then
      warn 'GitHub 加速下载失败，正在回退到官方地址'
      if ! curl -fL --proto '=https' --proto-redir '=https' --tlsv1.2 \
        --retry 3 --connect-timeout 15 --max-time 300 --max-filesize 134217728 --progress-bar \
        "${asset_url}" -o "${archive_file}"; then
        rm -f -- "${archive_file}" "${binary_file}"
        fail 'mihomo core 下载失败'
        return 1
      fi
    fi
  elif ! curl -fL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --retry 3 --connect-timeout 15 --max-time 300 --max-filesize 134217728 --progress-bar \
    "${asset_url}" -o "${archive_file}"; then
    rm -f -- "${archive_file}" "${binary_file}"
    fail 'mihomo core 下载失败'
    return 1
  fi
  actual_digest="$(sha256sum "${archive_file}" | awk '{print $1}')"
  if [[ "${actual_digest}" != "${expected_digest}" ]]; then
    rm -f -- "${archive_file}" "${binary_file}"
    fail 'mihomo core 的 SHA-256 与 GitHub 官方摘要不一致'
    return 1
  fi
  if ! "${STK_PYTHON}" "${CORE_DECOMPRESSOR}" --input "${archive_file}" \
    --output "${binary_file}" --max-bytes 268435456; then
    rm -f -- "${archive_file}" "${binary_file}"
    fail '下载的 core 压缩包无效'
    return 1
  fi
  rm -f -- "${archive_file}"
  chmod 0755 "${binary_file}"
  if ! "${binary_file}" -v >/dev/null 2>&1; then
    rm -f -- "${binary_file}"
    fail '下载的 core 无法在当前服务器运行'
    return 1
  fi

  if [[ -f "${CORE_BIN}" ]] && cmp -s -- "${binary_file}" "${CORE_BIN}"; then
    rm -f -- "${binary_file}"
    ok "mihomo core 已是最新版本：$(installed_version)"
    return 0
  fi

  backup_file=''
  if [[ -f "${CORE_BIN}" ]]; then
    backup_file="${CORE_BIN}.previous"
    if ! mv -f -- "${CORE_BIN}" "${backup_file}"; then
      rm -f -- "${binary_file}"
      fail '无法备份当前 mihomo core'
      return 1
    fi
  fi
  if ! mv -f -- "${binary_file}" "${CORE_BIN}"; then
    [[ -z "${backup_file}" ]] || mv -f -- "${backup_file}" "${CORE_BIN}"
    fail '无法安装新的 mihomo core，已恢复原版本'
    return 1
  fi
  ok "mihomo core 已更新：$(installed_version)"
  if [[ -n "${backup_file}" ]]; then
    info "上一版本保留在 ${backup_file}"
  fi
}

update_core() {
  run_locked update_core_unlocked
}

ensure_core() {
  if [[ ! -x "${CORE_BIN}" ]]; then
    warn '尚未安装 mihomo core，将先自动下载'
    update_core_unlocked
  fi
}

validate_config() {
  if ! "${CORE_BIN}" -t -d "${WORK_DIR}" -f "${CONFIG_FILE}" >/dev/null; then
    fail "mihomo 配置校验失败：${CONFIG_FILE}"
    return 1
  fi
}

validate_config_command() {
  [[ -x "${CORE_BIN}" ]] || { fail 'mihomo core 尚未安装，无法执行完整配置校验'; return 1; }
  secure_runtime_paths || return 1
  check_provider_url || return 1
  validate_config && ok "mihomo 配置有效：${CONFIG_FILE}"
}

remove_stale_pid_file() {
  local pid=''
  if [[ -r "${PID_FILE}" ]]; then
    read -r pid <"${PID_FILE}" || true
    if ! pid_is_managed_core "${pid}"; then
      rm -f -- "${PID_FILE}"
    fi
  fi
}

terminate_managed_pid() {
  local pid="$1"
  if ! pid_is_managed_core "${pid}"; then
    remove_stale_pid_file
    return 0
  fi
  kill -TERM "${pid}" 2>/dev/null || true
  for _ in {1..30}; do
    if ! pid_is_managed_core "${pid}"; then
      rm -f -- "${PID_FILE}"
      return 0
    fi
    sleep 0.1
  done
  warn "mihomo PID ${pid} 未响应 TERM，正在强制结束"
  kill -KILL "${pid}" 2>/dev/null || true
  for _ in {1..10}; do
    if ! pid_is_managed_core "${pid}"; then
      rm -f -- "${PID_FILE}"
      return 0
    fi
    sleep 0.1
  done
  fail "无法结束 mihomo PID ${pid}"
}

start_proxy_unlocked() {
  local tmux_command orphan_pid
  require_command tmux || return 1

  if session_exists; then
    if ! session_is_managed; then
      fail "tmux 会话 ${SESSION_NAME} 已被其他程序占用，未执行任何操作"
      return 1
    fi
    mark_session_managed || true
    if is_running; then
      warn 'mihomo 已经在运行，未重复创建 tmux 会话'
      return 0
    fi
    warn '检测到本项目的残留 tmux 会话，正在清理'
    tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null || true
  fi

  orphan_pid="$(managed_core_pid 2>/dev/null || true)"
  if [[ -n "${orphan_pid}" ]]; then
    if pid_is_direct_service "${orphan_pid}"; then
      warn "mihomo 已由 supervisord 直接监管（PID ${orphan_pid}）"
      return 0
    fi
    fail "检测到脱离 tmux 的 mihomo PID ${orphan_pid}；请先执行停止"
    return 1
  fi
  remove_stale_pid_file

  secure_runtime_paths || return 1
  check_provider_url || return 1
  ensure_core || return 1
  mkdir -p "${WORK_DIR}" "${WORK_DIR}/providers"
  validate_config || return 1

  printf -v tmux_command 'env STK_LOG_START_PROTECTED=1 %q %q' "${PROJECT_DIR}/pm.sh" '__run_core'
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
    if is_running; then
      sleep 0.5
      if is_running; then
        ok "mihomo 已启动，tmux 会话：${SESSION_NAME}"
        return 0
      fi
    fi
    sleep 0.2
  done

  if session_exists && session_is_managed; then
    tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null || true
  fi
  remove_stale_pid_file
  fail "mihomo 启动后立即退出，诊断文件：${LOG_FILE}"
  tail -n 20 "${LOG_FILE}" 2>/dev/null || true
  return 1
}

start_proxy() {
  run_locked start_proxy_unlocked
}

stop_proxy_unlocked() {
  local pid=''
  if session_exists; then
    if ! session_is_managed; then
      fail "tmux 会话 ${SESSION_NAME} 不属于本项目，拒绝停止"
      return 1
    fi
    pid="$(managed_core_pid 2>/dev/null || true)"
    if ! tmux kill-session -t "=${SESSION_NAME}" 2>/dev/null; then
      fail '无法停止 tmux 会话'
      return 1
    fi
    if [[ -n "${pid}" ]]; then
      terminate_managed_pid "${pid}" || return 1
    else
      remove_stale_pid_file
    fi
    ok 'mihomo 已停止'
    return 0
  fi

  pid="$(managed_core_pid 2>/dev/null || true)"
  if [[ -n "${pid}" ]]; then
    if pid_is_direct_service "${pid}"; then
      delegate_direct_service_action stop
      return
    fi
    warn "检测到脱离 tmux 的 mihomo PID ${pid}，正在恢复性停止"
    terminate_managed_pid "${pid}" || return 1
    ok 'mihomo 已停止'
    return 0
  fi
  remove_stale_pid_file
  warn 'mihomo 当前未运行'
  return 0
}

stop_proxy() {
  run_locked stop_proxy_unlocked
}

restart_proxy_unlocked() {
  local pid
  pid="$(managed_core_pid 2>/dev/null || true)"
  if [[ -n "${pid}" ]] && pid_is_direct_service "${pid}"; then
    delegate_direct_service_action restart
    return
  fi
  if session_exists || managed_core_pid >/dev/null 2>&1; then
    stop_proxy_unlocked || return 1
  fi
  start_proxy_unlocked
}

restart_proxy() {
  run_locked restart_proxy_unlocked
}

update_provider_unlocked() {
  local provider_cache="${WORK_DIR}/providers/${PROVIDER_NAME}.yaml"
  check_provider_url || return 1
  if ! is_running; then
    warn 'mihomo 当前未运行'
    return 1
  fi
  if [[ -e "${provider_cache}" ]]; then
    if ! touch -t 197001010000 -- "${provider_cache}"; then
      fail "无法标记 provider 缓存过期：${provider_cache}"
      return 1
    fi
  fi
  info "正在重启 mihomo 并刷新 ${PROVIDER_NAME}..."
  if [[ -n "$(managed_core_pid 2>/dev/null || true)" ]] &&
     pid_is_direct_service "$(managed_core_pid 2>/dev/null || true)"; then
    delegate_direct_service_action restart || return 1
  else
    restart_proxy_unlocked || return 1
  fi
  ok 'proxy provider 已触发更新'
}

update_provider() {
  run_locked update_provider_unlocked
}

run_core() {
  local pid_file_tmp="${PID_FILE}.$$" acquired_global_lock=0
  secure_runtime_paths || return 1
  printf '%s\n' "$$" >"${pid_file_tmp}"
  chmod 0600 "${pid_file_tmp}"
  mv -f -- "${pid_file_tmp}" "${PID_FILE}"
  if [[ "${STK_SERVICE_MODE:-}" == 'direct' ]]; then
    printf '\n[%s] starting mihomo\n' "$(date '+%F %T')"
    exec "${CORE_BIN}" -d "${WORK_DIR}" -f "${CONFIG_FILE}"
  fi
  if [[ "${STK_LOG_START_PROTECTED:-0}" != '1' && "${STK_GLOBAL_LOCK_HELD:-0}" != '1' ]]; then
    stk_lock_acquire || { rm -f -- "${PID_FILE}"; return 1; }
    acquired_global_lock=1
  fi
  exec 9>>"${LOG_FILE}" || {
    (( acquired_global_lock == 0 )) || stk_lock_release
    rm -f -- "${PID_FILE}"
    return 1
  }
  (( acquired_global_lock == 0 )) || stk_lock_release
  printf '\n[%s] starting mihomo\n' "$(date '+%F %T')" >&9
  exec "${CORE_BIN}" -d "${WORK_DIR}" -f "${CONFIG_FILE}" >&9 2>&1
}

prepare_service() {
  secure_runtime_paths || return 1
  check_provider_url || return 1
  ensure_core || return 1
  validate_config
}

pause_menu() {
  if [[ -t 0 ]]; then
    printf '\n%s' "${DIM}按 Enter 返回菜单...${RESET}"
    read -r _ || true
  fi
}

draw_menu() {
  local selected="$1"
  local state state_color state_detail index marker
  local pid='' provider core_version listener listener_port_value
  local listener_lines=''
  local -a labels=(
    '启动代理'
    '停止代理'
    '重启代理'
    '更新 proxy provider'
    '更新 mihomo core'
    '退出'
  )
  if session_exists; then
    if ! session_is_managed; then
      state='会话名冲突'
      state_color="${YELLOW}"
      state_detail="${state} · 未接管该会话"
    elif is_running; then
      pid="$(session_pid)"
      state='运行中'
      state_color="${GREEN}"
      state_detail="${state} · PID ${pid:-未知}"
    else
      state='异常'
      state_color="${YELLOW}"
      state_detail="${state} · 残留 tmux 会话"
    fi
  else
    pid="$(managed_core_pid 2>/dev/null || true)"
    if [[ -n "${pid}" ]]; then
      if pid_is_direct_service "${pid}"; then
        state='运行中'
        state_color="${GREEN}"
        state_detail="${state} · supervisord direct · PID ${pid}"
      else
        state='异常'
        state_color="${YELLOW}"
        state_detail="${state} · core 脱离 tmux · PID ${pid}"
      fi
    else
      state='已停止'
      state_color="${RED}"
      state_detail="${state}"
    fi
  fi
  while IFS= read -r listener; do
    [[ -n "${listener}" ]] || continue
    listener_port_value="$(listener_port "${listener}")"
    listener_lines+="$(printf '  %-14s %s\n' "${listener} 代理" "127.0.0.1:${listener_port_value:-未配置} (HTTP / SOCKS5)")"
    listener_lines+=$'\n'
  done < <(awk '/^listeners:/{inside=1;next} inside && /^[^[:space:]]/{exit} inside && /^  - name:/{sub(/^  - name:[[:space:]]*/,""); gsub(/[\047\042]/,""); print}' "${CONFIG_FILE}" 2>/dev/null)
  provider="$(provider_url)"
  core_version="$(installed_version)"
  if [[ "${core_version}" == 'Mihomo Meta '* ]]; then
    core_version="$(printf '%s\n' "${core_version}" | awk '{print $3}')"
  fi
  if [[ -t 1 ]]; then
    clear
  fi
  printf '%s%sProxy Dashboard%s\n\n' "${BOLD}" "${CYAN}" "${RESET}"
  printf '  %-14s %s● %s%s\n' '服务状态' "${state_color}" "${state_detail}" "${RESET}"
  printf '%s' "${listener_lines}"
  printf '  %-14s %s\n' 'tmux 会话' "${SESSION_NAME}"
  printf '  %-14s %s\n' 'mihomo core' "${core_version}"
  if [[ -n "${provider}" ]]; then
    printf '  %-14s %s%s%s\n' 'provider' "${GREEN}" '已配置' "${RESET}"
  else
    printf '  %-14s %s%s%s\n' 'provider' "${YELLOW}" '未配置' "${RESET}"
  fi
  printf '  %-14s %s%s%s\n\n' '项目目录' "${DIM}" "${PROJECT_DIR}" "${RESET}"
  for index in "${!labels[@]}"; do
    if (( index == selected )); then
      marker="${CYAN}›${RESET}"
      printf '  %s %s%s%s\n' "${marker}" "${BOLD}" "${labels[index]}" "${RESET}"
    else
      printf '    %s\n' "${labels[index]}"
    fi
  done
  printf '\n%s↑/↓ 选择  •  Enter 确认  •  q / Esc 退出%s\n' "${DIM}" "${RESET}"
}

interactive_menu() {
  local selected=0
  local item_count=6
  local key next_key final_key

  if [[ ! -t 0 || ! -t 1 ]]; then
    fail '交互管理面板需要在终端中运行；自动化场景请使用命令模式'
    return 1
  fi

  while true; do
    draw_menu "${selected}"
    if ! IFS= read -rsn1 key; then
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
      q|Q) return 0 ;;
      '')
        case "${selected}" in
          0) start_proxy; pause_menu ;;
          1) stop_proxy; pause_menu ;;
          2) restart_proxy; pause_menu ;;
          3) update_provider; pause_menu ;;
          4)
            if is_running; then
              warn 'core 更新后需重启才会生效'
            fi
            update_core
            pause_menu
            ;;
          5) return 0 ;;
        esac
        ;;
    esac
  done
}

usage() {
  cat <<'EOF'
用法：pm [命令]

不带命令             打开交互管理面板
start                 启动 mihomo
stop                  停止 mihomo
restart               重启 mihomo
update-provider       更新 proxy provider
update-core           下载/更新 mihomo core
status [--quiet]      输出运行状态；--quiet 仅使用退出码
endpoint LISTENER     输出 listener 的 host:port
config-path           输出当前私有 YAML 路径
help                  显示帮助
EOF
}

main() {
  case "${1:-menu}" in
    menu) interactive_menu ;;
    start) start_proxy ;;
    stop) stop_proxy ;;
    restart) restart_proxy ;;
    update-provider|provider) update_provider ;;
    validate) validate_config_command ;;
    update-core|core) update_core ;;
    status) show_status "${2:-}" ;;
    endpoint) show_endpoint "${2:-}" ;;
    config-path) printf '%s\n' "${CONFIG_FILE}" ;;
    help|-h|--help) usage ;;
    __run_core) run_core ;;
    __prepare_service) run_global_operation prepare_service ;;
    *) fail "未知命令：$1"; usage; return 2 ;;
  esac
}

main "$@"
