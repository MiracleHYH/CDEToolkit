#!/usr/bin/env bash
set -euo pipefail
umask 077
version=${NIDO_ACCEPT_VERSION:?}
old_version=${NIDO_ACCEPT_OLD_VERSION:?}
accelerator=${NIDO_ACCEPT_GITHUB_ACCELERATOR:-https://gh-proxy.org}
case $accelerator in https://gh-proxy.org|https://ghfast.top) ;; *) printf 'unsupported accelerator\n' >&2; exit 2 ;; esac
export NIDO_ACCEPT_GITHUB_ACCELERATOR="$accelerator"
install_version_mode=${NIDO_ACCEPT_INSTALL_VERSION_MODE:-latest}
case $install_version_mode in latest|tagged) ;; *) printf 'unsupported install version mode\n' >&2; exit 2 ;; esac
selected_transport=${NIDO_ACCEPT_TRANSPORT:-both}
case $selected_transport in
  both) transports=(direct accelerated) ;;
  direct|accelerated) transports=("$selected_transport") ;;
  *) printf 'unsupported transport\n' >&2; exit 2 ;;
esac
input=${NIDO_ACCEPT_INPUT:?}
output=${NIDO_ACCEPT_OUTPUT:?}
platform=${NIDO_ACCEPT_PLATFORM:?}
asset="nido-${platform//\//-}.tar.gz"
temporary_base=${NIDO_ACCEPT_TEMP_BASE:-/tmp}
case_root=$(mktemp -d "$temporary_base/nido-public-release.XXXXXX")
case_root=$(cd "$case_root" && pwd -P)
cleanup() { local status=$?; rm -rf -- "$case_root"; [[ $status = 0 ]] || printf 'FAIL: public acceptance exited %s\n' "$status" >&2; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export HOME="$case_root/runner-home" TMPDIR="$case_root/tmp" PATH=/usr/bin:/bin
mkdir -p "$HOME" "$TMPDIR" "$case_root/guard" "$case_root/before"
export CURL_HOME="$HOME" XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share"
export ZDOTDIR="$HOME" BASH_ENV=/dev/null ENV=/dev/null
unset CDET_PREFIX CDET_STORE CDET_HOME STK_HOME NIDO_VERSION NIDO_GITHUB_PROXY CODEX_SQLITE_HOME CODEX_INSTALL_DIR CODEX_NON_INTERACTIVE
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy
if [[ -n ${NIDO_ACCEPT_GENERAL_PROXY:-} ]]; then
  export HTTPS_PROXY="$NIDO_ACCEPT_GENERAL_PROXY" HTTP_PROXY="$NIDO_ACCEPT_GENERAL_PROXY" ALL_PROXY="$NIDO_ACCEPT_GENERAL_PROXY"
  export https_proxy="$NIDO_ACCEPT_GENERAL_PROXY" http_proxy="$NIDO_ACCEPT_GENERAL_PROXY" all_proxy="$NIDO_ACCEPT_GENERAL_PROXY"
fi
export NIDO_ACCEPT_ASSET="$asset" NIDO_ACCEPT_HTTP_LOG="$output/http-downloads.tsv" NIDO_ACCEPT_FORBIDDEN_LOG="$output/forbidden.log"
: > "$NIDO_ACCEPT_HTTP_LOG"
sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
for kind in new old; do
  [[ $(tar -tzf "$input/$kind/$asset") = nido ]]
  expected=$(awk -v name="$asset" 'NR==1 && NF==2 && length($1)==64 && $1~/^[0-9a-f]+$/ && $2==name {value=$1;next} {bad=1} END {if(NR!=1 || bad || value=="") exit 1;print value}' "$input/$kind/$asset.sha256")
  [[ $(sha256 "$input/$kind/$asset") = "$expected" ]]
  tar -xOzf "$input/$kind/$asset" nido > "$case_root/$kind-nido"
  chmod 0755 "$case_root/$kind-nido"
done
# These bytes already passed independent cosign verification and local digest
# checks. In particular old-nido is the actual published old executable.
[[ $("$case_root/new-nido" version) = "nido $version" ]]
[[ $("$case_root/old-nido" version) = "nido $old_version" ]]
printf 'new\t%s\nold\t%s\n' "$(sha256 "$case_root/new-nido")" "$(sha256 "$case_root/old-nido")" > "$output/executables.sha256"
for tool in codex mihomo clash gds systemctl supervisorctl launchctl sudo conda pip pip3 python python3; do
  cat > "$case_root/guard/$tool" <<'GUARD'
#!/bin/sh
printf '%s\n' "$0 $*" >> "$NIDO_ACCEPT_FORBIDDEN_LOG"
exit 98
GUARD
  chmod 0755 "$case_root/guard/$tool"
done
# Every installer response is fetched over real HTTPS and compared before it
# reaches the shell or archive extraction. Never replace a failed fetch with
# local input bytes, and never execute a response with mismatching signed bytes.
cat > "$case_root/guard/curl" <<'CURL'
#!/bin/bash
set -euo pipefail
arguments=(); target=''; url=''
while (($#)); do
  case $1 in
    -o|--output) target=${2:?}; shift 2 ;;
    http://*|https://*) [[ -z $url ]]; url=$1; shift ;;
    *) arguments+=("$1"); shift ;;
  esac
done
source=$url
case $source in "$NIDO_ACCEPT_GITHUB_ACCELERATOR"/https://*) source=${source#"$NIDO_ACCEPT_GITHUB_ACCELERATOR"/} ;; esac
case $source in
  https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/*|https://github.com/MiracleHYH/CDEToolkit/releases/download/v"$NIDO_ACCEPT_VERSION"/*) ;;
  *) printf 'unexpected public download URL\n' >&2; exit 93 ;;
esac
name=${source##*/}
case $name in install.sh|"$NIDO_ACCEPT_ASSET"|"$NIDO_ACCEPT_ASSET.sha256") ;; *) exit 94 ;; esac
fetched=$(mktemp "$TMPDIR/public-response.XXXXXX")
trap 'rm -f -- "$fetched"' EXIT
printf 'REQUEST\t%s\n' "$url" >> "$NIDO_ACCEPT_HTTP_LOG"
/usr/bin/curl --connect-timeout 15 --max-time 180 --max-filesize 268435456 "${arguments[@]}" --output "$fetched" "$url"
cmp "$fetched" "$NIDO_ACCEPT_INPUT/new/$name" || { printf 'public bytes differ from the cosign-verified input\n' >&2; exit 95; }
if command -v sha256sum >/dev/null; then sum=$(sha256sum "$fetched"); else sum=$(shasum -a 256 "$fetched"); fi
printf 'VERIFIED-BEFORE-EXEC\t%s\t%s\n' "$url" "${sum%% *}" >> "$NIDO_ACCEPT_HTTP_LOG"
if [[ -n $target ]]; then cp "$fetched" "$target"; else cat "$fetched"; fi
CURL
chmod 0755 "$case_root/guard/curl"
base_path="$case_root/guard:/usr/bin:/bin"
export PATH="$base_path"
protected=(.bashrc .zshrc .profile .condarc custom-conda.yaml custom-pip.conf .codex/auth.json .codex/config.toml .codex/.env .codex/sessions/one.jsonl .codex/state.sqlite .config/mihomo/config.yaml .config/gds/config.toml .conda/envs/keep.txt .cache/conda/pkgs/keep.txt .cache/pip/keep.txt)
protect() {
  local name=$1 relative
  mkdir -p "$HOME/.codex/sessions" "$HOME/.config/mihomo" "$HOME/.config/gds" "$HOME/.conda/envs" "$HOME/.cache/conda/pkgs" "$HOME/.cache/pip"
  for relative in "${protected[@]}"; do printf 'PROTECTED-SENTINEL: %s\n' "$relative" > "$HOME/$relative"; done
  printf '# native Conda configuration\nchannels: [defaults]\nssl_verify: true\n' > "$HOME/.condarc"
  printf '# private native source\nchannels: [conda-forge]\nssl_verify: true\n' > "$HOME/custom-conda.yaml"
  printf '[global]\nindex-url = https://original.invalid/simple\ncert = /private/test-certificate.pem\n' > "$HOME/custom-pip.conf"
  export CONDARC="$HOME/custom-conda.yaml" PIP_CONFIG_FILE="$HOME/custom-pip.conf"
  cp -R "$HOME" "$case_root/before/$name"
}
unchanged() {
  local name=$1 relative
  for relative in "${protected[@]}"; do cmp "$HOME/$relative" "$case_root/before/$name/$relative"; done
}
fresh_components() {
  "$NIDO_HOME/bin/nido" status --json > "$output/$1-status.json"
  ! grep -Eq '"configured"[[:space:]]*:[[:space:]]*true' "$output/$1-status.json"
  local name
  for name in codex proxy gds conda; do [[ ! -e $NIDO_HOME/$name && ! -L $NIDO_HOME/$name ]]; done
}
assert_program() {
  [[ $(command -v nido) = "$NIDO_HOME/bin/nido" ]]
  [[ $(nido version) = "nido $version" ]]
  cmp "$NIDO_HOME/bin/nido" "$case_root/new-nido"
  local previous_path=$PATH
  . "$NIDO_HOME/env.sh"
  [[ $PATH = "$previous_path" ]]
}
assert_shell_activation() {
  local shell label=$1
  for shell in /bin/bash /bin/zsh; do
    [[ $platform = darwin/* || $shell = /bin/bash ]] || continue
    [[ -x $shell ]]
    env -i PATH="$base_path" HOME="$HOME" NIDO_HOME="$NIDO_HOME" CODEX_HOME="$CODEX_HOME" \
      TMPDIR="$TMPDIR" ZDOTDIR="$HOME" BASH_ENV=/dev/null ENV=/dev/null \
      "$shell" -f -c '
        set -eu
        . "$1/env.sh"
        [ "$(command -v nido)" = "$1/bin/nido" ]
        [ "$(nido version)" = "nido $2" ]
        previous_path=$PATH
        . "$1/env.sh"
        [ "$PATH" = "$previous_path" ]
        printf "PASS %s command registration and idempotent PATH\n" "$3"
      ' public-accept "$NIDO_HOME" "$version" "${shell##*/}" > "$output/$label-${shell##*/}-activation.log"
  done
}
assert_transport_requests() {
  local transport=$1 prefix expected_prefix name
  prefix=https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/
  if [[ $transport = accelerated ]]; then prefix=$accelerator/$prefix; fi
  for name in install.sh "$asset" "$asset.sha256"; do
    expected_prefix=$prefix
    if [[ $install_version_mode = tagged && $name != install.sh ]]; then
      expected_prefix="https://github.com/MiracleHYH/CDEToolkit/releases/download/v$version/"
      if [[ $transport = accelerated ]]; then expected_prefix=$accelerator/$expected_prefix; fi
    fi
    grep -Fq $'VERIFIED-BEFORE-EXEC\t'"$expected_prefix$name"$'\t' "$NIDO_ACCEPT_HTTP_LOG"
  done
}
for transport in "${transports[@]}"; do
  export HOME="$case_root/$transport-user" TMPDIR="$case_root/$transport-tmp"
  export NIDO_HOME="$HOME/.nido" CODEX_HOME="$HOME/.codex" CURL_HOME="$HOME" ZDOTDIR="$HOME"
  export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share" XDG_STATE_HOME="$HOME/.local/state"
  mkdir -p "$HOME" "$TMPDIR"
  export PATH="$base_path"
  protect "$transport"
  official=https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/install.sh
  policy=direct; outer=$official
  if [[ $transport = accelerated ]]; then policy=$accelerator; outer="$policy/$official"; fi
  install_options=(--github-proxy "$policy")
  [[ $install_version_mode != tagged ]] || install_options+=(--version "$version")
  # Exact README one-line flow: public latest script and package/checksum,
  # explicit transport, then activation. The separate tagged mode adds only
  # the installer's existing --version option; it cannot claim latest success.
  curl --http1.1 -fsSL "$outer" | sh -s -- "${install_options[@]}" && . "${NIDO_HOME:-$HOME/.nido}/env.sh"
  assert_program
  assert_shell_activation "$transport"
  fresh_components "$transport-install"
  nido download show --json > "$output/$transport-policy.json"
  grep -Fq "$policy" "$output/$transport-policy.json"
  cp "$NIDO_HOME/config.toml" "$case_root/$transport-config-before"
  cp "$NIDO_HOME/env.sh" "$case_root/$transport-env-before"
  curl --http1.1 -fsSL "$outer" | sh -s -- "${install_options[@]}"
  cmp "$NIDO_HOME/config.toml" "$case_root/$transport-config-before"
  cmp "$NIDO_HOME/env.sh" "$case_root/$transport-env-before"
  unchanged "$transport"
  [[ ! -e $NIDO_ACCEPT_FORBIDDEN_LOG ]]
  assert_transport_requests "$transport"
  printf 'PASS %s %s installation, shell activation, repeat install and protected native data\n' "$transport" "$install_version_mode"

  export NIDO_HOME="$HOME/upgrade-from-public-$old_version"
  "$case_root/old-nido" --root "$NIDO_HOME" init --yes --github-proxy "$policy" > "$output/$transport-old-init.log"
  . "$NIDO_HOME/env.sh"
  [[ $(nido version) = "nido $old_version" ]]
  cmp "$NIDO_HOME/bin/nido" "$case_root/old-nido"
  mkdir -p "$NIDO_HOME/proxy" "$NIDO_HOME/gds"
  printf 'INTERNAL-PROXY-SENTINEL\n' > "$NIDO_HOME/proxy/config.yaml"
  printf 'INTERNAL-GDS-SENTINEL\n' > "$NIDO_HOME/gds/config.toml"
  printf 'USER-DATA-SENTINEL\n' > "$NIDO_HOME/user-data.txt"
  cp "$NIDO_HOME/config.toml" "$case_root/$transport-update-config-before"
  cp "$NIDO_HOME/env.sh" "$case_root/$transport-update-env-before"
  if nido update --version "$version" --github-proxy https://127.0.0.1:1 --yes > "$output/$transport-update-failure.log" 2>&1; then
    printf 'failed transport unexpectedly updated the binary\n' >&2; exit 1
  fi
  cmp "$NIDO_HOME/bin/nido" "$case_root/old-nido"
  cmp "$NIDO_HOME/config.toml" "$case_root/$transport-update-config-before"
  cmp "$NIDO_HOME/env.sh" "$case_root/$transport-update-env-before"
  nido update --check --github-proxy "$policy" --json > "$output/$transport-update-check.json"
  grep -Eq '"current"[[:space:]]*:[[:space:]]*"'"$old_version"'"' "$output/$transport-update-check.json"
  grep -Eq '"latest"[[:space:]]*:[[:space:]]*"'"$version"'"' "$output/$transport-update-check.json"
  grep -Eq '"available"[[:space:]]*:[[:space:]]*true' "$output/$transport-update-check.json"
  # Native Go HTTP checks exact public release metadata, API SHA-256/size and
  # the archive checksum. Installer downloads on this same route have already
  # been compared with signed inputs; verify the installed program again here.
  nido update --github-proxy "$policy" --yes --json > "$output/$transport-update.json"
  assert_program
  cmp "$NIDO_HOME/config.toml" "$case_root/$transport-update-config-before"
  cmp "$NIDO_HOME/env.sh" "$case_root/$transport-update-env-before"
  grep -Fxq 'INTERNAL-PROXY-SENTINEL' "$NIDO_HOME/proxy/config.yaml"
  grep -Fxq 'INTERNAL-GDS-SENTINEL' "$NIDO_HOME/gds/config.toml"
  grep -Fxq 'USER-DATA-SENTINEL' "$NIDO_HOME/user-data.txt"
  nido update --version "$version" --github-proxy https://127.0.0.1:1 --yes --json > "$output/$transport-update-noop.json"
  cmp "$NIDO_HOME/bin/nido" "$case_root/new-nido"
  if nido update --version "$old_version" --github-proxy https://127.0.0.1:1 --yes > "$output/$transport-update-downgrade.log" 2>&1; then
    printf 'downgrade unexpectedly succeeded\n' >&2; exit 1
  fi
  cmp "$NIDO_HOME/bin/nido" "$case_root/new-nido"
  cmp "$NIDO_HOME/config.toml" "$case_root/$transport-update-config-before"
  cmp "$NIDO_HOME/env.sh" "$case_root/$transport-update-env-before"
  assert_shell_activation "$transport-updated"
  [[ -z $(find "$NIDO_HOME/bin" -name '.nido-update-*' -print) ]]
  unchanged "$transport"
  # The public, signed upgrade result must expose the new Conda CLI. The
  # comprehensive suite supplies its own temporary HOME and fake read-only
  # upstreams; real Conda/Python/components remain forbidden in this harness.
  bash "$input/harness/conda-cli-isolation.sh" "$NIDO_HOME/bin/nido" > "$output/$transport-conda-cli.log" 2>&1
  unchanged "$transport"
  [[ ! -e $NIDO_ACCEPT_FORBIDDEN_LOG ]]
  assert_transport_requests "$transport"
  printf '{"version":"%s","old_version":"%s","platform":"%s","transport":"%s","accelerator":"%s","install_version_mode":"%s","install":true,"repeat_install":true,"shell_activation":true,"real_old_self_update":true,"offline_same_version_noop":true,"downgrade_rejected":true,"failed_update_preserved":true,"conda_fake_upstream_suite":true,"protected_data_unchanged":true}\n' \
    "$version" "$old_version" "$platform" "$transport" "$accelerator" "$install_version_mode" > "$output/$transport-result.json"
  printf 'PASS %s real signed public upgrade %s -> %s, failure preservation, offline noop and Conda isolation\n' "$transport" "$old_version" "$version"
done
[[ ! -e $NIDO_ACCEPT_FORBIDDEN_LOG ]]
case $selected_transport in
  both) transports_json='["direct","accelerated"]'; direct_pass=true; accelerated_pass=true ;;
  direct) transports_json='["direct"]'; direct_pass=true; accelerated_pass=null ;;
  accelerated) transports_json='["accelerated"]'; direct_pass=null; accelerated_pass=true ;;
esac
printf '{"version":"%s","old_version":"%s","old_binary":"actual-cosign-verified-public-release","platform":"%s","transports":%s,"accelerator":"%s","install_version_mode":"%s","installer_responses_match_signed_bytes_before_execution":true,"self_update_validation":"native-api-digest-and-sha256-plus-signed-output-match","direct_install":%s,"accelerated_install":%s,"direct_self_update":%s,"accelerated_self_update":%s,"offline_same_version_noop":true,"failed_update_preserved":true,"conda_fake_upstream_suite":true,"protected_data_unchanged":true,"real_components_invoked":false}\n' \
  "$version" "$old_version" "$platform" "$transports_json" "$accelerator" "$install_version_mode" "$direct_pass" "$accelerated_pass" "$direct_pass" "$accelerated_pass" > "$output/result.json"
printf 'SUCCESS Nido %s public acceptance on %s; real signed old release %s, temporary homes only\n' "$version" "$platform" "$old_version"
