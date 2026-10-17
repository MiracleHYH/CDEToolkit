#!/usr/bin/env bash

# This file is sourced from a shell startup file. State is accepted only when
# the PID start marker and a socket owned by the current user both match.
_stk_agent_state="${SERVER_TOOLKIT_STATE_DIR:-${HOME}/.local/state/server-toolkit}/ssh-agent.env"
_stk_agent_dir="$(dirname -- "${_stk_agent_state}")"
_stk_agent_lock="${_stk_agent_state}.lock"
_stk_agent_sock=''
_stk_agent_pid=''
_stk_agent_start=''
_stk_agent_lock_acquired=0

_stk_agent_process_start() {
  [[ "$1" =~ ^[0-9]+$ && -r "/proc/$1/stat" ]] || return 1
  awk '{ sub(/^.*\) /, ""); print $20; exit }' "/proc/$1/stat" 2>/dev/null
}

_stk_agent_load_state() {
  _stk_agent_sock=''
  _stk_agent_pid=''
  _stk_agent_start=''
  [[ -f "${_stk_agent_state}" && ! -L "${_stk_agent_state}" &&
     "$(stat -c '%a' "${_stk_agent_state}" 2>/dev/null || true)" == '600' ]] || return 1
  _stk_agent_sock="$(awk -F= '$1=="SSH_AUTH_SOCK" {sub(/^[^=]*=/,""); print; exit}' "${_stk_agent_state}")"
  _stk_agent_pid="$(awk -F= '$1=="SSH_AGENT_PID" {print $2; exit}' "${_stk_agent_state}")"
  _stk_agent_start="$(awk -F= '$1=="PROCESS_START" {print $2; exit}' "${_stk_agent_state}")"
}

_stk_agent_state_is_live() {
  local actual_start socket_owner
  _stk_agent_load_state || return 1
  [[ "${_stk_agent_sock}" == /* && "${_stk_agent_sock}" != *$'\n'* &&
     "${_stk_agent_pid}" =~ ^[0-9]+$ && "${_stk_agent_start}" =~ ^[0-9]+$ ]] || return 1
  kill -0 "${_stk_agent_pid}" 2>/dev/null || return 1
  [[ -S "${_stk_agent_sock}" ]] || return 1
  socket_owner="$(stat -c '%u' "${_stk_agent_sock}" 2>/dev/null || true)"
  [[ "${socket_owner}" == "$(id -u)" ]] || return 1
  actual_start="$(_stk_agent_process_start "${_stk_agent_pid}")" || return 1
  [[ "${actual_start}" == "${_stk_agent_start}" ]]
}

_stk_agent_export_state() {
  export SSH_AUTH_SOCK="${_stk_agent_sock}" SSH_AGENT_PID="${_stk_agent_pid}"
}

_stk_agent_lock_acquire() {
  local attempt=0 owner owner_start actual_start
  while (( attempt < 100 )); do
    if mkdir -- "${_stk_agent_lock}" 2>/dev/null; then
      chmod 0700 "${_stk_agent_lock}" || { rmdir -- "${_stk_agent_lock}" 2>/dev/null || true; return 1; }
      printf 'pid=%s\nprocess_start=%s\n' "$$" "$(_stk_agent_process_start "$$")" >"${_stk_agent_lock}/owner.conf"
      chmod 0600 "${_stk_agent_lock}/owner.conf"
      _stk_agent_lock_acquired=1
      return 0
    fi
    [[ -d "${_stk_agent_lock}" && ! -L "${_stk_agent_lock}" ]] || return 1
    owner="$(awk -F= '$1=="pid" {print $2; exit}' "${_stk_agent_lock}/owner.conf" 2>/dev/null || true)"
    owner_start="$(awk -F= '$1=="process_start" {print $2; exit}' "${_stk_agent_lock}/owner.conf" 2>/dev/null || true)"
    if [[ -z "${owner}" || -z "${owner_start}" ]] && (( attempt < 25 )); then
      sleep 0.02
      attempt=$((attempt + 1))
      continue
    fi
    actual_start="$(_stk_agent_process_start "${owner}" 2>/dev/null || true)"
    if [[ ! "${owner}" =~ ^[0-9]+$ || -z "${owner_start}" || "${actual_start}" != "${owner_start}" ]] ||
       ! kill -0 "${owner}" 2>/dev/null; then
      rm -f -- "${_stk_agent_lock}/owner.conf"
      rmdir -- "${_stk_agent_lock}" 2>/dev/null || true
      continue
    fi
    sleep 0.02
    attempt=$((attempt + 1))
  done
  return 1
}

_stk_agent_lock_release() {
  (( _stk_agent_lock_acquired == 1 )) || return 0
  rm -f -- "${_stk_agent_lock}/owner.conf"
  rmdir -- "${_stk_agent_lock}" 2>/dev/null || true
  _stk_agent_lock_acquired=0
}

if [[ ! -L "${_stk_agent_dir}" && ! -L "${_stk_agent_state}" ]]; then
  if _stk_agent_state_is_live; then
    _stk_agent_export_state
  elif command -v ssh-agent >/dev/null 2>&1; then
    mkdir -p -- "${_stk_agent_dir}"
    chmod 0700 "${_stk_agent_dir}"
    if _stk_agent_lock_acquire; then
      if _stk_agent_state_is_live; then
        _stk_agent_export_state
      else
        _stk_agent_output="$(ssh-agent -s 2>/dev/null || true)"
        _stk_agent_sock="$(sed -n 's/^SSH_AUTH_SOCK=\([^;]*\);.*/\1/p' <<<"${_stk_agent_output}")"
        _stk_agent_pid="$(sed -n 's/^SSH_AGENT_PID=\([0-9]*\);.*/\1/p' <<<"${_stk_agent_output}")"
        _stk_agent_start="$(_stk_agent_process_start "${_stk_agent_pid}" 2>/dev/null || true)"
        if [[ "${_stk_agent_sock}" == /* && "${_stk_agent_pid}" =~ ^[0-9]+$ &&
              "${_stk_agent_start}" =~ ^[0-9]+$ && -S "${_stk_agent_sock}" &&
              "$(stat -c '%u' "${_stk_agent_sock}" 2>/dev/null || true)" == "$(id -u)" ]]; then
          _stk_agent_tmp="$(mktemp "${_stk_agent_dir}/.ssh-agent.XXXXXX")"
          chmod 0600 "${_stk_agent_tmp}"
          printf 'SSH_AUTH_SOCK=%s\nSSH_AGENT_PID=%s\nPROCESS_START=%s\n' \
            "${_stk_agent_sock}" "${_stk_agent_pid}" "${_stk_agent_start}" >"${_stk_agent_tmp}"
          mv -f -- "${_stk_agent_tmp}" "${_stk_agent_state}"
          _stk_agent_export_state
          unset _stk_agent_tmp
        elif [[ "${_stk_agent_pid}" =~ ^[0-9]+$ ]]; then
          kill "${_stk_agent_pid}" 2>/dev/null || true
        fi
        unset _stk_agent_output
      fi
      _stk_agent_lock_release
    fi
  fi
fi

unset -f _stk_agent_process_start _stk_agent_load_state _stk_agent_state_is_live \
  _stk_agent_export_state _stk_agent_lock_acquire _stk_agent_lock_release
unset _stk_agent_state _stk_agent_dir _stk_agent_lock _stk_agent_sock _stk_agent_pid \
  _stk_agent_start _stk_agent_lock_acquired
