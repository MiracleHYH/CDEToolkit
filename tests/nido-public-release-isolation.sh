#!/usr/bin/env bash
# Opt-in public acceptance. Both asset directories must have been independently
# verified with cosign and the exact release workflow/tag identity beforehand.
# This harness never builds an old-version fixture or accesses the real HOME.
set -euo pipefail
umask 077

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 2; }
input=${NIDO_ACCEPT_INPUT:?set the new cosign-verified asset directory}
old_input=${NIDO_ACCEPT_OLD_INPUT:?set the old cosign-verified asset directory}
output=${NIDO_ACCEPT_OUTPUT:?set a fresh evidence directory}
version=${NIDO_ACCEPT_VERSION:-0.8.0}
old_version=${NIDO_ACCEPT_OLD_VERSION:-0.7.0}
mode=${NIDO_ACCEPT_MODE:-native}
platform=${NIDO_ACCEPT_PLATFORM:-}
tests_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
stable='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ $version =~ $stable && $old_version =~ $stable && $version != "$old_version" ]] || fail 'invalid release versions'
for directory in "$input" "$old_input"; do
  [[ $directory = /* && -d $directory && ! -L $directory ]] || fail 'asset inputs must be absolute regular directories'
done
[[ $output = /* && ! -e $output && ! -L $output && -d $(dirname "$output") ]] || fail 'output must be a new absolute directory with an existing parent'
case $mode in
  native|mac)
    case $(uname -s) in Darwin) native_os=darwin ;; Linux) native_os=linux ;; *) fail 'unsupported native OS' ;; esac
    case $(uname -m) in arm64|aarch64) native_arch=arm64 ;; x86_64|amd64) native_arch=amd64 ;; *) fail 'unsupported native architecture' ;; esac
    native_platform="$native_os/$native_arch"
    platform=${platform:-$native_platform}
    [[ $platform = "$native_platform" ]] || fail 'native platform must match this machine'
    mode=native ;;
  docker)
    platform=${platform:-linux/amd64}
    [[ $platform = linux/amd64 ]] || fail 'Docker acceptance currently supports the cached linux/amd64 image'
    command -v docker >/dev/null || fail 'existing Docker runtime is required' ;;
  *) fail 'NIDO_ACCEPT_MODE must be native or docker' ;;
esac
asset="nido-${platform//\//-}.tar.gz"
for directory in "$input" "$old_input"; do
  for name in "$asset" "$asset.sha256"; do
    [[ -s $directory/$name && -s $directory/$name.sigstore.json && ! -L $directory/$name ]] || fail "missing independently verified asset/bundle: $directory/$name"
  done
done
[[ -s $input/install.sh && -s $input/install.sh.sigstore.json && ! -L $input/install.sh ]] || fail 'new signed installer input is required'
for command in tar cmp awk curl; do command -v "$command" >/dev/null || fail "missing existing command: $command"; done
mkdir "$output"
output=$(cd "$output" && pwd -P)
mkdir -p "$output/inputs/new" "$output/inputs/old" "$output/inputs/harness"
for name in "$asset" "$asset.sha256"; do
  cp "$input/$name" "$input/$name.sigstore.json" "$output/inputs/new/"
  cp "$old_input/$name" "$old_input/$name.sigstore.json" "$output/inputs/old/"
done
cp "$input/install.sh" "$input/install.sh.sigstore.json" "$output/inputs/new/"
cp "$tests_dir/nido-public-release-inner.sh" "$tests_dir/conda-cli-isolation.sh" "$output/inputs/harness/"
printf 'new_version=%s\nold_version=%s\nplatform=%s\nmode=%s\nold_binary=actual-preverified-public-release\n' \
  "$version" "$old_version" "$platform" "$mode" > "$output/provenance.txt"
if [[ $mode = native ]]; then
  env -i PATH=/usr/bin:/bin NIDO_ACCEPT_INPUT="$output/inputs" NIDO_ACCEPT_OUTPUT="$output" \
    NIDO_ACCEPT_VERSION="$version" NIDO_ACCEPT_OLD_VERSION="$old_version" NIDO_ACCEPT_PLATFORM="$platform" \
    NIDO_ACCEPT_GENERAL_PROXY="${NIDO_ACCEPT_GENERAL_PROXY:-}" \
    /bin/bash "$output/inputs/harness/nido-public-release-inner.sh" 2>&1 | tee "$output/acceptance.log"
else
  # Only these copied public assets and harness files are exposed to the
  # ordinary container uid. Keep the independently verified source directories
  # private and leave native-mode input permissions unchanged.
  find "$output/inputs" -type d -exec chmod 0755 {} +
  find "$output/inputs" -type f -exec chmod 0644 {} +
  mkdir "$output/container-evidence"
  chmod 0777 "$output/container-evidence"
  # Read-only root and inputs, ordinary uid, private writable execution tmpfs;
  # no daemon socket, caller HOME, host directories or GPU devices are mounted.
  docker run --rm --pull never --platform linux/amd64 --read-only --cap-drop ALL \
    --security-opt no-new-privileges --user 10001:10001 --pids-limit 128 --memory 1g \
    --tmpfs /case:rw,exec,nosuid,nodev,mode=1777,size=768m \
    --mount "type=bind,source=$output/inputs,target=/inputs,readonly" \
    --mount "type=bind,source=$output/container-evidence,target=/evidence" \
    -e NIDO_ACCEPT_INPUT=/inputs -e NIDO_ACCEPT_OUTPUT=/evidence -e NIDO_ACCEPT_TEMP_BASE=/case \
    -e NIDO_ACCEPT_VERSION="$version" -e NIDO_ACCEPT_OLD_VERSION="$old_version" -e NIDO_ACCEPT_PLATFORM="$platform" \
    -e NIDO_ACCEPT_GENERAL_PROXY="${NIDO_ACCEPT_GENERAL_PROXY:-}" \
    "${NIDO_ACCEPT_DOCKER_IMAGE:-public.ecr.aws/docker/library/golang:1.23-bookworm}" \
    /bin/bash /inputs/harness/nido-public-release-inner.sh 2>&1 | tee "$output/acceptance.log"
fi
