#!/usr/bin/env bash

STK_GIT_PROFILE_STATE="${STK_USER_STATE_DIR}/git-profile.conf"
STK_GIT_PROFILE_NAME="$(stk_config_value git_name)"
STK_GIT_PROFILE_EMAIL="$(stk_config_value git_email)"
STK_GIT_PROFILE_BRANCH="$(stk_config_value git_default_branch)"

stk_git_profile_value() {
  git config --global --get "$1" 2>/dev/null || true
}

stk_git_profile_value_count() {
  git config --global --get-all "$1" 2>/dev/null | awk 'END { print NR+0 }'
}

stk_git_profile_require_config() {
  if [[ -z "${STK_GIT_PROFILE_NAME}" || -z "${STK_GIT_PROFILE_EMAIL}" ]]; then
    stk_fail '请先通过 stk config setup 或 stk config set 配置 git_name 与 git_email'
    return 1
  fi
}

stk_git_profile_restore_value() {
  local key="$1" present="$2" value="$3"
  git config --global --unset-all "${key}" >/dev/null 2>&1 || true
  if [[ "${present}" == '1' ]]; then
    git config --global --add "${key}" "${value}"
  fi
}

stk_git_profile_state_valid() {
  local name_present email_present branch_present managed_name managed_email managed_branch value
  stk_module_state_valid "${STK_GIT_PROFILE_STATE}" || return 1
  name_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" name_present)"
  email_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" email_present)"
  branch_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" branch_present)"
  for value in "${name_present}" "${email_present}" "${branch_present}"; do
    [[ "${value}" == '0' || "${value}" == '1' ]] || return 1
  done
  managed_name="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_name)"
  managed_email="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_email)"
  managed_branch="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_branch)"
  stk_config_value_valid git_name "${managed_name}" && [[ -n "${managed_name}" ]] || return 1
  stk_config_value_valid git_email "${managed_email}" && [[ -n "${managed_email}" ]] || return 1
  stk_config_value_valid git_default_branch "${managed_branch}" || return 1
  for value in name email branch; do
    [[ "$(stk_state_value "${STK_GIT_PROFILE_STATE}" "${value}_value")" != *[[:cntrl:]]* ]] || return 1
  done
  [[ "${name_present}" == '1' || -z "$(stk_state_value "${STK_GIT_PROFILE_STATE}" name_value)" ]] || return 1
  [[ "${email_present}" == '1' || -z "$(stk_state_value "${STK_GIT_PROFILE_STATE}" email_value)" ]] || return 1
  [[ "${branch_present}" == '1' || -z "$(stk_state_value "${STK_GIT_PROFILE_STATE}" branch_value)" ]]
}

stk_git_profile_state_matches_desired() {
  [[ "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_name)" == "${STK_GIT_PROFILE_NAME}" &&
     "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_email)" == "${STK_GIT_PROFILE_EMAIL}" &&
     "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_branch)" == "${STK_GIT_PROFILE_BRANCH}" ]]
}

stk_git_profile_values_match_state() {
  [[ "$(stk_git_profile_value 'user.name')" == "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_name)" ]] &&
    [[ "$(stk_git_profile_value 'user.email')" == "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_email)" ]] &&
    [[ "$(stk_git_profile_value 'init.defaultBranch')" == "$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_branch)" ]] &&
    [[ "$(stk_git_profile_value_count 'user.name')" == '1' ]] &&
    [[ "$(stk_git_profile_value_count 'user.email')" == '1' ]] &&
    [[ "$(stk_git_profile_value_count 'init.defaultBranch')" == '1' ]]
}

stk_git_profile_values_match() {
  [[ "$(stk_git_profile_value 'user.name')" == "${STK_GIT_PROFILE_NAME}" ]] &&
    [[ "$(stk_git_profile_value 'user.email')" == "${STK_GIT_PROFILE_EMAIL}" ]] &&
    [[ "$(stk_git_profile_value 'init.defaultBranch')" == "${STK_GIT_PROFILE_BRANCH}" ]] &&
    [[ "$(stk_git_profile_value_count 'user.name')" == '1' ]] &&
    [[ "$(stk_git_profile_value_count 'user.email')" == '1' ]] &&
    [[ "$(stk_git_profile_value_count 'init.defaultBranch')" == '1' ]]
}

stk_git_profile_status_value() {
  local configured_count=0
  if stk_git_profile_state_valid && stk_git_profile_state_matches_desired && stk_git_profile_values_match; then
    printf '已配置\n'
    return
  fi
  (( $(stk_git_profile_value_count 'user.name') > 0 )) && configured_count=$((configured_count + 1))
  (( $(stk_git_profile_value_count 'user.email') > 0 )) && configured_count=$((configured_count + 1))
  (( $(stk_git_profile_value_count 'init.defaultBranch') > 0 )) && configured_count=$((configured_count + 1))
  if (( configured_count == 0 )); then
    printf '未配置\n'
  elif stk_git_profile_state_valid && stk_git_profile_values_match_state; then
    printf '待更新\n'
  elif stk_git_profile_values_match; then
    printf '外部配置\n'
  elif (( configured_count < 3 )); then
    printf '部分配置\n'
  else
    printf '外部配置\n'
  fi
}

stk_git_profile_status() {
  local name email branch
  stk_require_command git || return 1
  name="$(stk_git_profile_value 'user.name')"
  email="$(stk_git_profile_value 'user.email')"
  branch="$(stk_git_profile_value 'init.defaultBranch')"
  printf 'Git 身份：%s\n' "$(stk_git_profile_status_value)"
  printf '  user.name:          %s\n' "${name:-未设置}"
  printf '  user.email:         %s\n' "${email:-未设置}"
  printf '  init.defaultBranch: %s\n' "${branch:-未设置}"
}

stk_git_profile_configure() {
  local force="${1:-0}" state name email branch
  local name_present=0 email_present=0 branch_present=0 temp_state=''
  local current_name_present=0 current_email_present=0 current_branch_present=0
  local current_name current_email current_branch
  stk_require_command git || return 1
  stk_git_profile_require_config || return 1
  name="$(stk_git_profile_value 'user.name')"
  email="$(stk_git_profile_value 'user.email')"
  branch="$(stk_git_profile_value 'init.defaultBranch')"
  current_name="${name}"
  current_email="${email}"
  current_branch="${branch}"
  [[ "$(stk_git_profile_value_count 'user.name')" == '0' ]] || name_present=1
  [[ "$(stk_git_profile_value_count 'user.email')" == '0' ]] || email_present=1
  [[ "$(stk_git_profile_value_count 'init.defaultBranch')" == '0' ]] || branch_present=1
  current_name_present="${name_present}"
  current_email_present="${email_present}"
  current_branch_present="${branch_present}"

  if (( $(stk_git_profile_value_count 'user.name') > 1 ||
        $(stk_git_profile_value_count 'user.email') > 1 ||
        $(stk_git_profile_value_count 'init.defaultBranch') > 1 )); then
    stk_fail '检测到多值 Git 身份配置，无法无损保存，未执行修改'
    return 1
  fi
  if [[ "${name}" == *$'\n'* || "${name}" == *$'\r'* ||
        "${email}" == *$'\n'* || "${email}" == *$'\r'* ||
        "${branch}" == *$'\n'* || "${branch}" == *$'\r'* ]]; then
    stk_fail '现有 Git 身份值包含换行符，无法安全保存'
    return 1
  fi

  if [[ -e "${STK_GIT_PROFILE_STATE}" || -L "${STK_GIT_PROFILE_STATE}" ]]; then
    if ! stk_git_profile_state_valid; then
      stk_fail "Git 身份状态文件无效：${STK_GIT_PROFILE_STATE}"
      return 1
    fi
    if stk_git_profile_state_matches_desired && stk_git_profile_values_match; then
      stk_ok 'Git 身份和默认分支已配置'
      return 0
    fi
    if ! stk_git_profile_values_match_state && (( force == 0 )); then
      stk_fail '受管 Git 身份已被外部修改；确认重新应用时请使用 --force'
      return 1
    fi
    name_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" name_present)"
    name="$(stk_state_value "${STK_GIT_PROFILE_STATE}" name_value)"
    email_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" email_present)"
    email="$(stk_state_value "${STK_GIT_PROFILE_STATE}" email_value)"
    branch_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" branch_present)"
    branch="$(stk_state_value "${STK_GIT_PROFILE_STATE}" branch_value)"
  else
    state="$(stk_git_profile_status_value)"
    if [[ "${state}" != '未配置' && ${force} -ne 1 ]]; then
      stk_fail '检测到现有 Git 身份配置；确认暂存并接管时请使用 --force'
      return 1
    fi
  fi
  mkdir -p -- "${STK_USER_STATE_DIR}"
  temp_state="$(mktemp "${STK_USER_STATE_DIR}/.git-profile.XXXXXX")" || return 1
  {
    printf 'schema_version=%s\n' "${STK_STATE_SCHEMA_VERSION}"
    printf 'name_present=%s\n' "${name_present}"
    printf 'name_value=%s\n' "${name}"
    printf 'email_present=%s\n' "${email_present}"
    printf 'email_value=%s\n' "${email}"
    printf 'branch_present=%s\n' "${branch_present}"
    printf 'branch_value=%s\n' "${branch}"
    printf 'managed_name=%s\n' "${STK_GIT_PROFILE_NAME}"
    printf 'managed_email=%s\n' "${STK_GIT_PROFILE_EMAIL}"
    printf 'managed_branch=%s\n' "${STK_GIT_PROFILE_BRANCH}"
  } >"${temp_state}"
  chmod 0600 "${temp_state}"

  if ! git config --global --replace-all user.name "${STK_GIT_PROFILE_NAME}" ||
     ! git config --global --replace-all user.email "${STK_GIT_PROFILE_EMAIL}" ||
     ! git config --global --replace-all init.defaultBranch "${STK_GIT_PROFILE_BRANCH}"; then
    stk_git_profile_restore_value 'user.name' "${current_name_present}" "${current_name}" || true
    stk_git_profile_restore_value 'user.email' "${current_email_present}" "${current_email}" || true
    stk_git_profile_restore_value 'init.defaultBranch' "${current_branch_present}" "${current_branch}" || true
    [[ -z "${temp_state}" ]] || rm -f -- "${temp_state}"
    return 1
  fi
  if ! mv -f -- "${temp_state}" "${STK_GIT_PROFILE_STATE}"; then
    stk_git_profile_restore_value 'user.name' "${current_name_present}" "${current_name}" || true
    stk_git_profile_restore_value 'user.email' "${current_email_present}" "${current_email}" || true
    stk_git_profile_restore_value 'init.defaultBranch' "${current_branch_present}" "${current_branch}" || true
    rm -f -- "${temp_state}"
    return 1
  fi
  stk_ok 'Git 身份和默认分支已配置'
}

stk_git_profile_remove() {
  local name email branch name_present email_present branch_present
  local managed_name managed_email managed_branch rollback_failed=0
  stk_require_command git || return 1
  if [[ ! -e "${STK_GIT_PROFILE_STATE}" && ! -L "${STK_GIT_PROFILE_STATE}" ]]; then
    stk_warn '没有有效的 server-toolkit Git 身份管理记录，未修改现有配置'
    return 0
  fi
  if ! stk_git_profile_state_valid; then
    stk_fail "Git 身份状态文件无效，拒绝修改 Git 配置：${STK_GIT_PROFILE_STATE}"
    return 1
  fi
  if ! stk_git_profile_values_match_state; then
    stk_fail 'Git 身份已被外部修改，为避免覆盖用户配置已中止'
    return 1
  fi
  name_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'name_present')"
  name="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'name_value')"
  email_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'email_present')"
  email="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'email_value')"
  branch_present="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'branch_present')"
  branch="$(stk_state_value "${STK_GIT_PROFILE_STATE}" 'branch_value')"
  managed_name="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_name)"
  managed_email="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_email)"
  managed_branch="$(stk_state_value "${STK_GIT_PROFILE_STATE}" managed_branch)"
  if ! stk_git_profile_restore_value 'user.name' "${name_present}" "${name}" ||
     ! stk_git_profile_restore_value 'user.email' "${email_present}" "${email}" ||
     ! stk_git_profile_restore_value 'init.defaultBranch' "${branch_present}" "${branch}"; then
    stk_git_profile_restore_value 'user.name' 1 "${managed_name}" || rollback_failed=1
    stk_git_profile_restore_value 'user.email' 1 "${managed_email}" || rollback_failed=1
    stk_git_profile_restore_value 'init.defaultBranch' 1 "${managed_branch}" || rollback_failed=1
    (( rollback_failed == 0 )) || stk_warn 'Git 身份恢复失败，且受管值未能完整回滚'
    return 1
  fi
  rm -f -- "${STK_GIT_PROFILE_STATE}"
  stk_ok 'Git 身份管理已移除，原配置已恢复'
}
