#!/usr/bin/env bash

set -uo pipefail

SCRIPT_PATH="${BASH_SOURCE[0]}"
RESOLVED_SELF="$(readlink -f -- "${SCRIPT_PATH}" 2>/dev/null || printf '%s' "${SCRIPT_PATH}")"
STK_ROOT="$(dirname -- "${RESOLVED_SELF}")"
# shellcheck source=lib/common.sh
source "${STK_ROOT}/lib/common.sh" || exit 1
# shellcheck source=lib/config.sh
source "${STK_ROOT}/lib/config.sh"
EFFECTIVE_CODEX_HOME="${CODEX_HOME:-$(stk_config_value codex_home)}"
ENV_FILE="${EFFECTIVE_CODEX_HOME}/.env"

export CODEX_HOME="${EFFECTIVE_CODEX_HOME}"

if [[ -r "${ENV_FILE}" ]]; then
  declare -A managed_proxy_values=()
  inside_managed_block=0
  managed_block_valid=1
  managed_begin_count=0
  managed_end_count=0
  while IFS= read -r line || [[ -n "${line}" ]]; do
    if [[ "${line}" == '# >>> server-toolkit codex-proxy >>>' ]]; then
      managed_begin_count=$((managed_begin_count + 1))
      if (( inside_managed_block || managed_begin_count > 1 || managed_end_count > 0 )); then
        managed_block_valid=0
      fi
      inside_managed_block=1
      continue
    fi
    if [[ "${line}" == '# <<< server-toolkit codex-proxy <<<' ]]; then
      managed_end_count=$((managed_end_count + 1))
      if (( ! inside_managed_block || managed_end_count > 1 )); then
        managed_block_valid=0
      fi
      inside_managed_block=0
      continue
    fi
    (( inside_managed_block )) || continue
    if [[ "${line}" =~ ^[[:space:]]*(export[[:space:]]+)?(HTTP_PROXY|HTTPS_PROXY|NO_PROXY|http_proxy|https_proxy|no_proxy)=([^[:space:]#]+)[[:space:]]*$ ]]; then
      key="${BASH_REMATCH[2]}"
      value="${BASH_REMATCH[3]}"
      if [[ "${value}" == \"*\" || "${value}" == \'*\' ]]; then
        value="${value:1:${#value}-2}"
      fi
      if [[ -v "managed_proxy_values[${key}]" ]]; then
        managed_block_valid=0
      else
        managed_proxy_values["${key}"]="${value}"
      fi
    elif [[ -n "${line}" ]]; then
      managed_block_valid=0
    fi
  done <"${ENV_FILE}"
  if (( inside_managed_block || managed_begin_count != 1 || managed_end_count != 1 ||
        ${#managed_proxy_values[@]} != 6 )); then
    managed_block_valid=0
  fi
  if (( managed_block_valid )); then
    unset HTTP_PROXY HTTPS_PROXY NO_PROXY ALL_PROXY
    unset http_proxy https_proxy no_proxy all_proxy
    for key in HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
      printf -v "${key}" '%s' "${managed_proxy_values[${key}]}"
      # shellcheck disable=SC2163 # key names an allowlisted proxy variable.
      export "${key}"
    done
  fi
fi

CANDIDATES=(
  "${EFFECTIVE_CODEX_HOME}/bin/codex"
  "${EFFECTIVE_CODEX_HOME}/packages/server-toolkit/bin/codex"
  "${EFFECTIVE_CODEX_HOME}/packages/standalone/current/bin/codex"
)

while IFS= read -r candidate; do
  CANDIDATES+=("${candidate}")
done < <(type -aP codex 2>/dev/null || true)

for candidate in "${CANDIDATES[@]}"; do
  [[ -x "${candidate}" ]] || continue
  resolved_candidate="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
  [[ -n "${resolved_candidate}" && "${resolved_candidate}" != "${RESOLVED_SELF}" ]] || continue
  # shellcheck disable=SC2093 # Successful handoff intentionally terminates the launcher.
  exec "${candidate}" "$@"
done

printf '错误：未找到真实的 Codex CLI；请先执行 stk codex install\n' >&2
exit 127
