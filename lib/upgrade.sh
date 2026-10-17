#!/usr/bin/env bash

stk_upgrade_snapshot_state_value() {
  stk_state_value "$1/meta.conf" "$2"
}

stk_upgrade_snapshot_add_external_paths() {
  local codex_home launcher_path supervisor_config record command_name path installed_gds_codex_home
  codex_home="$(stk_codex_bashrc_home)"
  [[ -n "${codex_home}" ]] || codex_home="$(stk_config_value codex_home)"
  launcher_path="$(stk_state_value "${STK_CODEX_LAUNCHER_STATE}" launcher_path)"

  for path in \
    "${STK_BASHRC_FILE}" "${codex_home}/.env" \
    "${STK_SSH_CONFIG_FILE}" "${STK_GITHUB_KEY_PATH}" "${STK_GITHUB_KNOWN_HOSTS_FILE}" \
    "${STK_SUPERVISOR_STATE}" "${STK_SUPERVISOR_DROPIN}" \
    "${STK_SYSTEMD_DIR}/server-toolkit.service" \
    "${STK_SYSTEMD_DIR}/server-toolkit-health.service" \
    "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" "${STK_LOGROTATE_FILE}" \
    "$(stk_install_bin_dir)/stk"; do
    stk_transaction_add "${path}" || return 1
  done
  if [[ -n "${launcher_path}" ]]; then
    stk_transaction_add "${launcher_path}" || return 1
  fi
  while IFS= read -r record; do
    command_name="$(awk -F'|' '{print $4}' <<<"${record}")"
    [[ "${command_name}" == '-' ]] || stk_transaction_add "$(stk_install_bin_dir)/${command_name}" || return 1
  done < <(stk_module_catalog)
  stk_transaction_add "${codex_home}/AGENTS.md" || return 1
  stk_transaction_add "${codex_home}/AGENTS.override.md" || return 1
  installed_gds_codex_home="$(stk_state_value "$(stk_gds_agents_state_file)" codex_home)"
  if [[ -n "${installed_gds_codex_home}" && "${installed_gds_codex_home}" != "${codex_home}" ]]; then
    stk_transaction_add "${installed_gds_codex_home}/AGENTS.md" || return 1
    stk_transaction_add "${installed_gds_codex_home}/AGENTS.override.md" || return 1
  fi
  supervisor_config="$(stk_supervisor_config 2>/dev/null || true)"
  [[ -z "${supervisor_config}" ]] || stk_transaction_add "${supervisor_config}" || return 1
}

stk_upgrade_snapshot_create() {
  local destination="$1" backend managed=0 proxy_state='disabled' gds_state='disabled'
  stk_validate_absolute_path "${destination}" || return 1
  [[ ! -e "${destination}" && ! -L "${destination}" ]] || {
    stk_fail "升级快照目标已存在：${destination}"
    return 1
  }
  mkdir -p -- "${destination}/files" || return 1
  chmod 0700 "${destination}" "${destination}/files" || return 1
  STK_TRANSACTION_ROOT="${destination}/files"
  STK_TRANSACTION_COUNT=0
  STK_TRANSACTION_PATHS=$'\n'
  stk_upgrade_snapshot_add_external_paths || return 1

  backend="$(stk_service_backend)"
  if [[ "${backend}" == 'supervisord' ]] && stk_supervisor_managed_installed; then managed=1; fi
  if stk_module_is_enabled proxy; then
    if "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then proxy_state='running'; else proxy_state='stopped'; fi
  fi
  if stk_module_is_enabled gds; then
    if "${STK_GDS_SCRIPT}" status --quiet >/dev/null 2>&1; then gds_state='running'; else gds_state='stopped'; fi
  fi
  {
    printf 'schema_version=1\n'
    printf 'transaction_count=%s\n' "${STK_TRANSACTION_COUNT}"
    printf 'service_backend=%s\n' "${backend}"
    printf 'service_managed=%s\n' "${managed}"
    printf 'proxy_state=%s\n' "${proxy_state}"
    printf 'gds_state=%s\n' "${gds_state}"
  } | stk_atomic_write "${destination}/meta.conf" 0600
}

stk_upgrade_snapshot_load() {
  local destination="$1" count
  stk_validate_absolute_path "${destination}" || return 1
  [[ -d "${destination}" && ! -L "${destination}" &&
     -f "${destination}/meta.conf" && ! -L "${destination}/meta.conf" &&
     -d "${destination}/files" && ! -L "${destination}/files" ]] || return 1
  [[ "$(stk_upgrade_snapshot_state_value "${destination}" schema_version)" == '1' ]] || return 1
  count="$(stk_upgrade_snapshot_state_value "${destination}" transaction_count)"
  [[ "${count}" =~ ^[0-9]+$ ]] || return 1
  # shellcheck disable=SC2034 # Consumed by stk_transaction_restore from transaction.sh.
  STK_TRANSACTION_ROOT="${destination}/files"
  STK_TRANSACTION_COUNT="${count}"
  # shellcheck disable=SC2034 # Reset shared transaction bookkeeping.
  STK_TRANSACTION_PATHS=''
}

stk_upgrade_restore_files() {
  local destination="$1" backend managed
  stk_upgrade_snapshot_load "${destination}" || { stk_fail '升级快照无效'; return 1; }
  stk_transaction_restore || return 1
  backend="$(stk_upgrade_snapshot_state_value "${destination}" service_backend)"
  managed="$(stk_upgrade_snapshot_state_value "${destination}" service_managed)"
  if [[ "${backend}" == 'supervisord' && "${managed}" == '1' ]]; then
    stk_supervisor_reload || { stk_fail '外部文件已恢复，但 supervisord 运行态恢复失败'; return 1; }
  elif [[ "${backend}" == 'systemd' ]] && command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || return 1
  fi
}

stk_upgrade_restore_module_runtime() {
  local destination="$1" module="$2" desired manager
  desired="$(stk_upgrade_snapshot_state_value "${destination}" "${module}_state")"
  [[ "${desired}" != 'disabled' ]] || return 0
  manager="$(stk_module_field "${module}" 5)" || return 1
  case "${desired}" in
    running) stk_service_module_action_or_fallback restart "${module}" "${manager}" ;;
    stopped)
      if "${manager}" status --quiet >/dev/null 2>&1; then
        stk_service_module_action_or_fallback stop "${module}" "${manager}"
      fi
      ;;
    *) stk_fail "升级快照的 ${module} 状态无效"; return 1 ;;
  esac
}

stk_upgrade_restore_runtime() {
  local destination="$1" result=0
  stk_upgrade_snapshot_load "${destination}" || { stk_fail '升级快照无效'; return 1; }
  stk_upgrade_restore_module_runtime "${destination}" proxy || result=1
  stk_upgrade_restore_module_runtime "${destination}" gds || result=1
  return "${result}"
}

stk_upgrade_restore_all() {
  local destination="$1"
  stk_upgrade_restore_files "${destination}" || return 1
  stk_upgrade_restore_runtime "${destination}"
}
