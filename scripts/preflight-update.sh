#!/usr/bin/env bash

set -euo pipefail

TARGET="${1:-}"
SCRIPT_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON_CACHE_DIR="$(mktemp -d /tmp/server-toolkit-update-pycache.XXXXXX)"
trap 'rm -rf -- "${PYTHON_CACHE_DIR}"' EXIT
# shellcheck disable=SC2034 # Read by lib/common.sh while it is sourced.
STK_ROOT="${SCRIPT_ROOT}"
# shellcheck source=../lib/common.sh
source "${SCRIPT_ROOT}/lib/common.sh"
# shellcheck source=../lib/python-runtime.sh
source "${SCRIPT_ROOT}/lib/python-runtime.sh"
[[ -n "${TARGET}" && "${TARGET}" == /* && -d "${TARGET}" && ! -L "${TARGET}" ]] || {
  printf '错误：更新预检目标必须是绝对路径的普通目录\n' >&2
  exit 2
}

for required in toolkit.sh install.sh lib/common.sh lib/python-runtime.sh lib/config.sh requirements/runtime.lock scripts/check.sh scripts/public-audit.sh; do
  [[ -f "${TARGET}/${required}" && ! -L "${TARGET}/${required}" ]] || {
    printf '错误：更新缺少可信入口 %s\n' "${required}" >&2
    exit 1
  }
done
for executable in toolkit.sh install.sh scripts/check.sh scripts/public-audit.sh; do
  [[ -x "${TARGET}/${executable}" ]] || { printf '错误：更新入口不可执行：%s\n' "${executable}" >&2; exit 1; }
done
if ! grep -Fqx '    url: __SUBSCRIPTION_URL__' "${TARGET}/proxy/config.example.yaml" ||
   ! grep -Fqx '    url: __SUBSCRIPTION_URL__' "${TARGET}/proxy/config.isp.example.yaml" ||
   ! grep -Fqx '    password: __ISP_PASSWORD__' "${TARGET}/proxy/config.isp.example.yaml"; then
  printf '错误：更新中的 Proxy 公开模板缺少安全占位符\n' >&2
  exit 1
fi

if find "${TARGET}" -path "${TARGET}/.git" -prune -o \( -type l -o -type b -o -type c -o -type p -o -type s \) -print -quit | grep -q .; then
  printf '错误：更新包含符号链接或特殊文件，拒绝安装\n' >&2
  exit 1
fi

mapfile -d '' -t bash_files < <(find "${TARGET}" -path "${TARGET}/.git" -prune -o -type f -name '*.sh' -print0)
(( ${#bash_files[@]} > 0 )) || { printf '错误：更新中没有 Bash 文件\n' >&2; exit 1; }
bash -n "${bash_files[@]}"

if ! stk_python_runtime_resolve; then
  stk_python_find_bootstrap || {
    stk_fail '当前 STK Python 私有环境不可用，且没有可用于更新预检的 Python'
    exit 1
  }
  STK_PYTHON="${STK_BOOTSTRAP_PYTHON_RESOLVED}"
fi
mapfile -d '' -t python_files < <(find "${TARGET}" -path "${TARGET}/.git" -prune -o -type f -name '*.py' -print0)
(( ${#python_files[@]} == 0 )) || PYTHONPYCACHEPREFIX="${PYTHON_CACHE_DIR}" \
  "${STK_PYTHON}" -m py_compile "${python_files[@]}"

private_marker='BEGIN '"(RSA |EC |OPENSSH )?"'PRIVATE KEY'
credential_pattern="${private_marker}|github_pat_[A-Za-z0-9_]{20,}|gh[opusr]_[A-Za-z0-9]{20,}|https?://[^[:space:]]+:[^[:space:]@]+@"
if rg -n -i --hidden --glob '!.git/**' "${credential_pattern}" "${TARGET}"; then
  printf '错误：更新内容疑似包含私钥、令牌或带凭据 URL\n' >&2
  exit 1
fi

printf '更新静态预检通过。\n'
