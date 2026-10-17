#!/usr/bin/env bash

set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
PREFLIGHT=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) [[ $# -ge 2 ]] || exit 2; shift 2 ;;
    --force) shift ;;
    --preflight) PREFLIGHT=1; shift ;;
    --migrate) shift ;;
    *) printf '✗ 未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done
if (( PREFLIGHT )); then
  STK_ROOT="${ROOT}"
  source "${ROOT}/lib/common.sh" || exit 1
  if [[ -L "${STK_BASHRC_FILE}" || ( -e "${STK_BASHRC_FILE}" && ! -f "${STK_BASHRC_FILE}" ) ]]; then
    stk_fail "Bash 配置必须是普通非符号链接文件：${STK_BASHRC_FILE}"
    exit 1
  fi
  if [[ -f "${STK_BASHRC_FILE}" ]]; then
    stk_file_marker_counts "${STK_BASHRC_FILE}" \
      '# >>> server-toolkit shell-convenience >>>' '# <<< server-toolkit shell-convenience <<<' || exit 1
  fi
  exit 0
fi
"${ROOT}/modules/shell/module.sh" configure
