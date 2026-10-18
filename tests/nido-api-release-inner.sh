#!/usr/bin/env bash
# Runs only inside the dedicated Linux amd64 release-acceptance container.
# Nido is either the explicitly labelled pre-release local input or a provided
# verified public release. API metadata and proxy are loopback fixtures. This
# is not a public API or real Mihomo acceptance.
set -euo pipefail
umask 077
[[ $(uname -s) = Linux && $(uname -m) = x86_64 && $(id -u) = 10001 ]]
input=${NIDO_API_RELEASE_INPUT:?}
output=${NIDO_API_RELEASE_OUTPUT:?}
version=${NIDO_API_RELEASE_VERSION:?}
input_scope=${NIDO_API_RELEASE_INPUT_SCOPE:?}
[[ $input_scope = verified-public || $input_scope = pre-release-local ]]
fixture="$input/api-fixture"
case_root=$(mktemp -d /case/nido-api-release.XXXXXX)
origin_pid=''
core_active=false
checks_complete=false
disabled_cases=0
positive_queries=0
export HOME="$case_root/home" TMPDIR="$case_root/tmp" PATH=/usr/bin:/bin
export NIDO_HOME="$HOME/.nido" CODEX_HOME="$HOME/.codex"
export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share"
export CURL_HOME="$HOME" ZDOTDIR="$HOME" BASH_ENV=/dev/null ENV=/dev/null
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy
unset NIDO_GITHUB_PROXY CONDARC PIP_CONFIG_FILE SSL_CERT_FILE SSL_CERT_DIR
mkdir -p "$HOME" "$TMPDIR" "$case_root/package" "$case_root/guard" "$case_root/before"
nido() { "$NIDO_HOME/bin/nido" --root "$NIDO_HOME" "$@"; }
cleanup() {
  local status=$? cleanup_status=0 passed=false
  trap - EXIT
  set +e
  if [[ $core_active = true ]]; then
    nido proxy stop --yes > "$output/cleanup-proxy.log" 2>&1
    [[ $? = 0 ]] || cleanup_status=1
  fi
  if [[ -n $origin_pid ]]; then
    if kill -0 "$origin_pid" 2>/dev/null; then kill "$origin_pid"; fi
    wait "$origin_pid" 2>/dev/null
  fi
  [[ $cleanup_status = 0 ]] || status=1
  if [[ $status = 0 && $checks_complete = true ]]; then passed=true; fi
  printf '{"scope":"nido-linux-amd64-release-api-contract","nido_input_scope":"%s","version":"%s","passed":%s,"checks_complete":%s,"disabled_online_calls":%s,"positive_api_queries":%s,"api_metadata":"private-loopback-TLS-fixture","core":"test-only-API-CONNECT-fixture","public_api_network_tested":false,"real_mihomo_tested":false,"source_nido_binary_used":%s,"cleanup_verified":%s}\n' \
    "$input_scope" "$version" "$passed" "$checks_complete" "$disabled_cases" "$positive_queries" "$([[ $input_scope = pre-release-local ]] && printf true || printf false)" "$([[ $cleanup_status = 0 ]] && printf true || printf false)" > "$output/result.json"
  printf '%s\n' "$status" > "$output/inner-exit.txt"
  rm -rf -- "$case_root"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
sha256() { sha256sum "$1" | awk '{print $1}'; }
[[ $(tar -tzf "$input/nido-linux-amd64.tar.gz") = nido ]]
expected=$(awk 'NR==1 && NF==2 && length($1)==64 && $1~/^[0-9a-f]+$/ && $2=="nido-linux-amd64.tar.gz" {value=$1;next} {bad=1} END {if(NR!=1 || bad || value=="") exit 1;print value}' "$input/nido-linux-amd64.tar.gz.sha256")
[[ $(sha256 "$input/nido-linux-amd64.tar.gz") = "$expected" ]]
tar -xOzf "$input/nido-linux-amd64.tar.gz" nido > "$case_root/package/nido"
chmod 0755 "$case_root/package/nido"
[[ $("$case_root/package/nido" version) = "nido $version" ]]
sha256sum "$case_root/package/nido" "$fixture" > "$output/executables.sha256"
for tool in curl wget codex mihomo clash gds git systemctl supervisorctl launchctl sudo npm conda pip pip3 python python3; do
  cat > "$case_root/guard/$tool" <<'GUARD'
#!/bin/sh
printf '%s\n' "$0 $*" >> "$NIDO_API_RELEASE_FORBIDDEN_LOG"
exit 98
GUARD
  chmod 0700 "$case_root/guard/$tool"
done
export PATH="$case_root/guard:/usr/bin:/bin" NIDO_API_RELEASE_FORBIDDEN_LOG="$output/forbidden.log"
export NIDO_API_RELEASE_CODEX_VERSION_LOG="$output/codex-version-fixture.log"
cat > "$case_root/guard/codex" <<'CODEX'
#!/bin/sh
if [ "$#" = 1 ] && [ "$1" = --version ]; then
  printf 'test-only Codex local version probe\n' >> "$NIDO_API_RELEASE_CODEX_VERSION_LOG"
  printf 'codex-cli 0.0.0\n'
  exit 0
fi
printf '%s\n' "$0 $*" >> "$NIDO_API_RELEASE_FORBIDDEN_LOG"
exit 98
CODEX
chmod 0700 "$case_root/guard/codex"
mkdir -p "$CODEX_HOME/sessions" "$HOME/.config/mihomo" "$HOME/.config/gds"
for relative in .bashrc .zshrc .profile .condarc .gitconfig .codex/auth.json .codex/config.toml .codex/.env .codex/sessions/one.jsonl .codex/state.sqlite .config/mihomo/config.yaml .config/gds/config.toml; do
  printf 'PROTECTED-SENTINEL %s\n' "$relative" > "$HOME/$relative"
done
printf 'HTTPS_PROXY=${USER_OWN_PROXY_EXPRESSION}\nKEEP=literal\n' > "$CODEX_HOME/.env"
cp -R "$HOME" "$case_root/before/external"
preserved() {
  local relative
  for relative in .bashrc .zshrc .profile .condarc .gitconfig .codex/auth.json .codex/config.toml .codex/.env .codex/sessions/one.jsonl .codex/state.sqlite .config/mihomo/config.yaml .config/gds/config.toml; do
    cmp "$HOME/$relative" "$case_root/before/external/$relative"
    [[ $(stat -c '%a:%u:%g' "$HOME/$relative") = $(stat -c '%a:%u:%g' "$case_root/before/external/$relative") ]]
  done
  cmp "$NIDO_HOME/bin/nido" "$case_root/package/nido"
  [[ ! -e "$output/forbidden.log" ]]
  [[ -z $(find "$NIDO_HOME/bin" -name '.nido-update-*' -print) ]]
}
snapshot_home() {
  local destination=$1 path
  {
    find "$HOME" -printf '%y:%m:%U:%G:%P:%l\n' | LC_ALL=C sort
    while IFS= read -r -d '' path; do sha256sum "$path"; done < <(find "$HOME" -type f -print0 | LC_ALL=C sort -z)
  } > "$destination"
}
"$case_root/package/nido" --root "$NIDO_HOME" init --github-proxy https://file-only.invalid --yes > "$output/init.log"
cmp "$NIDO_HOME/bin/nido" "$case_root/package/nido"
nido doctor --json > "$output/doctor-disabled.json"
nido codex setup --home "$CODEX_HOME" --yes > "$output/codex-setup.json"
"$fixture" origin "$case_root/origin" "$version" > "$output/origin.log" 2>&1 &
origin_pid=$!
for ((attempt=0; attempt<200; attempt++)); do
  [[ ! -s "$case_root/origin/origin.addr" ]] || break
  kill -0 "$origin_pid"
  sleep 0.05
done
[[ -s "$case_root/origin/origin.addr" && -s "$case_root/origin/ca.pem" ]]
{
  printf 'origin_pid=%s\n' "$origin_pid"
  printf 'origin_kernel_executable='
  readlink "/proc/$origin_pid/exe"
  printf 'shell_kernel_executable='
  readlink "/proc/$$/exe"
  stat -c 'origin_proc_owner=%u:%g' "/proc/$origin_pid"
} > "$output/kernel-process-observation.txt"
origin=$(cat "$case_root/origin/origin.addr")
[[ $origin =~ ^127\.0\.0\.1:[1-9][0-9]{0,4}$ ]]
mkdir "$case_root/empty-certs"
export SSL_CERT_FILE="$case_root/origin/ca.pem" SSL_CERT_DIR="$case_root/empty-certs"
api_count() { wc -l < "$case_root/origin/requests.jsonl"; }
connect_count() {
  if [[ -e "$NIDO_HOME/proxy/nido-test-connect.jsonl" ]]; then wc -l < "$NIDO_HOME/proxy/nido-test-connect.jsonl"; else printf '0\n'; fi
}
version_probe_count() {
  if [[ -e "$NIDO_API_RELEASE_CODEX_VERSION_LOG" ]]; then wc -l < "$NIDO_API_RELEASE_CODEX_VERSION_LOG"; else printf '0\n'; fi
}
disabled_call() {
  local label=$1 probes_before
  shift
  probes_before=$(version_probe_count)
  snapshot_home "$case_root/before/disabled-before"
  if nido "$@" > "$output/$label.log" 2>&1; then
    printf 'unconfigured API operation unexpectedly succeeded: %s\n' "$label" >&2
    exit 1
  fi
  grep -Fq 'Nido API 功能未启用' "$output/$label.log"
  snapshot_home "$case_root/before/disabled-after"
  cmp "$case_root/before/disabled-before" "$case_root/before/disabled-after"
  [[ $(api_count) = 0 && $(connect_count) = 0 ]]
  [[ $(version_probe_count) = "$probes_before" ]]
  preserved
  disabled_cases=$((disabled_cases+1))
}
next_version="${version%.*}.$(( ${version##*.} + 1 ))"
for state in missing empty; do
  if [[ $state = empty ]]; then
    printf 'schema = 1\nproduct = "nido-api-proxy"\nlistener = ""\n' > "$NIDO_HOME/download-proxy.toml"
    chmod 0600 "$NIDO_HOME/download-proxy.toml"
  fi
  nido download show --json > "$output/$state-download-show.json"
  grep -Fq '"api_state":"disabled"' "$output/$state-download-show.json"
  for route in saved-prefix direct explicit-proxy environment-proxy; do
    options=()
    unset HTTPS_PROXY HTTP_PROXY ALL_PROXY https_proxy http_proxy all_proxy
    case $route in
      direct) options=(--github-proxy direct) ;;
      explicit-proxy) options=(--proxy http://127.0.0.1:18790) ;;
      environment-proxy) export HTTPS_PROXY=http://127.0.0.1:18790 HTTP_PROXY=http://127.0.0.1:18790 ALL_PROXY=http://127.0.0.1:18790 ;;
    esac
    disabled_call "$state-$route-nido-check" update --check "${options[@]}"
    disabled_call "$state-$route-nido-version" update --version "$next_version" --yes "${options[@]}"
    disabled_call "$state-$route-proxy-install" proxy install --yes "${options[@]}"
    disabled_call "$state-$route-proxy-latest" proxy install --artifact latest --yes "${options[@]}"
    disabled_call "$state-$route-proxy-check" proxy update --check "${options[@]}"
    disabled_call "$state-$route-gds-install" gds install --yes "${options[@]}"
    disabled_call "$state-$route-gds-tag" gds install gds-v0.0.1 --yes "${options[@]}"
    disabled_call "$state-$route-gds-check" gds update --check "${options[@]}"
    # Codex deliberately rejects GitHub file-prefix flags. Its other three
    # route conditions still prove that only listener intent enables this API.
    if [[ $route != direct ]]; then
      disabled_call "$state-$route-codex-check" codex update --check "${options[@]}"
    fi
  done
done
unset HTTPS_PROXY HTTP_PROXY ALL_PROXY https_proxy http_proxy all_proxy
[[ $(api_count) = 0 ]]
printf 'PASS %s input: %s unbound online calls blocked without requests or writes\n' "$input_scope" "$disabled_cases"
# Local artifact installation and static previews work while disabled. The
# fixture is explicitly a test core; its bytes are not an upstream release.
nido proxy install --dry-run --json > "$output/offline-preview.json"
nido proxy install --artifact "$fixture" --sha256 "$(sha256 "$fixture")" --yes > "$output/local-install.json"
nido update --version "$version" --yes --json > "$output/same-version-noop.json"
[[ $(api_count) = 0 && $(connect_count) = 0 ]]
render_config() {
  printf '{"listeners":[{"name":"release-api","type":"http","listen":"127.0.0.1","port":%s}],"rules":["MATCH,DIRECT"],"nido-test-api-origin":"%s"}\n' "$1" "$origin" > "$case_root/api-config.yaml"
  chmod 0600 "$case_root/api-config.yaml"
}
render_config 18790
nido proxy configure --file "$case_root/api-config.yaml" --yes > "$output/local-configure.json"
nido proxy validate --json > "$output/local-validate.json"
nido proxy start --yes > "$output/proxy-start.json"
core_active=true
nido proxy status --json > "$output/proxy-running.json"
grep -Fq '"state":"running"' "$output/proxy-running.json"
printf 'listener:release-api\ny\n' | nido proxy bind download > "$output/bind.log" 2>&1
[[ $(stat -c %a "$NIDO_HOME/download-proxy.toml") = 600 ]]
nido download show --json > "$output/configured-download-show.json"
grep -Fq '"api_state":"configured"' "$output/configured-download-show.json"
grep -Fq '"api_listener":"release-api"' "$output/configured-download-show.json"
grep -Fq 'http://127.0.0.1:18790' "$output/configured-download-show.json"
cp "$NIDO_HOME/config.toml" "$case_root/before/config.toml"
cp "$NIDO_HOME/env.sh" "$case_root/before/env.sh"
cp "$NIDO_HOME/download-proxy.toml" "$case_root/before/binding.toml"
positive_query() {
  local label=$1 before_api before_connect
  shift
  before_api=$(api_count)
  before_connect=$(connect_count)
  nido "$@" > "$output/$label.json"
  [[ $(api_count) = $((before_api+1)) && $(connect_count) = $((before_connect+1)) ]]
  positive_queries=$((positive_queries+1))
  preserved
}
positive_query nido-positive update --check --json
grep -Fq '"latest":"'"$version"'"' "$output/nido-positive.json"
grep -Fq '"available":false' "$output/nido-positive.json"
positive_query proxy-positive proxy update --check --json
grep -Fq '"latest":"v0.0.1"' "$output/proxy-positive.json"
probes_before=$(version_probe_count)
positive_query codex-positive codex update --check --json
[[ $(version_probe_count) = $((probes_before+1)) ]]
grep -Fq '"latest":"0.164.0"' "$output/codex-positive.json"
cmp "$NIDO_HOME/config.toml" "$case_root/before/config.toml"
cmp "$NIDO_HOME/env.sh" "$case_root/before/env.sh"
cmp "$NIDO_HOME/download-proxy.toml" "$case_root/before/binding.toml"
before_api=$(api_count)
before_connect=$(connect_count)
if SSL_CERT_FILE="$case_root/no-such-ca.pem" nido update --check --json > "$output/bad-ca.log" 2>&1; then
  printf 'untrusted TLS origin unexpectedly succeeded\n' >&2; exit 1
fi
[[ $(api_count) = "$before_api" && $(connect_count) = $((before_connect+1)) ]]
printf 'UNKNOWN-OWNER-SENTINEL\n' > "$NIDO_HOME/download-proxy.toml"
snapshot_home "$case_root/before/bad-binding-before"
before_connect=$(connect_count)
if nido update --check --proxy http://127.0.0.1:18790 > "$output/bad-binding.log" 2>&1; then
  printf 'unknown binding was bypassed by explicit proxy\n' >&2; exit 1
fi
snapshot_home "$case_root/before/bad-binding-after"
cmp "$case_root/before/bad-binding-before" "$case_root/before/bad-binding-after"
[[ $(api_count) = "$before_api" && $(connect_count) = "$before_connect" ]]
cp "$case_root/before/binding.toml" "$NIDO_HOME/download-proxy.toml"
chmod 0600 "$NIDO_HOME/download-proxy.toml"
nido proxy stop --yes > "$output/proxy-stop.json"
core_active=false
if nido update --check > "$output/stopped-binding.log" 2>&1; then
  printf 'stopped listener unexpectedly served API metadata\n' >&2; exit 1
fi
[[ $(api_count) = "$before_api" && $(connect_count) = "$before_connect" ]]
# Persisted listener identity follows an explicitly applied port change.
render_config 18791
nido proxy configure --file "$case_root/api-config.yaml" --yes > "$output/reconfigure.json"
nido proxy start --yes > "$output/restart.json"
core_active=true
nido download show --json > "$output/reconfigured-download-show.json"
grep -Fq 'http://127.0.0.1:18791' "$output/reconfigured-download-show.json"
cmp "$NIDO_HOME/download-proxy.toml" "$case_root/before/binding.toml"
positive_query nido-reconfigured-positive update --check --json
nido proxy bind download --proxy off --yes > "$output/off.log"
nido download show --json > "$output/off-download-show.json"
grep -Fq '"api_state":"disabled"' "$output/off-download-show.json"
before_api=$(api_count)
before_connect=$(connect_count)
if nido update --check --proxy http://127.0.0.1:18791 > "$output/off-disabled.log" 2>&1; then
  printf 'one-off proxy re-enabled the disabled API feature\n' >&2; exit 1
fi
grep -Fq 'Nido API 功能未启用' "$output/off-disabled.log"
[[ $(api_count) = "$before_api" && $(connect_count) = "$before_connect" ]]
nido proxy status --json > "$output/off-core-running.json"
grep -Fq '"state":"running"' "$output/off-core-running.json"
preserved
cmp "$NIDO_HOME/config.toml" "$case_root/before/config.toml"
cmp "$NIDO_HOME/env.sh" "$case_root/before/env.sh"
cp "$case_root/origin/requests.jsonl" "$output/api-requests.jsonl"
cp "$NIDO_HOME/proxy/nido-test-connect.jsonl" "$output/proxy-connects.jsonl"
cp "$NIDO_HOME/download-proxy.toml" "$output/final-disabled-binding.toml"
checks_complete=true
printf 'PASS %s API contract: %s positive named-listener queries; bad CA/binding, stopped listener, port change and off verified; metadata is a loopback fixture\n' "$input_scope" "$positive_queries"
