#!/usr/bin/env bash

set -euo pipefail

(( $# > 0 )) || { printf '用法：apt-install.sh PACKAGE...\n' >&2; exit 2; }
[[ ${EUID} -eq 0 ]] || { printf '✗ 安装系统包需要 root\n' >&2; exit 1; }
command -v apt-get >/dev/null 2>&1 || { printf '✗ 当前仅支持 apt 系发行版\n' >&2; exit 1; }

STATE_ROOT="${SERVER_TOOLKIT_STATE_DIR:-${HOME}/.local/state/server-toolkit}"
MARKER="${STATE_ROOT}/apt-update.timestamp"
[[ ! -L "${STATE_ROOT}" && ! -L "${MARKER}" ]] || { printf '✗ apt 状态路径不能是符号链接\n' >&2; exit 1; }
mkdir -p -- "${STATE_ROOT}"
chmod 0700 "${STATE_ROOT}"

export DEBIAN_FRONTEND=noninteractive
if [[ ! -f "${MARKER}" ]] || ! find "${MARKER}" -mmin -60 -print -quit 2>/dev/null | grep -q .; then
  apt-get update
  (umask 077; date -u +%s >"${MARKER}")
fi
apt-get install -y --no-install-recommends "$@"
