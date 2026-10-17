#!/usr/bin/env bash

STK_SYSTEMD_DIR="${STK_SYSTEMD_DIR:-/etc/systemd/system}"
STK_LOGROTATE_FILE="${STK_LOGROTATE_FILE:-/etc/logrotate.d/server-toolkit}"
STK_SUPERVISOR_LABEL='supervisord-service'
STK_SUPERVISOR_INCLUDE_LABEL='supervisord-include'
STK_SUPERVISOR_STATE="${STK_USER_STATE_DIR}/supervisord-service.conf"
STK_SUPERVISOR_DROPIN="${STK_CONFIG_DIR}/supervisord/server-toolkit.conf"
STK_SUPERVISOR_CONFIG_OVERRIDE="${STK_SUPERVISOR_CONFIG:-}"
STK_SUPERVISORCTL_OVERRIDE="${STK_SUPERVISORCTL:-}"

stk_service_gds_enabled() {
  stk_module_enabled gds "${STK_GDS_SCRIPT}"
}

stk_service_identifier_supported() {
  [[ "$1" =~ ^[A-Za-z_][A-Za-z0-9_.-]*[$]?$ ]]
}

stk_service_path_supported() {
  local value="$1" backslash=$'\\' single_quote=$'\047'
  stk_validate_absolute_path "${value}" >/dev/null 2>&1 || return 1
  [[ "${value}" != *[[:space:]]* && "${value}" != *'#'* &&
     "${value}" != *'%'* && "${value}" != *'&'* &&
     "${value}" != *"${backslash}"* && "${value}" != *'"'* &&
     "${value}" != *"${single_quote}"* && "${value}" != *';'* &&
     "${value}" != *','* ]]
}

stk_service_render() {
  local source="$1" target="$2" user="$3" home="$4" group="$5" temp
  if ! stk_service_path_supported "${STK_ROOT}" ||
     ! stk_service_path_supported "${home}" ||
     ! stk_service_identifier_supported "${user}" ||
     ! stk_service_identifier_supported "${group}"; then
    stk_fail '服务模板值包含不受支持的字符，拒绝生成配置'
    return 1
  fi
  temp="$(mktemp "${target}.XXXXXX")" || return 1
  if ! sed -e "s#@ROOT@#${STK_ROOT}#g" \
    -e "s#@USER@#${user}#g" -e "s#@HOME@#${home}#g" \
    -e "s#@GROUP@#${group}#g" "${source}" >"${temp}"; then
    rm -f -- "${temp}"
    return 1
  fi
  chmod 0644 "${temp}"
  mv -f -- "${temp}" "${target}"
}

stk_service_backend() {
  local configured
  configured="$(stk_config_value service_backend auto)"
  if [[ "${configured}" == 'supervisord' ]]; then
    if stk_supervisor_config >/dev/null 2>&1 && stk_supervisorctl >/dev/null 2>&1; then printf 'supervisord\n'; else printf 'none\n'; fi
    return
  elif [[ "${configured}" == 'systemd' ]]; then
    if command -v systemctl >/dev/null 2>&1 && [[ "$(ps -p 1 -o comm= 2>/dev/null)" == 'systemd' ]]; then printf 'systemd\n'; else printf 'none\n'; fi
    return
  fi
  if command -v systemctl >/dev/null 2>&1 && [[ "$(ps -p 1 -o comm= 2>/dev/null)" == 'systemd' ]]; then
    printf 'systemd\n'
  elif stk_supervisor_config >/dev/null 2>&1 && stk_supervisorctl >/dev/null 2>&1; then
    printf 'supervisord\n'
  else
    printf 'none\n'
  fi
}

stk_supervisor_config() {
  local -a arguments=()
  local index candidate
  if [[ -n "${STK_SUPERVISOR_CONFIG_OVERRIDE}" ]]; then
    [[ -f "${STK_SUPERVISOR_CONFIG_OVERRIDE}" ]] || return 1
    printf '%s\n' "${STK_SUPERVISOR_CONFIG_OVERRIDE}"
    return 0
  fi
  if [[ -r /proc/1/cmdline ]]; then
    mapfile -d '' -t arguments </proc/1/cmdline
    for index in "${!arguments[@]}"; do
      if [[ "${arguments[index]}" == '-c' && -n "${arguments[index+1]:-}" ]]; then
        candidate="${arguments[index+1]}"
        [[ -f "${candidate}" ]] || return 1
        printf '%s\n' "${candidate}"
        return 0
      fi
    done
  fi
  for candidate in /etc/supervisor/supervisord.conf /etc/supervisord.conf; do
    if [[ -f "${candidate}" ]]; then printf '%s\n' "${candidate}"; return 0; fi
  done
  return 1
}

stk_supervisorctl() {
  local -a arguments=()
  local argument candidate
  if [[ -n "${STK_SUPERVISORCTL_OVERRIDE}" ]]; then
    [[ -x "${STK_SUPERVISORCTL_OVERRIDE}" ]] || return 1
    printf '%s\n' "${STK_SUPERVISORCTL_OVERRIDE}"
    return 0
  fi
  candidate="$(command -v supervisorctl 2>/dev/null || true)"
  if [[ -n "${candidate}" ]]; then printf '%s\n' "${candidate}"; return 0; fi
  if [[ -r /proc/1/cmdline ]]; then
    mapfile -d '' -t arguments </proc/1/cmdline
    for argument in "${arguments[@]}"; do
      if [[ "${argument}" == *supervisord* ]]; then
        candidate="$(dirname -- "${argument}")/supervisorctl"
        if [[ -x "${candidate}" ]]; then printf '%s\n' "${candidate}"; return 0; fi
      fi
    done
  fi
  return 1
}

stk_supervisor_call() {
  local controller config
  controller="$(stk_supervisorctl)" || { stk_fail '找不到 supervisorctl'; return 1; }
  config="$(stk_supervisor_config)" || { stk_fail '找不到 supervisord 配置'; return 1; }
  "${controller}" -c "${config}" "$@"
}

stk_supervisor_reload() {
  local output=''
  for _ in {1..30}; do
    if output="$(stk_supervisor_call reread 2>&1)" &&
      output+=$'\n'"$(stk_supervisor_call update 2>&1)"; then
      printf '%s\n' "${output}" | awk 'NF'
      return 0
    fi
    sleep 0.5
  done
  printf '%s\n' "${output}" >&2
  return 1
}

stk_supervisor_reload_once() {
  stk_supervisor_call reread >/dev/null 2>&1 &&
    stk_supervisor_call update >/dev/null 2>&1
}

stk_supervisor_program_content() {
  local service_user="$1" service_home="$2" module manager
  for module in proxy gds; do
    if [[ "${module}" == 'proxy' ]]; then manager="${STK_PM_SCRIPT}"; else manager="${STK_GDS_SCRIPT}"; fi
    stk_module_enabled "$([[ "${module}" == 'proxy' ]] && printf 'pm' || printf 'gds')" "${manager}" || continue
    printf '%s\n' \
      "[program:server-toolkit-${module}]" \
      "command=${manager} $([[ "${module}" == 'proxy' ]] && printf '__run_core' || printf '__run_monitor')" \
      "directory=${STK_ROOT}" \
      "user=${service_user}" \
      "environment=HOME=\"${service_home}\",STK_SERVICE_MODE=\"direct\"" \
      'autostart=true' \
      'autorestart=unexpected' \
      'startsecs=5' \
      'startretries=10' \
      'stopsignal=TERM' \
      'stopwaitsecs=40' \
      'stopasgroup=true' \
      'killasgroup=true' \
      'redirect_stderr=true' \
      "stdout_logfile=${STK_ROOT}/workdir/supervisor-${module}.log" \
      'stdout_logfile_maxbytes=5MB' \
      'stdout_logfile_backups=3' \
      'priority=50' \
      ''
  done
  printf '%s\n' \
    '[program:server-toolkit-maintenance]' \
    "command=${STK_ROOT}/scripts/log-maintenance.sh loop" \
    "directory=${STK_ROOT}" \
    "user=${service_user}" \
    "environment=HOME=\"${service_home}\"" \
    'autostart=true' \
    'autorestart=true' \
    'startsecs=2' \
    'startretries=10' \
    'stopsignal=TERM' \
    'stopasgroup=true' \
    'killasgroup=true' \
    'redirect_stderr=true' \
    "stdout_logfile=${STK_ROOT}/workdir/supervisor-maintenance.log" \
    'stdout_logfile_maxbytes=1MB' \
    'stdout_logfile_backups=2' \
    'priority=60'
}

stk_supervisor_managed_config() {
  local configured
  if stk_supervisor_state_load >/dev/null 2>&1; then
    if [[ "${STK_SUPERVISOR_STATE_LAYOUT}" == 'drop-in' ]] &&
       stk_supervisor_include_targets_dropin "${STK_SUPERVISOR_STATE_CONFIG}"; then
      printf '%s\n' "${STK_SUPERVISOR_DROPIN}"
      return 0
    elif [[ "${STK_SUPERVISOR_STATE_LAYOUT}" == 'main-config' ]] &&
         stk_managed_block_exists "${STK_SUPERVISOR_STATE_CONFIG}" "${STK_SUPERVISOR_LABEL}"; then
      printf '%s\n' "${STK_SUPERVISOR_STATE_CONFIG}"
      return 0
    fi
  fi
  configured="$(stk_supervisor_config 2>/dev/null || true)"
  [[ -n "${configured}" ]] || return 1
  if stk_managed_block_exists "${configured}" "${STK_SUPERVISOR_LABEL}"; then
    printf '%s\n' "${configured}"
    return 0
  fi
  if stk_supervisor_include_targets_dropin "${configured}"; then
    printf '%s\n' "${STK_SUPERVISOR_DROPIN}"
    return 0
  fi
  return 1
}

stk_supervisor_include_targets_dropin() {
  local config="$1" begin end expected
  begin="# >>> server-toolkit ${STK_SUPERVISOR_INCLUDE_LABEL} >>>"
  end="# <<< server-toolkit ${STK_SUPERVISOR_INCLUDE_LABEL} <<<"
  expected="files = ${STK_SUPERVISOR_DROPIN}"
  [[ -f "${config}" && ! -L "${config}" &&
     -f "${STK_SUPERVISOR_DROPIN}" && ! -L "${STK_SUPERVISOR_DROPIN}" ]] || return 1
  grep -Fqx '[program:server-toolkit-maintenance]' "${STK_SUPERVISOR_DROPIN}" || return 1
  awk -v begin="${begin}" -v end="${end}" -v expected="${expected}" '
    $0 == begin { inside=1; next }
    $0 == end { inside=0; next }
    inside && $0 == expected { found=1 }
    END { exit found ? 0 : 1 }
  ' "${config}"
}

stk_supervisor_state_load() {
  local config layout program_config actual_config backend
  stk_module_state_valid "${STK_SUPERVISOR_STATE}" || return 1
  backend="$(stk_state_value "${STK_SUPERVISOR_STATE}" backend)"
  config="$(stk_state_value "${STK_SUPERVISOR_STATE}" config)"
  layout="$(stk_state_value "${STK_SUPERVISOR_STATE}" layout)"
  program_config="$(stk_state_value "${STK_SUPERVISOR_STATE}" program_config)"
  actual_config="$(stk_supervisor_config 2>/dev/null || true)"
  [[ "${backend}" == 'supervisord' ]] || return 1
  [[ "${layout}" == 'main-config' || "${layout}" == 'drop-in' ]] || return 1
  [[ -n "${actual_config}" && "${config}" == "${actual_config}" &&
     -f "${config}" && ! -L "${config}" ]] || return 1
  stk_service_path_supported "${config}" || return 1
  if [[ "${layout}" == 'drop-in' ]]; then
    [[ "${program_config}" == "${STK_SUPERVISOR_DROPIN}" &&
       -f "${program_config}" && ! -L "${program_config}" ]] || return 1
    stk_service_path_supported "${program_config}" || return 1
  else
    [[ -z "${program_config}" ]] || return 1
  fi
  STK_SUPERVISOR_STATE_CONFIG="${config}"
  STK_SUPERVISOR_STATE_LAYOUT="${layout}"
  STK_SUPERVISOR_STATE_PROGRAM_CONFIG="${program_config}"
}

stk_supervisor_managed_installed() {
  stk_supervisor_managed_config >/dev/null 2>&1
}

stk_service_user_resolve() {
  local configured
  configured="$(stk_config_value service_user)"
  STK_SERVICE_USER_RESOLVED="${configured:-$(id -un)}"
  id "${STK_SERVICE_USER_RESOLVED}" >/dev/null 2>&1 || {
    stk_fail "服务运行用户不存在：${STK_SERVICE_USER_RESOLVED}"
    return 1
  }
  STK_SERVICE_HOME_RESOLVED="$(getent passwd "${STK_SERVICE_USER_RESOLVED}" | awk -F: 'NR==1 {print $6}')"
  [[ -n "${STK_SERVICE_HOME_RESOLVED}" ]] || {
    stk_fail "无法确定 ${STK_SERVICE_USER_RESOLVED} 的 HOME"
    return 1
  }
  if ! stk_service_identifier_supported "${STK_SERVICE_USER_RESOLVED}" ||
     ! stk_service_path_supported "${STK_SERVICE_HOME_RESOLVED}"; then
    stk_fail "服务用户或 HOME 包含不受支持的字符：${STK_SERVICE_USER_RESOLVED}"
    return 1
  fi
}

stk_service_user_test() {
  local service_user="$1"
  shift
  if [[ "${service_user}" == "$(id -un)" ]]; then
    "$@"
  elif [[ ${EUID} -eq 0 ]] && command -v runuser >/dev/null 2>&1; then
    runuser -u "${service_user}" -- "$@"
  else
    stk_fail "无法以服务用户 ${service_user} 验证路径权限（缺少 runuser 或权限不足）"
    return 1
  fi
}

stk_service_user_access_validate() {
  local service_user="$1" path
  for path in "${STK_ROOT}" "${STK_ROOT}/scripts"; do
    stk_service_user_test "${service_user}" test -x "${path}" || {
      stk_fail "服务用户 ${service_user} 无法进入：${path}"
      return 1
    }
  done
  for path in "${STK_PM_SCRIPT}" "${STK_GDS_SCRIPT}" "${STK_ROOT}/scripts/log-maintenance.sh" "${STK_ROOT}/scripts/service-start.sh"; do
    stk_service_user_test "${service_user}" test -x "${path}" || {
      stk_fail "服务用户 ${service_user} 无法执行：${path}"
      return 1
    }
  done
  stk_service_user_test "${service_user}" test -w "${STK_ROOT}/workdir" || {
    stk_fail "服务用户 ${service_user} 无法写入：${STK_ROOT}/workdir"
    return 1
  }
}

stk_supervisor_install() {
  local config service_user service_home content backup dropin_backup='' layout='drop-in' include_content restore_result=0
  local dropin_existed=0
  [[ ${EUID} -eq 0 ]] || { stk_fail '修改 supervisord 主配置需要 root 权限'; return 1; }
  config="$(stk_supervisor_config)" || { stk_fail '找不到 supervisord 主配置'; return 1; }
  [[ -f "${config}" && ! -L "${config}" ]] || {
    stk_fail "supervisord 主配置必须是普通非符号链接文件：${config}"
    return 1
  }
  [[ -w "${config}" ]] || { stk_fail "supervisord 配置不可写：${config}"; return 1; }
  if ! stk_service_path_supported "${STK_ROOT}" ||
     ! stk_service_path_supported "${STK_CONFIG_DIR}" ||
     ! stk_service_path_supported "${config}"; then
    stk_fail 'supervisord 安装路径包含不受支持的字符'
    return 1
  fi
  stk_service_user_resolve || return 1
  service_user="${STK_SERVICE_USER_RESOLVED}"
  service_home="${STK_SERVICE_HOME_RESOLVED}"
  if grep -Eq '^\[program:server-toolkit-(proxy|gds|maintenance)\]$' "${config}" &&
    ! stk_managed_block_exists "${config}" "${STK_SUPERVISOR_LABEL}"; then
    stk_fail '发现非本项目托管的同名 supervisord program，拒绝覆盖'
    return 1
  fi
  if [[ -L "${STK_USER_STATE_DIR}" || -L "${STK_SUPERVISOR_STATE}" ]]; then
    stk_fail 'supervisord 状态目录和状态文件不能是符号链接'
    return 1
  fi
  mkdir -p -- "${STK_USER_STATE_DIR}" "${STK_ROOT}/workdir"
  chmod 0700 "${STK_USER_STATE_DIR}" "${STK_ROOT}/workdir"
  chmod 0755 "${STK_ROOT}/scripts/log-maintenance.sh" "${STK_ROOT}/scripts/service-start.sh"
  stk_service_user_access_validate "${service_user}" || return 1
  if stk_proxy_enabled; then
    stk_service_user_test "${service_user}" env HOME="${service_home}" STK_SERVICE_MODE=direct \
      "${STK_PM_SCRIPT}" __prepare_service || {
      stk_fail 'Proxy 服务预检失败，未修改 supervisord 配置'
      return 1
    }
  fi
  if stk_service_gds_enabled; then
    stk_service_user_test "${service_user}" env HOME="${service_home}" STK_SERVICE_MODE=direct \
      "${STK_GDS_SCRIPT}" __prepare_service || {
      stk_fail 'GDS 服务预检失败，未修改 supervisord 配置'
      return 1
    }
  fi
  backup="$(mktemp "$(dirname -- "${config}")/.supervisord.conf.XXXXXX")" || return 1
  cp -p -- "${config}" "${backup}" || { rm -f -- "${backup}"; return 1; }
  content="$(stk_supervisor_program_content "${service_user}" "${service_home}")"
  if grep -Eq '^\[include\][[:space:]]*$' "${config}" &&
     ! stk_managed_block_exists "${config}" "${STK_SUPERVISOR_INCLUDE_LABEL}"; then
    layout='main-config'
    stk_warn '主配置已有外部 [include] 段，无法安全改写；继续使用受管主配置块'
    if ! stk_rewrite_managed_block "${config}" "${STK_SUPERVISOR_LABEL}" "${content}"; then
      rm -f -- "${backup}"
      return 1
    fi
  else
    mkdir -p -- "$(dirname -- "${STK_SUPERVISOR_DROPIN}")" || { rm -f -- "${backup}"; return 1; }
    chmod 0700 "$(dirname -- "${STK_SUPERVISOR_DROPIN}")"
    if [[ -e "${STK_SUPERVISOR_DROPIN}" ]]; then
      dropin_existed=1
      [[ -f "${STK_SUPERVISOR_DROPIN}" && ! -L "${STK_SUPERVISOR_DROPIN}" ]] || {
        rm -f -- "${backup}"; stk_fail 'supervisord drop-in 必须是普通非符号链接文件'; return 1;
      }
      dropin_backup="$(mktemp "$(dirname -- "${STK_SUPERVISOR_DROPIN}")/.supervisor-dropin.XXXXXX")" || { rm -f -- "${backup}"; return 1; }
      cp -p -- "${STK_SUPERVISOR_DROPIN}" "${dropin_backup}" || { rm -f -- "${backup}" "${dropin_backup}"; return 1; }
    fi
    printf '%s\n' "${content}" | stk_atomic_write "${STK_SUPERVISOR_DROPIN}" 0600 || { rm -f -- "${backup}" "${dropin_backup}"; return 1; }
    if ! stk_rewrite_managed_block "${config}" "${STK_SUPERVISOR_LABEL}" ''; then
      if (( dropin_existed )); then mv -f -- "${dropin_backup}" "${STK_SUPERVISOR_DROPIN}"; else rm -f -- "${STK_SUPERVISOR_DROPIN}"; fi
      rm -f -- "${backup}"
      return 1
    fi
    include_content="[include]
files = ${STK_SUPERVISOR_DROPIN}"
    if ! stk_rewrite_managed_block "${config}" "${STK_SUPERVISOR_INCLUDE_LABEL}" "${include_content}"; then
      mv -f -- "${backup}" "${config}"
      if (( dropin_existed )); then mv -f -- "${dropin_backup}" "${STK_SUPERVISOR_DROPIN}"; else rm -f -- "${STK_SUPERVISOR_DROPIN}"; fi
      return 1
    fi
  fi
  if ! stk_supervisor_reload; then
    restore_result=0
    mv -f -- "${backup}" "${config}" || restore_result=1
    if [[ -n "${dropin_backup}" ]]; then mv -f -- "${dropin_backup}" "${STK_SUPERVISOR_DROPIN}" || restore_result=1; else rm -f -- "${STK_SUPERVISOR_DROPIN}" || restore_result=1; fi
    stk_supervisor_reload_once || restore_result=1
    if (( restore_result == 0 )); then
      stk_fail 'supervisord 载入失败，已恢复原配置与运行态'
    else
      stk_fail 'supervisord 载入失败；已尝试恢复文件，但运行态仍需手动检查'
    fi
    return 1
  fi
  if ! {
    printf 'schema_version=%s\n' "${STK_STATE_SCHEMA_VERSION}"
    printf 'backend=supervisord\n'
    printf 'config=%s\n' "${config}"
    printf 'layout=%s\n' "${layout}"
    [[ "${layout}" == 'drop-in' ]] && printf 'program_config=%s\n' "${STK_SUPERVISOR_DROPIN}"
    printf 'service_user=%s\n' "${service_user}"
  } | stk_atomic_write "${STK_SUPERVISOR_STATE}" 0600; then
    restore_result=0
    mv -f -- "${backup}" "${config}" || restore_result=1
    if [[ -n "${dropin_backup}" ]]; then mv -f -- "${dropin_backup}" "${STK_SUPERVISOR_DROPIN}" || restore_result=1; else rm -f -- "${STK_SUPERVISOR_DROPIN}" || restore_result=1; fi
    stk_supervisor_reload_once || restore_result=1
    if (( restore_result == 0 )); then
      stk_fail 'supervisord 状态写入失败，已恢复原配置与运行态'
    else
      stk_fail 'supervisord 状态写入失败；已尝试恢复文件，但运行态仍需手动检查'
    fi
    return 1
  fi
  rm -f -- "${backup}" "${dropin_backup}"
  stk_ok 'supervisord 托管与容器内日志轮转已启用'
  if [[ "${layout}" == 'drop-in' ]]; then
    stk_ok "模块配置已持久化到 ${STK_SUPERVISOR_DROPIN}，主配置仅保留 include"
  else
    stk_ok "启动配置已持久化到 ${config}；supervisord 下次启动会自动加载"
  fi
}

stk_supervisor_program_exists() {
  local module="$1" config
  config="$(stk_supervisor_managed_config 2>/dev/null || true)"
  [[ -n "${config}" ]] && grep -Fqx "[program:server-toolkit-${module}]" "${config}"
}

stk_service_module_action() {
  local action="$1" module="$2"
  if [[ "$(stk_service_backend)" == 'supervisord' ]] && stk_supervisor_program_exists "${module}"; then
    stk_supervisor_call "${action}" "server-toolkit-${module}"
    return
  fi
  return 2
}

stk_supervisor_remove() {
  local config backup name recovery_name status_output layout program_config program_backup='' restore_result=0 desired
  local -a program_names=(server-toolkit-proxy server-toolkit-gds server-toolkit-maintenance)
  local -a stopped_names=()
  local -A was_active=()
  [[ ${EUID} -eq 0 ]] || { stk_fail '修改 supervisord 主配置需要 root 权限'; return 1; }
  if [[ -e "${STK_SUPERVISOR_STATE}" || -L "${STK_SUPERVISOR_STATE}" ]]; then
    if ! stk_supervisor_state_load; then
      stk_fail "supervisord 状态无效，拒绝依据该状态删除配置：${STK_SUPERVISOR_STATE}"
      return 1
    fi
    config="${STK_SUPERVISOR_STATE_CONFIG}"
    layout="${STK_SUPERVISOR_STATE_LAYOUT}"
    program_config="${STK_SUPERVISOR_STATE_PROGRAM_CONFIG}"
  else
    config="$(stk_supervisor_config)" || return 1
    layout='main-config'
    program_config=''
    if stk_supervisor_include_targets_dropin "${config}"; then
      layout='drop-in'
      program_config="${STK_SUPERVISOR_DROPIN}"
    fi
  fi
  [[ -f "${config}" && ! -L "${config}" ]] || { stk_fail "supervisord 主配置必须是普通非符号链接文件：${config}"; return 1; }
  if ! stk_managed_block_exists "${config}" "${STK_SUPERVISOR_LABEL}" &&
     ! stk_managed_block_exists "${config}" "${STK_SUPERVISOR_INCLUDE_LABEL}"; then
    stk_warn '未找到 server-toolkit 的 supervisord 托管块'
    return 0
  fi
  if [[ "${layout}" == 'drop-in' ]]; then
    [[ -f "${program_config}" && ! -L "${program_config}" ]] || {
      stk_fail "supervisord drop-in 必须是普通非符号链接文件：${program_config}"
      return 1
    }
  fi
  backup="$(mktemp "$(dirname -- "${config}")/.supervisord.remove.XXXXXX")" || return 1
  cp -p -- "${config}" "${backup}" || { rm -f -- "${backup}"; return 1; }
  if [[ "${layout}" == 'drop-in' ]]; then
    program_backup="$(mktemp "$(dirname -- "${program_config}")/.supervisor-remove.XXXXXX")" || { rm -f -- "${backup}"; return 1; }
    cp -p -- "${program_config}" "${program_backup}" || { rm -f -- "${backup}" "${program_backup}"; return 1; }
  fi
  for name in "${program_names[@]}"; do
    status_output="$(stk_supervisor_call status "${name}" 2>/dev/null || true)"
    if [[ "${status_output}" =~ ^${name}[[:space:]]+(RUNNING|STARTING|BACKOFF) ]]; then
      was_active["${name}"]=1
      stopped_names+=("${name}")
      if ! stk_supervisor_call stop "${name}"; then
        for recovery_name in "${stopped_names[@]}"; do stk_supervisor_call start "${recovery_name}" >/dev/null 2>&1 || true; done
        rm -f -- "${backup}" "${program_backup}"
        return 1
      fi
    else
      was_active["${name}"]=0
    fi
  done
  if ! stk_rewrite_managed_block "${config}" "${STK_SUPERVISOR_LABEL}" '' ||
     ! stk_rewrite_managed_block "${config}" "${STK_SUPERVISOR_INCLUDE_LABEL}" '' ||
     ! { [[ "${layout}" != 'drop-in' ]] || rm -f -- "${program_config}"; } ||
     ! stk_supervisor_call reread || ! stk_supervisor_call update; then
    restore_result=0
    mv -f -- "${backup}" "${config}" || restore_result=1
    [[ -z "${program_backup}" ]] || mv -f -- "${program_backup}" "${program_config}" || restore_result=1
    stk_supervisor_reload_once || restore_result=1
    for name in "${program_names[@]}"; do
      desired="${was_active[${name}]:-0}"
      status_output="$(stk_supervisor_call status "${name}" 2>/dev/null || true)"
      if [[ "${desired}" == '1' ]]; then
        if [[ ! "${status_output}" =~ ^${name}[[:space:]]+(RUNNING|STARTING|BACKOFF) ]]; then
          stk_supervisor_call start "${name}" >/dev/null 2>&1 || restore_result=1
        fi
      elif [[ "${status_output}" =~ ^${name}[[:space:]]+(RUNNING|STARTING|BACKOFF) ]]; then
        stk_supervisor_call stop "${name}" >/dev/null 2>&1 || restore_result=1
      fi
    done
    if (( restore_result == 0 )); then
      stk_fail '移除 supervisord 配置失败，已恢复主配置与运行态'
    else
      stk_fail '移除 supervisord 配置失败；已尝试恢复文件，但运行态仍需手动检查'
    fi
    return 1
  fi
  rm -f -- "${backup}"
  rm -f -- "${program_backup}"
  rm -f -- "${STK_SUPERVISOR_STATE}"
  stk_ok 'server-toolkit supervisord 托管配置已移除'
}

stk_supervisor_status() {
  local config output managed_config
  config="$(stk_supervisor_config 2>/dev/null || true)"
  printf '服务后端：supervisord\n'
  printf '主配置：%s\n' "${config:-未知}"
  managed_config="$(stk_supervisor_managed_config 2>/dev/null || true)"
  if [[ -n "${managed_config}" ]]; then
    printf '托管配置：已安装（%s）\n' "${managed_config}"
    output="$(stk_supervisor_call status 2>/dev/null || true)"
    printf '%s\n' "${output}" | awk '$1 ~ /^server-toolkit-/ { print "  " $0 }'
  else
    printf '托管配置：未安装\n'
  fi
}

stk_service_require_systemd() {
  [[ "$(stk_service_backend)" == 'systemd' ]] || { stk_fail '当前服务后端不是 systemd'; return 1; }
  [[ ${EUID} -eq 0 ]] || { stk_fail '安装系统服务需要 root 权限'; return 1; }
}

stk_systemd_snapshot_create() {
  local target slot
  STK_SYSTEMD_SNAPSHOT="$(mktemp -d "${TMPDIR:-/tmp}/server-toolkit-systemd.XXXXXX")" || return 1
  STK_SYSTEMD_SNAPSHOT_COUNT=0
  for target in \
    "${STK_SYSTEMD_DIR}/server-toolkit.service" \
    "${STK_SYSTEMD_DIR}/server-toolkit-health.service" \
    "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" \
    "${STK_LOGROTATE_FILE}"; do
    STK_SYSTEMD_SNAPSHOT_COUNT=$((STK_SYSTEMD_SNAPSHOT_COUNT + 1))
    slot="${STK_SYSTEMD_SNAPSHOT}/${STK_SYSTEMD_SNAPSHOT_COUNT}"
    printf '%s\n' "${target}" >"${slot}.path"
    if [[ -e "${target}" || -L "${target}" ]]; then
      [[ -f "${target}" && ! -L "${target}" ]] || {
        stk_fail "systemd 受管目标必须是普通非符号链接文件：${target}"
        rm -rf -- "${STK_SYSTEMD_SNAPSHOT}"
        return 1
      }
      printf 'present\n' >"${slot}.kind"
      cp -p -- "${target}" "${slot}.data" || { rm -rf -- "${STK_SYSTEMD_SNAPSHOT}"; return 1; }
    else
      printf 'absent\n' >"${slot}.kind"
    fi
  done
}

stk_systemd_snapshot_restore() {
  local index slot target kind result=0
  for ((index=1; index<=STK_SYSTEMD_SNAPSHOT_COUNT; index++)); do
    slot="${STK_SYSTEMD_SNAPSHOT}/${index}"
    [[ -f "${slot}.path" && ! -L "${slot}.path" &&
       -f "${slot}.kind" && ! -L "${slot}.kind" ]] || return 1
    target="$(<"${slot}.path")"
    kind="$(<"${slot}.kind")"
    stk_validate_absolute_path "${target}" >/dev/null 2>&1 || return 1
    case "${kind}" in
      present) [[ -f "${slot}.data" && ! -L "${slot}.data" ]] || return 1 ;;
      absent) [[ ! -e "${slot}.data" && ! -L "${slot}.data" ]] || return 1 ;;
      *) return 1 ;;
    esac
  done
  for ((index=STK_SYSTEMD_SNAPSHOT_COUNT; index>=1; index--)); do
    slot="${STK_SYSTEMD_SNAPSHOT}/${index}"
    target="$(<"${slot}.path")"
    kind="$(<"${slot}.kind")"
    rm -f -- "${target}" || { result=1; continue; }
    if [[ "${kind}" == 'present' ]]; then
      mkdir -p -- "$(dirname -- "${target}")" || { result=1; continue; }
      cp -p -- "${slot}.data" "${target}" || result=1
    fi
  done
  return "${result}"
}

stk_systemd_snapshot_finish() {
  [[ -z "${STK_SYSTEMD_SNAPSHOT:-}" ]] || rm -rf -- "${STK_SYSTEMD_SNAPSHOT}"
  STK_SYSTEMD_SNAPSHOT=''
  STK_SYSTEMD_SNAPSHOT_COUNT=0
}

stk_systemd_unit_enabled() {
  [[ "$(systemctl is-enabled "$1" 2>/dev/null || true)" == 'enabled' ]]
}

stk_systemd_unit_active() {
  [[ "$(systemctl is-active "$1" 2>/dev/null || true)" == 'active' ]]
}

stk_systemd_runtime_restore() {
  local service_enabled="$1" timer_enabled="$2" service_active="$3" timer_active="$4" result=0
  systemctl disable --now server-toolkit-health.timer server-toolkit.service >/dev/null 2>&1 || true
  systemctl daemon-reload || result=1
  (( service_enabled == 0 )) || systemctl enable server-toolkit.service || result=1
  (( timer_enabled == 0 )) || systemctl enable server-toolkit-health.timer || result=1
  (( service_active == 0 )) || systemctl start server-toolkit.service || result=1
  (( timer_active == 0 )) || systemctl start server-toolkit-health.timer || result=1
  return "${result}"
}

stk_systemd_rollback() {
  local service_enabled="$1" timer_enabled="$2" service_active="$3" timer_active="$4" result=0
  stk_systemd_snapshot_restore || result=1
  stk_systemd_runtime_restore "${service_enabled}" "${timer_enabled}" "${service_active}" "${timer_active}" || result=1
  if (( result == 0 )); then
    stk_systemd_snapshot_finish
  else
    stk_warn "systemd 回滚未完成；恢复快照已保留：${STK_SYSTEMD_SNAPSHOT}"
  fi
  return "${result}"
}

stk_systemd_targets_owned() {
  local service health timer logrotate target
  service="${STK_SYSTEMD_DIR}/server-toolkit.service"
  health="${STK_SYSTEMD_DIR}/server-toolkit-health.service"
  timer="${STK_SYSTEMD_DIR}/server-toolkit-health.timer"
  logrotate="${STK_LOGROTATE_FILE}"
  for target in "${service}" "${health}" "${timer}" "${logrotate}"; do
    [[ ! -e "${target}" && ! -L "${target}" ]] ||
      [[ -f "${target}" && ! -L "${target}" ]] || return 1
  done
  if [[ -e "${service}" ]] && ! grep -Fqx "WorkingDirectory=${STK_ROOT}" "${service}"; then return 1; fi
  if [[ -e "${health}" ]] && ! grep -Fqx "WorkingDirectory=${STK_ROOT}" "${health}"; then return 1; fi
  if [[ -e "${timer}" ]] && [[ "$(<"${STK_ROOT}/systemd/server-toolkit-health.timer")" != "$(<"${timer}")" ]]; then return 1; fi
  if [[ -e "${logrotate}" ]] && ! grep -Fq "${STK_ROOT}/proxy/workdir/mihomo.log" "${logrotate}"; then return 1; fi
}

stk_systemd_install() {
  local service_user service_group service_home
  local service_enabled=0 timer_enabled=0 service_active=0 timer_active=0
  stk_service_require_systemd || return 1
  if ! stk_service_path_supported "${STK_ROOT}"; then
    stk_fail 'systemd 安装路径包含不受支持的字符'
    return 1
  fi
  stk_service_user_resolve || return 1
  service_user="${STK_SERVICE_USER_RESOLVED}"
  service_group="$(id -gn "${service_user}")" || return 1
  service_home="${STK_SERVICE_HOME_RESOLVED}"
  stk_service_identifier_supported "${service_group}" || {
    stk_fail "服务用户组包含不受支持的字符：${service_group}"
    return 1
  }
  mkdir -p -- "${STK_ROOT}/workdir" || return 1
  chmod 0755 "${STK_ROOT}/scripts/log-maintenance.sh" "${STK_ROOT}/scripts/service-start.sh"
  stk_service_user_access_validate "${service_user}" || return 1
  stk_systemd_targets_owned || { stk_fail '发现不属于当前项目的同名 systemd/logrotate 配置'; return 1; }
  stk_systemd_unit_enabled server-toolkit.service && service_enabled=1
  stk_systemd_unit_enabled server-toolkit-health.timer && timer_enabled=1
  stk_systemd_unit_active server-toolkit.service && service_active=1
  stk_systemd_unit_active server-toolkit-health.timer && timer_active=1
  stk_systemd_snapshot_create || return 1
  if ! stk_service_render "${STK_ROOT}/systemd/server-toolkit.service.in" "${STK_SYSTEMD_DIR}/server-toolkit.service" "${service_user}" "${service_home}" "${service_group}" ||
     ! stk_service_render "${STK_ROOT}/systemd/server-toolkit-health.service.in" "${STK_SYSTEMD_DIR}/server-toolkit-health.service" "${service_user}" "${service_home}" "${service_group}" ||
     ! install -m 0644 "${STK_ROOT}/systemd/server-toolkit-health.timer" "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" ||
     ! stk_service_render "${STK_ROOT}/systemd/server-toolkit.logrotate.in" "${STK_LOGROTATE_FILE}" "${service_user}" "${service_home}" "${service_group}" ||
     ! systemctl daemon-reload ||
     ! systemctl enable --now server-toolkit.service server-toolkit-health.timer; then
    if stk_systemd_rollback "${service_enabled}" "${timer_enabled}" "${service_active}" "${timer_active}"; then
      stk_fail 'systemd 安装失败，已恢复原配置与启用状态'
    else
      stk_fail 'systemd 安装失败；文件已尝试恢复，运行态仍需手动检查'
    fi
    return 1
  fi
  stk_systemd_snapshot_finish
  stk_ok 'systemd 开机自启、健康恢复定时器和日志轮转已启用'
}

stk_systemd_remove() {
  local service_enabled=0 timer_enabled=0 service_active=0 timer_active=0
  stk_service_require_systemd || return 1
  stk_systemd_targets_owned || { stk_fail '同名 systemd/logrotate 配置不属于当前项目，拒绝移除'; return 1; }
  stk_systemd_unit_enabled server-toolkit.service && service_enabled=1
  stk_systemd_unit_enabled server-toolkit-health.timer && timer_enabled=1
  stk_systemd_unit_active server-toolkit.service && service_active=1
  stk_systemd_unit_active server-toolkit-health.timer && timer_active=1
  stk_systemd_snapshot_create || return 1
  if { (( timer_active == 0 )) || systemctl stop server-toolkit-health.timer; } &&
     { (( service_active == 0 )) || systemctl stop server-toolkit.service; } &&
     { (( timer_enabled == 0 )) || systemctl disable server-toolkit-health.timer; } &&
     { (( service_enabled == 0 )) || systemctl disable server-toolkit.service; } &&
     rm -f -- "${STK_SYSTEMD_DIR}/server-toolkit.service" "${STK_SYSTEMD_DIR}/server-toolkit-health.service" \
       "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" "${STK_LOGROTATE_FILE}" &&
     systemctl daemon-reload; then
    stk_systemd_snapshot_finish
  else
    if stk_systemd_rollback "${service_enabled}" "${timer_enabled}" "${service_active}" "${timer_active}"; then
      stk_fail 'systemd 移除失败，已恢复原配置与启用状态'
    else
      stk_fail 'systemd 移除失败；文件已尝试恢复，运行态仍需手动检查'
    fi
    return 1
  fi
  stk_ok 'server-toolkit systemd 配置已移除'
}

stk_systemd_status() {
  printf '服务后端：systemd\n'
  printf '开机服务：%s / %s\n' \
    "$(systemctl is-enabled server-toolkit.service 2>/dev/null || printf '未安装')" \
    "$(systemctl is-active server-toolkit.service 2>/dev/null || printf 'inactive')"
  printf '健康定时器：%s / %s\n' \
    "$(systemctl is-enabled server-toolkit-health.timer 2>/dev/null || printf '未安装')" \
    "$(systemctl is-active server-toolkit-health.timer 2>/dev/null || printf 'inactive')"
  [[ -f "${STK_LOGROTATE_FILE}" ]] && printf '日志轮转：已配置（10 MiB × 5）\n' || printf '日志轮转：未配置\n'
}

stk_service_install() {
  case "$(stk_service_backend)" in
    supervisord) stk_supervisor_install ;;
    systemd) stk_systemd_install ;;
    *) stk_fail '未检测到可管理的 supervisord 或 systemd' ;;
  esac
}

stk_service_remove() {
  case "$(stk_service_backend)" in
    supervisord) stk_supervisor_remove ;;
    systemd) stk_systemd_remove ;;
    *) stk_fail '未检测到可管理的 supervisord 或 systemd' ;;
  esac
}

stk_service_status() {
  case "$(stk_service_backend)" in
    supervisord) stk_supervisor_status ;;
    systemd) stk_systemd_status ;;
    *) printf '服务后端：不可用\n' ;;
  esac
}

stk_service_managed() {
  case "$(stk_service_backend)" in
    supervisord) stk_supervisor_managed_installed ;;
    systemd)
      [[ -f "${STK_SYSTEMD_DIR}/server-toolkit.service" &&
         ! -L "${STK_SYSTEMD_DIR}/server-toolkit.service" ]] &&
        grep -Fqx "WorkingDirectory=${STK_ROOT}" "${STK_SYSTEMD_DIR}/server-toolkit.service"
      ;;
    *) return 1 ;;
  esac
}

stk_service_health_value() {
  local backend config output required name recovering=0
  backend="$(stk_service_backend)"
  case "${backend}" in
    supervisord)
      config="$(stk_supervisor_config 2>/dev/null || true)"
      if [[ -z "${config}" ]] || ! stk_supervisor_managed_installed; then
        printf '未安装\n'
        return
      fi
      output="$(stk_supervisor_call status 2>/dev/null || true)"
      required='server-toolkit-maintenance'
      stk_proxy_enabled && required="${required} server-toolkit-proxy"
      stk_service_gds_enabled && required="${required} server-toolkit-gds"
      for name in ${required}; do
        if grep -Eq "^${name}[[:space:]]+RUNNING" <<<"${output}"; then
          continue
        elif grep -Eq "^${name}[[:space:]]+STARTING" <<<"${output}"; then
          recovering=1
        else
          printf '异常\n'
          return
        fi
      done
      if (( recovering )); then printf '恢复中\n'; else printf '运行中\n'; fi
      ;;
    systemd)
      if [[ ! -f "${STK_SYSTEMD_DIR}/server-toolkit.service" ||
            ! -f "${STK_SYSTEMD_DIR}/server-toolkit-health.timer" ]]; then
        printf '未安装\n'
      elif stk_systemd_unit_enabled server-toolkit.service &&
           stk_systemd_unit_enabled server-toolkit-health.timer &&
           stk_systemd_unit_active server-toolkit.service &&
           stk_systemd_unit_active server-toolkit-health.timer; then
        printf '运行中\n'
      else
        printf '异常\n'
      fi
      ;;
    *) printf '不可用\n' ;;
  esac
}
