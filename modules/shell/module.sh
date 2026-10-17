#!/usr/bin/env bash

set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
STK_ROOT="${ROOT}"
source "${ROOT}/lib/common.sh" || exit 1
LABEL='shell-convenience'
STATE_FILE="${STK_USER_STATE_DIR}/modules/shell.conf"

content() {
  cat <<'EOF'
export HISTCONTROL=ignoreboth:erasedups
export HISTSIZE=50000
export HISTFILESIZE=100000
shopt -s histappend checkwinsize
alias ll='ls -alF'
alias la='ls -A'
alias ..='cd ..'
alias ...='cd ../..'
command -v fzf >/dev/null 2>&1 && [[ -r /usr/share/doc/fzf/examples/key-bindings.bash ]] && source /usr/share/doc/fzf/examples/key-bindings.bash
EOF
}

case "${1:-status}" in
  configure)
    stk_module_state_prepare "${STATE_FILE}" || exit 1
    stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${LABEL}" "$(content)" || exit 1
    stk_module_state_write "${STATE_FILE}" || exit 1
    printf '✓ shell 便捷配置已安装，新 shell 生效\n'
    ;;
  status)
    if stk_module_state_valid "${STATE_FILE}" && stk_managed_block_exists "${STK_BASHRC_FILE}" "${LABEL}"; then [[ "${2:-}" == '--quiet' ]] || printf 'installed\n'; else [[ "${2:-}" == '--quiet' ]] || printf 'not installed\n'; exit 1; fi
    ;;
  remove) stk_rewrite_managed_block "${STK_BASHRC_FILE}" "${LABEL}" '' || exit 1; rm -f -- "${STATE_FILE}" ;;
  *) exit 2 ;;
esac
