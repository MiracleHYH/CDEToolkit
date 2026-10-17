#!/usr/bin/env bash

STK_PM_SCRIPT="${STK_PM_SCRIPT:-${STK_ROOT}/proxy/pm.sh}"

stk_proxy_enabled() {
  stk_module_enabled 'pm' "${STK_PM_SCRIPT}"
}

stk_proxy_endpoint() {
  local listener="${1:-$(stk_config_value proxy_common_listener)}"
  "${STK_PM_SCRIPT}" endpoint "${listener}"
}

stk_proxy_http_url() {
  local listener="${1:-$(stk_config_value proxy_common_listener)}" endpoint
  endpoint="$(stk_proxy_endpoint "${listener}")" || return 1
  printf 'http://%s\n' "${endpoint}"
}

stk_proxy_status() {
  if ! stk_proxy_enabled; then
    printf '未启用\n'
  elif "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then
    printf '运行中\n'
  else
    printf '已停止\n'
  fi
}

stk_proxy_config_path() {
  "${STK_PM_SCRIPT}" config-path
}

stk_proxy_ensure_running() {
  if ! stk_proxy_enabled; then
    stk_fail 'proxy 模块尚未安装；请先通过 ./install.sh 启用 proxy'
    return 1
  fi
  if "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then
    return 0
  fi
  stk_info '正在启动 Proxy Dashboard...'
  "${STK_PM_SCRIPT}" start
}
