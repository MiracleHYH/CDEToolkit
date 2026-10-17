#!/usr/bin/env bash

# id|display name|description|command|manager|installer|log|type
stk_module_catalog() {
  printf '%s\n' \
    "proxy|Proxy Dashboard|通过 pm 管理 mihomo 本地代理|pm|${STK_ROOT}/proxy/pm.sh|${STK_ROOT}/proxy/install.sh|${STK_ROOT}/proxy/workdir/mihomo.log|service" \
    "codex|Codex CLI|恢复或安装 Codex、迁移入口与代理配置|-|${STK_ROOT}/modules/codex/module.sh|${STK_ROOT}/modules/codex/install.sh|-|setup" \
    "gds|GPU Dynamic Scheduler|动态监控并保留空闲的 NVIDIA GPU|gds|${STK_ROOT}/gds/gds.sh|${STK_ROOT}/gds/install.sh|${STK_ROOT}/gds/workdir/gds.log|service" \
    "base-tools|Base Tools|常用检索、监控、归档与网络命令|-|${STK_ROOT}/modules/base-tools/module.sh|${STK_ROOT}/modules/base-tools/install.sh|-|setup" \
    "shell|Shell Convenience|Bash 历史、补全、fzf 与安全快捷命令|-|${STK_ROOT}/modules/shell/module.sh|${STK_ROOT}/modules/shell/install.sh|-|setup"
}

stk_module_record() {
  local requested="$1" record id
  while IFS= read -r record; do
    id="${record%%|*}"
    if [[ "${id}" == "${requested}" ]]; then
      printf '%s\n' "${record}"
      return 0
    fi
  done < <(stk_module_catalog)
  return 1
}

stk_module_field() {
  local requested="$1" field="$2" record
  record="$(stk_module_record "${requested}")" || return 1
  awk -F'|' -v field="${field}" '{ print $field }' <<<"${record}"
}

stk_module_is_enabled() {
  local id="$1" command_name manager type
  command_name="$(stk_module_field "${id}" 4)" || return 1
  manager="$(stk_module_field "${id}" 5)" || return 1
  type="$(stk_module_field "${id}" 8)" || return 1
  if [[ "${type}" == 'setup' || "${command_name}" == '-' ]]; then
    "${manager}" status --quiet >/dev/null 2>&1
  else
    stk_module_enabled "${command_name}" "${manager}"
  fi
}

stk_module_status_value() {
  local id="$1" manager type manager_status
  if ! stk_module_is_enabled "${id}"; then
    printf '未安装\n'
    return
  fi
  type="$(stk_module_field "${id}" 8)" || return 1
  if [[ "${type}" == 'setup' ]]; then
    printf '已配置\n'
    return
  fi
  manager="$(stk_module_field "${id}" 5)" || return 1
  manager_status="$("${manager}" status 2>/dev/null || true)"
  case "${manager_status}" in
    running) printf '运行中\n' ;;
    stopped) printf '已停止\n' ;;
    unhealthy|orphan|conflict) printf '异常\n' ;;
    *)
      if "${manager}" status --quiet >/dev/null 2>&1; then
        printf '运行中\n'
      else
        printf '已停止\n'
      fi
      ;;
  esac
}

stk_module_list() {
  local record id name description
  printf '%-12s %-24s %-10s %s\n' '模块' '名称' '状态' '说明'
  while IFS= read -r record; do
    IFS='|' read -r id name description _ <<<"${record}"
    printf '%-12s %-24s %-10s %s\n' "${id}" "${name}" "$(stk_module_status_value "${id}")" "${description}"
  done < <(stk_module_catalog)
}

stk_module_enabled_ids() {
  local record id
  while IFS= read -r record; do
    id="${record%%|*}"
    if stk_module_is_enabled "${id}"; then
      printf '%s\n' "${id}"
    fi
  done < <(stk_module_catalog)
  return 0
}

stk_gds_agents_label() {
  printf '%s\n' 'gds-gpu-cowork'
}

stk_gds_agents_source() {
  printf '%s/gds/global-agents.md\n' "${STK_ROOT}"
}

stk_gds_agents_state_file() {
  printf '%s/modules/gds.conf\n' "${STK_USER_STATE_DIR}"
}

stk_gds_agents_codex_home() {
  local codex_home
  codex_home="$(stk_codex_home)"
  stk_validate_absolute_path "${codex_home}" >/dev/null || return 1
  printf '%s\n' "${codex_home}"
}

stk_gds_agents_preflight() {
  local codex_home source label state_file installed_codex_home
  source="$(stk_gds_agents_source)"
  label="$(stk_gds_agents_label)"
  state_file="$(stk_gds_agents_state_file)"
  [[ -f "${source}" && ! -L "${source}" ]] || {
    stk_fail "缺少 GDS Codex 全局说明：${source}"
    return 1
  }
  codex_home="$(stk_gds_agents_codex_home)" || return 1
  stk_codex_validate_agents_files "${codex_home}" "${label}" || return 1
  if stk_module_state_valid "${state_file}"; then
    installed_codex_home="$(stk_state_value "${state_file}" codex_home)"
    stk_validate_absolute_path "${installed_codex_home}" >/dev/null 2>&1 || {
      stk_fail "GDS Codex 规则状态无效：${state_file}"
      return 1
    }
    if [[ "${installed_codex_home}" != "${codex_home}" ]]; then
      stk_fail 'GDS Codex 规则已安装到其他 CODEX_HOME；请先执行 stk module remove gds'
      return 1
    fi
  elif [[ -e "${state_file}" || -L "${state_file}" ]]; then
    stk_fail "GDS Codex 规则状态无效：${state_file}"
    return 1
  fi
}

stk_gds_agents_configure() {
  local codex_home source label state_file agents_file
  stk_gds_agents_preflight || return 1
  codex_home="$(stk_gds_agents_codex_home)" || return 1
  source="$(stk_gds_agents_source)"
  label="$(stk_gds_agents_label)"
  state_file="$(stk_gds_agents_state_file)"
  agents_file="$(stk_codex_install_agents_block "${codex_home}" "${label}" "${source}")" || return 1
  stk_module_state_write "${state_file}" \
    "codex_home=${codex_home}" "agents_file=${agents_file}" || return 1
  printf '✓ GDS 本地 GPU 协作规则已写入 %s\n' "${agents_file}"
}

stk_gds_agents_remove() {
  local current_home installed_home='' label state_file home seen=''
  local -a homes=()
  label="$(stk_gds_agents_label)"
  state_file="$(stk_gds_agents_state_file)"
  current_home="$(stk_gds_agents_codex_home)" || return 1
  if stk_module_state_valid "${state_file}"; then
    installed_home="$(stk_state_value "${state_file}" codex_home)"
    stk_validate_absolute_path "${installed_home}" >/dev/null 2>&1 || {
      stk_fail "GDS Codex 规则状态无效：${state_file}"
      return 1
    }
  elif [[ -e "${state_file}" || -L "${state_file}" ]]; then
    stk_fail "GDS Codex 规则状态无效：${state_file}"
    return 1
  fi
  for home in "${installed_home}" "${current_home}"; do
    [[ -n "${home}" && " ${seen} " != *" ${home} "* ]] || continue
    homes+=("${home}")
    seen+=" ${home}"
  done
  for home in "${homes[@]}"; do
    stk_codex_validate_agents_files "${home}" "${label}" || return 1
  done
  for home in "${homes[@]}"; do
    stk_codex_remove_agents_block "${home}" "${label}" || return 1
  done
  rm -f -- "${state_file}"
}

stk_module_transaction_begin() {
  local id="$1" command_name config proxy_config target codex_home installed_codex_home
  command_name="$(stk_module_field "${id}" 4)" || return 1
  stk_transaction_begin || return 1
  for target in \
    "${STK_CONFIG_FILE}" "${STK_STATE_META_FILE}" \
    "${STK_USER_STATE_DIR}/modules/${id}.conf"; do
    stk_transaction_add "${target}" || return 1
  done
  if [[ "${command_name}" != '-' ]]; then
    stk_transaction_add "$(stk_install_bin_dir)/${command_name}" || return 1
  fi
  case "${id}" in
    proxy)
      stk_transaction_add "${STK_PYTHON_RUNTIME_DIR}" || return 1
      proxy_config="$(stk_config_value proxy_config_path)"
      stk_transaction_add "${proxy_config}" || return 1
      stk_transaction_add "${proxy_config}.previous" || return 1
      ;;
    gds)
      stk_transaction_add "${STK_ROOT}/gds/core/scheduler" || return 1
      stk_transaction_add "${STK_ROOT}/gds/core/scheduler.source.sha256" || return 1
      codex_home="$(stk_gds_agents_codex_home)" || return 1
      stk_transaction_add "${codex_home}/AGENTS.md" || return 1
      stk_transaction_add "${codex_home}/AGENTS.override.md" || return 1
      installed_codex_home="$(stk_state_value "$(stk_gds_agents_state_file)" codex_home)"
      if [[ -n "${installed_codex_home}" && "${installed_codex_home}" != "${codex_home}" ]]; then
        stk_transaction_add "${installed_codex_home}/AGENTS.md" || return 1
        stk_transaction_add "${installed_codex_home}/AGENTS.override.md" || return 1
      fi
      ;;
    shell) stk_transaction_add "${STK_BASHRC_FILE}" || return 1 ;;
    base-tools)
      stk_transaction_add "$(stk_install_bin_dir)/fd" || return 1
      stk_transaction_add "$(stk_install_bin_dir)/bat" || return 1
      ;;
    codex) stk_codex_restore_snapshot_add "$(stk_codex_home)" || return 1 ;;
  esac
  if [[ "$(stk_module_field "${id}" 8)" == 'service' ]]; then
    stk_transaction_add "${STK_SUPERVISOR_STATE}" || return 1
    stk_transaction_add "${STK_SUPERVISOR_DROPIN}" || return 1
    config="$(stk_supervisor_config 2>/dev/null || true)"
    [[ -z "${config}" ]] || stk_transaction_add "${config}" || return 1
  fi
}

stk_module_install() {
  local id="$1" mode="${2:-install}" installer bin_dir config
  local -a installer_args=()
  [[ "${mode}" == 'install' || "${mode}" == 'migrate' ]] || return 2
  [[ "${mode}" != 'migrate' ]] || installer_args+=(--migrate)
  installer="$(stk_module_field "${id}" 6)" || { stk_fail "未知模块：${id}"; return 2; }
  [[ -x "${installer}" ]] || { stk_fail "模块安装器不可执行：${installer}"; return 1; }
  bin_dir="$(stk_install_bin_dir)"
  STK_CONFIG_LOCK_HELD=1 "${installer}" --preflight --prefix "${bin_dir}" "${installer_args[@]}" || return 1
  stk_module_transaction_begin "${id}" || { stk_transaction_finish; return 1; }
  if ! STK_CONFIG_LOCK_HELD=1 "${installer}" --prefix "${bin_dir}" "${installer_args[@]}"; then
    stk_transaction_rollback || stk_warn '模块安装失败，且快照未能完整恢复'
    return 1
  fi
  if [[ "$(stk_module_field "${id}" 8)" == 'service' && "$(stk_service_backend)" == 'supervisord' ]]; then
    config="$(stk_supervisor_config 2>/dev/null || true)"
    if [[ -n "${config}" ]] && stk_supervisor_managed_installed; then
      if ! stk_supervisor_install; then
        stk_transaction_rollback || stk_warn '服务配置失败，且模块快照未能完整恢复'
        stk_supervisor_reload >/dev/null 2>&1 || true
        return 1
      fi
    fi
  fi
  stk_transaction_finish
  if [[ "${mode}" == 'migrate' ]]; then
    stk_ok "模块 ${id} 已迁移到当前版本"
  else
    stk_ok "模块 ${id} 已安装"
  fi
}

stk_service_module_action_or_fallback() {
  local action="$1" id="$2" manager="$3" result
  stk_service_module_action "${action}" "${id}"
  result=$?
  if (( result == 0 )); then return 0; fi
  if (( result != 2 )); then return "${result}"; fi
  "${manager}" "${action}"
}

stk_module_start() {
  local id="$1" manager type
  manager="$(stk_module_field "${id}" 5)" || { stk_fail "未知模块：${id}"; return 2; }
  stk_module_is_enabled "${id}" || { stk_fail "模块 ${id} 未安装"; return 1; }
  type="$(stk_module_field "${id}" 8)"
  [[ "${type}" == 'service' ]] || { stk_fail "模块 ${id} 是配置模块，无需启动"; return 2; }
  stk_service_module_action_or_fallback start "${id}" "${manager}"
}

stk_module_stop() {
  local id="$1" manager type
  manager="$(stk_module_field "${id}" 5)" || { stk_fail "未知模块：${id}"; return 2; }
  stk_module_is_enabled "${id}" || { stk_fail "模块 ${id} 未安装"; return 1; }
  type="$(stk_module_field "${id}" 8)"
  [[ "${type}" == 'service' ]] || { stk_fail "模块 ${id} 是配置模块，无需停止"; return 2; }
  stk_service_module_action_or_fallback stop "${id}" "${manager}"
}

stk_module_restart() {
  local id="$1" manager type
  manager="$(stk_module_field "${id}" 5)" || { stk_fail "未知模块：${id}"; return 2; }
  stk_module_is_enabled "${id}" || { stk_fail "模块 ${id} 未安装"; return 1; }
  type="$(stk_module_field "${id}" 8)"
  [[ "${type}" == 'service' ]] || { stk_fail "模块 ${id} 是配置模块，无需重启"; return 2; }
  stk_service_module_action_or_fallback restart "${id}" "${manager}"
}

stk_logs() {
  local target="${1:-all}" follow="${2:-0}" lines="${3:-100}" log id record found=0
  local -a files=()
  [[ "${lines}" =~ ^[1-9][0-9]*$ ]] || { stk_fail '--lines 必须是正整数'; return 2; }
  case "${target}" in
    all)
      while IFS= read -r record; do
        log="$(awk -F'|' '{print $7}' <<<"${record}")"
        [[ "${log}" == /* ]] && files+=("${log}")
      done < <(stk_module_catalog)
      files+=("${STK_ROOT}/workdir/supervisor-maintenance.log")
      ;;
    service) files+=("${STK_ROOT}/workdir/supervisor-proxy.log" "${STK_ROOT}/workdir/supervisor-gds.log" "${STK_ROOT}/workdir/supervisor-maintenance.log") ;;
    *)
      log="$(stk_module_field "${target}" 7)" || { stk_fail "未知日志目标：${target}"; return 2; }
      files+=("${log}")
      ;;
  esac
  local -a existing=()
  for log in "${files[@]}"; do
    if [[ -f "${log}" && ! -L "${log}" ]]; then existing+=("${log}"); found=1; fi
  done
  (( found )) || { stk_fail "没有可读取的 ${target} 日志"; return 1; }
  if (( follow )); then tail -n "${lines}" -F -- "${existing[@]}"; else tail -n "${lines}" -- "${existing[@]}"; fi
}
