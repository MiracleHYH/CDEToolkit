#!/usr/bin/env bash

set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC2034 # Consumed while sourcing lib/common.sh.
STK_ROOT="${ROOT}"
# shellcheck source=../../lib/common.sh
source "${ROOT}/lib/common.sh" || exit 1
BIN_DIR="$(stk_default_bin_dir)"
PREFLIGHT=0
MIGRATE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) [[ $# -ge 2 ]] || exit 2; BIN_DIR="$2"; shift 2 ;;
    --force) shift ;;
    --preflight) PREFLIGHT=1; shift ;;
    --migrate) MIGRATE=1; shift ;;
    *) printf '✗ 未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done
stk_validate_absolute_path "${BIN_DIR}" >/dev/null || exit 2
[[ ${EUID} -eq 0 ]] || { printf '✗ 安装 base-tools 需要 root\n' >&2; exit 1; }
if (( MIGRATE == 0 )); then
  command -v apt-get >/dev/null 2>&1 || { printf '✗ base-tools 当前仅支持 apt 系发行版\n' >&2; exit 1; }
fi
(( PREFLIGHT == 0 )) || exit 0
if (( MIGRATE == 0 )); then
  "${ROOT}/scripts/apt-install.sh" ca-certificates curl git jq ripgrep fd-find fzf bat tree htop tmux unzip zip less rsync lsof netcat-openbsd dnsutils iproute2 procps
fi
"${ROOT}/modules/base-tools/module.sh" configure "${BIN_DIR}"
