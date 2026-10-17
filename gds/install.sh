#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
TOOLKIT_DIR="$(dirname -- "${PROJECT_DIR}")"
# shellcheck disable=SC2034 # Consumed while sourcing lib/common.sh.
STK_ROOT="${TOOLKIT_DIR}"
# shellcheck source=../lib/common.sh
source "${TOOLKIT_DIR}/lib/common.sh" || exit 1
# shellcheck source=../lib/config.sh
source "${TOOLKIT_DIR}/lib/config.sh"
# shellcheck source=../lib/codex.sh
source "${TOOLKIT_DIR}/lib/codex.sh"
# shellcheck source=../lib/modules.sh
source "${TOOLKIT_DIR}/lib/modules.sh"
MANAGER="${PROJECT_DIR}/gds.sh"
FORCE=0
BIN_DIR=''
PREFLIGHT=0

usage() {
  cat <<'EOF'
用法：./install.sh [--prefix BIN目录] [--force]

默认安装位置：
  root 用户       /usr/local/bin/gds
  普通用户        ~/.local/bin/gds

安装的只是命令入口；配置、状态和日志始终留在当前项目目录。
EOF
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
    --preflight)
      PREFLIGHT=1
      shift
      ;;
    --migrate)
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

if [[ ! -f "${MANAGER}" ]]; then
  printf '错误：找不到 %s\n' "${MANAGER}" >&2
  exit 1
fi

if [[ -z "${BIN_DIR}" ]]; then
  BIN_DIR="$(stk_default_bin_dir)"
fi

TARGET="${BIN_DIR%/}/gds"
stk_validate_absolute_path "${BIN_DIR}" >/dev/null || exit 2
if [[ -L "${PROJECT_DIR}/config.conf" ]]; then
  printf '错误：%s 是符号链接，拒绝作为配置使用\n' "${PROJECT_DIR}/config.conf" >&2
  exit 1
elif [[ ! -f "${PROJECT_DIR}/config.conf" ]]; then
  printf '错误：找不到 GDS 配置：%s\n' "${PROJECT_DIR}/config.conf" >&2
  exit 1
fi
if [[ -L "${PROJECT_DIR}/workdir" ]]; then
  printf '错误：%s 是符号链接，拒绝作为运行目录使用\n' "${PROJECT_DIR}/workdir" >&2
  exit 1
fi
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
stk_gds_agents_preflight || exit 1
command -v nvcc >/dev/null 2>&1 || { printf '错误：GDS 需要 nvcc\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf '错误：GDS 需要 sha256sum\n' >&2; exit 1; }
[[ -r "${PROJECT_DIR}/src/scheduler.cu" ]] || { printf '错误：找不到 scheduler 源码\n' >&2; exit 1; }
(( PREFLIGHT == 0 )) || exit 0

mkdir -p -- "${BIN_DIR}"
chmod 0755 "${MANAGER}"
chmod 0600 "${PROJECT_DIR}/config.conf"
mkdir -p -- "${PROJECT_DIR}/workdir"
chmod 0700 "${PROJECT_DIR}/workdir"

if ! "${MANAGER}" __ensure_core; then
  printf '错误：无法编译 scheduler CUDA worker\n' >&2
  exit 1
fi

if [[ -d "${TARGET}" && ! -L "${TARGET}" ]]; then
  printf '错误：%s 是目录，拒绝覆盖\n' "${TARGET}" >&2
  exit 1
fi

if [[ -L "${TARGET}" ]]; then
  CURRENT_LINK="$(readlink -- "${TARGET}" 2>/dev/null || true)"
  CURRENT_TARGET="$(readlink -f -- "${TARGET}" 2>/dev/null || true)"
  if [[ "${CURRENT_LINK}" == "${MANAGER}" || "${CURRENT_TARGET}" == "${MANAGER}" ]]; then
    stk_gds_agents_configure || exit 1
    printf '✓ gds 已安装，无需重复操作：%s\n' "${TARGET}"
    printf '  项目目录：%s\n' "${PROJECT_DIR}"
    exit 0
  fi
fi

if [[ -e "${TARGET}" || -L "${TARGET}" ]]; then
  if [[ ${FORCE} -ne 1 ]]; then
    printf '错误：%s 已存在；确认覆盖时请添加 --force\n' "${TARGET}" >&2
    exit 1
  fi
fi

TEMP_TARGET="${BIN_DIR%/}/.gds.$$.tmp"
trap 'rm -f -- "${TEMP_TARGET}"' EXIT
ln -s "${MANAGER}" "${TEMP_TARGET}"
mv -Tf -- "${TEMP_TARGET}" "${TARGET}"
trap - EXIT

stk_gds_agents_configure || exit 1

printf '✓ gds 已安装到 %s\n' "${TARGET}"
printf '  项目目录：%s\n' "${PROJECT_DIR}"

case ":${PATH}:" in
  *":${BIN_DIR}:"*) ;;
  *) printf '提示：%s 不在 PATH 中，请将它加入 shell 的 PATH。\n' "${BIN_DIR}" ;;
esac

if ! command -v tmux >/dev/null 2>&1; then
  printf '提示：尚未检测到 tmux，启动监听前需要安装。\n'
fi
if ! command -v nvidia-smi >/dev/null 2>&1; then
  printf '提示：尚未检测到 nvidia-smi；gds 只能在 NVIDIA GPU/驱动可用时启动。\n'
fi

printf '\n下一步：执行 gds 打开管理面板，或执行 gds start。\n'
