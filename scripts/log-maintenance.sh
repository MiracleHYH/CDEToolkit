#!/usr/bin/env bash

set -uo pipefail
umask 077

SCRIPT_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="${STK_LOG_ROOT:-${SCRIPT_ROOT}}"
# shellcheck disable=SC2034 # Consumed while sourcing lib/common.sh.
STK_ROOT="${SCRIPT_ROOT}"
# shellcheck source=../lib/common.sh
source "${SCRIPT_ROOT}/lib/common.sh"
MAX_BYTES="${STK_LOG_MAX_BYTES:-10485760}"
BACKUPS="${STK_LOG_BACKUPS:-5}"
INTERVAL="${STK_LOG_CHECK_INTERVAL:-60}"

[[ "${MAX_BYTES}" =~ ^[1-9][0-9]*$ ]] || { printf 'STK_LOG_MAX_BYTES must be a positive integer\n' >&2; exit 2; }
[[ "${BACKUPS}" =~ ^[1-9][0-9]*$ ]] || { printf 'STK_LOG_BACKUPS must be a positive integer\n' >&2; exit 2; }
[[ "${INTERVAL}" =~ ^[1-9][0-9]*$ ]] || { printf 'STK_LOG_CHECK_INTERVAL must be a positive integer\n' >&2; exit 2; }
for required_command in stat mktemp gzip mv chmod rm kill; do
  command -v "${required_command}" >/dev/null 2>&1 || {
    printf 'missing log maintenance dependency: %s\n' "${required_command}" >&2
    exit 127
  }
done

managed_writer_active() {
  local pid_file="$1" pid=''
  [[ -f "${pid_file}" && ! -L "${pid_file}" ]] || return 1
  IFS= read -r pid <"${pid_file}" || return 1
  [[ "${pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  kill -0 "${pid}" 2>/dev/null
}

rotate_file() {
  local file="$1" pid_file="$2" size index previous temporary
  [[ -f "${file}" && ! -L "${file}" ]] || return 0
  # copytruncate cannot be made lossless while the writer is appending. The
  # managed PID is therefore a hard exclusion: active logs are handled by the
  # service backend, while this script only compacts inactive legacy logs.
  managed_writer_active "${pid_file}" && return 0
  size="$(stat -c '%s' "${file}" 2>/dev/null || printf '0')"
  [[ "${size}" =~ ^[0-9]+$ ]] || return 0
  (( size >= MAX_BYTES )) || return 0

  temporary="$(mktemp "${file}.rotate.XXXXXX.gz")" || return 1
  if ! gzip -c -- "${file}" >"${temporary}"; then
    rm -f -- "${temporary}"
    return 1
  fi
  chmod 0600 "${temporary}" || { rm -f -- "${temporary}"; return 1; }
  if managed_writer_active "${pid_file}"; then
    rm -f -- "${temporary}"
    return 0
  fi

  for ((index=BACKUPS; index>=2; index--)); do
    previous=$((index - 1))
    if [[ -f "${file}.${previous}.gz" ]]; then
      mv -f -- "${file}.${previous}.gz" "${file}.${index}.gz" || {
        rm -f -- "${temporary}"
        return 1
      }
    fi
  done
  mv -f -- "${temporary}" "${file}.1.gz" || { rm -f -- "${temporary}"; return 1; }
  chmod 0600 "${file}" || return 1
  : >"${file}"
}

run_once() {
  local result=0
  rotate_file "${ROOT}/proxy/workdir/mihomo.log" "${ROOT}/proxy/workdir/mihomo.pid" || result=1
  rotate_file "${ROOT}/gds/workdir/gds.log" "${ROOT}/gds/workdir/gds.pid" || result=1
  return "${result}"
}

case "${1:-loop}" in
  once) stk_with_global_lock run_once ;;
  loop)
    while true; do
      stk_with_global_lock run_once
      sleep "${INTERVAL}" &
      wait $!
    done
    ;;
  *) printf 'usage: log-maintenance.sh once|loop\n' >&2; exit 2 ;;
esac
