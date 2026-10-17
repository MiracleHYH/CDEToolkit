#!/usr/bin/env bash

STK_UPDATE_STATE="${STK_USER_STATE_DIR}/self-update.conf"

stk_self_update_policy() {
  stk_config_value self_update_policy disabled
}

stk_self_update_policy_guard() {
  local policy signers
  policy="$(stk_self_update_policy)"
  if [[ "${policy}" == 'disabled' ]]; then
    stk_fail '自更新默认关闭；请审查 origin 后设置 self_update_policy=signed-tag（推荐）或 git-ff'
    return 1
  fi
  if [[ "${policy}" == 'signed-tag' ]]; then
    signers="$(stk_config_value self_update_signers)"
    if [[ -z "${signers}" ]]; then
      stk_fail 'signed-tag 策略要求先配置 self_update_signers 指纹允许列表'
      return 1
    fi
  fi
}

stk_self_update_signature_fingerprint() {
  awk '
    $1 == "[GNUPG:]" && $2 == "VALIDSIG" && $3 ~ /^[A-Fa-f0-9]+$/ { print toupper($3); exit }
    {
      for (field = 1; field <= NF; field++) {
        if ($field ~ /^SHA256:[A-Za-z0-9+\/=]+$/) { print $field; exit }
      }
    }
  '
}

stk_self_update_signer_allowed() {
  local fingerprint="$1" configured candidate
  local -a candidates=()
  configured="$(stk_config_value self_update_signers)"
  [[ -n "${fingerprint}" && -n "${configured}" ]] || return 1
  IFS=',' read -r -a candidates <<<"${configured}"
  for candidate in "${candidates[@]}"; do
    if [[ "${fingerprint}" == SHA256:* ]]; then
      [[ "${fingerprint}" == "${candidate}" ]] && return 0
    elif [[ "${fingerprint^^}" == "${candidate^^}" ]]; then
      return 0
    fi
  done
  return 1
}

stk_self_update_verify_target() {
  local target="$1" policy tag verified='' verification fingerprint
  policy="$(stk_self_update_policy)"
  [[ "${policy}" == 'signed-tag' ]] || return 0
  while IFS= read -r tag; do
    [[ -n "${tag}" ]] || continue
    if verification="$(git -C "${STK_ROOT}" verify-tag --raw "${tag}" 2>&1)"; then
      fingerprint="$(stk_self_update_signature_fingerprint <<<"${verification}")"
      stk_self_update_signer_allowed "${fingerprint}" || continue
      verified="${tag}"
      break
    fi
  done < <(git -C "${STK_ROOT}" tag --points-at "${target}" | sort -Vr)
  [[ -n "${verified}" ]] || {
    stk_fail 'signed-tag 策略要求目标提交对应签名 tag，且签名指纹必须位于 self_update_signers 允许列表'
    return 1
  }
  stk_ok "更新目标签名验证通过：${verified}"
}

stk_self_update_preflight() {
  local commit="$1" worktree result=0
  worktree="$(mktemp -d "${TMPDIR:-/tmp}/server-toolkit-update.XXXXXX")" || return 1
  if ! git -C "${STK_ROOT}" worktree add --quiet --detach "${worktree}" "${commit}"; then
    rm -rf -- "${worktree}"
    return 1
  fi
  "${STK_ROOT}/scripts/preflight-update.sh" "${worktree}" || result=$?
  git -C "${STK_ROOT}" worktree remove --force "${worktree}" >/dev/null 2>&1 || true
  rm -rf -- "${worktree}"
  return "${result}"
}

stk_version() {
  local version='unknown' commit=''
  [[ -r "${STK_ROOT}/VERSION" ]] && version="$(head -n 1 "${STK_ROOT}/VERSION")"
  if [[ -d "${STK_ROOT}/.git" ]] && command -v git >/dev/null 2>&1; then
    commit="$(git -C "${STK_ROOT}" rev-parse --short HEAD 2>/dev/null || true)"
  fi
  printf 'server-toolkit %s%s\n' "${version}" "$([[ -n "${commit}" ]] && printf ' (%s)' "${commit}")"
}

stk_git_update_target() {
  local upstream
  upstream="$(git -C "${STK_ROOT}" rev-parse --abbrev-ref '@{upstream}' 2>/dev/null || true)"
  if [[ -n "${upstream}" ]]; then
    printf '%s\n' "${upstream}"
  elif git -C "${STK_ROOT}" show-ref --verify --quiet refs/remotes/origin/main; then
    printf 'origin/main\n'
  else
    return 1
  fi
}

stk_self_update_check_repo() {
  [[ -d "${STK_ROOT}/.git" ]] || { stk_fail '当前安装不是 Git 工作树，无法自更新'; return 1; }
  stk_require_command git || return 1
  git -C "${STK_ROOT}" rev-parse --verify HEAD >/dev/null 2>&1 || {
    stk_fail '仓库还没有初始提交，无法自更新'
    return 1
  }
  if ! git -C "${STK_ROOT}" diff --quiet || ! git -C "${STK_ROOT}" diff --cached --quiet; then
    stk_fail '存在已跟踪文件的本地修改，拒绝自更新'
    return 1
  fi
}

stk_self_update_check() {
  local target current remote
  stk_self_update_policy_guard || return 1
  stk_self_update_check_repo || return 1
  remote="$(git -C "${STK_ROOT}" remote get-url origin 2>/dev/null || true)"
  [[ -n "${remote}" ]] || { stk_fail '未配置 origin，无法检查更新'; return 1; }
  git -C "${STK_ROOT}" fetch --quiet origin || return 1
  target="$(stk_git_update_target)" || { stk_fail '未找到上游分支'; return 1; }
  current="$(git -C "${STK_ROOT}" rev-parse HEAD)"
  if [[ "${current}" == "$(git -C "${STK_ROOT}" rev-parse "${target}")" ]]; then
    stk_ok '已是最新版本'
  else
    stk_self_update_verify_target "${target}" || return 1
    printf '当前：%s\n可用：%s\n' "$(git -C "${STK_ROOT}" show -s --format='%h %s' HEAD)" "$(git -C "${STK_ROOT}" show -s --format='%h %s' "${target}")"
  fi
}

stk_self_update() {
  local target old_commit new_commit branch
  stk_self_update_policy_guard || return 1
  stk_self_update_check_repo || return 1
  [[ -n "$(git -C "${STK_ROOT}" remote get-url origin 2>/dev/null || true)" ]] || {
    stk_fail '未配置 origin，无法自更新'
    return 1
  }
  git -C "${STK_ROOT}" fetch origin || return 1
  target="$(stk_git_update_target)" || { stk_fail '未找到上游分支'; return 1; }
  old_commit="$(git -C "${STK_ROOT}" rev-parse HEAD)"
  new_commit="$(git -C "${STK_ROOT}" rev-parse "${target}")"
  [[ "${old_commit}" != "${new_commit}" ]] || { stk_ok '已是最新版本'; return 0; }
  git -C "${STK_ROOT}" merge-base --is-ancestor "${old_commit}" "${new_commit}" || {
    stk_fail '上游不能快进，拒绝更新'
    return 1
  }
  stk_self_update_verify_target "${new_commit}" || return 1
  stk_info '在隔离 worktree 中执行可信的静态预检...'
  stk_self_update_preflight "${new_commit}" || {
    stk_fail '新版本静态预检失败，当前工作树未改变'
    return 1
  }
  branch="$(git -C "${STK_ROOT}" symbolic-ref --quiet --short HEAD 2>/dev/null || true)"
  [[ -n "${branch}" ]] || { stk_fail '当前处于 detached HEAD，拒绝更新'; return 1; }
  printf 'schema_version=%s\nprevious_commit=%s\nupdated_commit=%s\nbranch=%s\n' \
    "${STK_STATE_SCHEMA_VERSION}" "${old_commit}" "${new_commit}" "${branch}" |
    stk_atomic_write "${STK_UPDATE_STATE}" 0600 || return 1
  if ! git -C "${STK_ROOT}" merge --ff-only "${new_commit}"; then
    git -C "${STK_ROOT}" update-ref "refs/heads/${branch}" "${old_commit}" "${new_commit}" >/dev/null 2>&1 || true
    git -C "${STK_ROOT}" restore --source "${old_commit}" --staged --worktree -- . >/dev/null 2>&1 || true
    rm -f -- "${STK_UPDATE_STATE}"
    stk_fail '快进更新失败，已尝试恢复原提交'
    return 1
  fi
  if ! "${STK_ROOT}/toolkit.sh" __post-update; then
    git -C "${STK_ROOT}" update-ref "refs/heads/${branch}" "${old_commit}" "${new_commit}" >/dev/null 2>&1 || true
    git -C "${STK_ROOT}" restore --source "${old_commit}" --staged --worktree -- . >/dev/null 2>&1 || true
    rm -f -- "${STK_UPDATE_STATE}"
    stk_fail '更新后的配置或 Python 运行时检查失败，已恢复原提交'
    return 1
  fi
  stk_ok "已更新到 $(git -C "${STK_ROOT}" rev-parse --short HEAD)"
}

stk_self_update_rollback() {
  local old_commit new_commit branch current added path
  [[ -r "${STK_UPDATE_STATE}" ]] || { stk_fail '没有可回滚的更新记录'; return 1; }
  stk_self_update_check_repo || return 1
  old_commit="$(stk_state_value "${STK_UPDATE_STATE}" previous_commit)"
  new_commit="$(stk_state_value "${STK_UPDATE_STATE}" updated_commit)"
  branch="$(stk_state_value "${STK_UPDATE_STATE}" branch)"
  current="$(git -C "${STK_ROOT}" rev-parse HEAD)"
  [[ "${current}" == "${new_commit}" && -n "${old_commit}" && -n "${branch}" ]] || {
    stk_fail '当前版本与更新记录不一致，拒绝自动回滚'
    return 1
  }
  git -C "${STK_ROOT}" cat-file -e "${old_commit}^{commit}" || return 1
  git -C "${STK_ROOT}" update-ref "refs/heads/${branch}" "${old_commit}" "${new_commit}" || return 1
  git -C "${STK_ROOT}" restore --source "${old_commit}" --staged --worktree -- . || return 1
  added="$(git -C "${STK_ROOT}" diff --name-only --diff-filter=A "${old_commit}" "${new_commit}")"
  while IFS= read -r path; do
    [[ -n "${path}" && "${path}" != /* && "${path}" != *'..'* ]] || continue
    [[ -e "${STK_ROOT}/${path}" && ! -L "${STK_ROOT}/${path}" ]] && rm -f -- "${STK_ROOT}/${path}"
  done <<<"${added}"
  rm -f -- "${STK_UPDATE_STATE}"
  stk_ok "已回滚到 $(git -C "${STK_ROOT}" rev-parse --short HEAD)"
}

stk_remove_managed_entry() {
  local name="$1" manager="$2" target resolved
  target="$(stk_install_bin_dir)/${name}"
  if [[ -L "${target}" ]]; then
    resolved="$(readlink -f -- "${target}" 2>/dev/null || true)"
    [[ "${resolved}" == "${manager}" ]] && rm -f -- "${target}"
  fi
}

stk_module_remove() {
  local id="$1" command_name manager backend config type was_running=0
  command_name="$(stk_module_field "${id}" 4)" || { stk_fail "未知模块：${id}"; return 2; }
  manager="$(stk_module_field "${id}" 5)" || return 1
  type="$(stk_module_field "${id}" 8)"
  "${manager}" status --quiet >/dev/null 2>&1 && was_running=1
  stk_module_transaction_begin "${id}" || { stk_transaction_finish; return 1; }
  if [[ "${type}" == 'setup' ]]; then
    if ! "${manager}" remove; then
      stk_transaction_rollback || stk_warn '模块移除失败，且快照未能完整恢复'
      return 1
    fi
    stk_transaction_finish
    stk_ok "模块 ${id} 的托管配置已移除；系统软件包未卸载"
    return
  fi
  if stk_module_is_enabled "${id}" && ! "${manager}" stop; then
    stk_transaction_rollback || stk_warn '模块停止失败，且快照未能完整恢复'
    return 1
  fi
  if [[ "${id}" == 'gds' ]] && ! stk_gds_agents_remove; then
    stk_transaction_rollback || stk_warn 'GDS 全局规则移除失败，且快照未能完整恢复'
    return 1
  fi
  [[ "${command_name}" == '-' ]] || stk_remove_managed_entry "${command_name}" "${manager}"
  backend="$(stk_service_backend)"
  if [[ "${backend}" == 'supervisord' ]]; then
    config="$(stk_supervisor_config 2>/dev/null || true)"
    if [[ -n "${config}" ]] && stk_supervisor_managed_installed; then
      if ! stk_supervisor_install; then
        stk_transaction_rollback || stk_warn '服务更新失败，且模块快照未能完整恢复'
        stk_supervisor_reload >/dev/null 2>&1 || true
        if (( was_running )); then stk_service_module_action start "${id}" >/dev/null 2>&1 || "${manager}" start >/dev/null 2>&1 || true; fi
        return 1
      fi
    fi
  fi
  stk_transaction_finish
  stk_ok "模块 ${id} 已移除；配置与运行数据已保留"
}

stk_uninstall_snapshot_add() {
  local path="$1" slot next_index
  stk_validate_absolute_path "${path}" >/dev/null 2>&1 || return 1
  next_index=$((STK_UNINSTALL_SNAPSHOT_INDEX + 1))
  slot="${STK_UNINSTALL_SNAPSHOT}/${next_index}"
  printf '%s\n' "${path}" >"${slot}.path" || return 1
  if [[ -e "${path}" || -L "${path}" ]]; then
    printf 'present\n' >"${slot}.kind" || { rm -f -- "${slot}.path"; return 1; }
    cp -a -- "${path}" "${slot}.data" || {
      rm -rf -- "${slot}.path" "${slot}.kind" "${slot}.data"
      return 1
    }
  else
    printf 'absent\n' >"${slot}.kind" || { rm -f -- "${slot}.path"; return 1; }
  fi
  STK_UNINSTALL_SNAPSHOT_INDEX="${next_index}"
}

stk_uninstall_snapshot_restore() {
  local index slot path kind result=0
  for ((index=1; index<=STK_UNINSTALL_SNAPSHOT_INDEX; index++)); do
    slot="${STK_UNINSTALL_SNAPSHOT}/${index}"
    [[ -f "${slot}.path" && ! -L "${slot}.path" &&
       -f "${slot}.kind" && ! -L "${slot}.kind" ]] || return 1
    path="$(<"${slot}.path")"
    kind="$(<"${slot}.kind")"
    stk_validate_absolute_path "${path}" >/dev/null 2>&1 || return 1
    case "${kind}" in
      present) [[ -e "${slot}.data" || -L "${slot}.data" ]] || return 1 ;;
      absent) [[ ! -e "${slot}.data" && ! -L "${slot}.data" ]] || return 1 ;;
      *) return 1 ;;
    esac
  done
  for ((index=STK_UNINSTALL_SNAPSHOT_INDEX; index>=1; index--)); do
    slot="${STK_UNINSTALL_SNAPSHOT}/${index}"
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

stk_uninstall_snapshot_create() {
  local codex_home launcher_path launcher_resolved git_xdg supervisor_config record command_name status_output installed_gds_codex_home
  stk_validate_absolute_path "${STK_USER_STATE_DIR}" || return 1
  stk_validate_absolute_path "${STK_CONFIG_DIR}" || return 1
  STK_UNINSTALL_SNAPSHOT="$(mktemp -d "${TMPDIR:-/tmp}/server-toolkit-uninstall.XXXXXX")" || return 1
  STK_UNINSTALL_SNAPSHOT_INDEX=0
  STK_UNINSTALL_SERVICE_BACKEND="$(stk_service_backend)"
  STK_UNINSTALL_SERVICE_MANAGED=0
  STK_UNINSTALL_SYSTEMD_SERVICE_ENABLED=0
  STK_UNINSTALL_SYSTEMD_TIMER_ENABLED=0
  STK_UNINSTALL_SYSTEMD_SERVICE_ACTIVE=0
  STK_UNINSTALL_SYSTEMD_TIMER_ACTIVE=0
  STK_UNINSTALL_SUPERVISOR_PROXY_ACTIVE=0
  STK_UNINSTALL_SUPERVISOR_GDS_ACTIVE=0
  STK_UNINSTALL_SUPERVISOR_MAINTENANCE_ACTIVE=0
  STK_UNINSTALL_PROXY_STATE='disabled'
  STK_UNINSTALL_GDS_STATE='disabled'
  if stk_module_is_enabled proxy; then
    if "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then STK_UNINSTALL_PROXY_STATE='running'; else STK_UNINSTALL_PROXY_STATE='stopped'; fi
  fi
  if stk_module_is_enabled gds; then
    if "${STK_GDS_SCRIPT}" status --quiet >/dev/null 2>&1; then STK_UNINSTALL_GDS_STATE='running'; else STK_UNINSTALL_GDS_STATE='stopped'; fi
  fi
  if [[ "${STK_UNINSTALL_SERVICE_BACKEND}" == 'supervisord' ]] && stk_supervisor_managed_installed; then
    STK_UNINSTALL_SERVICE_MANAGED=1
    status_output="$(stk_supervisor_call status server-toolkit-proxy 2>/dev/null || true)"
    [[ "${status_output}" =~ ^server-toolkit-proxy[[:space:]]+(RUNNING|STARTING|BACKOFF) ]] && STK_UNINSTALL_SUPERVISOR_PROXY_ACTIVE=1
    status_output="$(stk_supervisor_call status server-toolkit-gds 2>/dev/null || true)"
    [[ "${status_output}" =~ ^server-toolkit-gds[[:space:]]+(RUNNING|STARTING|BACKOFF) ]] && STK_UNINSTALL_SUPERVISOR_GDS_ACTIVE=1
    status_output="$(stk_supervisor_call status server-toolkit-maintenance 2>/dev/null || true)"
    [[ "${status_output}" =~ ^server-toolkit-maintenance[[:space:]]+(RUNNING|STARTING|BACKOFF) ]] && STK_UNINSTALL_SUPERVISOR_MAINTENANCE_ACTIVE=1
  elif [[ "${STK_UNINSTALL_SERVICE_BACKEND}" == 'systemd' && -e "${STK_SYSTEMD_DIR}/server-toolkit.service" ]]; then
    STK_UNINSTALL_SERVICE_MANAGED=1
    stk_systemd_unit_enabled server-toolkit.service && STK_UNINSTALL_SYSTEMD_SERVICE_ENABLED=1
    stk_systemd_unit_enabled server-toolkit-health.timer && STK_UNINSTALL_SYSTEMD_TIMER_ENABLED=1
    stk_systemd_unit_active server-toolkit.service && STK_UNINSTALL_SYSTEMD_SERVICE_ACTIVE=1
    stk_systemd_unit_active server-toolkit-health.timer && STK_UNINSTALL_SYSTEMD_TIMER_ACTIVE=1
  fi
  codex_home="$(stk_codex_bashrc_home)"
  [[ -n "${codex_home}" ]] || codex_home="$(stk_config_value codex_home)"
  launcher_path="$(stk_state_value "${STK_CODEX_LAUNCHER_STATE}" launcher_path)"
  launcher_resolved="$(readlink -f -- "${STK_ROOT}/codex-launcher.sh" 2>/dev/null || true)"
  git_xdg="${XDG_CONFIG_HOME:-${STK_USER_HOME}/.config}/git/config"
  stk_uninstall_snapshot_add "${STK_USER_STATE_DIR}" || return 1
  stk_uninstall_snapshot_add "${STK_CONFIG_DIR}" || return 1
  stk_uninstall_snapshot_add "${STK_USER_HOME}/.gitconfig" || return 1
  [[ -z "${GIT_CONFIG_GLOBAL:-}" || "${GIT_CONFIG_GLOBAL}" == "${STK_USER_HOME}/.gitconfig" ]] || stk_uninstall_snapshot_add "${GIT_CONFIG_GLOBAL}" || return 1
  [[ "${git_xdg}" == "${STK_USER_HOME}/.gitconfig" ]] || stk_uninstall_snapshot_add "${git_xdg}" || return 1
  stk_uninstall_snapshot_add "${STK_BASHRC_FILE}" || return 1
  stk_uninstall_snapshot_add "${codex_home}/.env" || return 1
  stk_uninstall_snapshot_add "${STK_SSH_CONFIG_FILE}" || return 1
  stk_uninstall_snapshot_add "${STK_GITHUB_KEY_PATH}" || return 1
  stk_uninstall_snapshot_add "${STK_GITHUB_KNOWN_HOSTS_FILE}" || return 1
  if [[ -L "${launcher_path}" && "$(readlink -f -- "${launcher_path}" 2>/dev/null || true)" == "${launcher_resolved}" ]]; then
    stk_uninstall_snapshot_add "${launcher_path}" || return 1
  fi
  supervisor_config="$(stk_supervisor_config 2>/dev/null || true)"
  [[ -z "${supervisor_config}" ]] || stk_uninstall_snapshot_add "${supervisor_config}" || return 1
  stk_uninstall_snapshot_add "${STK_SYSTEMD_DIR}/server-toolkit.service" || return 1
  stk_uninstall_snapshot_add "${STK_SYSTEMD_DIR}/server-toolkit-health.service" || return 1
  stk_uninstall_snapshot_add "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" || return 1
  stk_uninstall_snapshot_add "${STK_LOGROTATE_FILE}" || return 1
  stk_uninstall_snapshot_add "$(stk_install_bin_dir)/stk" || return 1
  while IFS= read -r record; do
    command_name="$(awk -F'|' '{print $4}' <<<"${record}")"
    [[ "${command_name}" == '-' ]] || stk_uninstall_snapshot_add "$(stk_install_bin_dir)/${command_name}" || return 1
  done < <(stk_module_catalog)
  stk_uninstall_snapshot_add "${codex_home}/AGENTS.md" || return 1
  stk_uninstall_snapshot_add "${codex_home}/AGENTS.override.md" || return 1
  installed_gds_codex_home="$(stk_state_value "$(stk_gds_agents_state_file)" codex_home)"
  if [[ -n "${installed_gds_codex_home}" && "${installed_gds_codex_home}" != "${codex_home}" ]]; then
    stk_uninstall_snapshot_add "${installed_gds_codex_home}/AGENTS.md" || return 1
    stk_uninstall_snapshot_add "${installed_gds_codex_home}/AGENTS.override.md" || return 1
  fi
}

stk_uninstall_supervisor_program_restore() {
  local name="$1" desired="$2" status_output
  status_output="$(stk_supervisor_call status "${name}" 2>/dev/null || true)"
  if (( desired )); then
    if [[ ! "${status_output}" =~ ^${name}[[:space:]]+(RUNNING|STARTING|BACKOFF) ]]; then
      stk_supervisor_call start "${name}" >/dev/null 2>&1
    fi
  elif [[ "${status_output}" =~ ^${name}[[:space:]]+(RUNNING|STARTING|BACKOFF) ]]; then
    stk_supervisor_call stop "${name}" >/dev/null 2>&1
  fi
}

stk_uninstall_direct_module_restore() {
  local module="$1" desired="$2" manager
  [[ "${desired}" != 'disabled' ]] || return 0
  manager="$(stk_module_field "${module}" 5)" || return 1
  case "${desired}" in
    running) "${manager}" restart >/dev/null 2>&1 || "${manager}" start >/dev/null 2>&1 ;;
    stopped)
      if "${manager}" status --quiet >/dev/null 2>&1; then "${manager}" stop >/dev/null 2>&1; fi
      ;;
    *) return 1 ;;
  esac
}

stk_uninstall_runtime_restore() {
  local result=0
  if [[ "${STK_UNINSTALL_SERVICE_BACKEND:-none}" == 'supervisord' &&
        "${STK_UNINSTALL_SERVICE_MANAGED:-0}" == '1' ]]; then
    stk_supervisor_reload || result=1
    stk_uninstall_supervisor_program_restore server-toolkit-proxy "${STK_UNINSTALL_SUPERVISOR_PROXY_ACTIVE:-0}" || result=1
    stk_uninstall_supervisor_program_restore server-toolkit-gds "${STK_UNINSTALL_SUPERVISOR_GDS_ACTIVE:-0}" || result=1
    stk_uninstall_supervisor_program_restore server-toolkit-maintenance "${STK_UNINSTALL_SUPERVISOR_MAINTENANCE_ACTIVE:-0}" || result=1
  elif [[ "${STK_UNINSTALL_SERVICE_BACKEND:-none}" == 'systemd' &&
          "${STK_UNINSTALL_SERVICE_MANAGED:-0}" == '1' ]]; then
    stk_systemd_runtime_restore \
      "${STK_UNINSTALL_SYSTEMD_SERVICE_ENABLED:-0}" "${STK_UNINSTALL_SYSTEMD_TIMER_ENABLED:-0}" \
      "${STK_UNINSTALL_SYSTEMD_SERVICE_ACTIVE:-0}" "${STK_UNINSTALL_SYSTEMD_TIMER_ACTIVE:-0}" || result=1
  else
    stk_uninstall_direct_module_restore proxy "${STK_UNINSTALL_PROXY_STATE:-disabled}" || result=1
    stk_uninstall_direct_module_restore gds "${STK_UNINSTALL_GDS_STATE:-disabled}" || result=1
  fi
  return "${result}"
}

stk_uninstall_rollback() {
  local result=0
  stk_uninstall_snapshot_restore || result=1
  stk_uninstall_runtime_restore || result=1
  return "${result}"
}

stk_uninstall_snapshot_finish() {
  [[ -z "${STK_UNINSTALL_SNAPSHOT:-}" ]] || rm -rf -- "${STK_UNINSTALL_SNAPSHOT}"
  STK_UNINSTALL_SNAPSHOT=''
  STK_UNINSTALL_SNAPSHOT_INDEX=0
}

stk_uninstall_rollback_or_retain() {
  if stk_uninstall_rollback; then
    stk_uninstall_snapshot_finish
    return 0
  fi
  stk_warn "卸载回滚未完成；恢复快照已保留：${STK_UNINSTALL_SNAPSHOT}"
  return 1
}

stk_uninstall_purge_data_paths() {
  printf '%s\n' \
    "${STK_ROOT}/runtime/python" \
    "${STK_ROOT}/proxy/core" "${STK_ROOT}/proxy/workdir" \
    "${STK_ROOT}/gds/core" "${STK_ROOT}/gds/workdir" \
    "${STK_ROOT}/workdir"
}

stk_uninstall_purge_data_validate() {
  local path relative component current
  local -a components
  stk_validate_absolute_path "${STK_ROOT}" >/dev/null 2>&1 || return 1
  [[ ! -L "${STK_ROOT}" ]] || { stk_fail "STK_ROOT 不能是符号链接：${STK_ROOT}"; return 1; }
  while IFS= read -r path; do
    [[ "${path}" == "${STK_ROOT}/"* && "${path}" != "${STK_ROOT}" ]] || {
      stk_fail "拒绝清理不安全的数据路径：${path}"
      return 1
    }
    relative="${path#"${STK_ROOT}/"}"
    current="${STK_ROOT}"
    IFS='/' read -r -a components <<<"${relative}"
    for component in "${components[@]}"; do
      [[ -n "${component}" && "${component}" != '.' && "${component}" != '..' ]] || return 1
      current="${current}/${component}"
      [[ ! -L "${current}" ]] || {
        stk_fail "拒绝经由符号链接清理数据：${current}"
        return 1
      }
    done
  done < <(stk_uninstall_purge_data_paths)
}

stk_uninstall_purge_data() {
  local path
  stk_uninstall_purge_data_validate || return 1
  while IFS= read -r path; do
    rm -rf -- "${path}" || return 1
  done < <(stk_uninstall_purge_data_paths)
}

stk_uninstall_managed_dir_safe() {
  local path="$1" distribution_key="$2" recorded
  stk_validate_absolute_path "${path}" >/dev/null 2>&1 || return 1
  [[ ! -L "${path}" ]] || return 1
  recorded="$(_stk_distribution_value "${distribution_key}")"
  [[ "${path}" == /*/server-toolkit || ( -n "${recorded}" && "${path}" == "${recorded}" ) ]]
}

stk_uninstall_release_root_safe() {
  local distribution recorded
  distribution="$(_stk_distribution_value distribution)"
  recorded="$(_stk_distribution_value stk_home)"
  [[ "${distribution}" == 'github-release' && "${recorded}" == "${STK_ROOT}" ]] || return 1
  stk_uninstall_managed_dir_safe "${STK_ROOT}" stk_home || return 1
  [[ -f "${STK_ROOT}/toolkit.sh" && ! -L "${STK_ROOT}/toolkit.sh" &&
     -f "${STK_ROOT}/VERSION" && ! -L "${STK_ROOT}/VERSION" ]]
}

stk_uninstall_plan() {
  local purge_config="$1" purge_state="$2" purge_data="$3" purge_home="${4:-0}"
  printf '%s\n' \
    '将移除：supervisord/systemd 托管配置、模块入口、stk 入口' \
    '将恢复：Git 代理、Git 身份、Codex 代理/CODEX_HOME、GitHub SSH 托管修改'
  (( purge_config )) && printf '将删除：%s\n' "${STK_CONFIG_DIR}"
  (( purge_state )) && printf '将删除：%s\n' "${STK_USER_STATE_DIR}"
  (( purge_data )) && printf '将删除：Python 私有环境、proxy/gds core、workdir 与工具包日志\n'
  (( purge_home )) && printf '将删除：整个 Release STK_HOME（%s）\n' "${STK_ROOT}"
  (( purge_config )) || printf '将保留：%s\n' "${STK_CONFIG_DIR}"
  (( purge_data )) || printf '将保留：Python 私有环境、模块 core 和运行数据\n'
}

stk_uninstall() {
  local dry_run="${1:-0}" purge_config="${2:-0}" purge_state="${3:-0}" purge_data="${4:-0}"
  local purge_home="${5:-0}"
  local record id command_name manager target resolved config backend failed=0 managed_codex_home
  stk_uninstall_plan "${purge_config}" "${purge_state}" "${purge_data}" "${purge_home}"
  (( dry_run == 0 )) || { stk_ok 'dry-run 完成，未修改任何文件或进程'; return 0; }

  # Validate every irreversible target before changing services, configuration or data.
  (( purge_data == 0 )) || stk_uninstall_purge_data_validate || return 1
  if (( purge_config )); then
    stk_uninstall_managed_dir_safe "${STK_CONFIG_DIR}" config_dir || {
      stk_fail '配置目录路径不安全或不属于当前安装'
      return 1
    }
  fi
  if (( purge_state )); then
    stk_uninstall_managed_dir_safe "${STK_USER_STATE_DIR}" state_dir || {
      stk_fail '状态目录路径不安全或不属于当前安装'
      return 1
    }
  fi
  if (( purge_home )) && ! stk_uninstall_release_root_safe; then
    stk_fail '仅能对元数据与当前路径完全匹配的 GitHub Release 安装执行 --purge-all'
    return 1
  fi

  stk_uninstall_snapshot_create || { stk_fail '无法创建卸载回滚快照'; return 1; }

  backend="$(stk_service_backend)"
  if [[ "${backend}" == 'supervisord' ]]; then
    config="$(stk_supervisor_config 2>/dev/null || true)"
    if [[ -n "${config}" ]] && stk_supervisor_managed_installed; then
      stk_service_remove || failed=1
    fi
  elif [[ "${backend}" == 'systemd' && -e "${STK_SYSTEMD_DIR}/server-toolkit.service" ]]; then
    stk_service_remove || failed=1
  fi
  (( failed == 0 )) || {
    stk_uninstall_rollback_or_retain || stk_warn '卸载失败，且外部配置或服务运行态未能完整恢复'
    return 1
  }

  if [[ -e "${STK_GIT_PROXY_STATE}" || -L "${STK_GIT_PROXY_STATE}" ]]; then
    stk_git_proxy_disable || failed=1
  fi
  if [[ -e "${STK_GIT_PROFILE_STATE}" || -L "${STK_GIT_PROFILE_STATE}" ]]; then
    stk_git_profile_remove || failed=1
  fi
  managed_codex_home="$(stk_codex_bashrc_home)"
  [[ -n "${managed_codex_home}" ]] || managed_codex_home="$(stk_config_value codex_home)"
  if stk_managed_block_exists "${managed_codex_home}/.env" "${STK_CODEX_PROXY_LABEL}"; then
    stk_codex_proxy_disable "${managed_codex_home}" || failed=1
  fi
  stk_codex_bashrc_remove || failed=1
  stk_codex_launcher_remove || failed=1
  stk_github_ssh_agent_remove || failed=1
  stk_github_known_hosts_remove || failed=1
  if [[ -e "${STK_GITHUB_SSH_STATE}" || -L "${STK_GITHUB_SSH_STATE}" ]] ||
     stk_managed_block_exists "${STK_SSH_CONFIG_FILE}" "${STK_GITHUB_SSH_LABEL}"; then
    stk_github_ssh_remove 0 || failed=1
  fi
  (( failed == 0 )) || {
    stk_uninstall_rollback_or_retain || stk_warn '卸载失败，且外部配置或服务运行态未能完整恢复'
    stk_fail '卸载中止；已尝试恢复卸载前快照'
    return 1
  }

  while IFS= read -r record; do
    id="${record%%|*}"
    command_name="$(stk_module_field "${id}" 4)"
    manager="$(stk_module_field "${id}" 5)"
    if [[ "$(stk_module_field "${id}" 8)" == 'setup' ]]; then
      "${manager}" remove || failed=1
    else
      if [[ "${backend}" == 'none' ]] && stk_module_is_enabled "${id}"; then
        "${manager}" stop || failed=1
      fi
      if [[ "${id}" == 'gds' ]]; then
        stk_gds_agents_remove || failed=1
      fi
      [[ "${command_name}" == '-' ]] || stk_remove_managed_entry "${command_name}" "${manager}"
    fi
  done < <(stk_module_catalog)
  target="$(stk_install_bin_dir)/stk"
  resolved="$(readlink -f -- "${target}" 2>/dev/null || true)"
  [[ -L "${target}" && "${resolved}" == "${STK_ROOT}/toolkit.sh" ]] && rm -f -- "${target}"
  (( failed == 0 )) || {
    stk_uninstall_rollback_or_retain || stk_warn '卸载失败，且外部配置或服务运行态未能完整恢复'
    stk_fail '模块移除失败；已尝试恢复外部配置，服务和入口可能需要重新安装'
    return 1
  }
  (( purge_data == 0 )) || stk_uninstall_purge_data || failed=1
  if (( purge_config )); then
    (( failed )) || rm -rf -- "${STK_CONFIG_DIR}" || failed=1
  fi
  if (( purge_state )); then
    (( failed )) || rm -rf -- "${STK_USER_STATE_DIR}" || failed=1
  fi
  if (( failed )); then
    stk_uninstall_rollback_or_retain || stk_warn '清理失败，且外部配置或服务运行态未能完整恢复'
    return 1
  fi
  stk_uninstall_snapshot_finish
  if (( purge_home )); then
    rm -rf -- "${STK_ROOT}" || return 1
  fi
  stk_ok '卸载完成；所有可识别的受管外部配置均已恢复'
}
