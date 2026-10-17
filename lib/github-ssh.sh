#!/usr/bin/env bash

STK_GITHUB_SSH_LABEL='github-ssh'
STK_SSH_DIR="${STK_SSH_DIR:-${STK_USER_HOME}/.ssh}"
STK_SSH_CONFIG_FILE="${STK_SSH_CONFIG_FILE:-${STK_SSH_DIR}/config}"
STK_GITHUB_KEY_PATH="${STK_GITHUB_KEY_PATH:-$(stk_config_value github_key_path)}"
STK_GITHUB_KEY_SOURCE="${STK_GITHUB_KEY_SOURCE:-$(stk_config_value github_key_source)}"
STK_GITHUB_SSH_STATE="${STK_USER_STATE_DIR}/github-ssh.conf"
STK_GITHUB_KEY_BACKUP="${STK_USER_STATE_DIR}/github-ssh-key.backup"
STK_GITHUB_KNOWN_HOSTS_FILE="${STK_GITHUB_KNOWN_HOSTS_FILE:-${STK_SSH_DIR}/known_hosts}"
STK_GITHUB_KNOWN_HOSTS_LABEL='github-known-hosts'
STK_GITHUB_AGENT_LABEL='github-ssh-agent'
STK_GITHUB_HOST_ED25519='github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl'

stk_github_ssh_validate_paths() {
  stk_validate_absolute_path "${STK_SSH_DIR}" || return 1
  stk_validate_absolute_path "${STK_SSH_CONFIG_FILE}" || return 1
  stk_validate_absolute_path "${STK_GITHUB_KEY_PATH}" || return 1
  stk_validate_absolute_path "${STK_GITHUB_KNOWN_HOSTS_FILE}" || return 1
  if [[ -L "${STK_SSH_DIR}" || -L "${STK_SSH_CONFIG_FILE}" || -L "${STK_GITHUB_KEY_PATH}" ]]; then
    stk_fail 'GitHub SSH 目录、配置和目标私钥不能是符号链接'
    return 1
  fi
  if [[ "${STK_GITHUB_KEY_PATH}" == *'"'* ]]; then
    stk_fail 'GitHub SSH key 路径不能包含双引号'
    return 1
  fi
}

stk_github_known_hosts_configure() {
  stk_require_command ssh-keygen || return 1
  stk_validate_absolute_path "${STK_GITHUB_KNOWN_HOSTS_FILE}" || return 1
  if [[ -L "${STK_SSH_DIR}" || -L "${STK_GITHUB_KNOWN_HOSTS_FILE}" ]]; then
    stk_fail 'SSH 目录和 known_hosts 不能是符号链接'
    return 1
  fi
  mkdir -p -- "${STK_SSH_DIR}" || return 1
  chmod 0700 "${STK_SSH_DIR}" || return 1
  stk_rewrite_managed_block "${STK_GITHUB_KNOWN_HOSTS_FILE}" \
    "${STK_GITHUB_KNOWN_HOSTS_LABEL}" "${STK_GITHUB_HOST_ED25519}" 0644 || return 1
  if ! ssh-keygen -F github.com -f "${STK_GITHUB_KNOWN_HOSTS_FILE}" >/dev/null 2>&1; then
    stk_fail 'GitHub known_hosts 写入后校验失败'
    return 1
  fi
  stk_ok "GitHub 主机公钥已固定到 ${STK_GITHUB_KNOWN_HOSTS_FILE}"
}

stk_github_known_hosts_status_value() {
  if [[ "$(stk_github_ssh_managed_known_host)" == "${STK_GITHUB_HOST_ED25519}" ]] &&
    ssh-keygen -F github.com -f "${STK_GITHUB_KNOWN_HOSTS_FILE}" >/dev/null 2>&1; then
    printf '已配置\n'
  else
    printf '未配置\n'
  fi
}

stk_github_ssh_managed_known_host() {
  awk -v begin="# >>> server-toolkit ${STK_GITHUB_KNOWN_HOSTS_LABEL} >>>" \
      -v end="# <<< server-toolkit ${STK_GITHUB_KNOWN_HOSTS_LABEL} <<<" '
    $0 == begin { inside=1; next }
    $0 == end { exit }
    inside && NF { print; exit }
  ' "${STK_GITHUB_KNOWN_HOSTS_FILE}" 2>/dev/null
}

stk_github_ssh_agent_configure() {
  local content
  stk_require_command ssh-agent || return 1
  stk_require_command ssh-add || return 1
  content="source $(stk_shell_quote "${STK_ROOT}/scripts/ssh-agent.sh")"
  stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${STK_GITHUB_AGENT_LABEL}" "${content}" || return 1
  # shellcheck source=scripts/ssh-agent.sh
  source "${STK_ROOT}/scripts/ssh-agent.sh"
  if [[ -t 0 ]]; then
    ssh-add "${STK_GITHUB_KEY_PATH}" || return 1
    stk_ok 'SSH agent 已启用，密钥已解锁'
  else
    stk_ok 'SSH agent 已启用；请在交互终端执行 stk github-ssh agent 以解锁密钥'
  fi
}

stk_github_ssh_agent_status_value() {
  local expected_fingerprint
  if ! stk_managed_block_exists "${STK_BASHRC_FILE}" "${STK_GITHUB_AGENT_LABEL}"; then
    printf '未配置\n'
  elif expected_fingerprint="$(ssh-keygen -lf "${STK_GITHUB_KEY_PATH}" 2>/dev/null | awk 'NR == 1 { print $2 }')" &&
       [[ -n "${expected_fingerprint}" && -n "${SSH_AUTH_SOCK:-}" ]] &&
       ssh-add -l 2>/dev/null | awk -v expected="${expected_fingerprint}" '$2 == expected { found=1 } END { exit !found }'; then
    printf '密钥已加载\n'
  else
    printf '已配置，密钥未加载\n'
  fi
}

stk_github_ssh_agent_remove() {
  if stk_managed_block_exists "${STK_BASHRC_FILE}" "${STK_GITHUB_AGENT_LABEL}"; then
    stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${STK_GITHUB_AGENT_LABEL}" '' || return 1
    stk_ok 'SSH agent shell 启动配置已移除；当前 shell 中的 agent 不会被强制终止'
  fi
}

stk_github_known_hosts_remove() {
  if stk_managed_block_exists "${STK_GITHUB_KNOWN_HOSTS_FILE}" "${STK_GITHUB_KNOWN_HOSTS_LABEL}"; then
    stk_rewrite_managed_block "${STK_GITHUB_KNOWN_HOSTS_FILE}" "${STK_GITHUB_KNOWN_HOSTS_LABEL}" '' 0644 || return 1
    stk_ok 'server-toolkit 固定的 GitHub 主机公钥已移除'
  fi
}

stk_github_ssh_auth_test() {
  local output result=0
  output="$(ssh -T -o BatchMode=yes -o ConnectTimeout=10 git@github.com 2>&1)" || result=$?
  if grep -Fq 'successfully authenticated' <<<"${output}"; then
    stk_ok "${output}"
    return 0
  fi
  stk_fail "GitHub SSH 鉴权失败（ssh exit ${result}）：${output}"
  return 1
}

stk_github_ssh_validate_source() {
  local source_mode source_owner source_permissions key_kind='OPENSSH PRIVATE KEY'
  stk_require_command ssh || return 1
  stk_require_command ssh-keygen || return 1
  stk_require_command sha256sum || return 1
  if [[ -z "${STK_GITHUB_KEY_SOURCE}" ]]; then
    stk_fail '尚未配置 GitHub SSH 私钥源；请设置 github_key_source 或 STK_GITHUB_KEY_SOURCE'
    return 1
  fi
  stk_validate_absolute_path "${STK_GITHUB_KEY_SOURCE}" || return 1
  if [[ ! -f "${STK_GITHUB_KEY_SOURCE}" || -L "${STK_GITHUB_KEY_SOURCE}" || ! -r "${STK_GITHUB_KEY_SOURCE}" ]]; then
    stk_fail "GitHub SSH key 源必须是可读的普通非符号链接文件：${STK_GITHUB_KEY_SOURCE}"
    return 1
  fi
  source_mode="$(stat -c '%a' "${STK_GITHUB_KEY_SOURCE}" 2>/dev/null || true)"
  source_owner="$(stat -c '%u' "${STK_GITHUB_KEY_SOURCE}" 2>/dev/null || true)"
  [[ "${source_mode}" =~ ^[0-7]{3,4}$ && "${source_owner}" == "$(id -u)" ]] || {
    stk_fail 'GitHub SSH key 源必须由当前用户拥有且权限有效'
    return 1
  }
  source_permissions=$((8#${source_mode}))
  if (( (source_permissions & 0077) != 0 )); then
    stk_fail "GitHub SSH key 源不能允许 group/other 访问：${STK_GITHUB_KEY_SOURCE}（当前 ${source_mode}）"
    return 1
  fi
  if ! grep -Fqx -- "-----BEGIN ${key_kind}-----" "${STK_GITHUB_KEY_SOURCE}" ||
     ! grep -Fqx -- "-----END ${key_kind}-----" "${STK_GITHUB_KEY_SOURCE}"; then
    stk_fail 'GitHub SSH key 资源格式无效'
    return 1
  fi
  if ! ssh-keygen -lf "${STK_GITHUB_KEY_SOURCE}" >/dev/null 2>&1; then
    stk_fail 'GitHub SSH key 资源无法被 OpenSSH 识别'
    return 1
  fi
}

stk_github_managed_key_matches() {
  local expected actual
  [[ -f "${STK_GITHUB_KEY_PATH}" ]] || return 1
  expected="$(stk_state_value "${STK_GITHUB_SSH_STATE}" managed_key_sha256)"
  stk_github_ssh_state_valid || return 1
  [[ "${expected}" =~ ^[a-f0-9]{64}$ ]] || return 1
  actual="$(sha256sum "${STK_GITHUB_KEY_PATH}" 2>/dev/null | awk '{print $1}')"
  [[ "${actual}" == "${expected}" ]]
}

stk_github_ssh_config_content() {
  printf '%s\n' \
    'Host github.com' \
    '  HostName github.com' \
    '  User git' \
    "  IdentityFile \"${STK_GITHUB_KEY_PATH}\"" \
    '  IdentitiesOnly yes'
}

stk_github_ssh_managed_content() {
  awk -v begin="# >>> server-toolkit ${STK_GITHUB_SSH_LABEL} >>>" \
      -v end="# <<< server-toolkit ${STK_GITHUB_SSH_LABEL} <<<" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; exit }
    inside { print }
  ' "${STK_SSH_CONFIG_FILE}" 2>/dev/null
}

stk_github_ssh_effective_config_valid() {
  local effective
  command -v ssh >/dev/null 2>&1 || return 1
  effective="$(ssh -G -F "${STK_SSH_CONFIG_FILE}" github.com 2>/dev/null)" || return 1
  awk -v expected="${STK_GITHUB_KEY_PATH}" '
    $1 == "hostname" { hostname=$2 }
    $1 == "user" { user=$2 }
    $1 == "identitiesonly" { identities_only=$2 }
    $1 == "identityfile" { identity_count++; if ($2 == expected) identity_match++ }
    END {
      exit !(hostname == "github.com" && user == "git" &&
             identities_only == "yes" && identity_count == 1 && identity_match == 1)
    }
  ' <<<"${effective}"
}

stk_github_ssh_config_valid() {
  [[ "$(stk_github_ssh_managed_content)" == "$(stk_github_ssh_config_content)" ]] &&
    stk_github_ssh_effective_config_valid
}

stk_github_ssh_state_valid() {
  local original_present managed_digest
  stk_module_state_valid "${STK_GITHUB_SSH_STATE}" || return 1
  [[ "$(stk_state_value "${STK_GITHUB_SSH_STATE}" 'managed_key_path')" == "${STK_GITHUB_KEY_PATH}" ]] || return 1
  managed_digest="$(stk_state_value "${STK_GITHUB_SSH_STATE}" 'managed_key_sha256')"
  [[ "${managed_digest}" =~ ^[a-f0-9]{64}$ ]] || return 1
  original_present="$(stk_state_value "${STK_GITHUB_SSH_STATE}" 'original_key_present')"
  [[ "${original_present}" == '0' || "${original_present}" == '1' ]] || return 1
  [[ "${original_present}" == '0' ||
     ( -f "${STK_GITHUB_KEY_BACKUP}" && ! -L "${STK_GITHUB_KEY_BACKUP}" && -r "${STK_GITHUB_KEY_BACKUP}" ) ]]
}

stk_github_ssh_status_value() {
  local key_matches=0 config_managed=0 key_mode config_mode dir_mode
  if stk_github_managed_key_matches; then
    key_matches=1
  fi
  if stk_managed_block_exists "${STK_SSH_CONFIG_FILE}" "${STK_GITHUB_SSH_LABEL}"; then
    config_managed=1
  fi
  if (( key_matches && config_managed )); then
    key_mode="$(stat -c '%a' "${STK_GITHUB_KEY_PATH}" 2>/dev/null || true)"
    config_mode="$(stat -c '%a' "${STK_SSH_CONFIG_FILE}" 2>/dev/null || true)"
    dir_mode="$(stat -c '%a' "${STK_SSH_DIR}" 2>/dev/null || true)"
    if [[ "${key_mode}" != '600' || "${config_mode}" != '600' || "${dir_mode}" != '700' ]]; then
      printf '权限异常\n'
    elif ! stk_github_ssh_config_valid; then
      printf '配置异常\n'
    else
      printf '已配置\n'
    fi
  elif [[ -e "${STK_GITHUB_KEY_PATH}" && ${key_matches} -eq 0 ]]; then
    printf '冲突\n'
  elif (( key_matches || config_managed )); then
    printf '部分配置\n'
  else
    printf '未配置\n'
  fi
}

stk_github_ssh_status() {
  local state key_mode='-' config_mode='-'
  state="$(stk_github_ssh_status_value)"
  [[ ! -e "${STK_GITHUB_KEY_PATH}" ]] || key_mode="$(stat -c '%a' "${STK_GITHUB_KEY_PATH}" 2>/dev/null || printf '未知')"
  [[ ! -e "${STK_SSH_CONFIG_FILE}" ]] || config_mode="$(stat -c '%a' "${STK_SSH_CONFIG_FILE}" 2>/dev/null || printf '未知')"
  printf 'GitHub SSH：%s\n' "${state}"
  printf '  私钥文件: %s（权限 %s）\n' "${STK_GITHUB_KEY_PATH}" "${key_mode}"
  printf '  SSH 配置: %s（权限 %s）\n' "${STK_SSH_CONFIG_FILE}" "${config_mode}"
}

stk_github_ssh_configure() {
  local force="${1:-0}" state content temp_key key_backup config_backup managed_digest
  local had_key=0 had_config=0 new_state=0
  local persistent_state_tmp='' persistent_backup_tmp='' original_present=0
  stk_github_ssh_validate_paths || return 1
  stk_github_ssh_validate_source || return 1
  state="$(stk_github_ssh_status_value)"
  if [[ "${state}" == '冲突' && ${force} -ne 1 ]]; then
    stk_fail "${STK_GITHUB_KEY_PATH} 已存在且内容不同；确认接管时请使用 --force"
    return 1
  fi

  mkdir -p -- "${STK_SSH_DIR}"
  chmod 0700 "${STK_SSH_DIR}"
  mkdir -p -- "${STK_USER_STATE_DIR}"
  chmod 0700 "${STK_USER_STATE_DIR}"
  if [[ -L "${STK_GITHUB_KEY_BACKUP}" ||
        ( -e "${STK_GITHUB_KEY_BACKUP}" && ! -f "${STK_GITHUB_KEY_BACKUP}" ) ]]; then
    stk_fail "GitHub SSH 原私钥备份必须是普通非符号链接文件：${STK_GITHUB_KEY_BACKUP}"
    return 1
  fi
  if [[ -e "${STK_GITHUB_SSH_STATE}" || -L "${STK_GITHUB_SSH_STATE}" ]] && ! stk_github_ssh_state_valid; then
    stk_fail "GitHub SSH 状态文件无效：${STK_GITHUB_SSH_STATE}"
    return 1
  fi
  if [[ ! -e "${STK_GITHUB_SSH_STATE}" && ! -L "${STK_GITHUB_SSH_STATE}" ]]; then
    new_state=1
    if [[ -e "${STK_GITHUB_KEY_PATH}" || -L "${STK_GITHUB_KEY_PATH}" ]]; then
      original_present=1
      persistent_backup_tmp="$(mktemp "${STK_USER_STATE_DIR}/.github-ssh-key.XXXXXX")" || {
        rm -f -- "${persistent_state_tmp}"
        return 1
      }
      if ! cp -- "${STK_GITHUB_KEY_PATH}" "${persistent_backup_tmp}" || ! chmod 0600 "${persistent_backup_tmp}"; then
        rm -f -- "${persistent_state_tmp}" "${persistent_backup_tmp}"
        return 1
      fi
    fi
  else
    original_present="$(stk_state_value "${STK_GITHUB_SSH_STATE}" original_key_present)"
  fi
  managed_digest="$(sha256sum "${STK_GITHUB_KEY_SOURCE}" | awk '{print $1}')" || {
    rm -f -- "${persistent_backup_tmp}"
    return 1
  }
  persistent_state_tmp="$(mktemp "${STK_USER_STATE_DIR}/.github-ssh.XXXXXX")" || {
    rm -f -- "${persistent_backup_tmp}"
    return 1
  }
  {
    printf 'schema_version=%s\n' "${STK_STATE_SCHEMA_VERSION}"
    printf 'managed_key_path=%s\n' "${STK_GITHUB_KEY_PATH}"
    printf 'managed_key_sha256=%s\n' "${managed_digest}"
    printf 'original_key_present=%s\n' "${original_present}"
  } >"${persistent_state_tmp}"
  chmod 0600 "${persistent_state_tmp}"
  key_backup="$(mktemp "${STK_SSH_DIR}/.github-key.backup.XXXXXX")" || {
    rm -f -- "${persistent_state_tmp}" "${persistent_backup_tmp}"
    return 1
  }
  config_backup="$(mktemp "${STK_SSH_DIR}/.github-config.backup.XXXXXX")" || {
    rm -f -- "${key_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
    return 1
  }
  chmod 0600 "${key_backup}" "${config_backup}"
  if [[ -e "${STK_GITHUB_KEY_PATH}" || -L "${STK_GITHUB_KEY_PATH}" ]]; then
    if [[ -d "${STK_GITHUB_KEY_PATH}" && ! -L "${STK_GITHUB_KEY_PATH}" ]]; then
      rm -f -- "${key_backup}" "${config_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
      stk_fail "${STK_GITHUB_KEY_PATH} 是目录，无法安装私钥"
      return 1
    fi
    had_key=1
    if ! cp -- "${STK_GITHUB_KEY_PATH}" "${key_backup}" || ! chmod 0600 "${key_backup}"; then
      rm -f -- "${key_backup}" "${config_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
      return 1
    fi
  fi
  if [[ -e "${STK_SSH_CONFIG_FILE}" ]]; then
    had_config=1
    if ! cp -- "${STK_SSH_CONFIG_FILE}" "${config_backup}" || ! chmod 0600 "${config_backup}"; then
      rm -f -- "${key_backup}" "${config_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
      return 1
    fi
  fi

  temp_key="$(mktemp "${STK_SSH_DIR}/.github-key.XXXXXX")" || {
    rm -f -- "${key_backup}" "${config_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
    return 1
  }
  if ! cp -- "${STK_GITHUB_KEY_SOURCE}" "${temp_key}" ||
     ! chmod 0600 "${temp_key}" ||
     ! mv -Tf -- "${temp_key}" "${STK_GITHUB_KEY_PATH}"; then
    rm -f -- "${temp_key}" "${key_backup}" "${config_backup}" "${persistent_state_tmp}" "${persistent_backup_tmp}"
    return 1
  fi
  content="$(stk_github_ssh_config_content)"
  if ! stk_prepend_managed_block "${STK_SSH_CONFIG_FILE}" "${STK_GITHUB_SSH_LABEL}" "${content}" 0600; then
    if (( had_key )); then mv -f -- "${key_backup}" "${STK_GITHUB_KEY_PATH}"; else rm -f -- "${STK_GITHUB_KEY_PATH}"; fi
    if (( had_config )); then mv -f -- "${config_backup}" "${STK_SSH_CONFIG_FILE}"; else rm -f -- "${STK_SSH_CONFIG_FILE}"; fi
    rm -f -- "${persistent_state_tmp}" "${persistent_backup_tmp}"
    return 1
  fi
  if (( new_state )); then
    if [[ -n "${persistent_backup_tmp}" ]]; then
      if ! mv -Tf -- "${persistent_backup_tmp}" "${STK_GITHUB_KEY_BACKUP}"; then
        if (( had_key )); then mv -f -- "${key_backup}" "${STK_GITHUB_KEY_PATH}"; else rm -f -- "${STK_GITHUB_KEY_PATH}"; fi
        if (( had_config )); then mv -f -- "${config_backup}" "${STK_SSH_CONFIG_FILE}"; else rm -f -- "${STK_SSH_CONFIG_FILE}"; fi
        rm -f -- "${persistent_state_tmp}"
        return 1
      fi
    else
      rm -f -- "${STK_GITHUB_KEY_BACKUP}"
    fi
  fi
  if ! mv -Tf -- "${persistent_state_tmp}" "${STK_GITHUB_SSH_STATE}"; then
    if (( had_key )); then mv -f -- "${key_backup}" "${STK_GITHUB_KEY_PATH}"; else rm -f -- "${STK_GITHUB_KEY_PATH}"; fi
    if (( had_config )); then mv -f -- "${config_backup}" "${STK_SSH_CONFIG_FILE}"; else rm -f -- "${STK_SSH_CONFIG_FILE}"; fi
    if (( new_state )); then
      rm -f -- "${STK_GITHUB_KEY_BACKUP}"
    fi
    return 1
  fi
  rm -f -- "${key_backup}" "${config_backup}"
  stk_ok "GitHub SSH key 已配置：${STK_GITHUB_KEY_PATH}"
  stk_warn '使用该密钥认证时 SSH 会要求输入私钥密码，除非已加入 ssh-agent'
}

stk_github_ssh_remove() {
  local force="${1:-0}" original_present config_backup='' restored_key=''
  stk_github_ssh_validate_paths || return 1
  if [[ -e "${STK_GITHUB_SSH_STATE}" || -L "${STK_GITHUB_SSH_STATE}" ]]; then
    if ! stk_github_ssh_state_valid; then
      stk_fail "GitHub SSH 状态文件无效，拒绝删除私钥：${STK_GITHUB_SSH_STATE}"
      return 1
    fi
  fi
  if [[ -e "${STK_GITHUB_KEY_PATH}" ]] && (( force == 0 )) &&
     ! stk_github_managed_key_matches; then
    stk_fail '目标私钥已被外部修改，拒绝删除；确认删除时请使用 --force'
    return 1
  fi
  if [[ -e "${STK_SSH_CONFIG_FILE}" ]]; then
    config_backup="$(mktemp "${STK_SSH_DIR}/.github-remove-config.XXXXXX")" || return 1
    cp -- "${STK_SSH_CONFIG_FILE}" "${config_backup}" || { rm -f -- "${config_backup}"; return 1; }
    chmod 0600 "${config_backup}"
    stk_rewrite_managed_block "${STK_SSH_CONFIG_FILE}" "${STK_GITHUB_SSH_LABEL}" '' 0600 || {
      rm -f -- "${config_backup}"
      return 1
    }
  fi
  if stk_github_ssh_state_valid; then
    original_present="$(stk_state_value "${STK_GITHUB_SSH_STATE}" 'original_key_present')"
    if [[ "${original_present}" == '1' ]]; then
      restored_key="$(mktemp "${STK_SSH_DIR}/.github-restored-key.XXXXXX")" || {
        [[ -z "${config_backup}" ]] || mv -f -- "${config_backup}" "${STK_SSH_CONFIG_FILE}"
        return 1
      }
      if ! cp -- "${STK_GITHUB_KEY_BACKUP}" "${restored_key}" ||
         ! chmod 0600 "${restored_key}" ||
         ! mv -Tf -- "${restored_key}" "${STK_GITHUB_KEY_PATH}"; then
        rm -f -- "${restored_key}"
        [[ -z "${config_backup}" ]] || mv -f -- "${config_backup}" "${STK_SSH_CONFIG_FILE}"
        return 1
      fi
    else
      rm -f -- "${STK_GITHUB_KEY_PATH}"
    fi
    rm -f -- "${STK_GITHUB_SSH_STATE}" "${STK_GITHUB_KEY_BACKUP}"
  else
    stk_warn '缺少有效的 GitHub SSH 接管状态；已移除托管配置块，但保留目标私钥'
  fi
  rm -f -- "${config_backup}"
  stk_ok 'server-toolkit 管理的 GitHub SSH 配置已移除；有接管记录时原私钥已恢复'
}
