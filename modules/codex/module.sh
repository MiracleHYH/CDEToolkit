#!/usr/bin/env bash

set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
STK_ROOT="${ROOT}"
# shellcheck source=../../lib/common.sh
source "${ROOT}/lib/common.sh" || exit 1
# shellcheck source=../../lib/config.sh
source "${ROOT}/lib/config.sh"
# shellcheck source=../../lib/python-runtime.sh
source "${ROOT}/lib/python-runtime.sh"
# shellcheck source=../../lib/transaction.sh
source "${ROOT}/lib/transaction.sh"
# shellcheck source=../../lib/proxy-env.sh
source "${ROOT}/lib/proxy-env.sh"
# shellcheck source=../../lib/codex.sh
source "${ROOT}/lib/codex.sh"

case "${1:-status}" in
  configure) stk_codex_restore "$(stk_codex_home)" ;;
  status)
    target="$(stk_state_value "${STK_CODEX_LAUNCHER_STATE}" launcher_path)"
    if stk_codex_real_path "$(stk_codex_home)" >/dev/null 2>&1 && stk_codex_launcher_state_valid &&
       [[ -L "${target}" && "$(readlink -f -- "${target}" 2>/dev/null || true)" == "${ROOT}/codex-launcher.sh" ]]; then
      [[ "${2:-}" == '--quiet' ]] || printf 'installed\n'
    else
      [[ "${2:-}" == '--quiet' ]] || printf 'not installed\n'
      exit 1
    fi
    ;;
  remove)
    stk_codex_proxy_disable "$(stk_codex_home)" && stk_codex_launcher_remove && stk_codex_bashrc_remove
    ;;
  *) exit 2 ;;
esac
