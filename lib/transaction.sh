#!/usr/bin/env bash

stk_transaction_begin() {
  local transaction_base="${STK_TRANSACTION_BASE:-${STK_ROOT}/.transactions}"
  stk_validate_absolute_path "${transaction_base}" >/dev/null 2>&1 || return 1
  if [[ -L "${transaction_base}" || ( -e "${transaction_base}" && ! -d "${transaction_base}" ) ]]; then
    stk_fail "事务目录路径异常：${transaction_base}"
    return 1
  fi
  mkdir -p -- "${transaction_base}" || return 1
  chmod 0700 "${transaction_base}" || return 1
  STK_TRANSACTION_ROOT="$(mktemp -d "${transaction_base}/transaction.XXXXXX")" || return 1
  STK_TRANSACTION_COUNT=0
  STK_TRANSACTION_PATHS=$'\n'
}

stk_transaction_add() {
  local path="$1" slot required_kib available_kib reserve_kib
  stk_validate_absolute_path "${path}" >/dev/null 2>&1 || {
    stk_fail "事务目标路径不安全：${path}"
    return 1
  }
  [[ "${STK_TRANSACTION_PATHS}" != *$'\n'"${path}"$'\n'* ]] || return 0
  if [[ -e "${path}" || -L "${path}" ]] && command -v du >/dev/null 2>&1 && command -v df >/dev/null 2>&1; then
    required_kib="$(du -sk -- "${path}" 2>/dev/null | awk 'NR == 1 { print $1 }')"
    available_kib="$(df -Pk -- "${STK_TRANSACTION_ROOT}" 2>/dev/null | awk 'NR == 2 { print $4 }')"
    reserve_kib="${STK_TRANSACTION_MIN_FREE_KIB:-65536}"
    if [[ "${required_kib}" =~ ^[0-9]+$ && "${available_kib}" =~ ^[0-9]+$ &&
          "${reserve_kib}" =~ ^[0-9]+$ ]] &&
       (( required_kib + reserve_kib > available_kib )); then
      stk_fail "事务快照空间不足：${path} 约需 ${required_kib} KiB，当前可用 ${available_kib} KiB"
      return 1
    fi
  fi
  STK_TRANSACTION_COUNT=$((STK_TRANSACTION_COUNT + 1))
  slot="${STK_TRANSACTION_ROOT}/${STK_TRANSACTION_COUNT}"
  if ! printf '%s\n' "${path}" >"${slot}.path"; then
    STK_TRANSACTION_COUNT=$((STK_TRANSACTION_COUNT - 1))
    return 1
  fi
  if [[ -e "${path}" || -L "${path}" ]]; then
    if ! printf 'present\n' >"${slot}.kind"; then
      rm -f -- "${slot}.path"
      STK_TRANSACTION_COUNT=$((STK_TRANSACTION_COUNT - 1))
      return 1
    fi
    if ! cp -a --reflink=auto -- "${path}" "${slot}.data" 2>/dev/null; then
      rm -rf -- "${slot}.data"
      if ! cp -a -- "${path}" "${slot}.data"; then
        rm -rf -- "${slot}.data" "${slot}.path" "${slot}.kind"
        STK_TRANSACTION_COUNT=$((STK_TRANSACTION_COUNT - 1))
        return 1
      fi
    fi
  else
    if ! printf 'absent\n' >"${slot}.kind"; then
      rm -f -- "${slot}.path"
      STK_TRANSACTION_COUNT=$((STK_TRANSACTION_COUNT - 1))
      return 1
    fi
  fi
  STK_TRANSACTION_PATHS+="${path}"$'\n'
}

stk_transaction_restore() {
  local index slot path kind result=0
  # Validate the complete snapshot before deleting any live target. A corrupt or
  # incomplete rollback record must fail closed rather than being treated as
  # an "absent" record.
  for ((index=1; index<=STK_TRANSACTION_COUNT; index++)); do
    slot="${STK_TRANSACTION_ROOT}/${index}"
    if [[ ! -f "${slot}.path" || -L "${slot}.path" ||
          ! -f "${slot}.kind" || -L "${slot}.kind" ]]; then
      return 1
    fi
    path="$(<"${slot}.path")"
    kind="$(<"${slot}.kind")"
    stk_validate_absolute_path "${path}" >/dev/null 2>&1 || return 1
    case "${kind}" in
      present) [[ -e "${slot}.data" || -L "${slot}.data" ]] || return 1 ;;
      absent) [[ ! -e "${slot}.data" && ! -L "${slot}.data" ]] || return 1 ;;
      *) return 1 ;;
    esac
  done
  for ((index=STK_TRANSACTION_COUNT; index>=1; index--)); do
    slot="${STK_TRANSACTION_ROOT}/${index}"
    path="$(<"${slot}.path")"
    kind="$(<"${slot}.kind")"
    rm -rf -- "${path}" || { result=1; continue; }
    if [[ "${kind}" == 'present' ]]; then
      mkdir -p -- "$(dirname -- "${path}")" || { result=1; continue; }
      cp -a -- "${slot}.data" "${path}" || result=1
    fi
  done
  return "${result}"
}

stk_transaction_finish() {
  local transaction_parent=''
  [[ -z "${STK_TRANSACTION_ROOT:-}" ]] || transaction_parent="$(dirname -- "${STK_TRANSACTION_ROOT}")"
  [[ -z "${STK_TRANSACTION_ROOT:-}" ]] || rm -rf -- "${STK_TRANSACTION_ROOT}"
  [[ -z "${transaction_parent}" ]] || rmdir -- "${transaction_parent}" 2>/dev/null || true
  STK_TRANSACTION_ROOT=''
  STK_TRANSACTION_COUNT=0
  STK_TRANSACTION_PATHS=''
}

stk_transaction_rollback() {
  if stk_transaction_restore; then
    stk_transaction_finish
    return 0
  fi
  stk_warn "事务回滚未完成；恢复快照已保留：${STK_TRANSACTION_ROOT}"
  return 1
}
