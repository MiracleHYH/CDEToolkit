#!/usr/bin/env bash

set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
STK_ROOT="${ROOT}"
# shellcheck source=../../lib/common.sh
source "${ROOT}/lib/common.sh" || exit 1
# shellcheck source=../../lib/config.sh
source "${ROOT}/lib/config.sh"
# shellcheck source=../../lib/codex.sh
source "${ROOT}/lib/codex.sh"
BIN_DIR="$(stk_default_bin_dir)"
PREFLIGHT=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix) [[ $# -ge 2 ]] || exit 2; BIN_DIR="$2"; shift 2 ;;
    --preflight) PREFLIGHT=1; shift ;;
    --force|--migrate) shift ;;
    *) printf '✗ 未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done
stk_validate_absolute_path "${BIN_DIR}" || exit 2
target="${BIN_DIR}/codex"
if [[ -e "${target}" || -L "${target}" ]]; then
  [[ -L "${target}" && "$(readlink -f -- "${target}" 2>/dev/null || true)" == "${ROOT}/codex-launcher.sh" ]] || {
    printf '✗ Codex 入口已存在且不属于 toolkit：%s\n' "${target}" >&2
    exit 1
  }
fi
(( PREFLIGHT == 0 )) || exit 0
STK_CODEX_LAUNCHER_TARGET="${target}" "${ROOT}/modules/codex/module.sh" configure
