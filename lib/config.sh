#!/usr/bin/env bash

STK_CONFIG_SCHEMA_VERSION=4
STK_CONFIG_DIR="${SERVER_TOOLKIT_CONFIG_DIR:-${XDG_CONFIG_HOME:-${STK_USER_HOME}/.config}/server-toolkit}"
STK_CONFIG_FILE="${SERVER_TOOLKIT_CONFIG:-${STK_CONFIG_DIR}/config.conf}"

stk_config_default() {
  case "$1" in
    schema_version) printf '%s\n' "${STK_CONFIG_SCHEMA_VERSION}" ;;
    git_name) printf '\n' ;;
    git_email) printf '\n' ;;
    git_default_branch) printf '%s\n' 'main' ;;
    codex_home) printf '%s/.codex\n' "${STK_PLATFORM_LOCAL_ROOT:-${STK_USER_HOME}}" ;;
    conda_envs_dir) printf '%s/conda_envs\n' "${STK_PLATFORM_LOCAL_ROOT:-${STK_USER_HOME}/.local}" ;;
    no_proxy) printf '%s\n' 'localhost,127.0.0.1,::1' ;;
    proxy_common_listener) printf '%s\n' 'common' ;;
    proxy_codex_listener) printf '%s\n' 'common' ;;
    proxy_config_path) printf '%s/proxy.yaml\n' "${STK_CONFIG_DIR}" ;;
    proxy_release_proxy) printf '\n' ;;
    github_key_path) printf '%s/.ssh/server-toolkit-github\n' "${STK_USER_HOME}" ;;
    github_key_source) printf '\n' ;;
    service_backend) printf '%s\n' 'auto' ;;
    service_user) printf '\n' ;;
    self_update_policy) printf '%s\n' 'disabled' ;;
    self_update_signers) printf '\n' ;;
    *) return 1 ;;
  esac
}

stk_config_keys() {
  printf '%s\n' schema_version git_name git_email git_default_branch codex_home conda_envs_dir no_proxy \
    proxy_common_listener proxy_codex_listener proxy_config_path proxy_release_proxy github_key_path github_key_source service_backend service_user self_update_policy self_update_signers
}

stk_config_key_valid() {
  case "$1" in
    schema_version|git_name|git_email|git_default_branch|codex_home|conda_envs_dir|no_proxy|proxy_common_listener|proxy_codex_listener|proxy_config_path|proxy_release_proxy|github_key_path|github_key_source|service_backend|service_user|self_update_policy|self_update_signers) return 0 ;;
    *) return 1 ;;
  esac
}

stk_config_value_valid() {
  local key="$1" value="$2"
  [[ "${value}" != *$'\n'* && "${value}" != *$'\r'* ]] || return 1
  case "${key}" in
    schema_version) [[ "${value}" == "${STK_CONFIG_SCHEMA_VERSION}" ]] ;;
    git_name) [[ -z "${value}" || ( "${value}" != *[[:cntrl:]]* && "${value}" == *[![:space:]]* ) ]] ;;
    git_email) [[ -z "${value}" || ( "${value}" == *@* && "${value}" != *[[:space:]]* ) ]] ;;
    git_default_branch|proxy_common_listener|proxy_codex_listener) [[ "${value}" =~ ^[A-Za-z0-9._/-]+$ ]] ;;
    codex_home|conda_envs_dir|github_key_path|proxy_config_path) stk_validate_absolute_path "${value}" >/dev/null ;;
    github_key_source) [[ -z "${value}" ]] || stk_validate_absolute_path "${value}" >/dev/null ;;
    proxy_release_proxy) [[ -z "${value}" || "${value}" =~ ^https://[^[:space:]]+$ ]] ;;
    no_proxy) [[ -n "${value}" && "${value}" != *[[:space:]]* ]] ;;
    service_backend) [[ "${value}" == 'auto' || "${value}" == 'supervisord' || "${value}" == 'systemd' ]] ;;
    service_user) [[ -z "${value}" || "${value}" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]] ;;
    self_update_policy) [[ "${value}" == 'disabled' || "${value}" == 'signed-tag' || "${value}" == 'git-ff' ]] ;;
    self_update_signers)
      [[ -z "${value}" || "${value}" =~ ^([A-Fa-f0-9]{40,64}|SHA256:[A-Za-z0-9+/]{20,}={0,2})(,([A-Fa-f0-9]{40,64}|SHA256:[A-Za-z0-9+/]{20,}={0,2}))*$ ]]
      ;;
    *) return 1 ;;
  esac
}

stk_config_validate() {
  local file="${1:-${STK_CONFIG_FILE}}" line key value line_number=0 seen=' '
  [[ -r "${file}" ]] || { stk_fail "配置文件不可读：${file}"; return 1; }
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line_number=$((line_number + 1))
    [[ -z "${line}" || "${line}" == \#* ]] && continue
    if [[ "${line}" != *=* ]]; then
      stk_fail "配置第 ${line_number} 行不是 key=value"
      return 1
    fi
    key="${line%%=*}"; value="${line#*=}"
    if ! stk_config_key_valid "${key}"; then
      stk_fail "配置第 ${line_number} 行包含未知键：${key}"
      return 1
    fi
    if [[ "${seen}" == *" ${key} "* ]]; then
      stk_fail "配置键重复：${key}"
      return 1
    fi
    if ! stk_config_value_valid "${key}" "${value}"; then
      stk_fail "配置值无效：${key}"
      return 1
    fi
    seen+="${key} "
  done <"${file}"
  [[ "$(stk_state_value "${file}" schema_version)" == "${STK_CONFIG_SCHEMA_VERSION}" ]] || {
    stk_fail "配置 schema_version 缺失或不受支持"
    return 1
  }
}

stk_config_runtime_validate() {
  local file="${1:-${STK_CONFIG_FILE}}" file_mode dir_mode
  stk_validate_absolute_path "${file}" || return 1
  stk_validate_absolute_path "$(dirname -- "${file}")" || return 1
  stk_config_validate "${file}" || return 1
  [[ -f "${file}" && ! -L "${file}" && ! -L "$(dirname -- "${file}")" ]] || {
    stk_fail "运行时配置必须是普通非符号链接文件：${file}"
    return 1
  }
  file_mode="$(stat -c '%a' "${file}" 2>/dev/null || true)"
  dir_mode="$(stat -c '%a' "$(dirname -- "${file}")" 2>/dev/null || true)"
  [[ "${file_mode}" == '600' ]] || { stk_fail "配置文件权限必须为 600：${file}"; return 1; }
  [[ "${dir_mode}" == '700' ]] || { stk_fail "配置目录权限必须为 700：$(dirname -- "${file}")"; return 1; }
}

stk_config_value() {
  local key="$1" fallback="${2:-}" value=''
  stk_config_key_valid "${key}" || return 1
  if [[ -r "${STK_CONFIG_FILE}" ]]; then
    value="$(stk_state_value "${STK_CONFIG_FILE}" "${key}")"
    if [[ -n "${value}" ]] && stk_config_value_valid "${key}" "${value}"; then
      printf '%s\n' "${value}"
      return
    fi
  fi
  if [[ -n "${fallback}" ]]; then
    printf '%s\n' "${fallback}"
  else
    stk_config_default "${key}"
  fi
}

stk_config_render() {
  local key
  while IFS= read -r key; do
    printf '%s=%s\n' "${key}" "$(stk_config_value "${key}")"
  done < <(stk_config_keys)
}

stk_config_init() {
  stk_validate_absolute_path "${STK_CONFIG_DIR}" || return 1
  stk_validate_absolute_path "${STK_CONFIG_FILE}" || return 1
  if [[ -L "${STK_CONFIG_DIR}" || -L "${STK_CONFIG_FILE}" ]]; then
    stk_fail "配置目录或文件不能是符号链接：${STK_CONFIG_FILE}"
    return 1
  fi
  mkdir -p -- "${STK_CONFIG_DIR}" || return 1
  chmod 0700 "${STK_CONFIG_DIR}" || return 1
  if [[ -e "${STK_CONFIG_FILE}" ]]; then
    stk_config_validate "${STK_CONFIG_FILE}"
    return
  fi
  stk_config_render | stk_atomic_write "${STK_CONFIG_FILE}" 0600 || return 1
  stk_ok "已创建配置：${STK_CONFIG_FILE}"
}

stk_config_show() {
  if [[ -e "${STK_CONFIG_FILE}" ]]; then
    stk_config_validate "${STK_CONFIG_FILE}" || return 1
  fi
  printf '配置文件：%s%s\n' "${STK_CONFIG_FILE}" "$([[ -e "${STK_CONFIG_FILE}" ]] || printf '（尚未创建，显示默认值）')"
  stk_config_render
}

stk_config_get() {
  local key="$1"
  if ! stk_config_key_valid "${key}"; then
    stk_fail "未知配置键：${key}"
    return 2
  fi
  stk_config_value "${key}"
}

stk_config_set() {
  local key="$1" value="$2" temp_file
  if ! stk_config_key_valid "${key}" || [[ "${key}" == 'schema_version' ]]; then
    stk_fail "不能设置配置键：${key}"
    return 2
  fi
  if ! stk_config_value_valid "${key}" "${value}"; then
    stk_fail "配置值无效：${key}"
    return 2
  fi
  stk_config_init || return 1
  temp_file="$(mktemp "${STK_CONFIG_DIR}/.config.conf.XXXXXX")" || return 1
  if ! awk -F= -v key="${key}" -v value="${value}" '
    $1 == key { print key "=" value; found=1; next }
    { print }
    END { if (!found) print key "=" value }
  ' "${STK_CONFIG_FILE}" >"${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  chmod 0600 "${temp_file}"
  if ! stk_config_validate "${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  if ! mv -f -- "${temp_file}" "${STK_CONFIG_FILE}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  stk_ok "已更新 ${key}"
}

stk_config_edit() {
  local editor="${VISUAL:-${EDITOR:-vi}}" temp_file
  if [[ ! -e "${STK_CONFIG_FILE}" ]]; then
    stk_config_init || return 1
  elif [[ -L "${STK_CONFIG_DIR}" || -L "${STK_CONFIG_FILE}" || ! -f "${STK_CONFIG_FILE}" ]]; then
    stk_fail "配置文件必须是非符号链接的普通文件：${STK_CONFIG_FILE}"
    return 1
  fi
  temp_file="$(mktemp "${STK_CONFIG_DIR}/.config.conf.XXXXXX")" || return 1
  cp -- "${STK_CONFIG_FILE}" "${temp_file}" || { rm -f -- "${temp_file}"; return 1; }
  chmod 0600 "${temp_file}"
  # The editor is intentionally expanded into words so values such as "code --wait" work.
  # shellcheck disable=SC2086
  ${editor} "${temp_file}" || { rm -f -- "${temp_file}"; return 1; }
  if ! stk_config_validate "${temp_file}"; then
    stk_warn '配置无效，原文件未改变'
    rm -f -- "${temp_file}"
    return 1
  fi
  mv -f -- "${temp_file}" "${STK_CONFIG_FILE}" || return 1
  stk_ok '配置已更新'
}

stk_config_repair() {
  local backup temp_file key value seen=' '
  stk_validate_absolute_path "${STK_CONFIG_DIR}" || return 1
  stk_validate_absolute_path "${STK_CONFIG_FILE}" || return 1
  [[ -d "${STK_CONFIG_DIR}" && ! -L "${STK_CONFIG_DIR}" ]] || {
    stk_fail "配置目录必须是非符号链接的普通目录：${STK_CONFIG_DIR}"
    return 1
  }
  [[ -f "${STK_CONFIG_FILE}" && ! -L "${STK_CONFIG_FILE}" ]] || {
    stk_fail "配置文件必须是非符号链接的普通文件：${STK_CONFIG_FILE}"
    return 1
  }
  backup="$(mktemp "${STK_CONFIG_FILE}.backup.$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")" || return 1
  cp -p -- "${STK_CONFIG_FILE}" "${backup}" || { rm -f -- "${backup}"; return 1; }
  temp_file="$(mktemp "${STK_CONFIG_DIR}/.config.repair.XXXXXX")" || return 1
  while IFS= read -r key; do
    value=''
    while IFS='=' read -r candidate rest; do
      [[ "${candidate}" == "${key}" && "${seen}" != *" ${key} "* ]] || continue
      if stk_config_value_valid "${key}" "${rest}"; then value="${rest}"; fi
      seen+="${key} "
      break
    done <"${STK_CONFIG_FILE}"
    if [[ "${key}" == 'schema_version' ]]; then
      value="${STK_CONFIG_SCHEMA_VERSION}"
    elif [[ -z "${value}" ]] && ! stk_config_value_valid "${key}" '' >/dev/null 2>&1; then
      value="$(stk_config_default "${key}")"
    fi
    printf '%s=%s\n' "${key}" "${value}"
  done < <(stk_config_keys) >"${temp_file}"
  chmod 0600 "${temp_file}"
  if ! stk_config_validate "${temp_file}" || ! mv -f -- "${temp_file}" "${STK_CONFIG_FILE}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  stk_ok "配置已修复；原文件备份为 ${backup}"
}

stk_config_prompt_value() {
  local key="$1" label="$2" current input
  current="$(stk_config_value "${key}")"
  printf '%s [%s]: ' "${label}" "${current}" >&2
  read -r input || return 1
  [[ -n "${input}" ]] || input="${current}"
  stk_config_value_valid "${key}" "${input}" || { stk_fail "输入值无效：${key}"; return 1; }
  stk_config_set "${key}" "${input}" >/dev/null
}

stk_config_setup() {
  [[ -t 0 ]] || { stk_fail '个人配置向导需要交互终端'; return 1; }
  stk_config_init || return 1
  printf '个人配置只写入 %s，不会写入项目仓库。\n' "${STK_CONFIG_FILE}"
  stk_config_prompt_value git_name 'Git user.name（可留空）' || return 1
  stk_config_prompt_value git_email 'Git user.email（可留空）' || return 1
  stk_config_prompt_value git_default_branch 'Git 默认分支' || return 1
  stk_config_prompt_value codex_home 'CODEX_HOME' || return 1
  stk_config_prompt_value conda_envs_dir 'Conda 环境目录' || return 1
  stk_config_prompt_value proxy_common_listener '通用代理 listener' || return 1
  stk_config_prompt_value proxy_codex_listener 'Codex 代理 listener' || return 1
  stk_config_prompt_value proxy_config_path 'Proxy 私有 YAML 路径' || return 1
  stk_config_prompt_value no_proxy 'NO_PROXY' || return 1
  stk_config_prompt_value proxy_release_proxy 'GitHub release HTTPS 加速前缀（可留空）' || return 1
  stk_config_prompt_value github_key_source 'GitHub 私钥源文件绝对路径（可留空）' || return 1
  stk_config_prompt_value github_key_path 'GitHub 私钥安装路径' || return 1
  stk_config_prompt_value service_backend '服务后端 auto/supervisord/systemd' || return 1
  stk_config_prompt_value service_user '服务运行用户（留空使用当前用户）' || return 1
  stk_config_prompt_value self_update_policy '自更新策略 disabled/signed-tag/git-ff' || return 1
  stk_config_prompt_value self_update_signers 'signed-tag 允许的签名指纹（逗号分隔）' || return 1
  stk_ok '个人配置向导完成'
}
