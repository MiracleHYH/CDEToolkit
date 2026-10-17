#!/usr/bin/env bash

set -uo pipefail

SCRIPT_PATH="${BASH_SOURCE[0]}"
if command -v readlink >/dev/null 2>&1; then
  RESOLVED_PATH="$(readlink -f -- "${SCRIPT_PATH}" 2>/dev/null || true)"
  [[ -n "${RESOLVED_PATH}" ]] && SCRIPT_PATH="${RESOLVED_PATH}"
fi
STK_ROOT="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"

# shellcheck source=lib/common.sh
source "${STK_ROOT}/lib/common.sh" || exit 1
# shellcheck source=lib/config.sh
source "${STK_ROOT}/lib/config.sh"
# shellcheck source=lib/python-runtime.sh
source "${STK_ROOT}/lib/python-runtime.sh"
# shellcheck source=lib/transaction.sh
source "${STK_ROOT}/lib/transaction.sh"
# shellcheck source=lib/modules.sh
source "${STK_ROOT}/lib/modules.sh"
# shellcheck source=lib/proxy-env.sh
source "${STK_ROOT}/lib/proxy-env.sh"
# shellcheck source=lib/git-proxy.sh
source "${STK_ROOT}/lib/git-proxy.sh"
# shellcheck source=lib/git-profile.sh
source "${STK_ROOT}/lib/git-profile.sh"
# shellcheck source=lib/github-ssh.sh
source "${STK_ROOT}/lib/github-ssh.sh"
# shellcheck source=lib/codex.sh
source "${STK_ROOT}/lib/codex.sh"
# shellcheck source=lib/conda.sh
source "${STK_ROOT}/lib/conda.sh"
# shellcheck source=lib/service.sh
source "${STK_ROOT}/lib/service.sh"
# shellcheck source=lib/upgrade.sh
source "${STK_ROOT}/lib/upgrade.sh"
# shellcheck source=lib/doctor.sh
source "${STK_ROOT}/lib/doctor.sh"
# shellcheck source=lib/overview.sh
source "${STK_ROOT}/lib/overview.sh"
# shellcheck source=lib/lifecycle.sh
source "${STK_ROOT}/lib/lifecycle.sh"

STK_GDS_SCRIPT="${STK_GDS_SCRIPT:-${STK_ROOT}/gds/gds.sh}"

if [[ -t 1 ]] && command -v tput >/dev/null 2>&1 && [[ "$(tput colors 2>/dev/null || printf '0')" -ge 8 ]]; then
  RESET=$'\033[0m'; BOLD=$'\033[1m'; DIM=$'\033[2m'; CYAN=$'\033[36m'
else
  RESET=''; BOLD=''; DIM=''; CYAN=''
fi

stk_gds_enabled() {
  stk_module_enabled 'gds' "${STK_GDS_SCRIPT}"
}

stk_gds_status() {
  if ! stk_gds_enabled; then
    printf '未启用\n'
  elif "${STK_GDS_SCRIPT}" status --quiet >/dev/null 2>&1; then
    printf '运行中\n'
  else
    printf '已停止\n'
  fi
}

stk_pause() {
  if [[ -t 0 ]]; then
    printf '\n%s' "${DIM}按 Enter 返回菜单...${RESET}"
    read -r _ || true
  fi
}

stk_prompt_codex_home() {
  local default_home input
  default_home="$(stk_codex_home)"
  printf 'CODEX_HOME [%s]: ' "${default_home}" >&2
  read -r input || return 1
  printf '%s\n' "${input:-${default_home}}"
}

stk_run_module() {
  local id="$1"
  case "${id}" in
    proxy)
      if ! stk_proxy_enabled; then
        stk_fail 'proxy 模块尚未启用，请先执行 ./install.sh'
        return 1
      fi
      "${STK_PM_SCRIPT}"
      ;;
    gds)
      if ! stk_gds_enabled; then
        stk_fail 'gds 模块尚未启用，请先执行 ./install.sh'
        return 1
      fi
      "${STK_GDS_SCRIPT}"
      ;;
  esac
}

stk_interactive_git_proxy() {
  local state
  state="$(stk_git_proxy_status_value)"
  case "${state}" in
    '已配置') stk_git_proxy_disable ;;
    '外部配置')
      if stk_confirm '已有外部 Git 代理，要暂存并覆盖它吗？'; then
        stk_git_proxy_enable 1
      else
        stk_warn '已取消'
      fi
      ;;
    *) stk_git_proxy_enable 0 ;;
  esac
}

stk_interactive_codex_proxy() {
  local codex_home state force=0
  codex_home="$(stk_prompt_codex_home)" || return 1
  stk_validate_absolute_path "${codex_home}" || return 1
  state="$(stk_codex_proxy_status_value "${codex_home}")"
  case "${state}" in
    '已配置') stk_codex_proxy_disable "${codex_home}" ;;
    '外部配置'|'部分配置'|'混合配置')
      if stk_confirm '现有 .env 包含非托管代理行，要移除并由 server-toolkit 完整接管吗？'; then
        stk_codex_proxy_enable "${codex_home}" 1
      else
        stk_warn '已取消'
      fi
      ;;
    *) stk_codex_proxy_enable "${codex_home}" 0 ;;
  esac
}

stk_interactive_codex_install() {
  local codex_home existing force=0
  codex_home="$(stk_prompt_codex_home)" || return 1
  stk_validate_absolute_path "${codex_home}" || return 1
  existing="$(stk_codex_bashrc_home)"
  if [[ -n "${existing}" && "${existing}" != "${codex_home}" ]]; then
    if ! stk_confirm "${STK_BASHRC_FILE} 当前为 ${existing}，要改为 ${codex_home} 吗？"; then
      stk_warn '已取消'
      return 0
    fi
    force=1
  fi
  stk_codex_install "${codex_home}" "${force}"
}

stk_interactive_github_ssh() {
  local state force=0
  state="$(stk_github_ssh_status_value)"
  case "${state}" in
    '已配置') stk_github_ssh_status ;;
    '冲突')
      if stk_confirm '目标 GitHub 私钥已存在且内容不同，要由 server-toolkit 接管吗？'; then
        stk_github_ssh_configure 1
      else
        stk_warn '已取消'
      fi
      ;;
    *) stk_github_ssh_configure 0 ;;
  esac
}

stk_interactive_git_profile() {
  local state
  state="$(stk_git_profile_status_value)"
  case "${state}" in
    '已配置') stk_git_profile_status ;;
    '外部配置'|'部分配置')
      if stk_confirm '已有 Git 身份配置，要暂存并由 server-toolkit 接管吗？'; then
        stk_git_profile_configure 1
      else
        stk_warn '已取消'
      fi
      ;;
    *) stk_git_profile_configure 0 ;;
  esac
}

stk_prompt_conda_target() {
  local default_target input
  default_target="$(stk_conda_envs_dir)"
  printf 'Conda 环境目录 [%s]: ' "${default_target}" >&2
  read -r input || return 1
  STK_PARSED_CONDA_TARGET="${input:-${default_target}}"
  stk_validate_absolute_path "${STK_PARSED_CONDA_TARGET}"
}

stk_tui_enable_input_mode() {
  stty -echo -icanon min 1 time 0
}

stk_tui_restore_input_mode() {
  stty "$1"
}

stk_tui_clear() {
  [[ -t 1 ]] && printf '\033[2J\033[H'
}

stk_conda_menu_status_refresh() {
  STK_CONDA_MENU_TARGET="$(stk_conda_envs_dir)"
  STK_CONDA_MENU_CONFIG="${STK_CONDARC_FILE}"
  STK_CONDA_MENU_BIN="$(stk_conda_bin 2>/dev/null || true)"
  if [[ -n "${STK_CONDA_MENU_BIN}" ]]; then
    STK_CONDA_MENU_BASE="$("${STK_CONDA_MENU_BIN}" info --base 2>/dev/null || true)"
    STK_CONDA_MENU_FIRST="$(stk_conda_effective_env_dirs "${STK_CONDA_MENU_BIN}" | head -n 1)"
  else
    STK_CONDA_MENU_BASE=''
    STK_CONDA_MENU_FIRST=''
  fi
  STK_CONDA_MENU_STATUS="$(stk_conda_status_value)"
}

stk_draw_conda_menu() {
  local selected="$1" index marker
  local -a labels=(
    '修改目标目录'
    '迁移本机 Conda 环境（预览）'
    '迁移本机 Conda 环境'
    '返回主面板'
  )
  stk_tui_clear
  printf '%s%sConda 环境管理%s\n\n' "${BOLD}" "${CYAN}" "${RESET}"
  printf '  Conda：%s\n' "${STK_CONDA_MENU_BIN:-未安装}"
  if [[ -n "${STK_CONDA_MENU_BASE}" ]]; then
    printf '  Base：%s（不会迁移）\n' "${STK_CONDA_MENU_BASE}"
  else
    printf '  Base：未知\n'
  fi
  printf '  配置文件：%s\n' "${STK_CONDA_MENU_CONFIG}"
  printf '  envs_dirs 首位：%s\n' "${STK_CONDA_MENU_FIRST:-未知}"
  printf '  目标目录：%s\n' "${STK_CONDA_MENU_TARGET}"
  printf '  当前状态：%s\n\n' "${STK_CONDA_MENU_STATUS}"
  for index in 0 1 2 3; do
    [[ ${index} -eq ${selected} ]] && marker="${CYAN}›${RESET}" || marker=' '
    printf '  %s %s\n' "${marker}" "${labels[index]}"
  done
  printf '\n%s↑/↓ 选择  •  Enter 确认  •  r 刷新状态  •  q / Esc 返回%s\n' "${DIM}" "${RESET}"
}

stk_interactive_conda_menu() (
  local selected=0 item_count=4 key next_key final_key tty_state
  local STK_CONDA_MENU_STATUS STK_CONDA_MENU_TARGET STK_CONDA_MENU_CONFIG
  local STK_CONDA_MENU_BIN STK_CONDA_MENU_BASE STK_CONDA_MENU_FIRST
  tty_state="$(stty -g)" || return 1
  trap 'stk_tui_restore_input_mode "${tty_state}"' EXIT
  trap 'stk_tui_restore_input_mode "${tty_state}"; exit 130' HUP INT TERM
  stk_tui_enable_input_mode || return 1
  stk_conda_menu_status_refresh
  while true; do
    stk_draw_conda_menu "${selected}"
    IFS= read -rn1 key || return 0
    case "${key}" in
      $'\x1b')
        next_key=''; final_key=''
        IFS= read -rn1 -t 0.3 next_key || true
        if [[ "${next_key}" == '[' ]]; then
          IFS= read -rn1 -t 0.3 final_key || true
          case "${final_key}" in
            A) selected=$(( (selected - 1 + item_count) % item_count )) ;;
            B) selected=$(( (selected + 1) % item_count )) ;;
          esac
        elif [[ -z "${next_key}" ]]; then
          return 0
        fi
        ;;
      k) selected=$(( (selected - 1 + item_count) % item_count )) ;;
      j) selected=$(( (selected + 1) % item_count )) ;;
      r|R) stk_conda_menu_status_refresh ;;
      q|Q) return 0 ;;
      '')
        stk_tui_restore_input_mode "${tty_state}"
        case "${selected}" in
          0)
            if stk_prompt_conda_target; then
              stk_with_global_lock stk_conda_link "${STK_PARSED_CONDA_TARGET}"
            fi
            stk_pause
            ;;
          1)
            stk_conda_migrate "${STK_CONDA_MENU_TARGET}" 1 0
            stk_pause
            ;;
          2)
            stk_with_global_lock stk_conda_migrate "${STK_CONDA_MENU_TARGET}" 0 0
            stk_pause
            ;;
          3) return 0 ;;
        esac
        stk_tui_enable_input_mode || return 1
        stk_conda_menu_status_refresh
        ;;
    esac
  done
)

stk_menu_status_refresh() {
  STK_MENU_PROXY_STATUS="$(stk_proxy_status)"
  STK_MENU_GDS_STATUS="$(stk_gds_status)"
  STK_MENU_GIT_STATUS="$(stk_git_proxy_status_value)"
  STK_MENU_CODEX_HOME="$(stk_codex_home)"
  STK_MENU_CODEX_PROXY_STATUS="$(stk_codex_proxy_status_value "${STK_MENU_CODEX_HOME}")"
  STK_MENU_GIT_PROFILE_STATUS="$(stk_git_profile_status_value)"
  STK_MENU_GITHUB_SSH_STATUS="$(stk_github_ssh_status_value)"
  STK_MENU_CODEX_VERSION="$(stk_codex_version "${STK_MENU_CODEX_HOME}")"
  STK_MENU_CONDA_STATUS="$(stk_conda_status_value)"
}

stk_menu_run_action() {
  local tty_state="$1"
  shift
  stk_tui_restore_input_mode "${tty_state}"
  "$@"
  stk_pause
  stk_tui_enable_input_mode || return 1
  stk_menu_status_refresh
}

stk_draw_menu() {
  local selected="$1" index marker
  local -a labels=(
    'Proxy Dashboard'
    'GPU Dynamic Scheduler'
    '设置 / 取消 Git 代理'
    '设置 / 取消 Codex 代理'
    '配置 / 检查 Git 身份'
    '配置 / 检查 GitHub SSH'
    '安装 / 更新 Codex'
    'Conda 环境管理'
    '系统诊断'
    '退出'
  )
  stk_tui_clear
  printf '%s%sServer Toolkit%s\n\n' "${BOLD}" "${CYAN}" "${RESET}"
  printf '%s模块%s\n' "${BOLD}" "${RESET}"
  for index in 0 1; do
    [[ ${index} -eq ${selected} ]] && marker="${CYAN}›${RESET}" || marker=' '
    if (( index == 0 )); then
      printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_PROXY_STATUS}"
    else
      printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_GDS_STATUS}"
    fi
  done
  printf '\n%s快捷操作%s\n' "${BOLD}" "${RESET}"
  for index in 2 3 4 5 6 7 8 9; do
    [[ ${index} -eq ${selected} ]] && marker="${CYAN}›${RESET}" || marker=' '
    case "${index}" in
      2) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_GIT_STATUS}" ;;
      3) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_CODEX_PROXY_STATUS}" ;;
      4) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_GIT_PROFILE_STATUS}" ;;
      5) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_GITHUB_SSH_STATUS}" ;;
      6) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_CODEX_VERSION}" ;;
      7) printf '  %s %-28s %s\n' "${marker}" "${labels[index]}" "${STK_MENU_CONDA_STATUS}" ;;
      8) printf '  %s %s\n' "${marker}" "${labels[index]}" ;;
      9) printf '  %s %s\n' "${marker}" "${labels[index]}" ;;
    esac
  done
  printf '\n%s↑/↓ 选择  •  Enter 确认  •  r 刷新状态  •  q / Esc 退出%s\n' "${DIM}" "${RESET}"
}

stk_interactive_menu() (
  local selected=0 item_count=10 key next_key final_key tty_state
  local STK_MENU_PROXY_STATUS STK_MENU_GDS_STATUS
  local STK_MENU_GIT_STATUS STK_MENU_CODEX_HOME
  local STK_MENU_CODEX_PROXY_STATUS STK_MENU_GIT_PROFILE_STATUS STK_MENU_GITHUB_SSH_STATUS
  local STK_MENU_CODEX_VERSION STK_MENU_CONDA_STATUS
  if [[ ! -t 0 || ! -t 1 ]]; then
    stk_fail '交互面板需要终端；自动化场景请使用命令模式'
    return 1
  fi
  tty_state="$(stty -g)" || return 1
  trap 'stk_tui_restore_input_mode "${tty_state}"' EXIT
  trap 'stk_tui_restore_input_mode "${tty_state}"; exit 130' HUP INT TERM
  stk_tui_enable_input_mode || return 1
  stk_menu_status_refresh
  while true; do
    stk_draw_menu "${selected}"
    IFS= read -rn1 key || return 0
    case "${key}" in
      $'\x1b')
        next_key=''; final_key=''
        IFS= read -rn1 -t 0.3 next_key || true
        if [[ "${next_key}" == '[' ]]; then
          IFS= read -rn1 -t 0.3 final_key || true
          case "${final_key}" in
            A) selected=$(( (selected - 1 + item_count) % item_count )) ;;
            B) selected=$(( (selected + 1) % item_count )) ;;
          esac
        elif [[ -z "${next_key}" ]]; then
          return 0
        fi
        ;;
      k) selected=$(( (selected - 1 + item_count) % item_count )) ;;
      j) selected=$(( (selected + 1) % item_count )) ;;
      r|R) stk_menu_status_refresh ;;
      q|Q) return 0 ;;
      '')
        case "${selected}" in
          0) stk_menu_run_action "${tty_state}" stk_run_module proxy ;;
          1) stk_menu_run_action "${tty_state}" stk_run_module gds ;;
          2) stk_menu_run_action "${tty_state}" stk_interactive_git_proxy ;;
          3) stk_menu_run_action "${tty_state}" stk_interactive_codex_proxy ;;
          4) stk_menu_run_action "${tty_state}" stk_interactive_git_profile ;;
          5) stk_menu_run_action "${tty_state}" stk_interactive_github_ssh ;;
          6) stk_menu_run_action "${tty_state}" stk_interactive_codex_install ;;
          7)
            stk_tui_restore_input_mode "${tty_state}"
            stk_interactive_conda_menu
            stk_tui_enable_input_mode || return 1
            stk_menu_status_refresh
            ;;
          8) stk_menu_run_action "${tty_state}" stk_doctor ;;
          9) return 0 ;;
        esac
        ;;
    esac
  done
)

stk_usage() {
  cat <<'EOF'
用法：stk [命令]

不带命令                         打开 Server Toolkit 面板
proxy [configure|validate]       打开面板、重配私有 YAML 或校验配置
gds                              打开 GPU Dynamic Scheduler
git-proxy enable [--force]       设置 Git 代理
git-proxy disable                取消 Git 代理并恢复原配置
git-proxy status                 显示 Git 代理状态
codex-proxy enable [--home PATH] [--force]
                                 设置 Codex 代理
codex-proxy disable [--home PATH]取消 Codex 代理
codex-proxy status [--home PATH] 显示 Codex 代理状态
git-profile configure [--force]  配置 Git 身份和默认分支
git-profile status               显示 Git 身份配置状态
git-profile remove               移除管理并恢复原配置
github-ssh configure [--force]   配置 GitHub SSH key
github-ssh status                显示 GitHub SSH 配置状态
github-ssh remove [--force]      移除受管 GitHub SSH 配置
github-ssh known-hosts           固定 GitHub 官方 SSH 主机公钥
github-ssh agent                 启用持久 ssh-agent 并解锁密钥
github-ssh test                  测试 GitHub SSH 鉴权
codex install [--home PATH] [--force-home]
                                 使用官方脚本安装 / 更新 Codex
codex restore [--home PATH]      优先复用现有发布包并修复迁移路径
conda link [--path PATH]         将目标设为 Conda envs_dirs 首选目录
conda migrate [--target PATH] [--source PATH] [--dry-run] [--yes]
                                 迁移默认目录中的非 base 环境并完成链接
conda status                     显示 Conda base、目标目录与链接状态
doctor                           检查依赖、入口、服务与托管配置
runtime ensure                   校验并在需要时重建 STK 私有 Python 环境
status                           显示工具包、模块与配置总览
logs [all|proxy|gds|service]     查看日志（可加 -f、--lines N）
module list                      列出可用模块与状态
module enabled                   仅输出已安装模块 ID（便于自动化）
module install NAME              安装模块
module migrate NAME              将已安装模块迁移到当前版本
module start|stop|restart NAME   启停或重启模块
module remove NAME               移除模块入口并保留数据
version                          显示版本
self-update [check|run|rollback] 检查、安装或回滚 Git 上游版本
uninstall [--dry-run] [--purge-config] [--purge-state] [--purge-data] [--purge-all] [--yes]
                                 安全卸载；默认保留个人配置和模块数据
config show                      显示统一配置（文件不存在时显示默认值）
config setup                     交互填写个人配置
config get KEY                   读取单项配置
config set KEY VALUE             修改单项配置
config edit                      使用 $VISUAL / $EDITOR 编辑并验证配置
config validate                  验证配置文件
config repair                    备份并规范化损坏的配置
service install                  安装并启用 supervisord/systemd 托管与日志轮转
service status                   显示系统服务状态
service managed                  仅以退出码报告是否存在受管服务（供自动化使用）
service remove                   移除系统服务配置
help                             显示帮助
EOF
}

stk_parse_home_option() {
  STK_PARSED_HOME="$(stk_codex_home)"
  STK_FORCE_HOME=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --home)
        [[ $# -ge 2 ]] || { stk_fail '--home 缺少路径'; return 2; }
        STK_PARSED_HOME="$2"; shift 2 ;;
      --force-home) STK_FORCE_HOME=1; shift ;;
      *) stk_fail "未知参数：$1"; return 2 ;;
    esac
  done
  stk_validate_absolute_path "${STK_PARSED_HOME}"
}

stk_parse_codex_proxy_options() {
  STK_PARSED_HOME="$(stk_codex_home)"
  STK_FORCE_PROXY=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --home)
        [[ $# -ge 2 ]] || { stk_fail '--home 缺少路径'; return 2; }
        STK_PARSED_HOME="$2"; shift 2 ;;
      --force) STK_FORCE_PROXY=1; shift ;;
      *) stk_fail "未知参数：$1"; return 2 ;;
    esac
  done
  stk_validate_absolute_path "${STK_PARSED_HOME}"
}

stk_main() {
  local action="${1:-menu}" subaction force=0
  if [[ -e "${STK_CONFIG_FILE}" ]] && ! stk_config_runtime_validate "${STK_CONFIG_FILE}" >/dev/null 2>&1; then
    case "${action}:${2:-}" in
      help:*|-h:*|--help:*|version:*|doctor:*|config:validate|config:edit|config:repair) ;;
      *)
        stk_fail "统一配置无效：${STK_CONFIG_FILE}"
        stk_fail '已停止使用默认值继续运行；请执行 stk config validate，然后 stk config edit 或 stk config repair'
        return 1
        ;;
    esac
  fi
  case "${action}" in
    __post-update)
      [[ $# -eq 1 ]] || return 2
      stk_config_runtime_validate "${STK_CONFIG_FILE}" || return 1
      stk_prepare_state_root || return 1
      stk_python_runtime_ensure
      ;;
    __upgrade-snapshot) [[ $# -eq 2 ]] || return 2; stk_upgrade_snapshot_create "$2" ;;
    __upgrade-restore) [[ $# -eq 2 ]] || return 2; stk_upgrade_restore_all "$2" ;;
    __upgrade-restore-runtime) [[ $# -eq 2 ]] || return 2; stk_upgrade_restore_runtime "$2" ;;
    menu) stk_interactive_menu ;;
    doctor) [[ $# -eq 1 ]] || return 2; stk_doctor ;;
    status) [[ $# -eq 1 ]] || return 2; stk_overview_status ;;
    logs)
      local log_target='all' log_follow=0 log_lines=100
      shift
      while [[ $# -gt 0 ]]; do
        case "$1" in
          -f|--follow) log_follow=1; shift ;;
          --lines) [[ $# -ge 2 ]] || { stk_fail '--lines 缺少数量'; return 2; }; log_lines="$2"; shift 2 ;;
          all|proxy|gds|service) log_target="$1"; shift ;;
          *) stk_fail "未知 logs 参数：$1"; return 2 ;;
        esac
      done
      stk_logs "${log_target}" "${log_follow}" "${log_lines}"
      ;;
    runtime)
      subaction="${2:-ensure}"
      [[ $# -le 2 ]] || return 2
      case "${subaction}" in
        ensure) stk_with_global_lock stk_python_runtime_ensure ;;
        *) stk_fail "未知 runtime 操作：${subaction}"; return 2 ;;
      esac
      ;;
    module)
      subaction="${2:-list}"
      case "${subaction}" in
        list) [[ $# -le 2 ]] || return 2; stk_module_list ;;
        enabled) [[ $# -le 2 ]] || return 2; stk_module_enabled_ids ;;
        status) [[ $# -eq 3 ]] || { stk_fail '用法：stk module status NAME'; return 2; }; stk_module_status_value "$3" ;;
        install) [[ $# -eq 3 ]] || { stk_fail '用法：stk module install NAME'; return 2; }; stk_with_optional_global_lock stk_module_install "$3" ;;
        migrate)
          [[ $# -eq 3 ]] || { stk_fail '用法：stk module migrate NAME'; return 2; }
          stk_module_is_enabled "$3" || { stk_fail "模块 $3 未安装，无需迁移"; return 1; }
          stk_with_optional_global_lock stk_module_install "$3" migrate
          ;;
        start) [[ $# -eq 3 ]] || { stk_fail '用法：stk module start NAME'; return 2; }; stk_with_optional_global_lock stk_module_start "$3" ;;
        stop) [[ $# -eq 3 ]] || { stk_fail '用法：stk module stop NAME'; return 2; }; stk_with_optional_global_lock stk_module_stop "$3" ;;
        restart) [[ $# -eq 3 ]] || { stk_fail '用法：stk module restart NAME'; return 2; }; stk_with_optional_global_lock stk_module_restart "$3" ;;
        remove) [[ $# -eq 3 ]] || { stk_fail '用法：stk module remove NAME'; return 2; }; stk_with_optional_global_lock stk_module_remove "$3" ;;
        *) stk_fail "未知 module 操作：${subaction}"; return 2 ;;
      esac
      ;;
    version) [[ $# -eq 1 ]] || return 2; stk_version ;;
    self-update)
      subaction="${2:-run}"
      [[ $# -le 2 ]] || return 2
      case "${subaction}" in
        check) stk_self_update_check ;;
        run) stk_with_global_lock stk_self_update ;;
        rollback) stk_with_global_lock stk_self_update_rollback ;;
        *) stk_fail "未知 self-update 操作：${subaction}"; return 2 ;;
      esac
      ;;
    uninstall)
      local dry_run=0 purge_config=0 purge_state=0 purge_data=0 purge_home=0 confirmed=0
      shift
      while [[ $# -gt 0 ]]; do
        case "$1" in
          --dry-run) dry_run=1 ;;
          --purge-config) purge_config=1 ;;
          --purge-state) purge_state=1 ;;
          --purge-data) purge_data=1 ;;
          --purge-all) purge_config=1; purge_state=1; purge_data=1; purge_home=1 ;;
          --purge) purge_config=1; purge_state=1 ;;
          --yes) confirmed=1 ;;
          *) stk_fail "未知 uninstall 参数：$1"; return 2 ;;
        esac
        shift
      done
      if (( dry_run == 0 && (purge_config || purge_state || purge_data) && confirmed == 0 )); then
        if [[ -t 0 ]]; then
          stk_uninstall_plan "${purge_config}" "${purge_state}" "${purge_data}" "${purge_home}"
          stk_confirm '以上清理包含不可恢复的数据删除，继续吗？' || { stk_warn '已取消'; return 0; }
        else
          stk_fail '非交互清理配置/状态/数据时必须添加 --yes'
          return 2
        fi
      fi
      stk_with_optional_global_lock stk_uninstall "${dry_run}" "${purge_config}" "${purge_state}" "${purge_data}" "${purge_home}"
      ;;
    config)
      subaction="${2:-show}"
      case "${subaction}" in
        init) [[ $# -eq 2 ]] || return 2; stk_with_optional_global_lock stk_config_init ;;
        setup) [[ $# -eq 2 ]] || return 2; stk_with_optional_global_lock stk_config_setup ;;
        show) [[ $# -le 2 ]] || return 2; stk_config_show ;;
        get) [[ $# -eq 3 ]] || { stk_fail '用法：stk config get KEY'; return 2; }; stk_config_get "$3" ;;
        set) [[ $# -eq 4 ]] || { stk_fail '用法：stk config set KEY VALUE'; return 2; }; stk_with_optional_global_lock stk_config_set "$3" "$4" ;;
        edit) [[ $# -eq 2 ]] || return 2; stk_with_optional_global_lock stk_config_edit ;;
        validate) [[ $# -eq 2 ]] || return 2; stk_config_runtime_validate ;;
        repair) [[ $# -eq 2 ]] || return 2; stk_with_optional_global_lock stk_config_repair ;;
        *) stk_fail "未知 config 操作：${subaction}"; return 2 ;;
      esac
      ;;
    service)
      subaction="${2:-status}"
      [[ $# -le 2 ]] || return 2
      case "${subaction}" in
        install) stk_with_global_lock stk_service_install ;;
        status) stk_service_status ;;
        managed) stk_service_managed ;;
        remove) stk_with_global_lock stk_service_remove ;;
        *) stk_fail "未知 service 操作：${subaction}"; return 2 ;;
      esac
      ;;
    proxy)
      subaction="${2:-menu}"
      case "${subaction}" in
        menu) [[ $# -le 2 ]] || return 2; stk_run_module proxy ;;
        configure) [[ $# -eq 2 ]] || return 2; stk_with_global_lock "${STK_ROOT}/proxy/install.sh" --configure-only ;;
        validate) [[ $# -eq 2 ]] || return 2; "${STK_PM_SCRIPT}" validate ;;
        *) stk_fail "未知 proxy 操作：${subaction}"; return 2 ;;
      esac
      ;;
    gds) stk_run_module gds ;;
    git-proxy)
      subaction="${2:-status}"
      case "${subaction}" in
        enable)
          [[ "${3:-}" == '--force' ]] && force=1
          [[ $# -le $((force + 2)) ]] || { stk_fail '未知 git-proxy 参数'; return 2; }
          stk_with_global_lock stk_git_proxy_enable "${force}"
          ;;
        disable) [[ $# -eq 2 ]] || return 2; stk_with_global_lock stk_git_proxy_disable ;;
        status) [[ $# -le 2 ]] || return 2; stk_git_proxy_status ;;
        *) stk_fail "未知 git-proxy 操作：${subaction}"; return 2 ;;
      esac
      ;;
    codex-proxy)
      shift
      subaction="${1:-status}"
      [[ $# -gt 0 ]] && shift
      stk_parse_codex_proxy_options "$@" || return $?
      case "${subaction}" in
        enable) stk_with_global_lock stk_codex_proxy_enable "${STK_PARSED_HOME}" "${STK_FORCE_PROXY}" ;;
        disable) stk_with_global_lock stk_codex_proxy_disable "${STK_PARSED_HOME}" ;;
        status) stk_codex_proxy_status "${STK_PARSED_HOME}" ;;
        *) stk_fail "未知 codex-proxy 操作：${subaction}"; return 2 ;;
      esac
      ;;
    github-ssh)
      subaction="${2:-status}"
      case "${subaction}" in
        configure)
          [[ "${3:-}" == '--force' ]] && force=1
          [[ $# -le $((force + 2)) ]] || { stk_fail '未知 github-ssh 参数'; return 2; }
          stk_with_global_lock stk_github_ssh_configure "${force}"
          ;;
        status) [[ $# -eq 1 || $# -eq 2 ]] || return 2; stk_github_ssh_status ;;
        known-hosts) [[ $# -eq 2 ]] || return 2; stk_with_global_lock stk_github_known_hosts_configure ;;
        agent) [[ $# -eq 2 ]] || return 2; stk_with_global_lock stk_github_ssh_agent_configure ;;
        test) [[ $# -eq 2 ]] || return 2; stk_github_ssh_auth_test ;;
        remove)
          [[ "${3:-}" == '--force' ]] && force=1
          [[ $# -le $((force + 2)) ]] || { stk_fail '未知 github-ssh 参数'; return 2; }
          stk_with_global_lock stk_github_ssh_remove "${force}"
          ;;
        *) stk_fail "未知 github-ssh 操作：${subaction}"; return 2 ;;
      esac
      ;;
    git-profile)
      subaction="${2:-status}"
      case "${subaction}" in
        configure)
          [[ "${3:-}" == '--force' ]] && force=1
          [[ $# -le $((force + 2)) ]] || { stk_fail '未知 git-profile 参数'; return 2; }
          stk_with_global_lock stk_git_profile_configure "${force}"
          ;;
        status) [[ $# -eq 1 || $# -eq 2 ]] || return 2; stk_git_profile_status ;;
        remove) [[ $# -eq 2 ]] || return 2; stk_with_global_lock stk_git_profile_remove ;;
        *) stk_fail "未知 git-profile 操作：${subaction}"; return 2 ;;
      esac
      ;;
    codex)
      subaction="${2:-}"
      [[ "${subaction}" == 'install' || "${subaction}" == 'update' || "${subaction}" == 'restore' ]] || {
        stk_fail '用法：stk codex install|restore [--home PATH] [--force-home]'
        return 2
      }
      shift 2
      stk_parse_home_option "$@" || return $?
      if [[ "${subaction}" == 'restore' ]]; then
        stk_with_global_lock stk_codex_restore "${STK_PARSED_HOME}" "${STK_FORCE_HOME}"
      else
        stk_with_global_lock stk_codex_install "${STK_PARSED_HOME}" "${STK_FORCE_HOME}"
      fi
      ;;
    conda)
      subaction="${2:-status}"
      case "${subaction}" in
        link)
          local conda_target
          conda_target="$(stk_conda_envs_dir)"
          shift 2
          while [[ $# -gt 0 ]]; do
            case "$1" in
              --path) [[ $# -ge 2 ]] || { stk_fail '--path 缺少路径'; return 2; }; conda_target="$2"; shift 2 ;;
              *) stk_fail "未知 conda link 参数：$1"; return 2 ;;
            esac
          done
          stk_validate_absolute_path "${conda_target}" || return 2
          stk_with_global_lock stk_conda_link "${conda_target}"
          ;;
        migrate)
          local conda_target conda_dry_run=0 conda_assume_yes=0
          local -a conda_sources=()
          conda_target="$(stk_conda_envs_dir)"
          shift 2
          while [[ $# -gt 0 ]]; do
            case "$1" in
              --target) [[ $# -ge 2 ]] || { stk_fail '--target 缺少路径'; return 2; }; conda_target="$2"; shift 2 ;;
              --source) [[ $# -ge 2 ]] || { stk_fail '--source 缺少路径'; return 2; }; conda_sources+=("$2"); shift 2 ;;
              --dry-run) conda_dry_run=1; shift ;;
              --yes) conda_assume_yes=1; shift ;;
              *) stk_fail "未知 conda migrate 参数：$1"; return 2 ;;
            esac
          done
          stk_with_global_lock stk_conda_migrate "${conda_target}" "${conda_dry_run}" "${conda_assume_yes}" "${conda_sources[@]}"
          ;;
        status) [[ $# -le 2 ]] || return 2; stk_conda_status ;;
        *) stk_fail "未知 conda 操作：${subaction}"; return 2 ;;
      esac
      ;;
    help|-h|--help) stk_usage ;;
    *) stk_fail "未知命令：${action}"; stk_usage; return 2 ;;
  esac
}

stk_main "$@"
