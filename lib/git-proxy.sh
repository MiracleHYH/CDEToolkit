#!/usr/bin/env bash

STK_GIT_PROXY_STATE="${STK_USER_STATE_DIR}/git-proxy.conf"

stk_git_value() {
  git config --global --get "$1" 2>/dev/null || true
}

stk_git_value_count() {
  git config --global --get-all "$1" 2>/dev/null | awk 'END { print NR+0 }'
}

stk_git_proxy_state_valid() {
  local http_present https_present http_value https_value managed_value
  stk_module_state_valid "${STK_GIT_PROXY_STATE}" || return 1
  http_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" http_present)"
  https_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" https_present)"
  http_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" http_value)"
  https_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" https_value)"
  managed_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" managed_value)"
  [[ "${http_present}" == '0' || "${http_present}" == '1' ]] || return 1
  [[ "${https_present}" == '0' || "${https_present}" == '1' ]] || return 1
  [[ -n "${managed_value}" && "${managed_value}" != *[[:cntrl:]]* ]] || return 1
  [[ "${http_value}" != *[[:cntrl:]]* && "${https_value}" != *[[:cntrl:]]* ]] || return 1
  [[ "${http_present}" == '1' || -z "${http_value}" ]] || return 1
  [[ "${https_present}" == '1' || -z "${https_value}" ]]
}

stk_git_restore_value() {
  local key="$1" present="$2" value="$3"
  git config --global --unset-all "${key}" >/dev/null 2>&1 || true
  if [[ "${present}" == '1' ]]; then
    git config --global --add "${key}" "${value}"
  fi
}

stk_git_proxy_status_value() {
  local http_value https_value expected
  http_value="$(stk_git_value 'http.proxy')"
  https_value="$(stk_git_value 'https.proxy')"
  expected="$(stk_proxy_http_url 2>/dev/null || true)"
  if stk_git_proxy_state_valid && [[ -n "${expected}" && "${http_value}" == "${expected}" && "${https_value}" == "${expected}" &&
       "$(stk_state_value "${STK_GIT_PROXY_STATE}" managed_value)" == "${expected}" ]]; then
    printf '已配置\n'
  elif [[ -z "${http_value}" && -z "${https_value}" ]]; then
    printf '未配置\n'
  else
    printf '外部配置\n'
  fi
}

stk_git_proxy_status() {
  local http_value https_value
  stk_require_command git || return 1
  http_value="$(stk_git_value 'http.proxy')"
  https_value="$(stk_git_value 'https.proxy')"
  printf 'Git 代理：%s\n' "$(stk_git_proxy_status_value)"
  printf '  http.proxy:  %s\n' "${http_value:-未设置}"
  printf '  https.proxy: %s\n' "${https_value:-未设置}"
}

stk_git_proxy_enable() {
  local force="${1:-0}" expected old_http old_https http_present=0 https_present=0 temp_state
  local current_http_present=0 current_https_present=0 previous_managed='' state_exists=0
  local current_http current_https
  stk_require_command git || return 1
  stk_proxy_ensure_running || return 1
  expected="$(stk_proxy_http_url)" || return 1
  old_http="$(stk_git_value 'http.proxy')"
  old_https="$(stk_git_value 'https.proxy')"
  current_http="${old_http}"
  current_https="${old_https}"
  if (( $(stk_git_value_count 'http.proxy') > 1 || $(stk_git_value_count 'https.proxy') > 1 )); then
    stk_fail '检测到多值 Git 代理配置，无法无损保存，未执行修改'
    return 1
  fi
  if [[ "${old_http}" == *$'\n'* || "${old_http}" == *$'\r'* || "${old_https}" == *$'\n'* || "${old_https}" == *$'\r'* ]]; then
    stk_fail 'Git 代理值包含换行符，无法安全保存'
    return 1
  fi
  [[ -n "${old_http}" ]] && current_http_present=1
  [[ -n "${old_https}" ]] && current_https_present=1

  if [[ -e "${STK_GIT_PROXY_STATE}" || -L "${STK_GIT_PROXY_STATE}" ]]; then
    stk_git_proxy_state_valid || { stk_fail "Git 代理状态文件无效：${STK_GIT_PROXY_STATE}"; return 1; }
    state_exists=1
    previous_managed="$(stk_state_value "${STK_GIT_PROXY_STATE}" managed_value)"
    http_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" http_present)"
    old_http="$(stk_state_value "${STK_GIT_PROXY_STATE}" http_value)"
    https_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" https_present)"
    old_https="$(stk_state_value "${STK_GIT_PROXY_STATE}" https_value)"
  else
    http_present="${current_http_present}"
    https_present="${current_https_present}"
  fi

  if (( state_exists )) && [[ "${previous_managed}" == "${expected}" &&
       "${current_http}" == "${expected}" && "${current_https}" == "${expected}" ]]; then
    stk_ok "Git 代理已配置：${expected}"
    return 0
  fi
  if (( state_exists )) && { [[ "${current_http}" != "${previous_managed}" ]] ||
       [[ "${current_https}" != "${previous_managed}" ]]; } && (( force == 0 )); then
    stk_fail '受管 Git 代理已被外部修改；确认重新应用时请使用 --force'
    return 1
  fi
  if (( state_exists == 0 && force == 0 )) && { [[ -n "${current_http}" && "${current_http}" != "${expected}" ]] ||
       [[ -n "${current_https}" && "${current_https}" != "${expected}" ]]; }; then
    stk_fail '检测到现有 Git 代理，未执行覆盖；确认后使用 --force'
    return 1
  fi

  mkdir -p -- "${STK_USER_STATE_DIR}"
  temp_state="$(mktemp "${STK_USER_STATE_DIR}/.git-proxy.XXXXXX")" || return 1
  {
    printf 'schema_version=%s\n' "${STK_STATE_SCHEMA_VERSION}"
    printf 'http_present=%s\n' "${http_present}"
    printf 'http_value=%s\n' "${old_http}"
    printf 'https_present=%s\n' "${https_present}"
    printf 'https_value=%s\n' "${old_https}"
    printf 'managed_value=%s\n' "${expected}"
  } >"${temp_state}"
  chmod 0600 "${temp_state}"

  if ! git config --global --replace-all http.proxy "${expected}"; then
    rm -f -- "${temp_state}"
    return 1
  fi
  if ! git config --global --replace-all https.proxy "${expected}"; then
    stk_git_restore_value 'http.proxy' "${current_http_present}" "${current_http}" || true
    stk_git_restore_value 'https.proxy' "${current_https_present}" "${current_https}" || true
    rm -f -- "${temp_state}"
    return 1
  fi
  if ! mv -f -- "${temp_state}" "${STK_GIT_PROXY_STATE}"; then
    stk_git_restore_value 'http.proxy' "${current_http_present}" "${current_http}" || true
    stk_git_restore_value 'https.proxy' "${current_https_present}" "${current_https}" || true
    rm -f -- "${temp_state}"
    return 1
  fi
  stk_ok "Git 代理已设置：${expected}"
}

stk_git_proxy_disable() {
  local http_present http_value https_present https_value managed_value current_http current_https
  stk_require_command git || return 1
  current_http="$(stk_git_value 'http.proxy')"
  current_https="$(stk_git_value 'https.proxy')"
  if [[ ! -e "${STK_GIT_PROXY_STATE}" && ! -L "${STK_GIT_PROXY_STATE}" ]]; then
    stk_warn '没有 server-toolkit 管理的 Git 代理记录，未修改现有配置'
    return 0
  fi
  stk_git_proxy_state_valid || { stk_fail "Git 代理状态文件无效，拒绝修改 Git 配置：${STK_GIT_PROXY_STATE}"; return 1; }

  http_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" 'http_present')"
  http_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" 'http_value')"
  https_present="$(stk_state_value "${STK_GIT_PROXY_STATE}" 'https_present')"
  https_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" 'https_value')"
  managed_value="$(stk_state_value "${STK_GIT_PROXY_STATE}" 'managed_value')"
  if [[ "${current_http}" != "${managed_value}" || "${current_https}" != "${managed_value}" ]]; then
    stk_fail 'Git 代理已被外部修改，为避免覆盖用户配置已中止'
    return 1
  fi
  stk_git_restore_value 'http.proxy' "${http_present}" "${http_value}" || return 1
  if ! stk_git_restore_value 'https.proxy' "${https_present}" "${https_value}"; then
    git config --global --replace-all http.proxy "${managed_value}" || true
    return 1
  fi
  rm -f -- "${STK_GIT_PROXY_STATE}"
  stk_ok 'Git 代理已取消，原配置已恢复'
}
