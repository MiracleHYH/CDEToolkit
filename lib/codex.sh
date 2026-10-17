#!/usr/bin/env bash

STK_CODEX_PROXY_LABEL='codex-proxy'
STK_CODEX_HOME_LABEL='CODEX_HOME'
STK_CODEX_LAUNCHER_STATE="${STK_USER_STATE_DIR}/codex-launcher.conf"
STK_CODEX_INSTALL_URL='https://chatgpt.com/codex/install.sh'
STK_CODEX_PROXY_LISTENER="$(stk_config_value proxy_codex_listener)"
STK_CODEX_NO_PROXY="$(stk_config_value no_proxy)"

stk_codex_bashrc_home() {
  awk '
    /^[[:space:]]*(export[[:space:]]+)?CODEX_HOME[[:space:]]*=/ {
      value=$0
      sub(/^[[:space:]]*(export[[:space:]]+)?CODEX_HOME[[:space:]]*=[[:space:]]*/, "", value)
      sub(/[[:space:]]*#[^\047\042]*$/, "", value)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if ((substr(value,1,1) == "\047" && substr(value,length(value),1) == "\047") ||
          (substr(value,1,1) == "\042" && substr(value,length(value),1) == "\042")) {
        value=substr(value,2,length(value)-2)
      }
      result=value
    }
    END { if (result != "") print result }
  ' "${STK_BASHRC_FILE}" 2>/dev/null || true
}

stk_codex_home() {
  local configured
  if [[ -n "${CODEX_HOME:-}" ]]; then
    printf '%s\n' "${CODEX_HOME}"
    return
  fi
  configured="$(stk_codex_bashrc_home)"
  if [[ -n "${STK_PLATFORM_LOCAL_ROOT}" ]]; then
    case "${configured}" in
      /workspace/.local/.codex|/project_c20250514/250307/.local/.codex) configured='' ;;
    esac
  fi
  if [[ -n "${configured}" ]]; then
    printf '%s\n' "${configured}"
  else
    stk_config_value codex_home
  fi
}

stk_codex_effective_agents_file() {
  local codex_home="$1" override="${1}/AGENTS.override.md"
  if [[ -s "${override}" ]]; then
    printf '%s\n' "${override}"
  else
    printf '%s/AGENTS.md\n' "${codex_home}"
  fi
}

stk_codex_validate_agents_files() {
  local codex_home="$1" label="$2" file
  stk_validate_absolute_path "${codex_home}" >/dev/null || return 1
  for file in "${codex_home}/AGENTS.md" "${codex_home}/AGENTS.override.md"; do
    if [[ -L "${file}" || ( -e "${file}" && ! -f "${file}" ) ]]; then
      stk_fail "Codex 全局说明必须是普通非符号链接文件：${file}"
      return 1
    fi
    if [[ -f "${file}" ]]; then
      stk_file_marker_counts "${file}" \
        "# >>> server-toolkit ${label} >>>" \
        "# <<< server-toolkit ${label} <<<" || return 1
    fi
  done
}

stk_codex_agents_block_current() {
  local file="$1" label="$2" source="$3" expected actual
  [[ -f "${source}" && ! -L "${source}" ]] || return 1
  stk_managed_block_exists "${file}" "${label}" || return 1
  expected="$(<"${source}")"
  actual="$(awk -v begin="# >>> server-toolkit ${label} >>>" \
    -v end="# <<< server-toolkit ${label} <<<" '
      $0 == begin { inside=1; next }
      $0 == end { exit }
      inside { print }
    ' "${file}")"
  [[ "${actual}" == "${expected}" ]]
}

stk_codex_install_agents_block() {
  local codex_home="$1" label="$2" source="$3" agents_file previous_file
  [[ -f "${source}" && ! -L "${source}" ]] || {
    stk_fail "缺少 Codex 全局说明资源：${source}"
    return 1
  }
  stk_codex_validate_agents_files "${codex_home}" "${label}" || return 1
  agents_file="$(stk_codex_effective_agents_file "${codex_home}")"
  if [[ "${agents_file}" == "${codex_home}/AGENTS.override.md" ]]; then
    previous_file="${codex_home}/AGENTS.md"
  else
    previous_file="${codex_home}/AGENTS.override.md"
  fi
  if stk_managed_block_exists "${previous_file}" "${label}"; then
    stk_rewrite_managed_block "${previous_file}" "${label}" '' || return 1
  fi
  stk_rewrite_managed_block "${agents_file}" "${label}" "$(<"${source}")" || return 1
  printf '%s\n' "${agents_file}"
}

stk_codex_remove_agents_block() {
  local codex_home="$1" label="$2" file
  stk_codex_validate_agents_files "${codex_home}" "${label}" || return 1
  for file in "${codex_home}/AGENTS.md" "${codex_home}/AGENTS.override.md"; do
    if stk_managed_block_exists "${file}" "${label}"; then
      stk_rewrite_managed_block "${file}" "${label}" '' || return 1
    fi
  done
}

stk_single_quote() {
  local value="$1"
  value="${value//\'/\'\\\'\'}"
  printf "'%s'" "${value}"
}

stk_codex_real_path() {
  local codex_home="$1" candidate resolved launcher_resolved
  launcher_resolved="$(readlink -f -- "${STK_ROOT}/codex-launcher.sh" 2>/dev/null || true)"
  for candidate in \
    "${codex_home}/bin/codex" \
    "${codex_home}/packages/server-toolkit/bin/codex" \
    "${codex_home}/packages/standalone/current/bin/codex"; do
    [[ -x "${candidate}" ]] || continue
    resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
    if [[ -n "${resolved}" && "${resolved}" != "${launcher_resolved}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  while IFS= read -r candidate; do
    [[ -x "${candidate}" ]] || continue
    resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
    if [[ -n "${resolved}" && "${resolved}" != "${launcher_resolved}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done < <(type -aP codex 2>/dev/null || true)
  return 1
}

stk_codex_install_source() {
  local codex_home="$1" real_path resolved
  real_path="$(stk_codex_real_path "${codex_home}" 2>/dev/null || true)"
  if [[ -z "${real_path}" ]]; then
    printf '未安装\n'
    return
  fi
  resolved="$(readlink -f -- "${real_path}" 2>/dev/null || true)"
  case "${resolved}" in
    "${codex_home}"/packages/standalone/*|"${codex_home}"/bin/*|"${codex_home}"/packages/server-toolkit/bin/*) printf 'standalone' ;;
    *node_modules*|*/npm/*) printf 'npm' ;;
    *Homebrew*|*homebrew*|*/Cellar/*) printf 'Homebrew' ;;
    *) printf '未知来源' ;;
  esac
}

stk_codex_version() {
  local codex_home="$1" real_path version
  real_path="$(stk_codex_real_path "${codex_home}" 2>/dev/null || true)"
  if [[ -z "${real_path}" ]]; then
    printf '未安装\n'
    return
  fi
  version="$(CODEX_HOME="${codex_home}" "${real_path}" --version 2>/dev/null || true)"
  printf '%s\n' "${version:-版本未知}"
}

stk_codex_launcher_target() {
  local codex_home="$1" current resolved launcher_resolved
  if [[ -n "${STK_CODEX_LAUNCHER_TARGET:-}" ]]; then
    printf '%s\n' "${STK_CODEX_LAUNCHER_TARGET}"
    return
  fi
  launcher_resolved="$(readlink -f -- "${STK_ROOT}/codex-launcher.sh" 2>/dev/null || true)"
  current="$(command -v codex 2>/dev/null || true)"
  if [[ -n "${current}" ]]; then
    resolved="$(readlink -f -- "${current}" 2>/dev/null || true)"
    if [[ "${resolved}" == "${launcher_resolved}" || ( -L "${current}" && "${resolved}" == "${codex_home}"/packages/standalone/* ) ]]; then
      printf '%s\n' "${current}"
      return
    fi
  fi
  printf '%s/codex\n' "$(stk_install_bin_dir)"
}

stk_install_codex_launcher() {
  local codex_home="$1" target directory current resolved launcher temp_target
  launcher="${STK_ROOT}/codex-launcher.sh"
  target="$(stk_codex_launcher_target "${codex_home}")"
  stk_validate_absolute_path "${target}" || return 1
  stk_module_state_prepare "${STK_CODEX_LAUNCHER_STATE}" || return 1
  directory="$(dirname -- "${target}")"
  mkdir -p -- "${directory}"
  chmod 0755 "${launcher}"

  if [[ -d "${target}" && ! -L "${target}" ]]; then
    stk_fail "${target} 是目录，无法安装 Codex launcher"
    return 1
  fi
  if [[ -e "${target}" || -L "${target}" ]]; then
    resolved="$(readlink -f -- "${target}" 2>/dev/null || true)"
    if [[ "${resolved}" == "${launcher}" ]]; then
      stk_module_state_write "${STK_CODEX_LAUNCHER_STATE}" "launcher_path=${target}"
      return 0
    fi
    if [[ ! -L "${target}" || "${resolved}" != "${codex_home}"/packages/standalone/* ]]; then
      stk_fail "${target} 已是非 server-toolkit 管理的 Codex 入口，拒绝覆盖"
      return 1
    fi
  fi

  temp_target="${directory}/.codex-stk.$$.tmp"
  rm -f -- "${temp_target}"
  ln -s "${launcher}" "${temp_target}" || return 1
  mv -Tf -- "${temp_target}" "${target}" || {
    rm -f -- "${temp_target}"
    return 1
  }
  stk_module_state_write "${STK_CODEX_LAUNCHER_STATE}" "launcher_path=${target}" || return 1
  stk_ok "Codex launcher 已安装：${target}"
}

stk_codex_launcher_state_valid() {
  local target
  stk_module_state_valid "${STK_CODEX_LAUNCHER_STATE}" || return 1
  target="$(stk_state_value "${STK_CODEX_LAUNCHER_STATE}" launcher_path)"
  stk_validate_absolute_path "${target}" >/dev/null 2>&1
}

stk_codex_proxy_status_value() {
  local codex_home env_file external_state external_count mode
  codex_home="${1:-$(stk_codex_home)}"
  env_file="${codex_home}/.env"
  if stk_managed_block_exists "${env_file}" "${STK_CODEX_PROXY_LABEL}"; then
    if ! stk_codex_proxy_managed_block_valid "${codex_home}"; then
      printf '配置异常\n'
      return
    fi
    mode="$(stat -c '%a' "${env_file}" 2>/dev/null || true)"
    if [[ "${mode}" != '600' ]]; then
      printf '权限异常\n'
      return
    fi
    external_count="$(stk_codex_external_proxy_line_count "${env_file}")"
    if (( external_count > 0 )); then
      printf '混合配置\n'
    else
      printf '已配置\n'
    fi
    return
  fi
  external_state="$(awk -F= '
    /^[[:space:]]*(export[[:space:]]+)?(HTTP_PROXY|http_proxy)[[:space:]]*=/ {
      value=$0; sub(/^[^=]*=/, "", value); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value != "" && value != "\047\047" && value != "\042\042") http=1
    }
    /^[[:space:]]*(export[[:space:]]+)?(HTTPS_PROXY|https_proxy)[[:space:]]*=/ {
      value=$0; sub(/^[^=]*=/, "", value); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value != "" && value != "\047\047" && value != "\042\042") https=1
    }
    /^[[:space:]]*(export[[:space:]]+)?(NO_PROXY|no_proxy)[[:space:]]*=/ {
      value=$0; sub(/^[^=]*=/, "", value); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value != "" && value != "\047\047" && value != "\042\042") no_proxy=1
    }
    /^[[:space:]]*(export[[:space:]]+)?(ALL_PROXY|all_proxy)[[:space:]]*=/ {
      value=$0; sub(/^[^=]*=/, "", value); gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (value != "" && value != "\047\047" && value != "\042\042") all_proxy=1
    }
    END {
      if (http && https && no_proxy) print "external"
      else if (http || https || no_proxy || all_proxy) print "partial"
      else print "none"
    }
  ' "${env_file}" 2>/dev/null || printf 'none')"
  case "${external_state}" in
    external) printf '外部配置\n' ;;
    partial) printf '部分配置\n' ;;
    *) printf '未配置\n' ;;
  esac
}

stk_codex_proxy_source_label() {
  case "$1" in
    '已配置') printf 'server-toolkit managed block' ;;
    '混合配置') printf 'managed block + 现有 .env' ;;
    '配置异常'|'权限异常') printf 'server-toolkit managed block' ;;
    '外部配置'|'部分配置') printf '现有 .env' ;;
    *) printf '-' ;;
  esac
}

stk_codex_proxy_http_url() {
  stk_proxy_http_url "${STK_CODEX_PROXY_LISTENER}"
}

stk_codex_proxy_no_proxy() {
  printf '%s\n' "${STK_CODEX_NO_PROXY}"
}

stk_codex_proxy_env_content() {
  local http_url="$1" no_proxy="$2"
  printf '%s\n' \
    "HTTP_PROXY=\"${http_url}\"" \
    "HTTPS_PROXY=\"${http_url}\"" \
    "http_proxy=\"${http_url}\"" \
    "https_proxy=\"${http_url}\"" \
    "NO_PROXY=\"${no_proxy}\"" \
    "no_proxy=\"${no_proxy}\""
}

stk_codex_proxy_managed_content() {
  local file="$1"
  awk -v begin="# >>> server-toolkit ${STK_CODEX_PROXY_LABEL} >>>" \
      -v end="# <<< server-toolkit ${STK_CODEX_PROXY_LABEL} <<<" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; exit }
    inside { print }
  ' "${file}" 2>/dev/null
}

stk_codex_proxy_managed_block_valid() {
  local codex_home="$1" env_file http_url expected actual
  env_file="${codex_home}/.env"
  http_url="$(stk_codex_proxy_http_url 2>/dev/null)" || return 1
  expected="$(stk_codex_proxy_env_content "${http_url}" "$(stk_codex_proxy_no_proxy)")"
  actual="$(stk_codex_proxy_managed_content "${env_file}")"
  [[ "${actual}" == "${expected}" ]]
}

stk_codex_external_proxy_line_count() {
  local file="$1"
  awk -v begin="# >>> server-toolkit ${STK_CODEX_PROXY_LABEL} >>>" \
      -v end="# <<< server-toolkit ${STK_CODEX_PROXY_LABEL} <<<" '
    $0 == begin { managed=1; next }
    $0 == end { managed=0; next }
    !managed && /^[[:space:]]*(export[[:space:]]+)?(HTTP_PROXY|HTTPS_PROXY|NO_PROXY|ALL_PROXY|http_proxy|https_proxy|no_proxy|all_proxy)[[:space:]]*=/ {
      count++
    }
    END { print count+0 }
  ' "${file}" 2>/dev/null || printf '0\n'
}

stk_codex_remove_external_proxy_lines() {
  local file="$1" begin end directory temp_file existing_mode
  [[ -e "${file}" ]] || return 0
  if [[ -L "${file}" || ! -f "${file}" ]]; then
    stk_fail "Codex .env 必须是普通非符号链接文件：${file}"
    return 1
  fi
  begin="# >>> server-toolkit ${STK_CODEX_PROXY_LABEL} >>>"
  end="# <<< server-toolkit ${STK_CODEX_PROXY_LABEL} <<<"
  stk_file_marker_counts "${file}" "${begin}" "${end}" || return 1
  directory="$(dirname -- "${file}")"
  temp_file="$(mktemp "${directory}/.$(basename -- "${file}").takeover.XXXXXX")" || return 1
  existing_mode="$(stat -c '%a' "${file}" 2>/dev/null || true)"
  if ! awk -v begin="${begin}" -v end="${end}" '
    $0 == begin { managed=1; print; next }
    $0 == end { managed=0; print; next }
    !managed && /^[[:space:]]*(export[[:space:]]+)?(HTTP_PROXY|HTTPS_PROXY|NO_PROXY|ALL_PROXY|http_proxy|https_proxy|no_proxy|all_proxy)[[:space:]]*=/ { next }
    { print }
  ' "${file}" >"${temp_file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
  [[ -z "${existing_mode}" ]] || chmod "${existing_mode}" "${temp_file}"
  if ! mv -f -- "${temp_file}" "${file}"; then
    rm -f -- "${temp_file}"
    return 1
  fi
}

stk_codex_proxy_requires_force() {
  local state="$1"
  [[ "${state}" == '外部配置' || "${state}" == '部分配置' || "${state}" == '混合配置' ]]
}

stk_codex_proxy_assert_writable() {
  local state="$1" force="$2"
  if stk_codex_proxy_requires_force "${state}" && (( force == 0 )); then
    stk_fail '现有 .env 已包含外部代理配置；确认移除并由 server-toolkit 完整接管时请使用 --force'
    return 1
  fi
}

stk_codex_proxy_status() {
  local codex_home env_file mode state
  codex_home="${1:-$(stk_codex_home)}"
  env_file="${codex_home}/.env"
  state="$(stk_codex_proxy_status_value "${codex_home}")"
  printf 'Codex 代理：%s\n' "${state}"
  printf '  配置来源:   %s\n' "$(stk_codex_proxy_source_label "${state}")"
  printf '  CODEX_HOME: %s\n' "${codex_home}"
  printf '  配置文件:   %s\n' "${env_file}"
  if [[ -e "${env_file}" ]]; then
    mode="$(stat -c '%a' "${env_file}" 2>/dev/null || true)"
    printf '  文件权限:   %s\n' "${mode:-未知}"
  fi
}

stk_codex_proxy_enable() {
  local codex_home="$1" force="${2:-0}" effective_home env_file http_url no_proxy content state
  local launcher_target snapshot_path
  stk_validate_absolute_path "${codex_home}" || return 1
  effective_home="$(stk_codex_home)"
  if [[ "${codex_home}" != "${effective_home}" ]]; then
    stk_fail "指定路径与当前 CODEX_HOME 不同（${effective_home}）；请先 export CODEX_HOME 或通过 stk codex install 切换"
    return 1
  fi
  state="$(stk_codex_proxy_status_value "${codex_home}")"
  stk_codex_proxy_assert_writable "${state}" "${force}" || return 1
  env_file="${codex_home}/.env"
  if [[ -L "${env_file}" || ( -e "${env_file}" && ! -f "${env_file}" ) ]]; then
    stk_fail "Codex .env 必须是普通非符号链接文件：${env_file}"
    return 1
  fi
  stk_proxy_ensure_running || return 1
  http_url="$(stk_codex_proxy_http_url)" || return 1
  no_proxy="$(stk_codex_proxy_no_proxy)"
  mkdir -p -- "${codex_home}"
  launcher_target="$(stk_codex_launcher_target "${codex_home}")"
  stk_transaction_begin || return 1
  for snapshot_path in "${env_file}" "${STK_BASHRC_FILE}" "${launcher_target}" "${STK_CODEX_LAUNCHER_STATE}"; do
    if ! stk_transaction_add "${snapshot_path}"; then
      stk_transaction_rollback || stk_warn 'Codex 代理预检失败，且快照未能完整恢复'
      return 1
    fi
  done
  if (( force )); then
    if ! stk_codex_remove_external_proxy_lines "${env_file}"; then
      stk_transaction_rollback || stk_warn 'Codex .env 修改失败，且快照未能完整恢复'
      return 1
    fi
  fi
  content="$(stk_codex_proxy_env_content "${http_url}" "${no_proxy}")"
  if ! stk_rewrite_managed_block "${env_file}" "${STK_CODEX_PROXY_LABEL}" "${content}" 0600; then
    stk_transaction_rollback || stk_warn 'Codex 代理写入失败，且快照未能完整恢复'
    return 1
  fi
  if ! stk_install_codex_launcher "${codex_home}" ||
     ! stk_codex_bashrc_configure "${codex_home}" "${launcher_target}" 0; then
    stk_transaction_rollback || stk_warn 'Codex launcher/shell 配置失败，且快照未能完整恢复'
    return 1
  fi
  stk_transaction_finish
  stk_ok "Codex 代理已由 server-toolkit 接管：${env_file}"
}

stk_codex_proxy_disable() {
  local codex_home="$1" env_file
  stk_validate_absolute_path "${codex_home}" || return 1
  env_file="${codex_home}/.env"
  if [[ ! -e "${env_file}" ]]; then
    stk_warn 'Codex .env 不存在，无需修改'
    return 0
  fi
  stk_rewrite_managed_block "${env_file}" "${STK_CODEX_PROXY_LABEL}" '' 0600 || return 1
  stk_ok 'Codex 代理已取消；.env 中的其他内容已保留'
}

stk_codex_bashrc_remove() {
  if stk_managed_block_exists "${STK_BASHRC_FILE}" "${STK_CODEX_HOME_LABEL}"; then
    stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${STK_CODEX_HOME_LABEL}" '' || return 1
    stk_ok '受管 CODEX_HOME 与 PATH 配置已移除'
  fi
}

stk_codex_launcher_remove() {
  local target resolved launcher
  if [[ ! -e "${STK_CODEX_LAUNCHER_STATE}" && ! -L "${STK_CODEX_LAUNCHER_STATE}" ]]; then
    return 0
  fi
  if ! stk_codex_launcher_state_valid; then
    stk_fail "Codex launcher 状态文件无效：${STK_CODEX_LAUNCHER_STATE}"
    return 1
  fi
  target="$(stk_state_value "${STK_CODEX_LAUNCHER_STATE}" launcher_path)"
  launcher="$(readlink -f -- "${STK_ROOT}/codex-launcher.sh" 2>/dev/null || true)"
  resolved="$(readlink -f -- "${target}" 2>/dev/null || true)"
  if [[ -L "${target}" && -n "${launcher}" && "${resolved}" == "${launcher}" ]]; then
    rm -f -- "${target}" || return 1
  elif [[ -e "${target}" || -L "${target}" ]]; then
    stk_fail "Codex launcher 已被外部修改，拒绝移除：${target}"
    return 1
  fi
  rm -f -- "${STK_CODEX_LAUNCHER_STATE}"
}

stk_codex_bashrc_configure() {
  local codex_home="$1" launcher_target="$2" force="${3:-0}"
  local existing launcher_dir quoted_home quoted_bin content
  existing="$(stk_codex_bashrc_home)"
  if [[ -n "${existing}" && "${existing}" != "${codex_home}" && ${force} -ne 1 ]]; then
    stk_fail "${STK_BASHRC_FILE} 中已有不同的 CODEX_HOME：${existing}；确认后使用 --force-home"
    return 1
  fi
  launcher_dir="$(dirname -- "${launcher_target}")"
  quoted_home="$(stk_single_quote "${codex_home}")"
  quoted_bin="$(stk_single_quote "${launcher_dir}")"
  content="export CODEX_HOME=${quoted_home}
case \":\$PATH:\" in
  *\":${launcher_dir}:\"*) ;;
  *) export PATH=${quoted_bin}:\"\$PATH\" ;;
esac"
  stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${STK_CODEX_HOME_LABEL}" "${content}" || return 1
  stk_ok "CODEX_HOME 已写入 ${STK_BASHRC_FILE}"
}

stk_codex_download_installer() {
  local target="$1" http_url no_proxy
  STK_CODEX_NETWORK_MODE='direct'
  stk_info '检查 Codex 官方安装地址直连...'
  if env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u NO_PROXY \
    -u http_proxy -u https_proxy -u all_proxy -u no_proxy \
    curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --retry 3 --connect-timeout 10 --max-time 60 --max-filesize 4194304 \
    "${STK_CODEX_INSTALL_URL}" -o "${target}"; then
    return 0
  fi

  stk_warn '直连失败，尝试通过 proxy 模块下载'
  if ! stk_proxy_enabled || ! "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then
    stk_fail 'Codex 下载直连失败，且代理尚未运行；请明确启动代理后重试'
    return 1
  fi
  http_url="$(stk_codex_proxy_http_url)" || return 1
  no_proxy="$(stk_codex_proxy_no_proxy)"
  STK_CODEX_NETWORK_MODE='proxy'
  env -u ALL_PROXY -u all_proxy \
    HTTP_PROXY="${http_url}" HTTPS_PROXY="${http_url}" \
    http_proxy="${http_url}" https_proxy="${http_url}" \
    NO_PROXY="${no_proxy}" no_proxy="${no_proxy}" \
    curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --retry 3 --connect-timeout 10 --max-time 120 --max-filesize 4194304 \
    "${STK_CODEX_INSTALL_URL}" -o "${target}"
}

stk_codex_install_abort() {
  local installer_file="$1" message="${2:-Codex 安装失败}"
  rm -f -- "${installer_file}"
  if ! stk_transaction_rollback; then
    stk_warn 'Codex 安装失败，且配置/安装目录快照未能完整恢复'
  fi
  stk_fail "${message}"
}

stk_codex_restore_snapshot_add() {
  local home="$1" path
  for path in \
    "${home}/bin" "${home}/packages/standalone/current" \
    "${home}/packages/server-toolkit/bin/codex" "${home}/packages/app-server-daemon/current" \
    "${home}/config.toml" "${home}/.env" "${home}/AGENTS.md" "${home}/AGENTS.override.md" \
    "${home}/skills/volcml-cowork" "${STK_BASHRC_FILE}" "${STK_CONFIG_FILE}" \
    "${STK_CODEX_LAUNCHER_STATE}" "$(stk_codex_launcher_target "${home}")"; do
    stk_transaction_add "${path}" || return 1
  done
}

stk_codex_restore() {
  local home="$1" force_home="${2:-0}" target http_url content result old_home
  stk_validate_absolute_path "${home}" || return 1
  stk_python_runtime_ensure || return 1
  stk_transaction_begin || return 1
  if ! stk_codex_restore_snapshot_add "${home}"; then
    stk_transaction_finish
    return 1
  fi
  result=0
  "${STK_PYTHON}" "${STK_ROOT}/scripts/prepare-codex.py" --home "${home}" --toolkit "${STK_ROOT}" --check || result=$?
  if (( result == 3 )); then
    # The installer has its own transaction; keep this restore transaction intact.
    if ! (stk_codex_install "${home}" 0); then
      stk_transaction_rollback || stk_warn 'Codex 恢复未能完整回滚'
      return 1
    fi
  elif (( result != 0 )); then
    stk_transaction_rollback || stk_warn 'Codex 恢复未能完整回滚'
    return 1
  fi
  old_home="$(stk_codex_bashrc_home)"
  if [[ -n "${STK_PLATFORM_LOCAL_ROOT}" ]]; then
    case "${old_home}" in /workspace/.local/.codex|/project_c20250514/250307/.local/.codex) force_home=1 ;; esac
  fi
  target="$(stk_codex_launcher_target "${home}")"
  if ! "${STK_PYTHON}" "${STK_ROOT}/scripts/prepare-codex.py" \
      --home "${home}" --toolkit "${STK_ROOT}" --projects-root "${STK_PLATFORM_PROJECTS_ROOT}" ||
     ! stk_config_set codex_home "${home}" >/dev/null ||
     ! stk_install_codex_launcher "${home}" ||
     ! stk_codex_bashrc_configure "${home}" "${target}" "${force_home}"; then
    stk_transaction_rollback || stk_warn 'Codex 恢复未能完整回滚'
    return 1
  fi
  if ! CODEX_HOME="${home}" "$(stk_codex_real_path "${home}")" --version >/dev/null 2>&1; then
    stk_transaction_rollback || stk_warn 'Codex 可执行文件验证失败，未能完整回滚'
    return 1
  fi
  if [[ -f "$(stk_config_value proxy_config_path)" ]]; then
    if ! stk_codex_proxy_assert_writable "$(stk_codex_proxy_status_value "${home}")" 0 ||
       ! http_url="$(stk_codex_proxy_http_url)"; then
      stk_transaction_rollback || stk_warn 'Codex 代理配置失败，未能完整回滚'
      return 1
    fi
    content="$(stk_codex_proxy_env_content "${http_url}" "$(stk_codex_proxy_no_proxy)")"
    if ! stk_rewrite_managed_block "${home}/.env" "${STK_CODEX_PROXY_LABEL}" "${content}" 0600; then
      stk_transaction_rollback || stk_warn 'Codex 代理配置失败，未能完整回滚'
      return 1
    fi
  fi
  stk_transaction_finish
  stk_ok 'Codex 入口与配置已恢复，认证和会话保留；未启动 Codex 会话或代理服务'
}

stk_codex_install() {
  local codex_home="$1" force_home="${2:-0}"
  local launcher_target install_dir installer_file real_binary http_url no_proxy source_before
  stk_validate_absolute_path "${codex_home}" || return 1
  stk_require_command curl || return 1
  stk_require_command sh || return 1

  launcher_target="$(stk_codex_launcher_target "${codex_home}")"
  install_dir="${STK_CODEX_INSTALL_DIR:-${codex_home}/bin}"
  stk_validate_absolute_path "${install_dir}" || return 1
  if [[ "${install_dir}" == "$(dirname -- "${launcher_target}")" ]]; then
    install_dir="${codex_home}/packages/server-toolkit/bin"
  fi

  source_before="$(stk_codex_install_source "${codex_home}")"
  stk_info "当前 Codex：$(stk_codex_version "${codex_home}")（${source_before}）"
  installer_file="$(mktemp "${TMPDIR:-/tmp}/server-toolkit-codex.XXXXXX.sh")" || return 1
  if ! stk_codex_download_installer "${installer_file}"; then
    rm -f -- "${installer_file}"
    stk_fail 'Codex 官方安装脚本下载失败'
    return 1
  fi
  if [[ ! -s "${installer_file}" ]]; then
    rm -f -- "${installer_file}"
    stk_fail 'Codex 官方安装脚本为空'
    return 1
  fi
  if ! sh -n "${installer_file}"; then
    rm -f -- "${installer_file}"
    stk_fail 'Codex 官方安装脚本语法校验失败'
    return 1
  fi

  if ! stk_transaction_begin; then
    rm -f -- "${installer_file}"
    return 1
  fi
  for real_binary in "${STK_BASHRC_FILE}" "${launcher_target}" "${STK_CODEX_LAUNCHER_STATE}" "${install_dir}"; do
    if ! stk_transaction_add "${real_binary}"; then
      stk_codex_install_abort "${installer_file}" '无法创建 Codex 安装快照'
      return 1
    fi
  done
  if ! mkdir -p -- "${codex_home}" "${install_dir}" ||
     ! stk_codex_bashrc_configure "${codex_home}" "${launcher_target}" "${force_home}"; then
    stk_codex_install_abort "${installer_file}" '无法配置 Codex 安装目录或 shell 环境'
    return 1
  fi

  stk_info "使用官方脚本安装 / 更新 Codex（${STK_CODEX_NETWORK_MODE}）..."
  if [[ "${STK_CODEX_NETWORK_MODE}" == 'proxy' ]]; then
    if ! http_url="$(stk_codex_proxy_http_url)"; then
      stk_codex_install_abort "${installer_file}" '无法解析 Codex 代理地址'
      return 1
    fi
    no_proxy="$(stk_codex_proxy_no_proxy)"
    if ! env -u ALL_PROXY -u all_proxy \
      CODEX_HOME="${codex_home}" CODEX_NON_INTERACTIVE=1 CODEX_INSTALL_DIR="${install_dir}" \
      HTTP_PROXY="${http_url}" HTTPS_PROXY="${http_url}" \
      http_proxy="${http_url}" https_proxy="${http_url}" \
      NO_PROXY="${no_proxy}" no_proxy="${no_proxy}" sh "${installer_file}"; then
      stk_codex_install_abort "${installer_file}" 'Codex 官方安装脚本执行失败'
      return 1
    fi
  else
    if ! env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY -u NO_PROXY \
      -u http_proxy -u https_proxy -u all_proxy -u no_proxy \
      CODEX_HOME="${codex_home}" CODEX_NON_INTERACTIVE=1 CODEX_INSTALL_DIR="${install_dir}" \
      sh "${installer_file}"; then
      stk_codex_install_abort "${installer_file}" 'Codex 官方安装脚本执行失败'
      return 1
    fi
  fi
  rm -f -- "${installer_file}"

  real_binary="${install_dir}/codex"
  if [[ ! -x "${real_binary}" ]] || ! CODEX_HOME="${codex_home}" "${real_binary}" --version >/dev/null 2>&1; then
    stk_codex_install_abort "${installer_file}" "Codex 安装后验证失败：${real_binary}"
    return 1
  fi
  if ! stk_install_codex_launcher "${codex_home}"; then
    stk_codex_install_abort "${installer_file}" 'Codex launcher 安装失败'
    return 1
  fi
  stk_transaction_finish
  stk_ok "Codex 已安装 / 更新：$(CODEX_HOME="${codex_home}" "${real_binary}" --version)"
  stk_warn "${STK_BASHRC_FILE} 的环境变量会在新 shell 中自动生效"
}
