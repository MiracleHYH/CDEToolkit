#!/usr/bin/env bash

set -uo pipefail

SCRIPT_PATH="${BASH_SOURCE[0]}"
if command -v readlink >/dev/null 2>&1; then
  RESOLVED_PATH="$(readlink -f -- "${SCRIPT_PATH}" 2>/dev/null || true)"
  if [[ -n "${RESOLVED_PATH}" ]]; then
    SCRIPT_PATH="${RESOLVED_PATH}"
  fi
fi
TOOLKIT_DIR="$(cd -- "$(dirname -- "${SCRIPT_PATH}")" && pwd)"
TOOLKIT_MANAGER="${TOOLKIT_DIR}/toolkit.sh"
PROJECT_STATE_DIR="${SERVER_TOOLKIT_PROJECT_STATE_DIR:-${TOOLKIT_DIR}/workdir}"
INSTALL_STATE_FILE="${PROJECT_STATE_DIR}/install.conf"

STK_ROOT="${TOOLKIT_DIR}"
# shellcheck source=lib/common.sh
source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
# shellcheck source=lib/config.sh
source "${TOOLKIT_DIR}/lib/config.sh"
# shellcheck source=lib/codex.sh
source "${TOOLKIT_DIR}/lib/codex.sh"
# shellcheck source=lib/python-runtime.sh
source "${TOOLKIT_DIR}/lib/python-runtime.sh"
# shellcheck source=lib/transaction.sh
source "${TOOLKIT_DIR}/lib/transaction.sh"
# shellcheck source=lib/modules.sh
source "${TOOLKIT_DIR}/lib/modules.sh"
# shellcheck source=lib/proxy-env.sh
source "${TOOLKIT_DIR}/lib/proxy-env.sh"
STK_GDS_SCRIPT="${STK_ROOT}/gds/gds.sh"
# shellcheck source=lib/service.sh
source "${TOOLKIT_DIR}/lib/service.sh"

MODULE_IDS=()
MODULE_NAMES=()
MODULE_DESCRIPTIONS=()
MODULE_INSTALLERS=()
SELECTED=()

while IFS='|' read -r module_id module_name module_description _command _manager module_installer _log _type; do
  MODULE_IDS+=("${module_id}")
  MODULE_NAMES+=("${module_name}")
  MODULE_DESCRIPTIONS+=("${module_description}")
  MODULE_INSTALLERS+=("${module_installer}")
  SELECTED+=(0)
done < <(stk_module_catalog)

BIN_DIR=''
FORCE=0
MODE='interactive'
REQUESTED_MODULES=()

usage() {
  cat <<'EOF'
用法：./install.sh [选项]

不带参数              打开交互式模块选择器
--all                 安装所有模块
--no-modules          不安装模块，仅安装 stk
--module 模块        安装指定模块（可重复使用）
--list                列出可安装模块
--prefix BIN目录      将命令入口安装到指定目录
--force               允许各模块覆盖已有命令入口
-h, --help            显示帮助

交互界面：↑/↓ 移动，空格切换选中，Enter 开始安装。
stk 统一入口始终安装，模块可以全部不选。
EOF
}

list_modules() {
  local index
  for index in "${!MODULE_IDS[@]}"; do
    printf '%-12s %-20s %s\n' \
      "${MODULE_IDS[index]}" \
      "${MODULE_NAMES[index]}" \
      "${MODULE_DESCRIPTIONS[index]}"
  done
}

module_index() {
  local requested="$1" index
  for index in "${!MODULE_IDS[@]}"; do
    if [[ "${MODULE_IDS[index]}" == "${requested}" ]]; then
      printf '%s\n' "${index}"
      return 0
    fi
  done
  return 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)
      if [[ "${MODE}" == 'modules' || "${MODE}" == 'none' ]]; then
        printf '错误：--all 不能与 --module/--no-modules 同时使用\n' >&2
        exit 2
      fi
      MODE='all'
      shift
      ;;
    --no-modules)
      if [[ "${MODE}" == 'all' || "${MODE}" == 'modules' ]]; then
        printf '错误：--no-modules 不能与 --all/--module 同时使用\n' >&2
        exit 2
      fi
      MODE='none'
      shift
      ;;
    --module)
      if [[ $# -lt 2 ]]; then
        printf '错误：--module 缺少模块名\n' >&2
        exit 2
      fi
      if [[ "${MODE}" == 'all' || "${MODE}" == 'none' ]]; then
        printf '错误：--module 不能与 --all/--no-modules 同时使用\n' >&2
        exit 2
      fi
      MODE='modules'
      REQUESTED_MODULES+=("$2")
      shift 2
      ;;
    --list)
      list_modules
      exit 0
      ;;
    --prefix)
      if [[ $# -lt 2 ]]; then
        printf '错误：--prefix 缺少目录参数\n' >&2
        exit 2
      fi
      BIN_DIR="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf '错误：未知参数 %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${BIN_DIR}" ]]; then
  BIN_DIR="$(stk_default_bin_dir)"
fi
stk_validate_absolute_path "${BIN_DIR}" >/dev/null || exit 2
if [[ -L "${PROJECT_STATE_DIR}" ]]; then
  printf '错误：项目状态目录不能是符号链接：%s\n' "${PROJECT_STATE_DIR}" >&2
  exit 1
fi
mkdir -p -- "${PROJECT_STATE_DIR}"
chmod 0700 "${PROJECT_STATE_DIR}"
if [[ -L "${INSTALL_STATE_FILE}" ]]; then
  printf '错误：安装状态文件不能是符号链接：%s\n' "${INSTALL_STATE_FILE}" >&2
  exit 1
fi

draw_selector() {
  local cursor="$1" message="$2" index checkbox marker
  if [[ -t 1 ]]; then
    printf '\033[H\033[2J'
  fi
  printf 'Server Toolkit Installer\n\n'
  printf '选择要安装的模块：\n\n'
  for index in "${!MODULE_IDS[@]}"; do
    if (( SELECTED[index] )); then
      checkbox='[x]'
    else
      checkbox='[ ]'
    fi
    if (( index == cursor )); then
      marker='›'
    else
      marker=' '
    fi
    printf '  %s %s %-20s %s\n' \
      "${marker}" "${checkbox}" "${MODULE_NAMES[index]}" "${MODULE_DESCRIPTIONS[index]}"
  done
  printf '\n↑/↓ 移动  •  Space 选择  •  Enter 安装  •  q / Esc 退出\n'
  if [[ -n "${message}" ]]; then
    printf '\n%s\n' "${message}"
  fi
}

interactive_select() {
  local cursor=0 count="${#MODULE_IDS[@]}" key next_key final_key message=''
  if [[ ! -t 0 || ! -t 1 ]]; then
    printf '错误：交互选择需要终端；自动化场景请使用 --all 或 --module。\n' >&2
    return 2
  fi
  while true; do
    draw_selector "${cursor}" "${message}"
    message=''
    if ! IFS= read -rsn1 key; then
      return 1
    fi
    case "${key}" in
      $'\x1b')
        next_key=''
        final_key=''
        IFS= read -rsn1 -t 0.15 next_key || true
        if [[ "${next_key}" == '[' ]]; then
          IFS= read -rsn1 -t 0.15 final_key || true
          case "${final_key}" in
            A) cursor=$(( (cursor - 1 + count) % count )) ;;
            B) cursor=$(( (cursor + 1) % count )) ;;
          esac
        elif [[ -z "${next_key}" ]]; then
          return 1
        fi
        ;;
      k) cursor=$(( (cursor - 1 + count) % count )) ;;
      j) cursor=$(( (cursor + 1) % count )) ;;
      ' ') SELECTED[cursor]=$(( 1 - SELECTED[cursor] )) ;;
      q|Q) return 1 ;;
      '') return 0 ;;
    esac
  done
}

install_toolkit_entry() {
  local target="${BIN_DIR%/}/stk" current temp_target
  if [[ ! -f "${TOOLKIT_MANAGER}" ]]; then
    printf '错误：找不到 %s\n' "${TOOLKIT_MANAGER}" >&2
    return 1
  fi
  mkdir -p -- "${BIN_DIR}" "${PROJECT_STATE_DIR}"
  chmod 0755 "${TOOLKIT_MANAGER}"
  if [[ -d "${target}" && ! -L "${target}" ]]; then
    printf '错误：%s 是目录，拒绝覆盖\n' "${target}" >&2
    return 1
  fi
  if [[ -L "${target}" ]]; then
    current="$(readlink -f -- "${target}" 2>/dev/null || true)"
    if [[ "${current}" == "${TOOLKIT_MANAGER}" ]]; then
      printf '✓ stk 已安装，无需重复操作：%s\n' "${target}"
      printf 'schema_version=1\nbin_dir=%s\n' "${BIN_DIR}" |
        stk_atomic_write "${INSTALL_STATE_FILE}" 0600
      return 0
    fi
  fi
  if [[ -e "${target}" || -L "${target}" ]]; then
    if (( FORCE == 0 )); then
      printf '错误：%s 已存在；确认覆盖时请添加 --force\n' "${target}" >&2
      return 1
    fi
  fi
  temp_target="${BIN_DIR%/}/.stk.$$.tmp"
  rm -f -- "${temp_target}"
  ln -s "${TOOLKIT_MANAGER}" "${temp_target}" || return 1
  mv -Tf -- "${temp_target}" "${target}" || {
    rm -f -- "${temp_target}"
    return 1
  }
  printf 'schema_version=1\nbin_dir=%s\n' "${BIN_DIR}" |
    stk_atomic_write "${INSTALL_STATE_FILE}" 0600
  printf '✓ stk 已安装到 %s\n' "${target}"
}

install_preflight() {
  local index installer target current
  target="${BIN_DIR%/}/stk"
  if [[ -d "${target}" && ! -L "${target}" ]]; then
    printf '错误：%s 是目录，拒绝覆盖\n' "${target}" >&2
    return 1
  elif [[ -e "${target}" || -L "${target}" ]]; then
    current="$(readlink -f -- "${target}" 2>/dev/null || true)"
    if [[ "${current}" != "${TOOLKIT_MANAGER}" && ${FORCE} -ne 1 ]]; then
      printf '错误：%s 已存在；确认覆盖时请添加 --force\n' "${target}" >&2
      return 1
    fi
  fi
  stk_require_command sha256sum || return 1
  stk_python_find_bootstrap || return 1
  "${STK_BOOTSTRAP_PYTHON_RESOLVED}" -c 'import venv' >/dev/null 2>&1 || {
    stk_fail '当前 Python >= 3.10 缺少 venv 支持'
    return 1
  }
  for index in "${!MODULE_IDS[@]}"; do
    (( SELECTED[index] )) || continue
    installer="${MODULE_INSTALLERS[index]}"
    [[ -x "${installer}" ]] || { printf '错误：找不到可执行的模块安装器 %s\n' "${installer}" >&2; return 1; }
    if (( FORCE )); then
      "${installer}" --preflight --prefix "${BIN_DIR}" --force || return 1
    else
      "${installer}" --preflight --prefix "${BIN_DIR}" || return 1
    fi
  done
}

install_transaction_begin() {
  local proxy_config target supervisor_config codex_home installed_gds_codex_home
  codex_home="$(stk_codex_home)"
  stk_transaction_begin || return 1
  for target in \
    "${BIN_DIR%/}/stk" "${BIN_DIR%/}/pm" "${BIN_DIR%/}/gds" "${BIN_DIR%/}/codex" \
    "${BIN_DIR%/}/fd" "${BIN_DIR%/}/bat" \
    "${INSTALL_STATE_FILE}" "${STK_CONFIG_FILE}" "${STK_STATE_META_FILE}" \
    "${STK_USER_STATE_DIR}/modules/base-tools.conf" "${STK_USER_STATE_DIR}/modules/shell.conf" \
    "${STK_USER_STATE_DIR}/modules/gds.conf" \
    "${codex_home}/AGENTS.md" "${codex_home}/AGENTS.override.md" \
    "${STK_BASHRC_FILE}" "${STK_PYTHON_RUNTIME_DIR}" \
    "${STK_ROOT}/gds/core/scheduler" "${STK_ROOT}/gds/core/scheduler.source.sha256"; do
    stk_transaction_add "${target}" || return 1
  done
  installed_gds_codex_home="$(stk_state_value "$(stk_gds_agents_state_file)" codex_home)"
  if [[ -n "${installed_gds_codex_home}" && "${installed_gds_codex_home}" != "${codex_home}" ]]; then
    stk_transaction_add "${installed_gds_codex_home}/AGENTS.md" || return 1
    stk_transaction_add "${installed_gds_codex_home}/AGENTS.override.md" || return 1
  fi
  proxy_config="$(stk_config_value proxy_config_path)"
  stk_transaction_add "${proxy_config}" || return 1
  stk_transaction_add "${proxy_config}.previous" || return 1
  local index
  for index in "${!MODULE_IDS[@]}"; do
    if (( SELECTED[index] )) && [[ "${MODULE_IDS[index]}" == 'codex' ]]; then
      stk_codex_restore_snapshot_add "${codex_home}" || return 1
    fi
  done
  if install_selected_service_changed && [[ "$(stk_service_backend)" == 'supervisord' ]] &&
     stk_supervisor_managed_installed; then
    stk_transaction_add "${STK_SUPERVISOR_STATE}" || return 1
    stk_transaction_add "${STK_SUPERVISOR_DROPIN}" || return 1
    supervisor_config="$(stk_supervisor_config 2>/dev/null || true)"
    [[ -z "${supervisor_config}" ]] || stk_transaction_add "${supervisor_config}" || return 1
  fi
}

install_selected_service_changed() {
  local index
  for index in "${!MODULE_IDS[@]}"; do
    (( SELECTED[index] )) || continue
    [[ "$(stk_module_field "${MODULE_IDS[index]}" 8)" != 'service' ]] || return 0
  done
  return 1
}

prepare_selection() {
  local requested index
  case "${MODE}" in
    none)
      for index in "${!SELECTED[@]}"; do
        SELECTED[index]=0
      done
      ;;
    all)
      for index in "${!SELECTED[@]}"; do
        SELECTED[index]=1
      done
      ;;
    modules)
      for index in "${!SELECTED[@]}"; do
        SELECTED[index]=0
      done
      for requested in "${REQUESTED_MODULES[@]}"; do
        if ! index="$(module_index "${requested}")"; then
          printf '错误：未知模块 %s\n' "${requested}" >&2
          printf '可用模块：\n' >&2
          list_modules >&2
          return 2
        fi
        SELECTED[index]=1
      done
      ;;
    interactive)
      interactive_select
      ;;
  esac
}

install_selected() {
  local index installer result=0 configure_answer=''
  local -a installer_args=()
  installer_args+=(--prefix "${BIN_DIR}")
  if (( FORCE )); then
    installer_args+=(--force)
  fi

  install_preflight || return 1
  install_transaction_begin || { stk_transaction_finish; return 1; }
  printf '\n==> Server Toolkit (stk)\n'
  if ! stk_python_runtime_ensure; then
    stk_transaction_rollback || stk_warn 'Python 环境失败后，安装快照未能完整恢复'
    return 1
  fi
  if ! install_toolkit_entry; then
    stk_transaction_rollback || stk_warn '入口安装失败后，安装快照未能完整恢复'
    return 1
  fi
  if ! STK_CONFIG_LOCK_HELD=1 "${TOOLKIT_MANAGER}" config init; then
    stk_transaction_rollback || stk_warn '配置初始化失败后，安装快照未能完整恢复'
    return 1
  fi
  case ":${PATH}:" in
    *":${BIN_DIR}:"*) ;;
    *) printf '提示：%s 不在 PATH 中，请将它加入 shell PATH。\n' "${BIN_DIR}" ;;
  esac
  printf '\n'
  for index in "${!MODULE_IDS[@]}"; do
    (( SELECTED[index] )) || continue
    installer="${MODULE_INSTALLERS[index]}"
    printf '==> %s (%s)\n' "${MODULE_NAMES[index]}" "${MODULE_IDS[index]}"
    if ! STK_CONFIG_LOCK_HELD=1 "${installer}" "${installer_args[@]}"; then
      printf '错误：模块 %s 安装失败\n' "${MODULE_IDS[index]}" >&2
      result=1
      break
    fi
    printf '\n'
  done
  if (( result == 0 )) && install_selected_service_changed &&
     [[ "$(stk_service_backend)" == 'supervisord' ]] && stk_supervisor_managed_installed; then
    printf '==> 同步 supervisord 托管配置\n'
    if ! stk_supervisor_install; then
      result=1
    fi
  fi
  if (( result == 0 )) && [[ "${MODE}" == 'interactive' && -t 0 && -t 1 ]]; then
    printf '是否现在填写个人配置？ [y/N] '
    read -r configure_answer || true
    if [[ "${configure_answer}" == 'y' || "${configure_answer}" == 'Y' ]]; then
      STK_CONFIG_LOCK_HELD=1 "${TOOLKIT_MANAGER}" config setup || result=1
    fi
  fi
  if (( result == 0 )); then
    stk_transaction_finish
    printf '✓ Server Toolkit 已完成所选模块的安装。\n'
  else
    stk_transaction_rollback || stk_warn '安装失败，且快照未能完整恢复'
    if install_selected_service_changed && [[ "$(stk_service_backend)" == 'supervisord' ]] &&
       stk_supervisor_managed_installed; then
      stk_supervisor_reload >/dev/null 2>&1 || stk_warn '文件已回滚，但 supervisord 运行态需要手动检查'
    fi
  fi
  return "${result}"
}

prepare_selection || exit $?
stk_lock_acquire || exit 1
STK_GLOBAL_LOCK_HELD=1
export STK_GLOBAL_LOCK_HELD
trap 'stk_lock_release' EXIT
if [[ -e "${STK_CONFIG_FILE}" ]] && ! stk_config_runtime_validate "${STK_CONFIG_FILE}"; then
  printf '错误：统一配置无法验证，安装已中止。\n' >&2
  stk_lock_release
  trap - EXIT
  exit 1
fi
install_selected
install_result=$?
stk_lock_release
trap - EXIT
exit "${install_result}"
