#!/usr/bin/env bash

stk_overview_status() {
  local version='unknown' config_state='默认值' id record codex_home
  [[ -r "${STK_ROOT}/VERSION" ]] && version="$(head -n 1 "${STK_ROOT}/VERSION")"
  if [[ -e "${STK_CONFIG_FILE}" ]]; then
    if stk_config_validate "${STK_CONFIG_FILE}" >/dev/null 2>&1; then config_state='有效'; else config_state='无效'; fi
  fi
  printf 'Server Toolkit %s\n' "${version}"
  printf '根目录：%s\n' "${STK_ROOT}"
  printf '配置：  %s（%s）\n' "${config_state}" "${STK_CONFIG_FILE}"
  printf '服务：  %s / %s\n' "$(stk_service_backend)" "$(stk_service_health_value)"
  printf '\n模块：\n'
  while IFS= read -r record; do
    id="${record%%|*}"
    printf '  %-10s %s\n' "${id}" "$(stk_module_status_value "${id}")"
  done < <(stk_module_catalog)
  codex_home="$(stk_codex_home)"
  printf '\n配置项：\n'
  printf '  Git 代理     %s\n' "$(stk_git_proxy_status_value)"
  printf '  Git 身份     %s\n' "$(stk_git_profile_status_value)"
  printf '  GitHub SSH   %s\n' "$(stk_github_ssh_status_value)"
  printf '  Codex 代理   %s\n' "$(stk_codex_proxy_status_value "${codex_home}")"
  printf '  Codex        %s\n' "$(stk_codex_version "${codex_home}")"
  printf '  Conda 环境   %s（%s）\n' "$(stk_conda_status_value)" "$(stk_conda_envs_dir)"
}
