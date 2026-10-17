#!/usr/bin/env bash

set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

candidate_file="$(mktemp)"
publish_tree="$(mktemp -d)"
trap 'rm -f -- "${candidate_file}"; rm -rf -- "${publish_tree}"' EXIT
git ls-files -z --cached --others --exclude-standard >"${candidate_file}"

failures=0
if ! grep -Fqx '    url: __SUBSCRIPTION_URL__' proxy/config.example.yaml ||
   ! grep -Fqx '    url: __SUBSCRIPTION_URL__' proxy/config.isp.example.yaml ||
   ! grep -Fqx '    server: __ISP_SERVER__' proxy/config.isp.example.yaml ||
   ! grep -Fqx '    password: __ISP_PASSWORD__' proxy/config.isp.example.yaml; then
  printf '✗ Proxy 公开模板缺少安全占位符\n' >&2
  failures=$((failures + 1))
fi
file_count=0
while IFS= read -r -d '' file; do
  file_count=$((file_count + 1))
  [[ -f "${file}" ]] || continue
  case "${file}" in
    proxy/config.yaml|*/.env|*.pem|*.key|id_rsa|id_ed25519)
      printf '✗ 不应发布的敏感文件：%s\n' "${file}" >&2
      failures=$((failures + 1))
      ;;
  esac
  if grep -Iq . "${file}" && grep -Eq '^[[:space:]]*-----BEGIN (OPENSSH|RSA|EC|DSA) PRIVATE KEY-----[[:space:]]*$' "${file}"; then
    printf '✗ 检测到私钥内容：%s\n' "${file}" >&2
    failures=$((failures + 1))
  fi
  if grep -Iq . "${file}" && grep -Eq '(github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|xox[baprs]-[A-Za-z0-9-]{20,})' "${file}"; then
    printf '✗ 检测到疑似访问令牌：%s\n' "${file}" >&2
    failures=$((failures + 1))
  fi
  mkdir -p -- "${publish_tree}/$(dirname -- "${file}")"
  cp -p -- "${file}" "${publish_tree}/${file}"
done <"${candidate_file}"

if [[ -n "${STK_PUBLIC_FORBIDDEN_PATTERN:-}" ]]; then
  if xargs -0 -r grep -EIn -- "${STK_PUBLIC_FORBIDDEN_PATTERN}" <"${candidate_file}"; then
    printf '✗ 检测到 STK_PUBLIC_FORBIDDEN_PATTERN 指定的私人内容\n' >&2
    failures=$((failures + 1))
  fi
fi

if command -v gitleaks >/dev/null 2>&1; then
  gitleaks dir --no-banner --redact "${publish_tree}" || failures=$((failures + 1))
  if git rev-parse --verify HEAD >/dev/null 2>&1; then
    gitleaks git --no-banner --redact --log-opts='--all' "${ROOT}" || failures=$((failures + 1))
  fi
fi

if (( failures > 0 )); then
  printf 'public audit: %d issue(s)\n' "${failures}" >&2
  exit 1
fi
printf 'public audit: ok (%s publishable files)\n' "${file_count}"
