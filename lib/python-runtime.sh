#!/usr/bin/env bash

STK_PYTHON_RUNTIME_DIR="${STK_PYTHON_RUNTIME_DIR:-${STK_ROOT}/runtime/python}"
STK_PYTHON_REQUIREMENTS="${STK_ROOT}/requirements/runtime.lock"
STK_PYTHON_RUNTIME_STATE="${STK_PYTHON_RUNTIME_DIR}/.stk-runtime"
STK_PYTHON_RUNTIME_SCHEMA_VERSION=2

stk_python_find_bootstrap() {
  local candidate resolved conda_executable conda_base
  local -a candidates=()

  if [[ -n "${STK_BOOTSTRAP_PYTHON:-}" ]]; then
    candidates+=("${STK_BOOTSTRAP_PYTHON}")
  else
    candidate="$(type -P python3 2>/dev/null || true)"
    [[ -z "${candidate}" ]] || candidates+=("${candidate}")
    candidate="$(type -P python 2>/dev/null || true)"
    [[ -z "${candidate}" ]] || candidates+=("${candidate}")
    conda_executable="$(type -P conda 2>/dev/null || true)"
    if [[ -n "${conda_executable}" ]]; then
      conda_base="$("${conda_executable}" info --base 2>/dev/null || true)"
      [[ -x "${conda_base}/bin/python" ]] && candidates+=("${conda_base}/bin/python")
      [[ -x "${conda_base}/bin/python3" ]] && candidates+=("${conda_base}/bin/python3")
    fi
  fi

  for candidate in "${candidates[@]}"; do
    if [[ "${candidate}" != */* ]]; then
      candidate="$(type -P -- "${candidate}" 2>/dev/null || true)"
    fi
    resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
    stk_validate_absolute_path "${resolved}" >/dev/null 2>&1 || continue
    [[ -x "${resolved}" ]] || continue
    case "${resolved}" in "${STK_PYTHON_RUNTIME_DIR}"/*) continue ;; esac
    if "${resolved}" -c 'import sys; raise SystemExit(sys.version_info < (3, 10))' >/dev/null 2>&1; then
      STK_BOOTSTRAP_PYTHON_RESOLVED="${resolved}"
      return 0
    fi
  done

  stk_fail '未检测到可用的 Python >= 3.10；请先安装带 venv 支持的 Python 后再运行 STK'
  return 1
}

stk_python_bootstrap_fingerprint() {
  local executable_digest identity
  [[ -n "${STK_BOOTSTRAP_PYTHON_RESOLVED:-}" && -x "${STK_BOOTSTRAP_PYTHON_RESOLVED}" ]] || return 1
  executable_digest="$(sha256sum "${STK_BOOTSTRAP_PYTHON_RESOLVED}" | awk '{print $1}')" || return 1
  identity="$("${STK_BOOTSTRAP_PYTHON_RESOLVED}" -c \
    'import sys, sysconfig; print("%s|%s|%s" % (sys.version, sys.implementation.cache_tag, sysconfig.get_config_var("SOABI") or ""))')" || return 1
  printf '%s\n%s\n%s\n' "${STK_BOOTSTRAP_PYTHON_RESOLVED}" "${executable_digest}" "${identity}" |
    sha256sum | awk '{print $1}'
}

stk_python_requirements_digest() {
  [[ -f "${STK_PYTHON_REQUIREMENTS}" && ! -L "${STK_PYTHON_REQUIREMENTS}" ]] || {
    stk_fail "Python 依赖锁定文件缺失：${STK_PYTHON_REQUIREMENTS}"
    return 1
  }
  sha256sum "${STK_PYTHON_REQUIREMENTS}" | awk '{print $1}'
}

stk_python_runtime_resolve() {
  local expected_digest recorded_digest python
  expected_digest="$(stk_python_requirements_digest)" || return 1
  if [[ ! -d "${STK_PYTHON_RUNTIME_DIR}" || -L "${STK_PYTHON_RUNTIME_DIR}" ||
        ! -f "${STK_PYTHON_RUNTIME_STATE}" || -L "${STK_PYTHON_RUNTIME_STATE}" ]]; then
    return 1
  fi
  recorded_digest="$(stk_state_value "${STK_PYTHON_RUNTIME_STATE}" requirements_sha256)"
  [[ "$(stk_state_value "${STK_PYTHON_RUNTIME_STATE}" schema_version)" == "${STK_PYTHON_RUNTIME_SCHEMA_VERSION}" &&
     "${recorded_digest}" == "${expected_digest}" &&
     "$(stk_state_value "${STK_PYTHON_RUNTIME_STATE}" runtime_dir)" == "${STK_PYTHON_RUNTIME_DIR}" ]] || return 1
  python="${STK_PYTHON_RUNTIME_DIR}/bin/python"
  [[ -x "${python}" ]] || python="${STK_PYTHON_RUNTIME_DIR}/bin/python3"
  if [[ ! -x "${python}" ]] ||
     ! "${python}" -c 'import sys; raise SystemExit(sys.version_info < (3, 10))' >/dev/null 2>&1 ||
     ! "${python}" -m pip --version >/dev/null 2>&1 ||
     ! "${python}" -m pip check >/dev/null 2>&1; then
    return 1
  fi
  STK_PYTHON="${python}"
}

stk_python_runtime_restore_previous() {
  local previous="$1"
  [[ -e "${STK_PYTHON_RUNTIME_DIR}" || -L "${STK_PYTHON_RUNTIME_DIR}" ]] &&
    rm -rf -- "${STK_PYTHON_RUNTIME_DIR}"
  [[ ! -d "${previous}" ]] || mv -- "${previous}" "${STK_PYTHON_RUNTIME_DIR}"
}

stk_python_runtime_ensure() {
  local parent previous requirements_digest python_version bootstrap_fingerprint result=0
  stk_python_find_bootstrap || return 1
  bootstrap_fingerprint="$(stk_python_bootstrap_fingerprint)" || {
    stk_fail '无法计算 Python 引导解释器指纹'
    return 1
  }
  if stk_python_runtime_resolve >/dev/null 2>&1 &&
     [[ "$(stk_state_value "${STK_PYTHON_RUNTIME_STATE}" bootstrap_fingerprint)" == "${bootstrap_fingerprint}" ]]; then
    return 0
  fi
  requirements_digest="$(stk_python_requirements_digest)" || return 1

  parent="$(dirname -- "${STK_PYTHON_RUNTIME_DIR}")"
  previous="${parent}/.python.previous"
  stk_validate_absolute_path "${STK_PYTHON_RUNTIME_DIR}" >/dev/null 2>&1 || {
    stk_fail "Python 私有环境路径无效：${STK_PYTHON_RUNTIME_DIR}"
    return 1
  }
  if [[ -L "${parent}" || ( -e "${parent}" && ! -d "${parent}" ) ||
        -L "${STK_PYTHON_RUNTIME_DIR}" || ( -e "${STK_PYTHON_RUNTIME_DIR}" && ! -d "${STK_PYTHON_RUNTIME_DIR}" ) ]]; then
    stk_fail "Python 私有环境路径必须是普通目录且不能是符号链接：${STK_PYTHON_RUNTIME_DIR}"
    return 1
  fi
  mkdir -p -- "${parent}" || return 1
  chmod 0700 "${parent}" || return 1

  if [[ -d "${previous}" && ! -L "${previous}" ]]; then
    if [[ -e "${STK_PYTHON_RUNTIME_DIR}" || -L "${STK_PYTHON_RUNTIME_DIR}" ]]; then
      rm -rf -- "${STK_PYTHON_RUNTIME_DIR}"
    fi
    mv -- "${previous}" "${STK_PYTHON_RUNTIME_DIR}" || return 1
    if stk_python_runtime_resolve >/dev/null 2>&1 &&
       [[ "$(stk_state_value "${STK_PYTHON_RUNTIME_STATE}" bootstrap_fingerprint)" == "${bootstrap_fingerprint}" ]]; then
      return 0
    fi
  elif [[ -e "${previous}" || -L "${previous}" ]]; then
    stk_fail "Python 环境恢复路径异常：${previous}"
    return 1
  fi

  if [[ -d "${STK_PYTHON_RUNTIME_DIR}" ]]; then
    mv -- "${STK_PYTHON_RUNTIME_DIR}" "${previous}" || return 1
  fi
  trap 'stk_python_runtime_restore_previous "${previous}"; trap - RETURN' RETURN

  if ! "${STK_BOOTSTRAP_PYTHON_RESOLVED}" -m venv --copies "${STK_PYTHON_RUNTIME_DIR}"; then
    stk_fail '无法创建 Python 私有环境；请确认当前 Python 提供 venv/ensurepip 支持'
    result=1
  elif ! "${STK_PYTHON_RUNTIME_DIR}/bin/python" -m pip --version >/dev/null 2>&1; then
    stk_fail 'Python 私有环境中缺少 pip；请安装完整的 Python venv 支持'
    result=1
  elif grep -Eq '^[[:space:]]*[^#[:space:]]' "${STK_PYTHON_REQUIREMENTS}" &&
       ! "${STK_PYTHON_RUNTIME_DIR}/bin/python" -m pip install \
         --disable-pip-version-check --no-input --require-hashes \
         -r "${STK_PYTHON_REQUIREMENTS}"; then
    stk_fail 'Python 私有环境依赖安装失败'
    result=1
  fi
  if (( result == 0 )); then
    python_version="$("${STK_PYTHON_RUNTIME_DIR}/bin/python" --version 2>&1)"
    printf 'schema_version=%s\nrequirements_sha256=%s\nbootstrap_python=%s\nbootstrap_fingerprint=%s\npython_version=%s\nruntime_dir=%s\n' \
      "${STK_PYTHON_RUNTIME_SCHEMA_VERSION}" "${requirements_digest}" "${STK_BOOTSTRAP_PYTHON_RESOLVED}" \
      "${bootstrap_fingerprint}" "${python_version}" "${STK_PYTHON_RUNTIME_DIR}" |
      stk_atomic_write "${STK_PYTHON_RUNTIME_STATE}" 0600 || result=1
  fi
  if (( result != 0 )); then
    stk_python_runtime_restore_previous "${previous}"
    trap - RETURN
    return "${result}"
  fi
  rm -rf -- "${previous}"
  trap - RETURN
  # shellcheck disable=SC2034 # Consumed by the sourcing caller.
  STK_PYTHON="${STK_PYTHON_RUNTIME_DIR}/bin/python"
  stk_ok "Python 私有环境已就绪：${STK_PYTHON_RUNTIME_DIR}"
}
