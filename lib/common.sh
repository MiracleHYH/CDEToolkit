#!/usr/bin/env bash

if [[ -z "${STK_ROOT:-}" ]]; then
  STK_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
fi

STK_USER_HOME="${HOME:?HOME 未设置}"
STK_PLATFORM_LOCAL_ROOT=''
STK_PLATFORM_PROJECTS_ROOT=''
STK_PLATFORM_DATA_ROOT=''
_stk_platform_file="${SERVER_TOOLKIT_PLATFORM_FILE:-${STK_ROOT}/platforms/zenith-dev.conf}"
if [[ "${SERVER_TOOLKIT_PLATFORM:-auto}" != 'generic' && -r "${_stk_platform_file}" && ! -L "${_stk_platform_file}" ]]; then
  _stk_platform_local="$(awk -F= '$1 == "local_root" { sub(/^[^=]*=/, ""); print; exit }' "${_stk_platform_file}")"
  if [[ -n "${SERVER_TOOLKIT_PLATFORM_FILE:-}" || "${SERVER_TOOLKIT_PLATFORM:-auto}" == 'zenith-dev' ||
        "${STK_ROOT}" == "${_stk_platform_local}/server-toolkit" ]]; then
    if ! awk -F= '
      /^#/ || /^$/ { next }
      NF != 2 || $1 !~ /^(schema_version|local_root|projects_root|data_root)$/ || seen[$1]++ { exit 1 }
      $1 == "schema_version" && $2 != "1" { exit 1 }
      $1 != "schema_version" && ($2 !~ /^\// || $2 == "/" || $2 ~ /[[:space:]]|\/\.\.?\/|\/\// || $2 ~ /\/\.\.?$/) { exit 1 }
      END { if (length(seen) != 4) exit 1 }
    ' "${_stk_platform_file}"; then
      printf '✗ 平台路径配置无效：%s\n' "${_stk_platform_file}" >&2
      return 1
    fi
    STK_PLATFORM_LOCAL_ROOT="${_stk_platform_local}"
    STK_PLATFORM_PROJECTS_ROOT="$(awk -F= '$1 == "projects_root" { print $2; exit }' "${_stk_platform_file}")"
    STK_PLATFORM_DATA_ROOT="$(awk -F= '$1 == "data_root" { print $2; exit }' "${_stk_platform_file}")"
  fi
fi
if [[ ( -n "${SERVER_TOOLKIT_PLATFORM_FILE:-}" || "${SERVER_TOOLKIT_PLATFORM:-auto}" == 'zenith-dev' ) &&
      -z "${STK_PLATFORM_LOCAL_ROOT}" ]]; then
  printf '✗ 无法读取平台路径配置：%s\n' "${_stk_platform_file}" >&2
  return 1
fi
STK_INSTALL_METADATA_FILE="${STK_ROOT}/config/install.conf"
_stk_distribution_value() {
  local key="$1"
  [[ -r "${STK_INSTALL_METADATA_FILE}" && ! -L "${STK_INSTALL_METADATA_FILE}" ]] || return 0
  awk -F= -v key="${key}" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' \
    "${STK_INSTALL_METADATA_FILE}" 2>/dev/null || true
}
_stk_dist_config_dir="$(_stk_distribution_value config_dir)"
_stk_dist_state_dir="$(_stk_distribution_value state_dir)"
case "${_stk_dist_config_dir}" in /|*'/../'*|*'/./'*|*'//'*) _stk_dist_config_dir='' ;; /*) ;; *) _stk_dist_config_dir='' ;; esac
case "${_stk_dist_state_dir}" in /|*'/../'*|*'/./'*|*'//'*) _stk_dist_state_dir='' ;; /*) ;; *) _stk_dist_state_dir='' ;; esac
_stk_default_config_dir="${XDG_CONFIG_HOME:-${STK_USER_HOME}/.config}/server-toolkit"
_stk_default_state_dir="${STK_USER_HOME}/.local/state/server-toolkit"
if [[ -n "${STK_PLATFORM_LOCAL_ROOT}" ]]; then
  _stk_default_config_dir="${STK_PLATFORM_LOCAL_ROOT}/config/server-toolkit"
  _stk_default_state_dir="${STK_PLATFORM_LOCAL_ROOT}/state/server-toolkit"
fi
SERVER_TOOLKIT_CONFIG_DIR="${SERVER_TOOLKIT_CONFIG_DIR:-${_stk_dist_config_dir:-${_stk_default_config_dir}}}"
SERVER_TOOLKIT_CONFIG="${SERVER_TOOLKIT_CONFIG:-${SERVER_TOOLKIT_CONFIG_DIR}/config.conf}"
SERVER_TOOLKIT_STATE_DIR="${SERVER_TOOLKIT_STATE_DIR:-${_stk_dist_state_dir:-${_stk_default_state_dir}}}"
export SERVER_TOOLKIT_CONFIG_DIR SERVER_TOOLKIT_CONFIG SERVER_TOOLKIT_STATE_DIR
# shellcheck disable=SC2034 # Used by libraries sourced after common.sh.
STK_USER_STATE_DIR="${SERVER_TOOLKIT_STATE_DIR}"
STK_PROJECT_STATE_DIR="${SERVER_TOOLKIT_PROJECT_STATE_DIR:-${STK_ROOT}/workdir}"
STK_INSTALL_STATE_FILE="${STK_PROJECT_STATE_DIR}/install.conf"
STK_BASHRC_FILE="${STK_BASHRC_FILE:-${STK_USER_HOME}/.bashrc}"
# shellcheck disable=SC2034 # Shared record schema consumed by sourced feature libraries.
STK_STATE_SCHEMA_VERSION=1
STK_STATE_META_SCHEMA_VERSION=2
STK_STATE_META_FILE="${STK_USER_STATE_DIR}/state.conf"
STK_GLOBAL_LOCK_DIR="${STK_GLOBAL_LOCK_DIR:-${STK_ROOT}.toolkit.lock}"
STK_INSTALL_LOCK_DIR="${STK_ROOT}.install.lock"

stk_info() { printf '› %s\n' "$*"; }
stk_ok() { printf '✓ %s\n' "$*"; }
stk_warn() { printf '! %s\n' "$*" >&2; }
stk_fail() { printf '✗ %s\n' "$*" >&2; return 1; }

stk_require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    stk_fail "缺少依赖：$1"
    return 1
  fi
}

stk_state_value() {
  local file="$1" key="$2"
  awk -F= -v key="${key}" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' \
    "${file}" 2>/dev/null || true
}

stk_atomic_write() {
  local target="$1" mode="${2:-0600}" directory temp_file
  directory="$(dirname -- "${target}")"
  if [[ -L "${target}" || ( -e "${target}" && ! -f "${target}" ) ]]; then
    stk_fail "${target} 必须是普通非符号链接文件，拒绝写入"
    return 1
  fi
  mkdir -p -- "${directory}" || return 1
  temp_file="$(mktemp "${directory}/.$(basename -- "${target}").XXXXXX")" || return 1
  chmod "${mode}" "${temp_file}" || { rm -f -- "${temp_file}"; return 1; }
  if ! cat >"${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  if ! mv -f -- "${temp_file}" "${target}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
}

stk_prepare_private_directory() {
  local directory="$1"
  stk_validate_absolute_path "${directory}" || return 1
  if [[ -L "${directory}" || ( -e "${directory}" && ! -d "${directory}" ) ]]; then
    stk_fail "私有目录必须是普通目录且不能是符号链接：${directory}"
    return 1
  fi
  mkdir -p -- "${directory}" || return 1
  chmod 0700 "${directory}"
}

stk_module_state_valid() {
  local file="$1"
  [[ -f "${file}" && ! -L "${file}" && "$(stk_state_value "${file}" schema_version)" == '1' ]]
}

stk_module_state_prepare() {
  local file="$1"
  stk_prepare_state_root || return 1
  stk_prepare_private_directory "$(dirname -- "${file}")" || return 1
  if [[ -L "${file}" || ( -e "${file}" && ! -f "${file}" ) ]]; then
    stk_fail "模块状态必须是普通文件且不能是符号链接：${file}"
    return 1
  fi
}

stk_module_state_write() {
  local file="$1" record
  shift
  for record in "$@"; do
    if [[ "${record}" == *[[:cntrl:]]* ]]; then
      stk_fail '模块状态值不能包含控制字符'
      return 1
    fi
  done
  stk_module_state_prepare "${file}" || return 1
  {
    printf 'schema_version=1\n'
    for record in "$@"; do
      printf '%s\n' "${record}"
    done
  } | stk_atomic_write "${file}" 0600
}

stk_prepare_state_root() {
  local toolkit_version='unknown'
  stk_prepare_private_directory "${STK_USER_STATE_DIR}" || return 1
  if [[ -e "${STK_STATE_META_FILE}" && ! -f "${STK_STATE_META_FILE}" ]]; then
    stk_fail "状态元数据不是普通文件：${STK_STATE_META_FILE}"
    return 1
  fi
  if [[ -e "${STK_STATE_META_FILE}" ]] && ! stk_state_schema_valid "${STK_STATE_META_FILE}"; then
    stk_fail "状态元数据版本不受支持：${STK_STATE_META_FILE}"
    return 1
  fi
  [[ -r "${STK_ROOT}/VERSION" ]] && toolkit_version="$(head -n 1 "${STK_ROOT}/VERSION")"
  printf 'schema_version=%s\ntoolkit_version=%s\n' \
    "${STK_STATE_META_SCHEMA_VERSION}" "${toolkit_version}" |
    stk_atomic_write "${STK_STATE_META_FILE}" 0600
}

stk_state_schema_valid() {
  local file="$1" version
  [[ -r "${file}" ]] || return 1
  version="$(stk_state_value "${file}" 'schema_version')"
  [[ "${version}" == "${STK_STATE_META_SCHEMA_VERSION}" ]]
}

stk_process_start_marker() {
  local pid="$1"
  [[ "${pid}" =~ ^[0-9]+$ && -r "/proc/${pid}/stat" ]] || return 1
  awk '{ sub(/^.*\) /, ""); print $20; exit }' "/proc/${pid}/stat" 2>/dev/null
}

stk_lock_owner_is_live() {
  local owner_pid="$1" owner_start="$2" actual_start
  [[ "${owner_pid}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${owner_pid}" 2>/dev/null || return 1
  # Old lock records did not contain a process start marker. Treat a live PID
  # conservatively until that process exits.
  [[ -n "${owner_start}" ]] || return 0
  actual_start="$(stk_process_start_marker "${owner_pid}" 2>/dev/null || true)"
  [[ -n "${actual_start}" && "${actual_start}" == "${owner_start}" ]]
}

stk_lock_acquire() {
  local owner_pid owner_start process_start lock_parent attempt=0
  if [[ "${STK_INSTALL_LOCK_HELD:-0}" != '1' && -e "${STK_INSTALL_LOCK_DIR}" ]]; then
    stk_fail "Release 安装/升级正在运行：${STK_INSTALL_LOCK_DIR}"
    return 1
  fi
  lock_parent="$(dirname -- "${STK_GLOBAL_LOCK_DIR}")"
  stk_validate_absolute_path "${STK_GLOBAL_LOCK_DIR}" >/dev/null 2>&1 || return 1
  if [[ -L "${lock_parent}" || ( -e "${lock_parent}" && ! -d "${lock_parent}" ) ]]; then
    stk_fail "锁目录的父路径异常：${lock_parent}"
    return 1
  fi
  mkdir -p -- "${lock_parent}" || return 1
  while ! mkdir -- "${STK_GLOBAL_LOCK_DIR}" 2>/dev/null; do
    owner_pid="$(stk_state_value "${STK_GLOBAL_LOCK_DIR}/owner.conf" 'pid')"
    owner_start="$(stk_state_value "${STK_GLOBAL_LOCK_DIR}/owner.conf" 'process_start')"
    if stk_lock_owner_is_live "${owner_pid}" "${owner_start}"; then
      if (( attempt >= 50 )); then
        stk_fail "另一个 stk 写操作仍在运行（PID ${owner_pid}）"
        return 1
      fi
      sleep 0.1
      attempt=$((attempt + 1))
      continue
    fi
    if [[ -d "${STK_GLOBAL_LOCK_DIR}" && ! -L "${STK_GLOBAL_LOCK_DIR}" ]]; then
      rm -f -- "${STK_GLOBAL_LOCK_DIR}/owner.conf"
      rmdir -- "${STK_GLOBAL_LOCK_DIR}" 2>/dev/null || {
        stk_fail "无法清理过期锁：${STK_GLOBAL_LOCK_DIR}"
        return 1
      }
      continue
    fi
    stk_fail "锁路径异常：${STK_GLOBAL_LOCK_DIR}"
    return 1
  done
  process_start="$(stk_process_start_marker "$$" 2>/dev/null || true)"
  printf 'pid=%s\nprocess_start=%s\nstarted_at=%s\n' "$$" "${process_start}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" |
    stk_atomic_write "${STK_GLOBAL_LOCK_DIR}/owner.conf" 0600 || {
      rmdir -- "${STK_GLOBAL_LOCK_DIR}" 2>/dev/null || true
      return 1
    }
  if ! stk_prepare_state_root; then
    stk_lock_release
    return 1
  fi
}

stk_lock_release() {
  local owner_pid
  owner_pid="$(stk_state_value "${STK_GLOBAL_LOCK_DIR}/owner.conf" 'pid')"
  if [[ "${owner_pid}" == "$$" ]]; then
    rm -f -- "${STK_GLOBAL_LOCK_DIR}/owner.conf"
    rmdir -- "${STK_GLOBAL_LOCK_DIR}" 2>/dev/null || true
  fi
}

stk_with_global_lock() {
  local result
  stk_lock_acquire || return 1
  trap 'stk_lock_release' RETURN
  STK_GLOBAL_LOCK_HELD=1 "$@"
  result=$?
  trap - RETURN
  stk_lock_release
  return "${result}"
}

stk_with_optional_global_lock() {
  if [[ "${STK_CONFIG_LOCK_HELD:-0}" == '1' || "${STK_GLOBAL_LOCK_HELD:-0}" == '1' ]]; then
    "$@"
  else
    stk_with_global_lock "$@"
  fi
}

stk_default_bin_dir() {
  if [[ -n "${STK_PLATFORM_LOCAL_ROOT}" ]]; then
    printf '%s/bin\n' "${STK_PLATFORM_LOCAL_ROOT}"
  elif [[ ${EUID} -eq 0 ]]; then
    printf '/usr/local/bin\n'
  else
    printf '%s/.local/bin\n' "${STK_USER_HOME}"
  fi
}

stk_install_bin_dir() {
  local configured
  configured="$(_stk_distribution_value 'bin_dir')"
  [[ -n "${configured}" ]] || configured="$(stk_state_value "${STK_INSTALL_STATE_FILE}" 'bin_dir')"
  if [[ -n "${configured}" ]]; then
    printf '%s\n' "${configured}"
  else
    stk_default_bin_dir
  fi
}

stk_module_enabled() {
  local command_name="$1" manager="$2" candidate resolved configured_bin
  configured_bin="$(_stk_distribution_value 'bin_dir')"
  [[ -n "${configured_bin}" ]] || configured_bin="$(stk_state_value "${STK_INSTALL_STATE_FILE}" 'bin_dir')"
  candidate="$(stk_install_bin_dir)/${command_name}"
  if [[ -L "${candidate}" ]]; then
    resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
    [[ "${resolved}" == "${manager}" ]] && return 0
  fi
  [[ -z "${configured_bin}" ]] || return 1
  candidate="$(command -v "${command_name}" 2>/dev/null || true)"
  if [[ -n "${candidate}" ]]; then
    resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
    [[ "${resolved}" == "${manager}" ]]
    return
  fi
  return 1
}

stk_validate_absolute_path() {
  local path="$1"
  if [[ "${path}" != /* || "${path}" == '/' || "${path}" == *[[:cntrl:]]* ||
        "/${path#/}/" == *'/../'* || "/${path#/}/" == *'/./'* || "${path}" == *'//'* ]]; then
    stk_fail "路径必须是规范化、非根目录且不含控制字符的绝对路径：${path}"
    return 1
  fi
}

stk_file_marker_counts() {
  local file="$1" begin="$2" end="$3" begin_count end_count
  begin_count="$(grep -Fxc -- "${begin}" "${file}" 2>/dev/null || true)"
  end_count="$(grep -Fxc -- "${end}" "${file}" 2>/dev/null || true)"
  if [[ "${begin_count}" != "${end_count}" || "${begin_count}" -gt 1 ]]; then
    stk_fail "${file} 中的 server-toolkit 标记不完整，拒绝自动修改"
    return 1
  fi
  if ! awk -v begin="${begin}" -v end="${end}" '
    $0 == begin {
      if (inside || seen_begin) bad=1
      inside=1; seen_begin=1
    }
    $0 == end {
      if (!inside || seen_end) bad=1
      inside=0; seen_end=1
    }
    END {
      if (inside || seen_begin != seen_end) bad=1
      exit bad ? 1 : 0
    }
  ' "${file}"; then
    stk_fail "${file} 中的 server-toolkit 标记顺序异常，拒绝自动修改"
    return 1
  fi
}

stk_prepare_managed_file() {
  local file="$1" directory
  directory="$(dirname -- "${file}")"
  mkdir -p -- "${directory}"
  if [[ -L "${file}" ]]; then
    stk_fail "${file} 是符号链接；为避免替换链接，拒绝自动修改"
    return 1
  fi
  if [[ -e "${file}" && ! -f "${file}" ]]; then
    stk_fail "${file} 不是普通文件，拒绝自动修改"
    return 1
  fi
  if [[ ! -e "${file}" ]]; then
    (umask 077; : >"${file}") || return 1
  fi
}

stk_rewrite_managed_block() {
  local file="$1" label="$2" content="${3:-}" mode="${4:-}"
  local begin="# >>> server-toolkit ${label} >>>"
  local end="# <<< server-toolkit ${label} <<<"
  local directory temp_file existing_mode

  directory="$(dirname -- "${file}")"
  stk_prepare_managed_file "${file}" || return 1
  stk_file_marker_counts "${file}" "${begin}" "${end}" || return 1
  existing_mode="$(stat -c '%a' "${file}" 2>/dev/null || true)"
  temp_file="$(mktemp "${directory}/.$(basename -- "${file}").XXXXXX")" || return 1

  if ! awk -v begin="${begin}" -v end="${end}" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; next }
    !inside { lines[++count]=$0 }
    END {
      while (count > 0 && lines[count] == "") count--
      for (i=1; i<=count; i++) print lines[i]
    }
  ' "${file}" >"${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi

  if [[ -n "${content}" ]]; then
    if [[ -s "${temp_file}" ]]; then
      printf '\n' >>"${temp_file}"
    fi
    printf '%s\n%s\n%s\n' "${begin}" "${content}" "${end}" >>"${temp_file}"
  elif [[ -s "${temp_file}" ]]; then
    printf '\n' >>"${temp_file}"
  fi

  if [[ -n "${mode}" ]]; then
    chmod "${mode}" "${temp_file}"
  elif [[ -n "${existing_mode}" ]]; then
    chmod "${existing_mode}" "${temp_file}"
  fi
  if ! mv -f -- "${temp_file}" "${file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
}

stk_prepend_managed_block() {
  local file="$1" label="$2" content="$3" mode="${4:-}"
  local begin="# >>> server-toolkit ${label} >>>"
  local end="# <<< server-toolkit ${label} <<<"
  local directory temp_file existing_mode

  directory="$(dirname -- "${file}")"
  stk_prepare_managed_file "${file}" || return 1
  stk_file_marker_counts "${file}" "${begin}" "${end}" || return 1
  existing_mode="$(stat -c '%a' "${file}" 2>/dev/null || true)"
  temp_file="$(mktemp "${directory}/.$(basename -- "${file}").XXXXXX")" || return 1

  printf '%s\n%s\n%s\n' "${begin}" "${content}" "${end}" >"${temp_file}"
  if ! awk -v begin="${begin}" -v end="${end}" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; next }
    !inside { lines[++count]=$0 }
    END {
      first=1
      while (first <= count && lines[first] == "") first++
      while (count >= first && lines[count] == "") count--
      if (first <= count) print ""
      for (i=first; i<=count; i++) print lines[i]
    }
  ' "${file}" >>"${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi

  if [[ -n "${mode}" ]]; then
    chmod "${mode}" "${temp_file}"
  elif [[ -n "${existing_mode}" ]]; then
    chmod "${existing_mode}" "${temp_file}"
  fi
  if ! mv -f -- "${temp_file}" "${file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
}

stk_managed_block_exists() {
  local file="$1" label="$2"
  grep -Fqx -- "# >>> server-toolkit ${label} >>>" "${file}" 2>/dev/null
}

stk_shell_quote() {
  printf '%q' "$1"
}

stk_confirm() {
  local prompt="$1" answer
  if [[ ! -t 0 ]]; then
    return 1
  fi
  printf '%s [y/N] ' "${prompt}"
  read -r answer || return 1
  [[ "${answer}" == 'y' || "${answer}" == 'Y' || "${answer}" == 'yes' || "${answer}" == 'YES' ]]
}
