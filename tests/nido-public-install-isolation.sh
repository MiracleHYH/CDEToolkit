#!/usr/bin/env bash
# Explicit online acceptance of the two parameter-free README commands.
# Only preverified public assets and a separately built PTY fixture enter the
# ordinary-user container. This script never builds or publishes Nido.
set -euo pipefail
umask 077
fail() { printf 'FAIL public installer: %s\n' "$*" >&2; exit 2; }
input=${NIDO_PUBLIC_INSTALL_INPUT:?set the signed public release asset directory}
fixture=${NIDO_PUBLIC_INSTALL_FIXTURE:?set the separately built static Linux amd64 PTY fixture}
output=${NIDO_PUBLIC_INSTALL_OUTPUT:?set a fresh absolute evidence directory}
version=${NIDO_PUBLIC_INSTALL_VERSION:?set the exact public stable version}
commit=${NIDO_PUBLIC_INSTALL_COMMIT:?set the signed release commit}
image=${NIDO_PUBLIC_INSTALL_IMAGE:-public.ecr.aws/docker/library/golang@sha256:2015125fe4a3d97f9ba4ad55d458c2d1f025fdf9cb73d8f423a29e448d59da5e}
accelerator=${NIDO_PUBLIC_INSTALL_ACCELERATOR:-https://gh-proxy.org}
scripts=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ && $commit =~ ^[0-9a-f]{40}$ ]] || fail 'expected exact stable version and commit'
[[ $accelerator =~ ^https://[A-Za-z0-9.-]+\.[A-Za-z][A-Za-z0-9.-]*$ ]] || fail 'accelerator must be an HTTPS DNS origin'
[[ $input = /* && -d $input && ! -L $input ]] || fail 'public inputs must be an ordinary absolute directory'
[[ $fixture = /* && -f $fixture && -s $fixture && ! -L $fixture ]] || fail 'PTY fixture must be a trusted ordinary absolute file'
[[ $output = /* && ! -e $output && ! -L $output && -d $(dirname "$output") ]] || fail 'output must be fresh, absolute and have an existing parent'
case "$input$fixture$output" in *$'\n'*|*$'\r'*|*,*) fail 'paths cannot contain newlines or commas' ;; esac
for tool in docker cosign file shasum; do command -v "$tool" >/dev/null || fail "missing existing tool: $tool"; done
description=$(LC_ALL=C file -b "$fixture")
[[ $description = *'ELF 64-bit'* && $description = *'x86-64'* && $description = *'statically linked'* ]] || fail 'PTY fixture must be a static Linux amd64 ELF'
mkdir "$output" "$output/inputs"
output=$(cd "$output" && pwd -P)
identity="https://github.com/MiracleHYH/CDEToolkit/.github/workflows/release.yml@refs/tags/v$version"
for name in nido-linux-amd64.tar.gz nido-linux-amd64.tar.gz.sha256 install.sh; do
  for path in "$input/$name" "$input/$name.sigstore.json"; do
    [[ -f $path && -s $path && ! -L $path ]] || fail "missing ordinary signed input: $path"
  done
  cp "$input/$name" "$input/$name.sigstore.json" "$output/inputs/"
  cosign verify-blob "$output/inputs/$name" --bundle "$output/inputs/$name.sigstore.json" \
    --certificate-identity "$identity" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-github-workflow-repository MiracleHYH/CDEToolkit \
    --certificate-github-workflow-sha "$commit" \
    --certificate-github-workflow-ref "refs/tags/v$version" \
    --certificate-github-workflow-trigger push \
    --certificate-github-workflow-name release > "$output/cosign-$name.log" 2>&1
done
cp "$fixture" "$output/inputs/pty-fixture"
cp "$scripts/nido-public-install-inner.sh" "$output/inputs/inner.sh"
find "$output/inputs" -type d -exec chmod 0755 {} +
find "$output/inputs" -type f -exec chmod 0644 {} +
chmod 0555 "$output/inputs/pty-fixture"
shasum -a 256 "$output/inputs/"* "${BASH_SOURCE[0]}" > "$output/inputs.sha256"
mkdir "$output/container-evidence"
chmod 0777 "$output/container-evidence"
printf 'version=%s\ncommit=%s\nplatform=linux/amd64\nuid=10001\nnetwork=real-public-HTTPS\ninstaller=cosign-verified-release\npty=real-controlling-terminal\ncurl=/usr/bin/curl-unwrapped\nfirst_same_url_response=compared-before-execution\nsecond_pipeline_response=not-verified-before-execution\ninstalled_binary=compared-with-signed-release\nreal_home=not-mounted\nreal_components=never-invoked\n' "$version" "$commit" > "$output/provenance.txt"
cid=''
cleanup() { [[ -z $cid ]] || docker rm -f "$cid" >/dev/null; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
cid=$(docker create --pull never --platform linux/amd64 --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 10001:10001 --pids-limit 128 --memory 1g \
  --tmpfs /case:rw,exec,nosuid,nodev,mode=1777,size=768m \
  --mount "type=bind,source=$output/inputs,target=/inputs,readonly" \
  --mount "type=bind,source=$output/container-evidence,target=/evidence" \
  -e NIDO_PUBLIC_INSTALL_VERSION="$version" -e NIDO_PUBLIC_INSTALL_ACCELERATOR="$accelerator" \
  -e NIDO_PUBLIC_INSTALL_GENERAL_PROXY="${NIDO_PUBLIC_INSTALL_GENERAL_PROXY:-}" \
  --entrypoint /bin/bash "$image" /inputs/inner.sh)
[[ $cid =~ ^[0-9a-f]{64}$ ]] || fail 'Docker did not return a container identity'
printf '%s\n' "$cid" > "$output/container.id"
docker inspect "$cid" > "$output/container-inspect.json"
set +e
docker start -a "$cid" > "$output/console.log" 2>&1
attached=$?
status=$(docker inspect --format '{{.State.ExitCode}}' "$cid")
inspected=$?
set -e
[[ $attached = 0 && $inspected = 0 && $status = 0 ]] || fail "isolated acceptance failed; inspect $output"
[[ -s $output/container-evidence/result.json ]] || fail 'successful process omitted its result'
grep -Fq '"passed":true' "$output/container-evidence/result.json" || fail 'acceptance did not report success'
printf 'PASS public README installer commands, three routes, cancellation and shell registration\nEvidence: %s\n' "$output"
