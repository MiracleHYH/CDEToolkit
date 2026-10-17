#!/usr/bin/env bash

set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
STK_ROOT="${ROOT}"
source "${ROOT}/lib/common.sh"
source "${ROOT}/lib/proxy-env.sh"
GDS_SCRIPT="${ROOT}/gds/gds.sh"

module_enabled() {
  stk_module_enabled "$1" "$2"
}

start_enabled() {
  local result=0
  if module_enabled pm "${STK_PM_SCRIPT}" && ! "${STK_PM_SCRIPT}" status --quiet >/dev/null 2>&1; then
    "${STK_PM_SCRIPT}" start || result=1
  fi
  if module_enabled gds "${GDS_SCRIPT}" && ! "${GDS_SCRIPT}" status --quiet >/dev/null 2>&1; then
    "${GDS_SCRIPT}" start || result=1
  fi
  return "${result}"
}

stop_enabled() {
  local result=0
  if module_enabled gds "${GDS_SCRIPT}"; then
    "${GDS_SCRIPT}" stop || result=1
  fi
  if module_enabled pm "${STK_PM_SCRIPT}"; then
    "${STK_PM_SCRIPT}" stop || result=1
  fi
  return "${result}"
}

case "${1:-health}" in
  start|health) start_enabled ;;
  stop) stop_enabled ;;
  *) printf 'usage: service-start.sh start|stop|health\n' >&2; exit 2 ;;
esac
