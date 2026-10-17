#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
MANAGER="${PROJECT_DIR}/pm.sh"
TOOLKIT_DIR="$(dirname -- "${PROJECT_DIR}")"
STK_ROOT="${TOOLKIT_DIR}"
# shellcheck source=../lib/common.sh
source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
# shellcheck source=../lib/python-runtime.sh
source "${TOOLKIT_DIR}/lib/python-runtime.sh"
CENTRAL_CONFIG="${SERVER_TOOLKIT_CONFIG:-${XDG_CONFIG_HOME:-${HOME}/.config}/server-toolkit/config.conf}"
CONFIG_FILE="$(awk -F= '$1=="proxy_config_path" {sub(/^[^=]*=/,""); print; exit}' "${CENTRAL_CONFIG}" 2>/dev/null || true)"
DEFAULT_CONFIG_DIR="${SERVER_TOOLKIT_CONFIG_DIR:-${XDG_CONFIG_HOME:-${HOME}/.config}/server-toolkit}"
CONFIG_FILE="${PROXY_CONFIG_FILE:-${CONFIG_FILE:-${DEFAULT_CONFIG_DIR}/proxy.yaml}}"
FORCE=0
BIN_DIR=''
HAS_ISP=''
SUBSCRIPTION_URL=''
ISP_SERVER=''
ISP_PORT=''
ISP_CIPHER=''
ISP_PASSWORD=''
INPUT_FILE=''
CONFIGURE_ONLY=0
RECONFIGURE=0
PREFLIGHT=0

usage() {
  cat <<'EOF'
用法：./install.sh [--prefix BIN目录] [--force] [--reconfigure]
       ./install.sh --configure-only

默认安装位置：
  root 用户       /usr/local/bin/pm
  普通用户        ~/.local/bin/pm

首次交互安装会询问订阅 URL 以及是否存在 Shadowsocks ISP 节点。
自动化安装可使用 --input-file 指向权限为 0600、包含五个 NUL 分隔字段的文件：
订阅 URL、ISP server、ISP port、ISP cipher、ISP password。
EOF
}

update_toolkit_config() {
  local key="$1" value="$2"
  if [[ "${STK_CONFIG_LOCK_HELD:-0}" == '1' ]]; then
    export STK_ROOT="${TOOLKIT_DIR}"
    # shellcheck source=../lib/common.sh
    source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
    # shellcheck source=../lib/config.sh
    source "${TOOLKIT_DIR}/lib/config.sh"
    stk_config_set "${key}" "${value}" >/dev/null
  else
    "${TOOLKIT_DIR}/toolkit.sh" config set "${key}" "${value}" >/dev/null
  fi
}

restore_private_config() {
  local result
  local -a fields=()
  # Only the explicit persistent-platform layout adopts old private snapshots.
  [[ -n "${STK_PLATFORM_LOCAL_ROOT}" ]] || return 1
  result="$("${STK_PYTHON}" "${TOOLKIT_DIR}/scripts/find-proxy-backup.py" "${TOOLKIT_DIR}")" || return 1
  mapfile -t fields <<<"${result}"
  [[ ${#fields[@]} -eq 3 ]] || return 1
  cat -- "${fields[0]}" | stk_atomic_write "${CONFIG_FILE}" 0600 || return 1
  chmod 0700 "$(dirname -- "${CONFIG_FILE}")" || return 1
  if ! update_toolkit_config proxy_config_path "${CONFIG_FILE}" ||
     ! update_toolkit_config proxy_common_listener "${fields[1]}" ||
     ! update_toolkit_config proxy_codex_listener "${fields[2]}"; then
    rm -f -- "${CONFIG_FILE}"
    return 1
  fi
  printf '✓ 已从本地私有快照恢复代理配置和 listener；未启动代理。\n'
}

prompt_required() {
  local prompt="$1" variable_name="$2" secret="${3:-0}" value
  while [[ -z "${!variable_name}" ]]; do
    if (( secret )); then
      read -rsp "${prompt}: " value || return 1
      printf '\n' >&2
    else
      read -rp "${prompt}: " value || return 1
    fi
    printf -v "${variable_name}" '%s' "${value}"
  done
}

render_private_config() {
  local template listener='common' answer='' secrets_file candidate backup='' input_mode central_backup=''
  local previous_config="${CONFIG_FILE}.previous"
  local retained_backup='' retained_central_backup='' recovery_paths=''
  local had_config=0 had_central=0 rollback_failed=0
  local -a input_fields=()

  restore_private_configs() {
    rollback_failed=0
    if (( had_config )); then
      if mv -f -- "${backup}" "${CONFIG_FILE}"; then
        backup=''
      else
        retained_backup="${backup}"
        backup=''
        rollback_failed=1
      fi
    elif ! rm -f -- "${CONFIG_FILE}"; then
      rollback_failed=1
    fi
    if (( had_central )); then
      if mv -f -- "${central_backup}" "${CENTRAL_CONFIG}"; then
        central_backup=''
      else
        retained_central_backup="${central_backup}"
        central_backup=''
        rollback_failed=1
      fi
    elif ! rm -f -- "${CENTRAL_CONFIG}"; then
      rollback_failed=1
    fi
    (( rollback_failed == 0 ))
  }

  report_restore_failure() {
    recovery_paths=''
    [[ -z "${retained_backup}" ]] || recovery_paths+=" Proxy 原配置：${retained_backup}"
    [[ -z "${retained_central_backup}" ]] || recovery_paths+=" 统一配置：${retained_central_backup}"
    printf '错误：恢复原配置失败；恢复备份已保留。%s\n' "${recovery_paths:- 请手动检查目标文件。}" >&2
  }
  if [[ -n "${INPUT_FILE}" ]]; then
    [[ -f "${INPUT_FILE}" && ! -L "${INPUT_FILE}" ]] || { printf '错误：安全输入必须是普通非符号链接文件\n' >&2; return 2; }
    input_mode="$(stat -c '%a' "${INPUT_FILE}")"
    [[ "${input_mode}" =~ ^[0-7]00$ ]] || { printf '错误：安全输入不能允许 group/other 访问\n' >&2; return 2; }
    mapfile -d '' -t input_fields <"${INPUT_FILE}"
    [[ ${#input_fields[@]} -eq 5 ]] || { printf '错误：安全输入必须包含五个 NUL 分隔字段\n' >&2; return 2; }
    SUBSCRIPTION_URL="${input_fields[0]}"; ISP_SERVER="${input_fields[1]}"; ISP_PORT="${input_fields[2]}"
    ISP_CIPHER="${input_fields[3]}"; ISP_PASSWORD="${input_fields[4]}"
    if [[ -n "${ISP_SERVER}${ISP_PORT}${ISP_CIPHER}${ISP_PASSWORD}" ]]; then HAS_ISP=1; else HAS_ISP=0; fi
  fi
  if [[ -z "${HAS_ISP}" ]]; then
    if [[ -t 0 ]]; then
      read -rp '是否有 Shadowsocks ISP 节点？ [y/N] ' answer || true
      case "${answer:-}" in y|Y|yes|YES) HAS_ISP=1 ;; *) HAS_ISP=0 ;; esac
    else
      HAS_ISP=0
    fi
  fi
  [[ "${HAS_ISP}" == '0' || "${HAS_ISP}" == '1' ]] || {
    printf '错误：ISP 模式必须为 0 或 1\n' >&2
    return 2
  }
  if [[ -t 0 ]]; then
    prompt_required '订阅 URL（HTTPS）' SUBSCRIPTION_URL || return 1
  fi
  if [[ -n "${SUBSCRIPTION_URL}" && "${SUBSCRIPTION_URL}" != https://* ]]; then
    printf '错误：订阅 URL 必须使用 HTTPS\n' >&2
    return 2
  fi
  template="${PROJECT_DIR}/config.example.yaml"
  if [[ "${HAS_ISP}" == '1' ]]; then
    template="${PROJECT_DIR}/config.isp.example.yaml"
    if [[ -t 0 ]]; then
      prompt_required 'ISP server' ISP_SERVER || return 1
      prompt_required 'ISP port' ISP_PORT || return 1
      prompt_required 'ISP Shadowsocks cipher' ISP_CIPHER || return 1
      prompt_required 'ISP password' ISP_PASSWORD 1 || return 1
    fi
    [[ -n "${ISP_SERVER}" && "${ISP_SERVER}" != *[[:space:]]* ]] || { printf '错误：ISP server 无效\n' >&2; return 2; }
    if [[ ! "${ISP_PORT}" =~ ^[0-9]+$ ]] || (( ISP_PORT < 1 || ISP_PORT > 65535 )); then
      printf '错误：ISP port 无效\n' >&2
      return 2
    fi
    [[ -n "${ISP_CIPHER}" && "${ISP_CIPHER}" != *[[:space:]]* && -n "${ISP_PASSWORD}" ]] || { printf '错误：ISP cipher/password 不能为空\n' >&2; return 2; }
    listener='isp'
  fi
  if [[ -L "${previous_config}" || ( -e "${previous_config}" && ! -f "${previous_config}" ) ]]; then
    printf '错误：Proxy 旧配置备份必须是普通非符号链接文件：%s\n' "${previous_config}" >&2
    return 1
  fi
  mkdir -p -- "$(dirname -- "${CONFIG_FILE}")"
  chmod 0700 "$(dirname -- "${CONFIG_FILE}")"
  secrets_file="$(mktemp "$(dirname -- "${CONFIG_FILE}")/.proxy-input.XXXXXX")" || return 1
  candidate="$(mktemp "$(dirname -- "${CONFIG_FILE}")/.proxy-candidate.XXXXXX")" || { rm -f -- "${secrets_file}"; return 1; }
  chmod 0600 "${secrets_file}" "${candidate}"
  trap 'rm -f -- "${secrets_file:-}" "${candidate:-}" "${backup:-}" "${central_backup:-}"; trap - RETURN' RETURN
  printf '%s\0%s\0%s\0%s\0%s\0' "${SUBSCRIPTION_URL}" "${ISP_SERVER}" "${ISP_PORT}" "${ISP_CIPHER}" "${ISP_PASSWORD}" >"${secrets_file}"
  "${STK_PYTHON}" "${PROJECT_DIR}/render_config.py" \
    --template "${template}" --output "${candidate}" --input-file "${secrets_file}" || return 1
  if [[ -x "${PROJECT_DIR}/core/mihomo" ]]; then
    "${PROJECT_DIR}/core/mihomo" -t -d "${PROJECT_DIR}/workdir" -f "${candidate}" >/dev/null || {
      printf '错误：新 Proxy 配置未通过 mihomo 校验，原配置未改变\n' >&2
      return 1
    }
  fi
  if [[ -e "${CONFIG_FILE}" ]]; then
    had_config=1
    backup="$(mktemp "$(dirname -- "${CONFIG_FILE}")/.proxy-old.XXXXXX")" || return 1
    cp -p -- "${CONFIG_FILE}" "${backup}" || return 1
    chmod 0600 "${backup}"
  fi
  if [[ -e "${CENTRAL_CONFIG}" ]]; then
    had_central=1
    central_backup="$(mktemp "$(dirname -- "${CENTRAL_CONFIG}")/.config-old.XXXXXX")" || return 1
    cp -p -- "${CENTRAL_CONFIG}" "${central_backup}" || return 1
    chmod 0600 "${central_backup}"
  fi
  mv -f -- "${candidate}" "${CONFIG_FILE}" || return 1
  chmod 0600 "${CONFIG_FILE}"
  if ! update_toolkit_config proxy_config_path "${CONFIG_FILE}" ||
     ! update_toolkit_config proxy_codex_listener "${listener}"; then
    if restore_private_configs; then
      printf '错误：统一配置更新失败，Proxy 配置已恢复\n' >&2
    else
      report_restore_failure
    fi
    return 1
  fi
  if (( had_config )) && ! mv -Tf -- "${backup}" "${previous_config}"; then
    if restore_private_configs; then
      printf '错误：无法保存 Proxy 旧配置，已恢复原配置\n' >&2
    else
      report_restore_failure
    fi
    return 1
  fi
  backup=''
  rm -f -- "${central_backup}"
  if [[ -n "${SUBSCRIPTION_URL}" ]]; then
    printf '✓ 私有 Proxy 配置已生成：%s\n' "${CONFIG_FILE}"
  else
    printf '提示：已生成无 ISP 模板 %s，请填写订阅 URL。\n' "${CONFIG_FILE}"
  fi
  trap - RETURN
  rm -f -- "${secrets_file}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
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
    --reconfigure)
      RECONFIGURE=1
      shift
      ;;
    --configure-only)
      CONFIGURE_ONLY=1
      RECONFIGURE=1
      shift
      ;;
    --preflight)
      PREFLIGHT=1
      shift
      ;;
    --migrate)
      shift
      ;;
    --input-file)
      [[ $# -ge 2 ]] || { printf '错误：--input-file 缺少路径\n' >&2; exit 2; }
      INPUT_FILE="$2"
      shift 2
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

if [[ -e "${CENTRAL_CONFIG}" ]]; then
  export STK_ROOT="${TOOLKIT_DIR}"
  # shellcheck source=../lib/common.sh
  source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
  # shellcheck source=../lib/config.sh
  source "${TOOLKIT_DIR}/lib/config.sh"
  if ! stk_config_runtime_validate "${CENTRAL_CONFIG}"; then
    printf '错误：统一配置无效；请先执行 stk config edit 或 stk config repair\n' >&2
    exit 1
  fi
fi

if [[ ! -f "${MANAGER}" ]]; then
  printf '错误：找不到 %s\n' "${MANAGER}" >&2
  exit 1
fi

if ! stk_validate_absolute_path "${CONFIG_FILE}" >/dev/null 2>&1; then
  printf '错误：Proxy 私有配置路径必须是绝对路径\n' >&2
  exit 1
elif [[ -L "${CONFIG_FILE}" ]]; then
  printf '错误：%s 是符号链接，拒绝作为敏感配置使用\n' "${CONFIG_FILE}" >&2
  exit 1
elif [[ -e "${CONFIG_FILE}" && ! -f "${CONFIG_FILE}" ]]; then
  printf '错误：%s 不是普通文件\n' "${CONFIG_FILE}" >&2
  exit 1
fi

TARGET="${BIN_DIR%/}/pm"
if (( CONFIGURE_ONLY == 0 )); then
  if [[ -d "${TARGET}" && ! -L "${TARGET}" ]]; then
    printf '错误：%s 是目录，拒绝覆盖\n' "${TARGET}" >&2
    exit 1
  elif [[ -e "${TARGET}" || -L "${TARGET}" ]]; then
    CURRENT_TARGET="$(readlink -f -- "${TARGET}" 2>/dev/null || true)"
    if [[ "${CURRENT_TARGET}" != "${MANAGER}" && ${FORCE} -ne 1 ]]; then
      printf '错误：%s 已存在；确认覆盖时请添加 --force\n' "${TARGET}" >&2
      exit 1
    fi
  fi
fi
(( PREFLIGHT == 0 )) || exit 0

stk_python_runtime_ensure || exit 1

if [[ ! -e "${CONFIG_FILE}" && ${RECONFIGURE} -eq 0 && -z "${INPUT_FILE}" ]] && restore_private_config; then
  :
elif [[ ! -e "${CONFIG_FILE}" || ${RECONFIGURE} -eq 1 ]]; then
  render_private_config
else
  chmod 0600 "${CONFIG_FILE}"
fi
if (( CONFIGURE_ONLY )); then
  printf '✓ Proxy 配置完成；如由 supervisord 托管，请执行 stk module restart proxy 使其生效。\n'
  exit 0
fi
mkdir -p -- "${PROJECT_DIR}/workdir" "${PROJECT_DIR}/workdir/providers"
chmod 0700 "${PROJECT_DIR}/workdir" "${PROJECT_DIR}/workdir/providers"

if [[ -z "${BIN_DIR}" ]]; then
  if [[ ${EUID} -eq 0 ]]; then
    BIN_DIR='/usr/local/bin'
  else
    BIN_DIR="${HOME}/.local/bin"
  fi
fi

mkdir -p -- "${BIN_DIR}"
chmod 0755 "${MANAGER}"

if [[ -L "${TARGET}" ]]; then
  CURRENT_LINK="$(readlink -- "${TARGET}" 2>/dev/null || true)"
  CURRENT_TARGET="$(readlink -f -- "${TARGET}" 2>/dev/null || true)"
  if [[ "${CURRENT_LINK}" == "${MANAGER}" || "${CURRENT_TARGET}" == "${MANAGER}" ]]; then
    printf '✓ pm 已安装，无需重复操作：%s\n' "${TARGET}"
    printf '  项目目录：%s\n' "${PROJECT_DIR}"
    exit 0
  fi
fi

if [[ -d "${TARGET}" && ! -L "${TARGET}" ]]; then
  printf '错误：%s 是目录，拒绝覆盖\n' "${TARGET}" >&2
  exit 1
fi

if [[ -e "${TARGET}" || -L "${TARGET}" ]]; then
  if [[ ${FORCE} -ne 1 ]]; then
    printf '错误：%s 已存在；确认覆盖时请添加 --force\n' "${TARGET}" >&2
    exit 1
  fi
fi

TEMP_TARGET="${BIN_DIR%/}/.pm.$$.tmp"
trap 'rm -f -- "${TEMP_TARGET}"' EXIT
ln -s "${MANAGER}" "${TEMP_TARGET}"
mv -Tf -- "${TEMP_TARGET}" "${TARGET}"
trap - EXIT

printf '✓ pm 已安装到 %s\n' "${TARGET}"
printf '  项目目录：%s\n' "${PROJECT_DIR}"

case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *)
    printf '提示：%s 不在 PATH 中，请将它加入 shell 的 PATH。\n' "${BIN_DIR}"
    ;;
esac

printf '\n下一步：执行 pm。订阅地址位于 %s。\n' "${CONFIG_FILE}"
