#!/usr/bin/env bash

STK_CONDARC_FILE="${STK_CONDARC_FILE:-${CONDARC:-${STK_USER_HOME}/.condarc}}"

stk_conda_bin() {
  local candidate
  if [[ -n "${STK_CONDA_BIN:-}" ]]; then
    candidate="${STK_CONDA_BIN}"
  else
    candidate="$(command -v conda 2>/dev/null || true)"
  fi
  [[ -n "${candidate}" && -x "${candidate}" ]] || return 1
  printf '%s\n' "${candidate}"
}

stk_conda_envs_dir() {
  stk_config_value conda_envs_dir
}

stk_conda_effective_env_dirs() {
  local conda_bin="$1"
  CONDARC="${STK_CONDARC_FILE}" "${conda_bin}" config --show envs_dirs 2>/dev/null | awk '
    $0 == "envs_dirs:" { inside=1; next }
    inside && /^  - / {
      value=$0
      sub(/^  - /, "", value)
      if ((substr(value, 1, 1) == "\047" && substr(value, length(value), 1) == "\047") ||
          (substr(value, 1, 1) == "\042" && substr(value, length(value), 1) == "\042")) {
        value=substr(value, 2, length(value)-2)
      }
      print value
      next
    }
    inside { exit }
  '
}

stk_conda_target_available() {
  local target="$1"
  [[ -d "${target}" && ! -L "${target}" && -w "${target}" && -x "${target}" ]]
}

stk_conda_status_value() {
  local target conda_bin first
  target="$(stk_conda_envs_dir)"
  conda_bin="$(stk_conda_bin 2>/dev/null || true)"
  if [[ -z "${conda_bin}" ]]; then
    printf 'Conda 未安装\n'
    return
  fi
  first="$(stk_conda_effective_env_dirs "${conda_bin}" | head -n 1)"
  if [[ "${first}" == "${target}" ]] && stk_conda_target_available "${target}"; then
    printf '已链接\n'
  elif [[ "${first}" == "${target}" ]]; then
    printf '目录不可用\n'
  elif stk_conda_effective_env_dirs "${conda_bin}" | grep -Fqx -- "${target}"; then
    printf '顺序异常\n'
  else
    printf '未链接\n'
  fi
}

stk_conda_config_path_validate() {
  local parent
  stk_validate_absolute_path "${STK_CONDARC_FILE}" || return 1
  parent="$(dirname -- "${STK_CONDARC_FILE}")"
  if [[ -L "${parent}" || ( -e "${parent}" && ! -d "${parent}" ) ||
        -L "${STK_CONDARC_FILE}" || ( -e "${STK_CONDARC_FILE}" && ! -f "${STK_CONDARC_FILE}" ) ]]; then
    stk_fail "Conda 配置必须是普通非符号链接文件：${STK_CONDARC_FILE}"
    return 1
  fi
}

stk_conda_target_prepare() {
  local target="$1" existed=0
  stk_validate_absolute_path "${target}" || return 1
  if [[ -L "${target}" || ( -e "${target}" && ! -d "${target}" ) ]]; then
    stk_fail "Conda 环境目标必须是普通非符号链接目录：${target}"
    return 1
  fi
  [[ -d "${target}" ]] && existed=1
  mkdir -p -- "${target}" || return 1
  (( existed )) || chmod 0700 "${target}" || return 1
  [[ -w "${target}" && -x "${target}" ]] || {
    stk_fail "Conda 环境目标不可写：${target}"
    return 1
  }
}

stk_conda_configure_envs_dir() {
  local target="$1" conda_bin parent candidate mode old_dir seen=$'\n' first
  local -a current_dirs=()
  conda_bin="$(stk_conda_bin)" || { stk_fail '未检测到 conda；请先安装 Conda'; return 1; }
  stk_conda_config_path_validate || return 1
  stk_conda_target_prepare "${target}" || return 1
  mapfile -t current_dirs < <(stk_conda_effective_env_dirs "${conda_bin}")
  parent="$(dirname -- "${STK_CONDARC_FILE}")"
  mkdir -p -- "${parent}" || return 1
  candidate="$(mktemp "${parent}/.condarc.stk.XXXXXX")" || return 1
  trap 'rm -f -- "${candidate:-}"; trap - RETURN' RETURN
  if [[ -e "${STK_CONDARC_FILE}" ]]; then
    cp -p -- "${STK_CONDARC_FILE}" "${candidate}" || return 1
    mode="$(stat -c '%a' "${STK_CONDARC_FILE}" 2>/dev/null || true)"
  else
    mode=600
  fi
  chmod "${mode:-600}" "${candidate}" || return 1

  "${conda_bin}" config --file "${candidate}" --remove envs_dirs "${target}" >/dev/null 2>&1 || true
  "${conda_bin}" config --file "${candidate}" --prepend envs_dirs "${target}" >/dev/null || return 1
  seen+="${target}"$'\n'
  for old_dir in "${current_dirs[@]}"; do
    [[ -n "${old_dir}" && "${seen}" != *$'\n'"${old_dir}"$'\n'* ]] || continue
    "${conda_bin}" config --file "${candidate}" --append envs_dirs "${old_dir}" >/dev/null || return 1
    seen+="${old_dir}"$'\n'
  done
  "${conda_bin}" config --file "${candidate}" --validate >/dev/null || return 1
  chmod "${mode:-600}" "${candidate}" || return 1
  mv -f -- "${candidate}" "${STK_CONDARC_FILE}" || return 1
  candidate=''

  first="$(stk_conda_effective_env_dirs "${conda_bin}" | head -n 1)"
  if [[ "${first}" != "${target}" ]]; then
    if [[ -n "${CONDA_ENVS_PATH:-}" ]]; then
      stk_fail "CONDA_ENVS_PATH 正在覆盖 .condarc；请将 ${target} 放到该变量首位或取消该变量"
    else
      stk_fail "Conda 未将目标目录置于 envs_dirs 首位：${target}"
    fi
    return 1
  fi
}

stk_conda_link() {
  local target="$1" target_existed=0
  [[ -e "${target}" || -L "${target}" ]] && target_existed=1
  stk_transaction_begin || return 1
  stk_transaction_add "${STK_CONFIG_FILE}" || { stk_transaction_finish; return 1; }
  stk_transaction_add "${STK_CONDARC_FILE}" || { stk_transaction_finish; return 1; }
  if ! stk_config_set conda_envs_dir "${target}" >/dev/null ||
     ! stk_conda_configure_envs_dir "${target}"; then
    stk_transaction_rollback || stk_warn 'Conda 链接失败，且配置快照未能完整恢复'
    (( target_existed )) || rmdir -- "${target}" 2>/dev/null || true
    return 1
  fi
  stk_transaction_finish
  stk_ok "Conda envs_dirs 首选目录已设置为 ${target}"
  stk_info 'STK 未修改 shell 初始化；现有 conda activate/source activate 能力保持不变'
}

stk_conda_environment_valid() {
  local prefix="$1"
  [[ -d "${prefix}" && ! -L "${prefix}" &&
     -f "${prefix}/conda-meta/history" && ! -L "${prefix}/conda-meta/history" ]]
}

stk_conda_move_environment() {
  local conda_bin="$1" source="$2" destination="$3"
  if ! CONDA_REPORT_ERRORS=false "${conda_bin}" create \
      --prefix "${destination}" --clone "${source}" --yes; then
    if [[ -d "${destination}" && ! -L "${destination}" ]]; then
      rm -rf -- "${destination}"
    elif [[ -e "${destination}" || -L "${destination}" ]]; then
      stk_warn "克隆失败且目标路径状态异常，请手动检查：${destination}"
    fi
    stk_fail "Conda 环境克隆失败，源环境保持不变：${source}"
    return 1
  fi
  stk_conda_environment_valid "${destination}" || {
    if [[ -d "${destination}" && ! -L "${destination}" ]]; then
      rm -rf -- "${destination}"
    fi
    stk_fail "克隆后的 Conda 环境校验失败，已清理目标并保留源环境：${source}"
    return 1
  }
  if ! CONDA_REPORT_ERRORS=false "${conda_bin}" env remove --prefix "${source}" --yes; then
    stk_fail "目标环境已克隆成功，但无法删除源环境：${source}"
    stk_fail "目标保留在 ${destination}；确认其可用后可手动删除源环境"
    return 1
  fi
  if ! stk_conda_environment_valid "${destination}" || [[ -e "${source}" || -L "${source}" ]]; then
    stk_fail "Conda 环境迁移结果异常；请检查源和目标：${source} -> ${destination}"
    return 1
  fi
}

stk_conda_migrate() {
  local target="$1" dry_run="$2" assume_yes="$3"
  shift 3
  local conda_bin base active source env_dir env_name destination canonical_base canonical_env
  local duplicate_key selected_count=0 index
  local -a sources=("$@") env_sources=() env_destinations=()
  local -A destination_sources=()
  conda_bin="$(stk_conda_bin)" || { stk_fail '未检测到 conda；请先安装 Conda'; return 1; }
  base="$("${conda_bin}" info --base 2>/dev/null || true)"
  stk_validate_absolute_path "${base}" || { stk_fail '无法确定 Conda base 路径'; return 1; }
  stk_validate_absolute_path "${target}" || return 1
  if (( ${#sources[@]} == 0 )); then
    sources=("${base}/envs" "${STK_USER_HOME}/.conda/envs")
  fi
  active="${CONDA_PREFIX:-}"
  canonical_base="$(readlink -f -- "${base}" 2>/dev/null || printf '%s' "${base}")"

  for source in "${sources[@]}"; do
    stk_validate_absolute_path "${source}" || return 1
    [[ "${source}" != "${target}" && "${source}" != "${target}/"* && "${target}" != "${source}/"* ]] || {
      stk_fail "Conda 源目录与目标目录不能相同或相互包含：${source} / ${target}"
      return 1
    }
    [[ ! -e "${source}" ]] && continue
    [[ -d "${source}" && ! -L "${source}" ]] || {
      stk_fail "Conda 环境源必须是普通非符号链接目录：${source}"
      return 1
    }
    for env_dir in "${source}"/*; do
      [[ -e "${env_dir}" || -L "${env_dir}" ]] || continue
      [[ -L "${env_dir}" ]] && {
        stk_fail "拒绝迁移符号链接形式的 Conda 环境：${env_dir}"
        return 1
      }
      stk_conda_environment_valid "${env_dir}" || continue
      canonical_env="$(readlink -f -- "${env_dir}" 2>/dev/null || true)"
      [[ -n "${canonical_env}" ]] || return 1
      if [[ "${canonical_env}" == "${canonical_base}" ]]; then
        stk_warn "跳过 base 环境：${env_dir}"
        continue
      fi
      if [[ -n "${active}" && "${canonical_env}" == "$(readlink -f -- "${active}" 2>/dev/null || true)" ]]; then
        stk_fail "不能迁移当前已激活环境；请先 conda deactivate：${env_dir}"
        return 1
      fi
      env_name="$(basename -- "${env_dir}")"
      destination="${target}/${env_name}"
      stk_validate_absolute_path "${destination}" || return 1
      if [[ -n "${destination_sources[${destination}]:-}" ]]; then
        duplicate_key="${destination_sources[${destination}]}"
        stk_fail "多个源环境同名，无法迁移：${duplicate_key} / ${env_dir}"
        return 1
      fi
      if [[ -e "${destination}" || -L "${destination}" ]]; then
        stk_fail "目标环境已存在，拒绝覆盖：${destination}"
        return 1
      fi
      destination_sources["${destination}"]="${env_dir}"
      env_sources+=("${env_dir}")
      env_destinations+=("${destination}")
      selected_count=$((selected_count + 1))
    done
  done

  printf 'Conda 环境迁移计划：\n'
  if (( selected_count == 0 )); then
    printf '  没有发现待迁移的非 base 环境\n'
  else
    for index in "${!env_sources[@]}"; do
      printf '  %s -> %s\n' "${env_sources[index]}" "${env_destinations[index]}"
    done
  fi
  printf '  envs_dirs 首选目录：%s\n' "${target}"
  (( dry_run == 0 )) || return 0
  if (( selected_count > 0 && assume_yes == 0 )); then
    if [[ -t 0 ]]; then
      stk_confirm '迁移会在成功克隆后删除原环境，继续吗？' || { stk_warn '已取消'; return 0; }
    else
      stk_fail '非交互迁移必须添加 --yes'
      return 2
    fi
  fi

  stk_conda_link "${target}" || return 1
  for index in "${!env_sources[@]}"; do
    stk_info "正在迁移 $(basename -- "${env_sources[index]}")"
    if ! stk_conda_move_environment "${conda_bin}" "${env_sources[index]}" "${env_destinations[index]}"; then
      stk_fail '迁移已停止；已完成的环境和 envs_dirs 配置保持有效，未处理的源环境仍被识别'
      return 1
    fi
    stk_ok "已迁移：${env_destinations[index]}"
  done
  stk_ok "Conda 环境迁移完成：${selected_count} 个"
}

stk_conda_status() {
  local target conda_bin base first
  target="$(stk_conda_envs_dir)"
  conda_bin="$(stk_conda_bin 2>/dev/null || true)"
  printf 'Conda：%s\n' "${conda_bin:-未安装}"
  printf '目标目录：%s\n' "${target}"
  if [[ -z "${conda_bin}" ]]; then
    printf '链接状态：不可用\n'
    return 1
  fi
  base="$("${conda_bin}" info --base 2>/dev/null || true)"
  first="$(stk_conda_effective_env_dirs "${conda_bin}" | head -n 1)"
  printf 'Base：%s（不会迁移）\n' "${base:-未知}"
  printf 'envs_dirs 首位：%s\n' "${first:-未知}"
  printf '链接状态：%s\n' "$(stk_conda_status_value)"
  [[ "${first}" == "${target}" ]] && stk_conda_target_available "${target}"
}
