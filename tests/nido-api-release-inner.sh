#!/usr/bin/env bash
# Only trusted local/public Nido inputs; private loopback TLS and a fake core.
# All three routes terminate inside this container's network namespace.
set -Eeuo pipefail
trap 'printf "FAIL API contract at line %s\n" "$LINENO" >&2' ERR
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
manual_core_pid=''
connect_log=''
core_active=false
checks_complete=false
rejected_calls=0
positive_queries=0
asset_installs=0
asset_candidates=0
failed_installs=0
native_rejections=0
routes_verified=false
fallback_verified=false
codex_independent=false
gds_unregistered=false
builtin_verified=false
native_upgrade_verified=false
native_reload_verified=false
export HOME="$case_root/home" TMPDIR="$case_root/tmp" PATH=/usr/bin:/bin
export NIDO_HOME="$HOME/.nido" CODEX_HOME="$HOME/.codex"
export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_DATA_HOME="$HOME/.local/share"
export CURL_HOME="$HOME" ZDOTDIR="$HOME" BASH_ENV=/dev/null ENV=/dev/null
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy
unset NIDO_GITHUB_PROXY CONDARC PIP_CONFIG_FILE SSL_CERT_FILE SSL_CERT_DIR
mkdir -p "$HOME" "$TMPDIR" "$case_root/package" "$case_root/guard" "$case_root/before"
nido() { "$NIDO_HOME/bin/nido" --root "$NIDO_HOME" "$@"; }
cleanup() {
  local status=$? cleanup_status=0 passed=false evidence_exported=false
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
  if [[ -n $manual_core_pid ]]; then
    if kill -0 "$manual_core_pid" 2>/dev/null; then kill "$manual_core_pid"; fi
    wait "$manual_core_pid" 2>/dev/null
  fi
  [[ $cleanup_status = 0 ]] || status=1
  if [[ -f $case_root/origin/requests.jsonl ]]; then cp "$case_root/origin/requests.jsonl" "$output/network-requests.jsonl" || status=1; fi
  if [[ -f $case_root/manual-proxy/nido-test-connect.jsonl ]]; then cp "$case_root/manual-proxy/nido-test-connect.jsonl" "$output/custom-proxy-connects.jsonl" || status=1; fi
  if [[ -f $NIDO_HOME/proxy/nido-test-connect.jsonl ]]; then cp "$NIDO_HOME/proxy/nido-test-connect.jsonl" "$output/proxy-connects.jsonl" || status=1; fi
  if [[ -f $NIDO_HOME/proxy/nido-test-controller.jsonl ]]; then cp "$NIDO_HOME/proxy/nido-test-controller.jsonl" "$output/controller-requests.jsonl" || status=1; fi
  write_result() {
    passed=false
    if [[ $status = 0 && $checks_complete = true ]]; then passed=true; fi
    printf '{"scope":"nido-linux-amd64-release-network-contract","nido_input_scope":"%s","version":"%s","passed":%s,"checks_complete":%s,"rejected_without_requests_or_writes":%s,"positive_api_queries":%s,"verified_local_fake_core_installs":%s,"rejected_online_asset_candidates":%s,"failed_installs_preserved":%s,"native_update_failures_preserved":%s,"exclusive_github_file_routes_tested":%s,"automatic_fallback_tested":false,"cross_mode_fallback_rejected":%s,"codex_independent":%s,"gds_unregistered":%s,"builtin_process_identity_verified":%s,"native_update_same_birth_verified":%s,"native_reload_verified":%s,"api_metadata":"private-loopback-TLS-fixture","core":"test-only-API-file-CONNECT-and-native-controller-fixture","public_api_network_tested":false,"real_mihomo_tested":false,"source_nido_binary_used":%s,"cleanup_verified":%s,"evidence_exported":%s}\n' \
      "$input_scope" "$version" "$passed" "$checks_complete" "$rejected_calls" "$positive_queries" "$asset_installs" "$asset_candidates" "$failed_installs" "$native_rejections" "$routes_verified" "$fallback_verified" "$codex_independent" "$gds_unregistered" "$builtin_verified" "$native_upgrade_verified" "$native_reload_verified" "$([[ $input_scope = pre-release-local ]] && printf true || printf false)" "$([[ $cleanup_status = 0 ]] && printf true || printf false)" "$evidence_exported" > "$output/result.json" || return 1
    printf '%s\n' "$status" > "$output/inner-exit.txt"
  }
  write_result || status=1
  if find "$output" -mindepth 1 -type f -exec chmod 0644 -- {} + &&
      find "$output" -mindepth 1 -type d -exec chmod 0755 -- {} +; then
    evidence_exported=true
  else
    status=1
    printf 'FAIL: cannot export this run evidence permissions\n' >&2
  fi
  write_result || status=1
  chmod 0644 -- "$output/result.json" "$output/inner-exit.txt" || status=1
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
protected_files=(.bashrc .zshrc .profile .condarc .gitconfig .codex/auth.json .codex/config.toml .codex/.env .codex/sessions/one.jsonl .codex/state.sqlite .config/mihomo/config.yaml .config/gds/config.toml)
mkdir -p "$CODEX_HOME/sessions" "$HOME/.config/mihomo" "$HOME/.config/gds"
for relative in "${protected_files[@]}"; do printf 'PROTECTED-SENTINEL %s\n' "$relative" > "$HOME/$relative"; done
printf 'HTTPS_PROXY=${USER_OWN_PROXY_EXPRESSION}\nKEEP=literal\n' > "$CODEX_HOME/.env"
cp -R "$HOME" "$case_root/before/external"
preserved() {
  local relative
  for relative in "${protected_files[@]}"; do
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
    find "$HOME" ! -path "$NIDO_HOME/proxy/nido-test-connect.jsonl" ! -path "$NIDO_HOME/proxy/nido-test-controller.jsonl" -printf '%y:%m:%U:%G:%P:%l\n' | LC_ALL=C sort
    while IFS= read -r -d '' path; do sha256sum "$path"; done < <(find "$HOME" -type f ! -path "$NIDO_HOME/proxy/nido-test-connect.jsonl" ! -path "$NIDO_HOME/proxy/nido-test-controller.jsonl" -print0 | LC_ALL=C sort -z)
  } > "$destination"
}
"$case_root/package/nido" --root "$NIDO_HOME" init --yes > "$output/init.log"
nido codex setup --home "$CODEX_HOME" --yes > "$output/codex-setup.json"
cp "$NIDO_HOME/config.toml" "$case_root/before/unselected.toml"
NIDO_API_FIXTURE_LISTEN=127.0.0.1:443 "$fixture" origin "$case_root/origin" "$version" > "$output/origin.log" 2>&1 &
origin_pid=$!
for ((attempt=0; attempt<600; attempt++)); do
  [[ ! -s "$case_root/origin/origin.addr" ]] || break
  kill -0 "$origin_pid"
  sleep 0.05
done
[[ $(cat "$case_root/origin/origin.addr") = 127.0.0.1:443 && -s "$case_root/origin/ca.pem" ]]
{
  printf 'origin_pid=%s\n' "$origin_pid"
  printf 'origin_kernel_executable='
  readlink "/proc/$origin_pid/exe"
  printf 'shell_kernel_executable='
  readlink "/proc/$$/exe"
  stat -c 'origin_proc_owner=%u:%g' "/proc/$origin_pid"
  cat /proc/sys/net/ipv4/ip_unprivileged_port_start
  cat /etc/hosts
} > "$output/kernel-process-observation.txt"
mkdir "$case_root/empty-certs"
export SSL_CERT_FILE="$case_root/origin/ca.pem" SSL_CERT_DIR="$case_root/empty-certs"
request_count() { wc -l < "$case_root/origin/requests.jsonl"; }
connect_count() { if [[ -n $connect_log && -e $connect_log ]]; then wc -l < "$connect_log"; else printf '0\n'; fi; }
version_probe_count() { if [[ -e "$NIDO_API_RELEASE_CODEX_VERSION_LOG" ]]; then wc -l < "$NIDO_API_RELEASE_CODEX_VERSION_LOG"; else printf '0\n'; fi; }
reject_without_requests() {
  local label=$1 before_requests before_connect probes_before
  shift
  before_requests=$(request_count); before_connect=$(connect_count); probes_before=$(version_probe_count)
  snapshot_home "$case_root/before/rejected-before"
  if nido "$@" > "$output/$label.log" 2>&1; then printf 'unexpected success: %s\n' "$label" >&2; exit 1; fi
  snapshot_home "$case_root/before/rejected-after"
  cmp "$case_root/before/rejected-before" "$case_root/before/rejected-after"
  [[ $(request_count) = "$before_requests" && $(connect_count) = "$before_connect" && $(version_probe_count) = "$probes_before" ]]
  preserved
  rejected_calls=$((rejected_calls+1))
}
# Defaults are direct and usable. Invalid explicit choices reject before any
# request or write; GitHub Accelerate does not authorize or gate API access.
reject_without_requests removed-auto download set auto --yes
reject_without_requests removed-gds gds status
reject_without_requests missing-proxy-reference download set proxy --yes
reject_without_requests unsafe-prefix download set http://gh-proxy.org --yes
reject_without_requests unsafe-proxy download proxy file:///tmp/unsafe --yes
nido status --json > "$output/registered-components.json"
! grep -Fq '"name":"gds"' "$output/registered-components.json"
gds_unregistered=true
nido proxy install --dry-run --json > "$output/offline-preview.json"
nido proxy install --artifact "$fixture" --sha256 "$(sha256 "$fixture")" --yes > "$output/local-install.json"
asset_installs=$((asset_installs+1))
render_config() {
  printf '{"listeners":[{"name":"release-api","type":"http","listen":"127.0.0.1","port":%s}],"rules":["MATCH,DIRECT"],"external-controller":"127.0.0.1:18792","secret":"isolated-controller-secret","nido-test-api-origin":"127.0.0.1:443"}\n' "$1" > "$case_root/api-config.yaml"
  chmod 0600 "$case_root/api-config.yaml"
}
render_config 18790
cp "$case_root/api-config.yaml" "$NIDO_HOME/proxy/config.yaml"
nido proxy validate --json > "$output/local-validate.json"
# A directly owned fixture child provides the custom PROXY route first. The
# mandatory Nido-managed process identity tests remain at the end; emulators
# which report their own /proc/exe must still fail that complete contract.
mkdir "$case_root/manual-proxy"
cp "$case_root/api-config.yaml" "$case_root/manual-proxy/config.yaml"
connect_log="$case_root/manual-proxy/nido-test-connect.jsonl"
"$fixture" -d "$case_root/manual-proxy" -f "$case_root/manual-proxy/config.yaml" > "$output/manual-core.log" 2>&1 &
manual_core_pid=$!
manual_ready=false
for ((attempt=0; attempt<200; attempt++)); do
  kill -0 "$manual_core_pid"
  if (exec 3<> /dev/tcp/127.0.0.1/18790) 2>/dev/null; then manual_ready=true; break; fi
  sleep 0.05
done
[[ $manual_ready = true ]]
positive_query() {
  local label=$1 expected_connect=$2 expected_host=$3 before_requests before_connect
  shift 3
  before_requests=$(request_count); before_connect=$(connect_count)
  cp "$NIDO_HOME/config.toml" "$case_root/before/query-config.toml"
  cp "$NIDO_HOME/env.sh" "$case_root/before/query-env.sh"
  nido "$@" > "$output/$label.json"
  [[ $(request_count) = $((before_requests+1)) && $(connect_count) = $((before_connect+expected_connect)) ]]
  tail -n1 "$case_root/origin/requests.jsonl" | grep -Fq '"host":"'"$expected_host"'"'
  cmp "$NIDO_HOME/config.toml" "$case_root/before/query-config.toml"
  cmp "$NIDO_HOME/env.sh" "$case_root/before/query-env.sh"
  positive_queries=$((positive_queries+1))
  preserved
}
# APIs use only Nido Proxy, independently of the file route. Poison ambient
# variables to prove None and prefix do not inherit another route.
export HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1
for route in none https://gh-proxy.org; do
  label=none; [[ $route = none ]] || label=prefix
  nido download set "$route" --yes > "$output/set-$label.json"
  before_requests=$(request_count); before_connect=$(connect_count)
  nido update --version "$version" --yes --json > "$output/same-version-$label-noop.json"
  [[ $(request_count) = "$before_requests" && $(connect_count) = "$before_connect" ]]
  positive_query "$label-nido-api-direct" 0 api.github.com update --check --json
  positive_query "$label-mihomo-api-direct" 0 api.github.com proxy update --check --json
  grep -Fq '"latest":"'"$version"'"' "$output/$label-nido-api-direct.json"
  grep -Fq '"latest":"v0.0.1"' "$output/$label-mihomo-api-direct.json"
done
unset HTTPS_PROXY HTTP_PROXY ALL_PROXY https_proxy http_proxy all_proxy
nido download proxy http://127.0.0.1:18790 --yes > "$output/set-general-proxy.json"
positive_query prefix-api-through-proxy 1 api.github.com update --check --json
# Even an unusable GitHub file prefix cannot affect Codex API requests, and
# its expression-valued runtime .env is not interpreted as Nido networking.
nido download set https://gh-proxy.org/fail --yes > "$output/set-prefix-failure.json"
probes_before=$(version_probe_count)
positive_query codex-nido-proxy 1 api.github.com codex update --check --json
[[ $(version_probe_count) = $((probes_before+1)) ]]
grep -Fq '"latest":"0.164.0"' "$output/codex-nido-proxy.json"
codex_independent=true
positive_query failing-file-prefix-does-not-route-api 1 api.github.com update --check --json
# Invalid version metadata still rejects through the selected API route and
# cannot change configuration, authentication, sessions or installed programs.
printf 'bad-tag\n' > "$case_root/origin/response-mode"
for component in nido proxy codex; do
  query=(update --check --json)
  [[ $component = nido ]] || query=("$component" "${query[@]}")
  snapshot_home "$case_root/before/metadata-before"
  before_requests=$(request_count); before_connect=$(connect_count)
  if nido "${query[@]}" > "$output/invalid-metadata-$component.log" 2>&1; then exit 1; fi
  snapshot_home "$case_root/before/metadata-after"
  cmp "$case_root/before/metadata-before" "$case_root/before/metadata-after"
  [[ $(request_count) = $((before_requests+1)) && $(connect_count) = $((before_connect+1)) ]]
  preserved
done
rm "$case_root/origin/response-mode"
# First installation uses a fixed, production SHA and never queries the API.
# The fake payload cannot satisfy that identity: validate routing and safe
# rejection, not an online installation of a real core. The shared working API
# Proxy is deliberately ignored by None and prefix and used only by reference.
for route in none prefix proxy; do
  target="$HOME/asset-$route"
  accelerate=$route; [[ $route != prefix ]] || accelerate=https://gh-proxy.org
  "$case_root/package/nido" --root "$target" init --github-accelerate "$accelerate" --proxy http://127.0.0.1:18790 --yes > "$output/asset-$route-init.json"
  mkdir -p "$target/proxy/data"
  printf 'ONLINE-CANDIDATE-DATA-SENTINEL\n' > "$target/proxy/data/keep"
  : > "$target/proxy/.lock"
  chmod 0600 "$target/proxy/.lock"
  snapshot_home "$case_root/before/asset-route-before"
  before_requests=$(request_count); before_connect=$(connect_count)
  if "$target/bin/nido" --root "$target" proxy install --yes > "$output/asset-$route-rejected.log" 2>&1; then
    printf 'fake payload unexpectedly satisfied pinned official SHA: %s\n' "$route" >&2; exit 1
  fi
  snapshot_home "$case_root/before/asset-route-after"
  cmp "$case_root/before/asset-route-before" "$case_root/before/asset-route-after"
  [[ $(request_count) = $((before_requests+1)) ]]
  expected_connect=0; [[ $route != proxy ]] || expected_connect=1
  [[ $(connect_count) = $((before_connect+expected_connect)) ]]
  expected_host=github.com; [[ $route != prefix ]] || expected_host=gh-proxy.org
  tail -n1 "$case_root/origin/requests.jsonl" | grep -Fq '"host":"'"$expected_host"'"'
  [[ ! -e "$target/proxy/mihomo" && ! -e "$target/proxy/process.json" ]]
  grep -Fq SHA-256 "$output/asset-$route-rejected.log"
  asset_candidates=$((asset_candidates+1)); failed_installs=$((failed_installs+1))
  preserved
done
routes_verified=true
# A failed accelerated file must not fall through to the reachable official
# origin or acquire the otherwise working general Proxy. No API is needed.
target="$HOME/rejected-prefix"
"$case_root/package/nido" --root "$target" init --github-accelerate https://gh-proxy.org/fail --proxy http://127.0.0.1:18790 --yes > "$output/prefix-failure-init.json"
mkdir -p "$target/proxy/data"
printf 'PREFIX-DATA-SENTINEL\n' > "$target/proxy/data/keep"
: > "$target/proxy/.lock"
chmod 0600 "$target/proxy/.lock"
snapshot_home "$case_root/before/prefix-failure-before"
before_requests=$(request_count); before_connect=$(connect_count)
if "$target/bin/nido" --root "$target" proxy install --yes > "$output/prefix-file-failure.log" 2>&1; then exit 1; fi
snapshot_home "$case_root/before/prefix-failure-after"
cmp "$case_root/before/prefix-failure-before" "$case_root/before/prefix-failure-after"
[[ $(request_count) = $((before_requests+1)) && $(connect_count) = "$before_connect" ]]
tail -n1 "$case_root/origin/requests.jsonl" | grep -Fq '"host":"gh-proxy.org"'
[[ ! -e "$target/proxy/mihomo" ]]
failed_installs=$((failed_installs+1))
preserved
# Unknown old API sidecars are preserved and ignored by explicit typed modes.
printf 'UNKNOWN-OWNER-SENTINEL\n' > "$NIDO_HOME/download-proxy.toml"
chmod 0600 "$NIDO_HOME/download-proxy.toml"
cp "$NIDO_HOME/download-proxy.toml" "$case_root/before/legacy-sidecar"
nido download set none --yes > "$output/select-over-legacy-sidecar.json"
nido download proxy none --yes > "$output/clear-general-proxy.json"
positive_query direct-ignores-legacy-sidecar 0 api.github.com update --check --json
cmp "$NIDO_HOME/download-proxy.toml" "$case_root/before/legacy-sidecar"
# Keep TLS verification active, with no fallback or protected-data changes.
before_requests=$(request_count); before_connect=$(connect_count)
if SSL_CERT_FILE="$case_root/no-such-ca.pem" nido update --check > "$output/bad-ca.log" 2>&1; then exit 1; fi
[[ $(request_count) = "$before_requests" && $(connect_count) = "$before_connect" ]]
# A dead custom PROXY must not try the now-reachable DIRECT route either.
kill "$manual_core_pid"
wait "$manual_core_pid" || [[ $? = 143 ]]
manual_core_pid=''
nido download proxy http://127.0.0.1:18790 --yes > "$output/custom-before-failure.json"
reject_without_requests stopped-custom update --check
fallback_verified=true
# Nido downloads a core only during first install. The retired staged update
# route must not return: exercise metadata/hash/size boundaries as failed fresh
# installs instead, retaining unknown configuration/data already in that root.
nido download set https://gh-proxy.org --yes > "$output/failed-install-route.json"
for response in deny bad-size bad-asset; do
  printf '%s\n' "$response" > "$case_root/origin/response-mode"
  target="$HOME/rejected-install-$response"
  "$case_root/package/nido" --root "$target" init --github-accelerate https://gh-proxy.org --yes > "$output/rejected-$response-init.json"
  mkdir -p "$target/proxy/data"
  printf 'UNKNOWN-DATA-SENTINEL\n' > "$target/proxy/data/keep"
  : > "$target/proxy/.lock"
  chmod 0600 "$target/proxy/.lock"
  snapshot_home "$case_root/before/install-before"
  before_requests=$(request_count)
  if "$target/bin/nido" --root "$target" proxy install --yes --json > "$output/failed-install-$response.log" 2>&1; then printf 'bad install unexpectedly succeeded: %s\n' "$response" >&2; exit 1; fi
  snapshot_home "$case_root/before/install-after"
  cmp "$case_root/before/install-before" "$case_root/before/install-after"
  [[ $(request_count) = $((before_requests+1)) ]]
  [[ ! -e "$target/proxy/mihomo" ]]
  failed_installs=$((failed_installs+1))
  preserved
done
rm "$case_root/origin/response-mode"
# Turning off only Proxy does not alter the file route or an unowned sidecar.
nido download proxy none --yes > "$output/clear-proxy.json"
nido download set none --yes > "$output/clear-accelerate.json"
nido download show --json > "$output/final-network.json"
grep -Fq '"github_accelerate":"none"' "$output/final-network.json"
positive_query none-is-direct 0 api.github.com update --check --json
cmp "$NIDO_HOME/download-proxy.toml" "$case_root/before/legacy-sidecar"
preserved
# The complete contract requires kernel ownership of Nido-managed processes.
# No skip or interpreter/identity fallback is allowed on emulated containers.
cp "$connect_log" "$output/custom-proxy-connects.jsonl"
connect_log="$NIDO_HOME/proxy/nido-test-connect.jsonl"
nido proxy start --yes > "$output/proxy-start.json"
core_active=true
nido proxy status --json > "$output/proxy-running.json"
grep -Fq '"state":"running"' "$output/proxy-running.json"
nido download proxy http://127.0.0.1:18790 --yes > "$output/set-PROXY-builtin.json"
positive_query PROXY-builtin-nido 1 api.github.com update --check --json
positive_query PROXY-builtin-mihomo 1 api.github.com proxy update --check --json
# Native core updates use only the captured private controller and core's own
# updater. Poison Nido's saved download route: no API/asset/CONNECT request may
# result from either the failed acknowledgements or successful same-PID exec.
nido download set https://gh-proxy.org/fail --yes --json > "$output/native-independent-mode.json"
controller_posts() { awk '/"method":"POST"/ { count++ } END { print count+0 }' "$NIDO_HOME/proxy/nido-test-controller.jsonl"; }
for response in reject malformed ack-noop drop-response; do
  printf '%s\n' "$response" > "$NIDO_HOME/proxy/nido-test-native-mode"
  snapshot_home "$case_root/before/native-before"
  before_requests=$(request_count); before_connect=$(connect_count); before_posts=$(controller_posts)
  if nido proxy update --yes --json > "$output/native-failed-$response.log" 2>&1; then printf 'native failure unexpectedly succeeded: %s\n' "$response" >&2; exit 1; fi
  snapshot_home "$case_root/before/native-after"
  cmp "$case_root/before/native-before" "$case_root/before/native-after"
  [[ $(request_count) = "$before_requests" && $(connect_count) = "$before_connect" && $(controller_posts) = $((before_posts+1)) ]]
  native_rejections=$((native_rejections+1))
  preserved
done
rm "$NIDO_HOME/proxy/nido-test-native-mode"
pid_before=$(sed -n 's/.*"pid":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$NIDO_HOME/proxy/process.json" | head -n1)
[[ $pid_before =~ ^[1-9][0-9]*$ ]]
birth_before=$(awk '{print $22}' "/proc/$pid_before/stat")
for round in 1 2; do
  cp "$NIDO_HOME/proxy/mihomo" "$case_root/before/native-old-program"
  before_requests=$(request_count); before_connect=$(connect_count); before_posts=$(controller_posts)
  nido proxy update --yes --json > "$output/native-success-$round.json"
  [[ $(request_count) = "$before_requests" && $(connect_count) = "$before_connect" && $(controller_posts) = $((before_posts+1)) ]]
  [[ $(sha256 "$NIDO_HOME/proxy/mihomo") != $(sha256 "$case_root/before/native-old-program") ]]
  cmp "$NIDO_HOME/proxy/meta-backup/mihomo" "$case_root/before/native-old-program"
  [[ ! -e "$NIDO_HOME/proxy/meta-update" ]]
  grep -Fq "$(sha256 "$NIDO_HOME/proxy/mihomo")" "$NIDO_HOME/proxy/process.json"
  [[ $(sed -n 's/.*"pid":[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$NIDO_HOME/proxy/process.json" | head -n1) = "$pid_before" ]]
  [[ $(awk '{print $22}' "/proc/$pid_before/stat") = "$birth_before" ]]
  preserved
done
native_upgrade_verified=true
# Explicit read-only core state and native provider refresh remain local.
before_requests=$(request_count)
nido proxy groups --json > "$output/native-groups.json"
grep -Fq fixture-group "$output/native-groups.json"
nido proxy provider-update all --yes --json > "$output/native-providers.json"
[[ $(request_count) = "$before_requests" ]]
# Advanced users may edit config.yaml themselves; native reload applies this
# exact path, reexecutes the fixture in the same birth and observes new ports.
render_config 18791
cp "$case_root/api-config.yaml" "$NIDO_HOME/proxy/config.yaml"
nido proxy reload --yes --json > "$output/native-reload.json"
[[ $(awk '{print $22}' "/proc/$pid_before/stat") = "$birth_before" ]]
native_reload_verified=true
nido download proxy http://127.0.0.1:18791 --yes --json > "$output/native-reload-mode.json"
positive_query builtin-native-reloaded 1 api.github.com update --check --json
nido proxy stop --yes > "$output/proxy-stop.json"
core_active=false
reject_without_requests stopped-builtin update --check
render_config 18793
cp "$case_root/api-config.yaml" "$NIDO_HOME/proxy/config.yaml"
nido proxy start --yes > "$output/restart.json"
core_active=true
nido download proxy http://127.0.0.1:18793 --yes > "$output/reconfigured-proxy-url.json"
positive_query builtin-reconfigured 1 api.github.com update --check --json
nido proxy stop --yes > "$output/final-proxy-stop.json"
core_active=false
builtin_verified=true
preserved
cp "$case_root/origin/requests.jsonl" "$output/network-requests.jsonl"
cp "$NIDO_HOME/proxy/nido-test-connect.jsonl" "$output/proxy-connects.jsonl"
checks_complete=true
printf 'PASS %s: %s safe rejections; %s API queries; %s verified local fake-core installs; %s failed online candidates and %s native failures preserve data; native same-birth update/reload, exclusive file routes, independent API Proxy, Codex runtime isolation and GDS removal verified\n' "$input_scope" "$rejected_calls" "$positive_queries" "$asset_installs" "$failed_installs" "$native_rejections"
