#!/usr/bin/env bash

STK_DOCTOR_FAILURES=0
STK_DOCTOR_WARNINGS=0

stk_doctor_line() {
  local level="$1" label="$2" detail="${3:-}"
  case "${level}" in
    PASS) printf '✓ %-20s %s\n' "${label}" "${detail}" ;;
    WARN) printf '! %-20s %s\n' "${label}" "${detail}"; STK_DOCTOR_WARNINGS=$((STK_DOCTOR_WARNINGS + 1)) ;;
    FAIL) printf '✗ %-20s %s\n' "${label}" "${detail}"; STK_DOCTOR_FAILURES=$((STK_DOCTOR_FAILURES + 1)) ;;
  esac
}

stk_doctor_file_mode() {
  local label="$1" file="$2" expected="$3" actual
  if [[ ! -e "${file}" ]]; then
    stk_doctor_line WARN "${label}" "不存在：${file}"
    return
  fi
  actual="$(stat -c '%a' "${file}" 2>/dev/null || true)"
  if [[ "${actual}" == "${expected}" ]]; then
    stk_doctor_line PASS "${label}" "权限 ${actual}"
  else
    stk_doctor_line FAIL "${label}" "权限 ${actual:-未知}，应为 ${expected}"
  fi
}

stk_doctor_entry() {
  local name="$1" manager="$2" candidate resolved
  candidate="$(stk_install_bin_dir)/${name}"
  resolved="$(readlink -f -- "${candidate}" 2>/dev/null || true)"
  if [[ -L "${candidate}" && "${resolved}" == "${manager}" ]]; then
    stk_doctor_line PASS "命令 ${name}" "${candidate}"
  elif [[ -e "${candidate}" || -L "${candidate}" ]]; then
    stk_doctor_line FAIL "命令 ${name}" "入口指向异常：${candidate}"
  else
    stk_doctor_line WARN "命令 ${name}" '未安装'
  fi
}

stk_doctor_listener() {
  local listener="$1" endpoint port
  endpoint="$(stk_proxy_endpoint "${listener}" 2>/dev/null || true)"
  port="${endpoint##*:}"
  if [[ ! "${port}" =~ ^[0-9]+$ ]]; then
    stk_doctor_line FAIL "代理监听 ${listener}" '配置无效'
  elif command -v ss >/dev/null 2>&1 &&
    ss -H -ltn 2>/dev/null | awk -v suffix=":${port}" '$4 ~ suffix "$" { found=1 } END { exit !found }'; then
    stk_doctor_line PASS "代理监听 ${listener}" "${endpoint}"
  else
    stk_doctor_line WARN "代理监听 ${listener}" "未检测到 ${endpoint}"
  fi
}

stk_doctor() {
  local command state state_mode config_mode config_dir_mode codex_home proxy_state gds_state gpu_device
  STK_DOCTOR_FAILURES=0
  STK_DOCTOR_WARNINGS=0
  printf 'Server Toolkit Doctor\n\n'

  if [[ -e "${STK_CONFIG_FILE}" ]] && stk_config_validate "${STK_CONFIG_FILE}" >/dev/null 2>&1; then
    config_mode="$(stat -c '%a' "${STK_CONFIG_FILE}" 2>/dev/null || true)"
    config_dir_mode="$(stat -c '%a' "$(dirname -- "${STK_CONFIG_FILE}")" 2>/dev/null || true)"
    if [[ -f "${STK_CONFIG_FILE}" && ! -L "${STK_CONFIG_FILE}" &&
          ! -L "$(dirname -- "${STK_CONFIG_FILE}")" &&
          "${config_mode}" == '600' && "${config_dir_mode}" == '700' ]]; then
      stk_doctor_line PASS '统一配置' "schema ${STK_CONFIG_SCHEMA_VERSION}，文件 ${config_mode}，目录 ${config_dir_mode}"
    else
      stk_doctor_line FAIL '统一配置' "路径或权限异常：文件 ${config_mode:-未知}，目录 ${config_dir_mode:-未知}"
    fi
  elif [[ -e "${STK_CONFIG_FILE}" ]]; then
    stk_doctor_line FAIL '统一配置' "格式或 schema 无效：${STK_CONFIG_FILE}"
  else
    stk_doctor_line WARN '统一配置' '尚未创建；执行 stk config init'
  fi
  if stk_state_schema_valid "${STK_STATE_META_FILE}"; then
    state_mode="$(stat -c '%a' "${STK_STATE_META_FILE}" 2>/dev/null || true)"
    if [[ "${state_mode}" == '600' ]]; then
      stk_doctor_line PASS '状态 schema' "v${STK_STATE_META_SCHEMA_VERSION}，权限 ${state_mode}"
    else
      stk_doctor_line FAIL '状态 schema' "v${STK_STATE_META_SCHEMA_VERSION}，权限 ${state_mode:-未知}，应为 600"
    fi
  else
    stk_doctor_line WARN '状态 schema' '尚未初始化或版本不受支持'
  fi

  for command in bash awk sed stat readlink mktemp tail date; do
    if command -v "${command}" >/dev/null 2>&1; then
      stk_doctor_line PASS "依赖 ${command}" "$(command -v "${command}")"
    else
      stk_doctor_line FAIL "依赖 ${command}" '缺失'
    fi
  done

  if stk_python_runtime_resolve >/dev/null 2>&1; then
    stk_doctor_line PASS 'Python 私有环境' "${STK_PYTHON}"
  else
    stk_doctor_line FAIL 'Python 私有环境' '不可用；请执行 stk runtime ensure'
  fi

  stk_doctor_entry stk "${STK_ROOT}/toolkit.sh"
  stk_doctor_entry pm "${STK_PM_SCRIPT}"
  stk_doctor_entry gds "${STK_GDS_SCRIPT}"

  if stk_proxy_enabled; then
    proxy_state="$(${STK_PM_SCRIPT} status 2>/dev/null || true)"
    if [[ "${proxy_state}" == 'running' ]]; then
      stk_doctor_line PASS 'Proxy' '运行中'
      stk_doctor_listener "$(stk_config_value proxy_common_listener)"
      stk_doctor_listener "$(stk_config_value proxy_codex_listener)"
    else
      stk_doctor_line WARN 'Proxy' "${proxy_state:-状态未知}"
    fi
    stk_doctor_file_mode 'Proxy 配置' "$(stk_proxy_config_path)" 600
  else
    stk_doctor_line WARN 'Proxy' '模块未启用'
  fi

  if stk_gds_enabled; then
    gds_state="$(${STK_GDS_SCRIPT} status 2>/dev/null || true)"
    gpu_device="$(awk -F= '$1=="gpu_device" {print $2; exit}' "${STK_ROOT}/gds/config.conf" 2>/dev/null || true)"
    if [[ "${gds_state}" == 'running' ]]; then
      stk_doctor_line PASS 'GDS' "运行中，GPU ${gpu_device:-0}"
    else
      stk_doctor_line WARN 'GDS' "${gds_state:-状态未知}"
    fi
    if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then
      stk_doctor_line PASS 'NVIDIA 驱动' 'GPU 可访问'
    else
      stk_doctor_line FAIL 'NVIDIA 驱动' 'nvidia-smi 无法访问 GPU'
    fi
  else
    stk_doctor_line WARN 'GDS' '模块未启用'
  fi

  state="$(stk_git_proxy_status_value)"
  if [[ "${state}" == '已配置' ]]; then stk_doctor_line PASS 'Git 代理' "${state}"; else stk_doctor_line WARN 'Git 代理' "${state}"; fi
  state="$(stk_git_profile_status_value)"
  if [[ "${state}" == '已配置' ]]; then stk_doctor_line PASS 'Git 身份' "${state}"; else stk_doctor_line WARN 'Git 身份' "${state}"; fi
  state="$(stk_github_ssh_status_value)"
  case "${state}" in
    已配置) stk_doctor_line PASS 'GitHub SSH' "${state}" ;;
    未配置) stk_doctor_line WARN 'GitHub SSH' "${state}" ;;
    *) stk_doctor_line FAIL 'GitHub SSH' "${state}" ;;
  esac
  state="$(stk_github_known_hosts_status_value)"
  if [[ "${state}" == '已配置' ]]; then stk_doctor_line PASS 'GitHub 主机公钥' "${state}"; else stk_doctor_line WARN 'GitHub 主机公钥' "${state}"; fi
  state="$(stk_github_ssh_agent_status_value)"
  case "${state}" in
    密钥已加载) stk_doctor_line PASS 'SSH agent' "${state}" ;;
    *) stk_doctor_line WARN 'SSH agent' "${state}" ;;
  esac
  codex_home="$(stk_codex_home)"
  state="$(stk_codex_proxy_status_value "${codex_home}")"
  case "${state}" in
    已配置) stk_doctor_line PASS 'Codex 代理' "${state}" ;;
    未配置|外部配置) stk_doctor_line WARN 'Codex 代理' "${state}" ;;
    *) stk_doctor_line FAIL 'Codex 代理' "${state}" ;;
  esac
  state="$(stk_codex_version "${codex_home}")"
  if [[ "${state}" == '未安装' ]]; then stk_doctor_line WARN 'Codex' "${state}"; else stk_doctor_line PASS 'Codex' "${state}"; fi

  state="$(stk_conda_status_value)"
  case "${state}" in
    已链接) stk_doctor_line PASS 'Conda 环境目录' "$(stk_conda_envs_dir)" ;;
    'Conda 未安装') stk_doctor_line WARN 'Conda 环境目录' 'Conda 未安装；该功能可选' ;;
    *) stk_doctor_line WARN 'Conda 环境目录' "${state}；执行 stk conda link" ;;
  esac

  state="$(stk_service_health_value)"
  case "${state}" in
    运行中) stk_doctor_line PASS '服务托管' "$(stk_service_backend) 运行中" ;;
    恢复中) stk_doctor_line WARN '服务托管' "$(stk_service_backend) 正在恢复" ;;
    未安装) stk_doctor_line WARN '服务托管' "$(stk_service_backend) 未安装；可执行 stk service install" ;;
    不可用) stk_doctor_line WARN '服务托管' '未检测到 supervisord 或 systemd' ;;
    *) stk_doctor_line FAIL '服务托管' "$(stk_service_backend) ${state}" ;;
  esac

  printf '\n结果：%d 个错误，%d 个警告\n' "${STK_DOCTOR_FAILURES}" "${STK_DOCTOR_WARNINGS}"
  (( STK_DOCTOR_FAILURES == 0 ))
}
