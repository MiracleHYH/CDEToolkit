#!/usr/bin/env bash
# Acceptance for an independently cosign-verified public Nido Linux amd64 tar.
# The default mode requires verified public inputs. Explicit pre-release-local
# mode is separately labelled and never fabricates signature verification.
# Neither mode runs Nido on the host. API/files and the local CONNECT/controller
# core are explicit loopback fixtures, not proof of any public network provider.
set -euo pipefail
umask 077
fail() { printf 'FAIL release API isolation: %s\n' "$*" >&2; exit 2; }
input=${NIDO_API_RELEASE_INPUT:?set the independently cosign-verified public asset directory}
input_scope=${NIDO_API_RELEASE_INPUT_SCOPE:-verified-public}
fixture=${NIDO_API_RELEASE_FIXTURE:?set the built trusted static Linux amd64 test-only API fixture}
output=${NIDO_API_RELEASE_OUTPUT:?set a fresh absolute evidence directory}
version=${NIDO_API_RELEASE_VERSION:?set the exact public stable release version}
image=${NIDO_API_RELEASE_IMAGE:-public.ecr.aws/docker/library/golang:1.23-bookworm}
tests_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail 'version must be stable X.Y.Z'
case $input_scope in verified-public|pre-release-local) ;; *) fail 'input scope must be verified-public or explicitly pre-release-local' ;; esac
[[ $input = /* && -d $input && ! -L $input ]] || fail 'asset input must be an absolute ordinary directory'
names=(nido-linux-amd64.tar.gz nido-linux-amd64.tar.gz.sha256)
[[ $input_scope != verified-public ]] || names+=(nido-linux-amd64.tar.gz.sigstore.json nido-linux-amd64.tar.gz.sha256.sigstore.json)
for name in "${names[@]}"; do
  [[ -f $input/$name && -s $input/$name && ! -L $input/$name ]] || fail "missing independently verified ordinary input: $name"
done
[[ $fixture = /* && -f $fixture && -s $fixture && ! -L $fixture ]] || fail 'fixture must be an absolute trusted regular ELF'
[[ $output = /* && ! -e $output && ! -L $output && -d $(dirname "$output") ]] || fail 'output must be fresh, absolute and have an existing parent'
case "$output$input$fixture" in *$'\n'*|*$'\r'*|*,*) fail 'input/output paths cannot contain newlines or commas' ;; esac
for tool in docker file shasum tar awk; do command -v "$tool" >/dev/null || fail "missing existing tool: $tool"; done
description=$(LC_ALL=C file -b "$fixture")
[[ $description = *'ELF 64-bit'* && $description = *'x86-64'* && $description = *'statically linked'* ]] || fail 'fixture must be a static Linux amd64 ELF'
mkdir "$output"
output=$(cd "$output" && pwd -P)
mkdir "$output/inputs" "$output/container-evidence"
for name in "${names[@]}"; do cp "$input/$name" "$output/inputs/$name"; done
cp "$fixture" "$output/inputs/api-fixture"
cp "$tests_dir/nido-api-release-inner.sh" "$output/inputs/inner.sh"
chmod 0755 "$output/inputs"
chmod 0644 "$output/inputs/"*
chmod 0555 "$output/inputs/api-fixture"
chmod 0777 "$output/container-evidence"
shasum -a 256 "$output/inputs/"* "${BASH_SOURCE[0]}" "$tests_dir/fixtures/nido-api-release/main.go" > "$output/inputs.sha256"
printf 'version=%s\nnido_input_scope=%s\nplatform=linux/amd64\nuid=10001\nnetwork=none-loopback-only\nfile_routes=None,prefix,Proxy-reference\napi_route=independent-Nido-Proxy\nmetadata=private-loopback-TLS-fixture\nassets=trusted-fake-core-gzip\ncore=test-only-api-file-CONNECT-and-native-controller-fixture\nreal_mihomo=not-tested\npublic_api_network=not-tested\nreal_home=not-mounted\nsource=not-mounted\n' "$version" "$input_scope" > "$output/provenance.txt"
printf 'host_uname=%s\n' "$(uname -srm)" >> "$output/provenance.txt"
if [[ $input_scope = verified-public ]]; then
  printf 'nido=provided-independently-cosign-verified-public-release\nsource_nido_binary=never-built-or-used\n' >> "$output/provenance.txt"
else
  printf 'nido=trusted-pre-release-local-build\nsigstore=not-required-and-not-claimed\npublic_release_acceptance=false\n' >> "$output/provenance.txt"
fi
date -u '+%Y-%m-%dT%H:%M:%SZ' > "$output/start-utc.txt"
cid=''
cleanup() {
  local status=$?
  trap - EXIT
  if [[ $cid =~ ^[0-9a-f]{64}$ ]]; then
    if ! docker rm -f "$cid" > "$output/container-cleanup.log" 2>&1; then
      printf 'FAIL release API isolation: could not remove this run container\n' >&2
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
  --cap-drop ALL --security-opt no-new-privileges --user 10001:10001 --pids-limit 128 --memory 1g \
  --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  --add-host api.github.com:127.0.0.1 --add-host github.com:127.0.0.1 --add-host gh-proxy.org:127.0.0.1 \
  --tmpfs /case:rw,exec,nosuid,nodev,mode=1777,size=768m \
  --mount "type=bind,source=$output/inputs,target=/inputs,readonly" \
  --mount "type=bind,source=$output/container-evidence,target=/evidence" \
  -e NIDO_API_RELEASE_INPUT=/inputs -e NIDO_API_RELEASE_OUTPUT=/evidence -e NIDO_API_RELEASE_VERSION="$version" -e NIDO_API_RELEASE_INPUT_SCOPE="$input_scope" \
  --entrypoint /bin/bash "$image" /inputs/inner.sh)
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
if [[ $result = 0 ]]; then
  [[ -s $output/container-evidence/result.json ]] || fail 'successful process omitted its contract result'
  grep -Fq '"passed":true' "$output/container-evidence/result.json" || fail 'contract result is not passing'
  printf 'PASS %s API contract on isolated Linux amd64; public API and real Mihomo remain separate\nEvidence: %s\n' "$input_scope" "$output"
else
  printf 'FAIL signed release API contract; inspect %s\n' "$output" >&2
fi
exit "$result"
