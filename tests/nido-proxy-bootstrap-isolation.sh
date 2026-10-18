#!/usr/bin/env bash
# Offline Linux amd64 runner for the Proxy bootstrap contract tests.
# Local artifacts use a fake runner; HTTP uses loopback TLS/CONNECT fixtures.
# Includes the source-only listener binding and unconfigured API gate.
# This does not install/start a real Mihomo core or validate public networking.
set -euo pipefail
umask 077
fail() { printf 'FAIL bootstrap isolation: %s\n' "$*" >&2; exit 2; }
test_binary=${NIDO_BOOTSTRAP_TEST_BINARY:?set an already compiled trusted Linux amd64 nido.test}
sdk=${NIDO_BOOTSTRAP_SDK:?set an already extracted executable Linux amd64 Go SDK}
output=${NIDO_BOOTSTRAP_OUTPUT:?set a fresh absolute evidence directory}
image=${NIDO_BOOTSTRAP_IMAGE:-public.ecr.aws/docker/library/golang:1.23-bookworm}
for tool in docker file shasum; do command -v "$tool" >/dev/null || fail "missing existing tool: $tool"; done
[[ $test_binary = /* && -f $test_binary && ! -L $test_binary && -r $test_binary ]] || fail 'test binary must be an absolute regular file'
[[ $sdk = /* && -d $sdk && ! -L $sdk ]] || fail 'SDK must be an absolute regular directory'
[[ -x $sdk/bin/go && -x $sdk/pkg/tool/linux_amd64/compile && -r $sdk/src/runtime/runtime2.go ]] || fail 'a complete executable Linux SDK is required; no downloads are performed'
sdk=$(cd "$sdk" && pwd -P)
# The module-cache toolchain has owner-only executable bits; do not chmod it.
# An extracted SDK must instead already be traversable/executable by uid10001.
blocked=$(find "$sdk/bin" "$sdk/pkg/tool/linux_amd64" -type f ! -perm -0001 -print -quit) || fail 'cannot inspect SDK executable permissions'
[[ -z $blocked ]] || fail 'SDK tools must already be executable by the container uid'
description=$(LC_ALL=C file -b "$test_binary")
[[ $description = *'ELF 64-bit'* && $description = *'x86-64'* && $description = *'statically linked'* ]] || fail 'test input must be a static Linux amd64 ELF'
[[ $output = /* && ! -e $output && ! -L $output && -d $(dirname "$output") ]] || fail 'evidence output must be new with an existing parent'
case "$sdk,$output" in *$'\n'*|*$'\r'*) fail 'paths cannot contain newlines' ;; esac
case "$sdk$output" in *,*) fail 'mount paths cannot contain comma' ;; esac
mkdir "$output"
output=$(cd "$output" && pwd -P)
mkdir "$output/inputs" "$output/container-evidence"
cp "$test_binary" "$output/inputs/nido.test"
chmod 0555 "$output/inputs/nido.test"
chmod 0755 "$output/inputs"
chmod 0777 "$output/container-evidence"
shasum -a 256 "$output/inputs/nido.test" "$sdk/bin/go" "$sdk/pkg/tool/linux_amd64/compile" "${BASH_SOURCE[0]}" > "$output/inputs.sha256"
printf 'scope=offline-linux-amd64-proxy-bootstrap-contract\nnetwork=none-loopback-only\nlocal_artifact=fake-runner\nhttp=local-tls-connect-fixtures\nreal_mihomo_start=not-tested\npublic_acceptance=not-tested\nsource=not-mounted\n' > "$output/provenance.txt"
date -u '+%Y-%m-%dT%H:%M:%SZ' > "$output/start-utc.txt"
cid=''
cleanup() {
  local status=$?
  trap - EXIT
  if [[ $cid =~ ^[0-9a-f]{64}$ ]]; then
    if ! docker rm -f "$cid" > "$output/container-cleanup.log" 2>&1; then
      printf 'FAIL bootstrap isolation: cannot remove this run container\n' >&2
      [[ $status != 0 ]] || status=1
    fi
  fi
  printf '%s\n' "$status" > "$output/outer-exit.txt"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cid=$(docker create --pull never --platform linux/amd64 --read-only --network none \
  --cap-drop ALL --security-opt no-new-privileges --user 10001:10001 --pids-limit 256 --memory 1536m \
  --tmpfs /case:rw,exec,nosuid,nodev,mode=1777,size=1g \
  --mount "type=bind,source=$output/inputs,target=/inputs,readonly" \
  --mount "type=bind,source=$sdk,target=/opt/go,readonly" \
  --mount "type=bind,source=$output/container-evidence,target=/evidence" \
  -e HOME=/case/home -e TMPDIR=/case/tmp -e GOTMPDIR=/case/tmp -e GOROOT=/opt/go \
  -e GOTOOLCHAIN=local -e GOPROXY=off -e GOSUMDB=off -e GOWORK=off -e GOENV=off -e GOTELEMETRY=off \
  -e GOCACHE=/case/go-cache -e GOPATH=/case/go-path -e GOMODCACHE=/case/go-mod -e GOMAXPROCS=2 \
  --entrypoint /bin/bash "$image" -euo pipefail -c '
    umask 077
    [[ $(uname -s) = Linux && $(uname -m) = x86_64 && $(id -u) = 10001 ]]
    unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy NIDO_GITHUB_PROXY NIDO_TEST_MIHOMO NIDO_TEST_NIDO
    mkdir -p "$HOME" "$TMPDIR" "$GOCACHE" "$GOPATH" "$GOMODCACHE"
    /opt/go/bin/go version > /evidence/go-version.txt
    test_names=(
      TestProxyBootstrapLocalArtifactWithoutHTTP
      TestProxyBootstrapRejectsLocalChecksumBeforeCoreExecution
      TestProxyBootstrapOfficialUpdateThroughConnect
      TestAPIProxyBindingPersistsAndProtectsOtherTools
      TestAPIProxyBindingRefusesUnknownOrUnsafeFiles
      TestAPIProxyBindingRejectsUnsupportedAndStaleSelections
      TestAPIProxyLocalAndDryRunIgnoreBrokenBinding
      TestAPIProxyBindingAutomaticUpdate
      TestAPIGateDisabledBlocksEveryOnlineEntryPoint
      TestAPIGateReadOnlyDisabledAndOffState
      TestAPIGateOneOffProxyRequiresNonemptyBinding
    )
    # Fix this contract by complete names: later audit tests run separately,
    # without silently widening or invalidating this eleven-test entry point.
    test_pattern=$(printf "%s|" "${test_names[@]}")
    test_pattern="^(${test_pattern%|})$"
    /inputs/nido.test -test.list "$test_pattern" > /evidence/test-list.txt
    for test_name in "${test_names[@]}"; do
      grep -Fxq "$test_name" /evidence/test-list.txt
    done
    [[ $(wc -l < /evidence/test-list.txt) -eq 11 ]]
    /inputs/nido.test -test.v -test.count=1 -test.timeout=8m -test.run "$test_pattern" 2>&1 | tee /evidence/tests.log
    if grep -Eq -- "--- (FAIL|SKIP):|WARNING: DATA RACE|^panic:|^fatal error:" /evidence/tests.log; then
      exit 1
    else
      grep_status=$?
      [[ $grep_status = 1 ]]
    fi
    grep -Eq "^--- PASS: TestProxyBootstrapLocalArtifactWithoutHTTP[[:space:]]" /evidence/tests.log
    grep -Eq "^--- PASS: TestProxyBootstrapRejectsLocalChecksumBeforeCoreExecution[[:space:]]" /evidence/tests.log
    grep -Eq "^--- PASS: TestProxyBootstrapOfficialUpdateThroughConnect[[:space:]]" /evidence/tests.log
    while IFS= read -r test_name; do
      grep -Fq -- "--- PASS: $test_name (" /evidence/tests.log
    done < /evidence/test-list.txt
    grep -Fxq PASS /evidence/tests.log
    printf "{\"scope\":\"offline-linux-amd64-proxy-bootstrap-contract\",\"passed\":true,\"persistent_api_binding_tested\":true,\"unconfigured_api_disabled_tested\":true,\"local_artifact_runner\":\"fake\",\"http_fixture\":\"loopback-tls-connect\",\"real_mihomo_start_tested\":false,\"public_network_acceptance\":false}\n" > /evidence/result.json
  ')
[[ $cid =~ ^[0-9a-f]{64}$ ]] || fail 'Docker did not return a container identity'
printf '%s\n' "$cid" > "$output/container.id"
docker inspect "$cid" > "$output/container-inspect-before-start.json"
set +e
docker start -a "$cid" > "$output/console.log" 2>&1
attach_status=$?
container_status=$(docker inspect --format '{{.State.ExitCode}}' "$cid")
inspect_status=$?
set -e
result=$attach_status
if [[ $inspect_status != 0 || ! $container_status =~ ^[0-9]+$ ]]; then result=1
elif [[ $container_status != 0 ]]; then result=$container_status
fi
date -u '+%Y-%m-%dT%H:%M:%SZ' > "$output/end-utc.txt"
printf '%s\n' "$result" > "$output/outer-exit.txt"
if [[ $result = 0 ]]; then
  [[ -s $output/container-evidence/result.json ]] || fail 'successful container has no contract result'
  printf 'PASS isolated bootstrap contracts; real Mihomo start and public acceptance remain untested\nEvidence: %s\n' "$output"
else
  printf 'FAIL bootstrap contract tests; see evidence: %s\n' "$output" >&2
fi
exit "$result"
