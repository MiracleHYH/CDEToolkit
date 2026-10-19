#!/usr/bin/env bash
# Real public traffic; no curl wrappers, local HTTP impersonation, or real core.
set -Eeuo pipefail
trap 'printf "FAIL public installer at line %s\n" "$LINENO" >&2' ERR
umask 077
[[ $(uname -s) = Linux && $(uname -m) = x86_64 && $(id -u) = 10001 ]]
version=${NIDO_PUBLIC_INSTALL_VERSION:?}
accelerator=${NIDO_PUBLIC_INSTALL_ACCELERATOR:?}
case_root=$(mktemp -d /case/nido-public-install.XXXXXX)
cleanup() {
  local status=$?
  trap - EXIT
  find /evidence -type f -exec chmod 0644 {} + || status=1
  rm -rf -- "$case_root"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export HOME="$case_root/runner-home" TMPDIR="$case_root/tmp" PATH=/usr/bin:/bin
mkdir "$HOME" "$TMPDIR" "$case_root/guard"
unset NIDO_HOME CODEX_HOME CDET_PREFIX CDET_STORE CDET_HOME STK_HOME NIDO_VERSION NIDO_NETWORK_MODE NIDO_GITHUB_PROXY NIDO_PROXY
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy
if [[ -n ${NIDO_PUBLIC_INSTALL_GENERAL_PROXY:-} ]]; then
  export HTTP_PROXY="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY" HTTPS_PROXY="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY" ALL_PROXY="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY"
  export http_proxy="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY" https_proxy="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY" all_proxy="$NIDO_PUBLIC_INSTALL_GENERAL_PROXY"
fi
export SHELL=/bin/bash BASH_ENV=/dev/null ENV=/dev/null TERM=xterm-256color
export NIDO_PUBLIC_INSTALL_VERSION="$version" NIDO_PUBLIC_INSTALL_PREFIX="$accelerator"
export NIDO_PUBLIC_INSTALL_SIGNED_BINARY="$case_root/signed-nido"
[[ $(tar -tzf /inputs/nido-linux-amd64.tar.gz) = nido ]]
expected=$(awk 'NR==1 && NF==2 && length($1)==64 && $1~/^[0-9a-f]+$/ && $2=="nido-linux-amd64.tar.gz" {value=$1;next} {bad=1} END {if(NR!=1 || bad || value=="") exit 1;print value}' /inputs/nido-linux-amd64.tar.gz.sha256)
[[ $(sha256sum /inputs/nido-linux-amd64.tar.gz | awk '{print $1}') = "$expected" ]]
tar -xOzf /inputs/nido-linux-amd64.tar.gz nido > "$NIDO_PUBLIC_INSTALL_SIGNED_BINARY"
chmod 0500 "$NIDO_PUBLIC_INSTALL_SIGNED_BINARY"
[[ $("$NIDO_PUBLIC_INSTALL_SIGNED_BINARY" version) = "nido $version" ]]
for tool in codex mihomo clash gds systemctl supervisorctl launchctl sudo conda pip pip3 python python3 npm; do
  cat > "$case_root/guard/$tool" <<'GUARD'
#!/bin/sh
printf '%s\n' "$0" >> /evidence/forbidden-components.log
exit 98
GUARD
  chmod 0500 "$case_root/guard/$tool"
done
export PATH="$case_root/guard:/usr/bin:/bin"
protected=(.codex/auth.json .codex/config.toml .codex/.env .codex/state.sqlite .codex/sessions/one.jsonl .config/mihomo/config.yaml .config/gds/config.toml .condarc .profile .zshrc)
snapshot() {
  find "$HOME" -mindepth 1 -printf '%P|%y|%m\n' | LC_ALL=C sort
  find "$HOME" -type f -exec sha256sum {} + | LC_ALL=C sort
}
checks=0
for transport in direct accelerated; do
  official=https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh
  address=$official
  [[ $transport != accelerated ]] || address="$accelerator/$official"
  # This first same-URL response is genuinely fetched and matched to signed
  # input bytes. The following literal curl|sh fetch is separate and unwrapped;
  # it does not gain a claim of response signature checking before execution.
  /usr/bin/curl -fsSL --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 240 \
    --max-filesize 188743680 "$address" --output "$case_root/$transport-preflight.sh"
  cmp "$case_root/$transport-preflight.sh" /inputs/install.sh
  printf '%s  %s\n' "$(sha256sum "$case_root/$transport-preflight.sh" | awk '{print $1}')" "$address" >> /evidence/same-url-preflight.sha256
  export NIDO_PUBLIC_INSTALL_URL="$address"
  for scenario in none prefix proxy cancel; do
    label="$transport-$scenario"
    export HOME="$case_root/$label-home" TMPDIR="$case_root/$label-tmp"
    export CURL_HOME="$HOME" ZDOTDIR="$HOME" XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share" XDG_STATE_HOME="$HOME/.local/state"
    mkdir -p "$HOME/.codex/sessions" "$HOME/.config/mihomo" "$HOME/.config/gds" "$TMPDIR"
    printf '# USER BASHRC\n' > "$HOME/.bashrc"
    for relative in "${protected[@]}"; do printf 'PROTECTED: %s\n' "$relative" > "$HOME/$relative"; done
    cp -R "$HOME" "$case_root/$label-before"
    snapshot > "/evidence/$label-before.sha256"
    export NIDO_PUBLIC_INSTALL_ROOT="$HOME/云端 Work Area/nido manager"
    export NIDO_PUBLIC_INSTALL_ROUTE="$scenario" NIDO_PUBLIC_INSTALL_CANCEL=false
    if [[ $scenario = cancel ]]; then export NIDO_PUBLIC_INSTALL_ROUTE=none NIDO_PUBLIC_INSTALL_CANCEL=true; fi
    export NIDO_PUBLIC_INSTALL_TRANSCRIPT="/evidence/$label-terminal.log"
    /inputs/pty-fixture > "/evidence/$label-result.json"
    for relative in "${protected[@]}"; do
      cmp "$HOME/$relative" "$case_root/$label-before/$relative"
      [[ $(stat -c '%a:%u:%g' "$HOME/$relative") = "$(stat -c '%a:%u:%g' "$case_root/$label-before/$relative")" ]]
    done
    if [[ $scenario = cancel ]]; then
      snapshot > "/evidence/$label-after.sha256"
      cmp "/evidence/$label-before.sha256" "/evidence/$label-after.sha256"
      [[ ! -e $NIDO_PUBLIC_INSTALL_ROOT && ! -e $HOME/.nido ]]
    else
      [[ ! -e $HOME/.nido ]]
      grep -Fq '# USER BASHRC' "$HOME/.bashrc"
      [[ $(grep -Fc '# >>> nido shell integration >>>' "$HOME/.bashrc") = 1 ]]
    fi
    [[ -z $(find "$TMPDIR" -mindepth 1 -print -quit) ]]
    [[ ! -e /evidence/forbidden-components.log ]]
    checks=$((checks+1))
    printf 'PASS %s genuine public no-argument installer, real PTY, custom directory, protected data\n' "$label"
  done
done
printf '{"version":"%s","passed":true,"real_public_pipeline_cases":%s,"entry_urls":["direct","accelerated"],"routes":["none","prefix","proxy"],"real_controlling_pty":true,"curl_intercepted":false,"installer_arguments":false,"custom_unicode_space_directory":true,"new_interactive_shell_registration":true,"default_cancellation_writes_nothing":true,"protected_data_unchanged":true,"same_url_preflight_matches_signed_script":true,"second_pipeline_response_verified_before_execution":false,"installed_binary_matches_signed_release":true,"real_components_invoked":false}\n' "$version" "$checks" > /evidence/result.json
find /evidence -type f -exec chmod 0644 {} +
printf 'SUCCESS public Nido %s interactive installation acceptance\n' "$version"
