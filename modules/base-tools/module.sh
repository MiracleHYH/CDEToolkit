#!/usr/bin/env bash

set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC2034 # Consumed while sourcing lib/common.sh.
STK_ROOT="${ROOT}"
source "${ROOT}/lib/common.sh" || exit 1
STATE_DIR="${STK_USER_STATE_DIR}/modules"
STATE_FILE="${STATE_DIR}/base-tools.conf"

status_ok() {
  local command bin_dir fdfind='' batcat=''
  stk_module_state_valid "${STATE_FILE}" || return 1
  bin_dir="$(stk_state_value "${STATE_FILE}" bin_dir)"
  stk_validate_absolute_path "${bin_dir}" >/dev/null 2>&1 || return 1
  for command in curl git jq rg fzf tree htop tmux unzip zip rsync lsof nc dig ss ps; do command -v "${command}" >/dev/null 2>&1 || return 1; done
  fdfind="$(readlink -f -- "$(command -v fdfind 2>/dev/null || true)" 2>/dev/null || true)"
  if [[ -n "${fdfind}" ]] && ! command -v fd >/dev/null 2>&1; then
    [[ -L "${bin_dir}/fd" && "$(readlink -f -- "${bin_dir}/fd" 2>/dev/null || true)" == "${fdfind}" ]] || return 1
  fi
  batcat="$(readlink -f -- "$(command -v batcat 2>/dev/null || true)" 2>/dev/null || true)"
  if [[ -n "${batcat}" ]] && ! command -v bat >/dev/null 2>&1; then
    [[ -L "${bin_dir}/bat" && "$(readlink -f -- "${bin_dir}/bat" 2>/dev/null || true)" == "${batcat}" ]] || return 1
  fi
}

install_shim() {
  local name="$1" source="$2" target="${bin_dir}/$1" resolved source_resolved temp
  source_resolved="$(readlink -f -- "${source}" 2>/dev/null || true)"
  [[ -n "${source_resolved}" ]] || return 1
  if [[ -e "${target}" || -L "${target}" ]]; then
    resolved="$(readlink -f -- "${target}" 2>/dev/null || true)"
    [[ -L "${target}" && "${resolved}" == "${source_resolved}" ]] && return 0
    printf '✗ %s 已存在且不属于 server-toolkit\n' "${target}" >&2
    return 1
  fi
  temp="${bin_dir}/.${name}.$$.tmp"
  ln -s -- "${source_resolved}" "${temp}" || return 1
  mv -Tf -- "${temp}" "${target}"
}

case "${1:-status}" in
  configure)
    bin_dir="${2:-/usr/local/bin}"
    stk_validate_absolute_path "${bin_dir}" || exit 2
    stk_module_state_prepare "${STATE_FILE}" || exit 1
    mkdir -p -- "${bin_dir}"
    if ! command -v fd >/dev/null 2>&1 && command -v fdfind >/dev/null 2>&1; then install_shim fd "$(command -v fdfind)"; fi
    if ! command -v bat >/dev/null 2>&1 && command -v batcat >/dev/null 2>&1; then install_shim bat "$(command -v batcat)"; fi
    stk_module_state_write "${STATE_FILE}" "bin_dir=${bin_dir}" || exit 1
    printf '✓ base-tools 已配置\n'
    ;;
  status)
    if status_ok; then [[ "${2:-}" == '--quiet' ]] || printf 'installed\n'; else [[ "${2:-}" == '--quiet' ]] || printf 'not installed\n'; exit 1; fi
    ;;
  remove)
    bin_dir="$(awk -F= '$1=="bin_dir" {print $2; exit}' "${STATE_FILE}" 2>/dev/null || true)"
    if stk_validate_absolute_path "${bin_dir}" >/dev/null 2>&1; then
      [[ -L "${bin_dir}/fd" && "$(readlink -f "${bin_dir}/fd")" == "$(readlink -f -- "$(command -v fdfind 2>/dev/null || true)" 2>/dev/null || true)" ]] && rm -f -- "${bin_dir}/fd"
      [[ -L "${bin_dir}/bat" && "$(readlink -f "${bin_dir}/bat")" == "$(readlink -f -- "$(command -v batcat 2>/dev/null || true)" 2>/dev/null || true)" ]] && rm -f -- "${bin_dir}/bat"
    fi
    rm -f -- "${STATE_FILE}"
    ;;
  *) exit 2 ;;
esac
