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
tests_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
version=${NIDO_ACCEPT_VERSION:-$(cat "$tests_dir/../VERSION")}
old_version=${NIDO_ACCEPT_OLD_VERSION:-0.8.0}
accelerator=${NIDO_ACCEPT_GITHUB_ACCELERATOR:-https://gh-proxy.org}
install_version_mode=${NIDO_ACCEPT_INSTALL_VERSION_MODE:-latest}
transport=${NIDO_ACCEPT_TRANSPORT:-both}
metadata_auth=${NIDO_ACCEPT_METADATA_AUTH:-public}
mode=${NIDO_ACCEPT_MODE:-native}
platform=${NIDO_ACCEPT_PLATFORM:-}
stable='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ $version =~ $stable && $old_version =~ $stable && $version != "$old_version" ]] || fail 'invalid release versions'
case $accelerator in https://gh-proxy.org|https://ghfast.top) ;; *) fail 'unsupported public acceptance accelerator' ;; esac
case $install_version_mode in latest|tagged) ;; *) fail 'install version mode must be latest or tagged' ;; esac
case $transport in both|direct|accelerated) ;; *) fail 'transport must be both, direct or accelerated' ;; esac
case $metadata_auth in public|gh) ;; *) fail 'metadata auth must be public or gh' ;; esac
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
for command in tar cmp awk curl jq; do command -v "$command" >/dev/null || fail "missing existing command: $command"; done
[[ $metadata_auth != gh ]] || command -v gh >/dev/null || fail 'gh is required for authenticated host metadata reads'
sha256_file() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
mkdir "$output"
output=$(cd "$output" && pwd -P)
mkdir -p "$output/inputs/new" "$output/inputs/old" "$output/inputs/harness"
for name in "$asset" "$asset.sha256"; do
  cp "$input/$name" "$input/$name.sigstore.json" "$output/inputs/new/"
  cp "$old_input/$name" "$old_input/$name.sigstore.json" "$output/inputs/old/"
done
cp "$input/install.sh" "$input/install.sh.sigstore.json" "$output/inputs/new/"
cp "$tests_dir/nido-public-release-isolation.sh" "$tests_dir/nido-public-release-inner.sh" "$tests_dir/conda-cli-isolation.sh" "$output/inputs/harness/"
for name in nido-public-release-isolation.sh nido-public-release-inner.sh conda-cli-isolation.sh; do
  printf '%s  %s\n' "$(sha256_file "$output/inputs/harness/$name")" "$name"
done > "$output/harness.sha256"
# Recheck the current official API against the already signed input bytes
# before any released program runs. This is an additional input freshness gate;
# the client's real update path still uses its own API digest and SHA-256 checks.
for kind in new old; do
  selected_version=$version
  [[ $kind = new ]] || selected_version=$old_version
  metadata="$output/$kind-public-api-before.json"
  curl_args=(-q --http1.1 --fail --silent --show-error --location --proto '=https' --proto-redir '=https'
    --connect-timeout 15 --max-time 120 --max-filesize 4194304)
  [[ -z ${NIDO_ACCEPT_GENERAL_PROXY:-} ]] || curl_args+=(--proxy "$NIDO_ACCEPT_GENERAL_PROXY")
  if [[ $metadata_auth = gh ]]; then
    gh api --hostname github.com "repos/MiracleHYH/CDEToolkit/releases/tags/v$selected_version" > "$metadata"
  else
    curl "${curl_args[@]}" --output "$metadata" "https://api.github.com/repos/MiracleHYH/CDEToolkit/releases/tags/v$selected_version"
  fi
  jq -e --arg tag "v$selected_version" '.tag_name == $tag and .draft == false and .prerelease == false' "$metadata" >/dev/null
  names=("$asset" "$asset.sha256")
  [[ $kind != new ]] || names+=(install.sh)
  for name in "${names[@]}"; do
    selected="$output/inputs/$kind/$name"
    jq -e --arg name "$name" --arg digest "sha256:$(sha256_file "$selected")" --argjson size "$(wc -c < "$selected")" \
      --arg url "https://github.com/MiracleHYH/CDEToolkit/releases/download/v$selected_version/$name" \
      '[.assets[] | select(.name == $name)] | length == 1 and .[0].digest == $digest and .[0].size == $size and .[0].browser_download_url == $url' "$metadata" >/dev/null
  done
done
printf 'new_version=%s\nold_version=%s\nplatform=%s\nmode=%s\nhost_uname=%s\naccelerator=%s\ninstall_version_mode=%s\nselected_transport=%s\nhost_metadata_auth=%s\nclient_update_auth=anonymous-native\nold_binary=actual-preverified-public-release\nofficial_api_inputs_match_before_execution=true\n' \
  "$version" "$old_version" "$platform" "$mode" "$(uname -srm)" "$accelerator" "$install_version_mode" "$transport" "$metadata_auth" > "$output/provenance.txt"
if [[ $mode = native ]]; then
  env -i PATH=/usr/bin:/bin NIDO_ACCEPT_INPUT="$output/inputs" NIDO_ACCEPT_OUTPUT="$output" \
    NIDO_ACCEPT_VERSION="$version" NIDO_ACCEPT_OLD_VERSION="$old_version" NIDO_ACCEPT_PLATFORM="$platform" \
    NIDO_ACCEPT_GITHUB_ACCELERATOR="$accelerator" \
    NIDO_ACCEPT_INSTALL_VERSION_MODE="$install_version_mode" \
    NIDO_ACCEPT_TRANSPORT="$transport" \
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
    -e NIDO_ACCEPT_GITHUB_ACCELERATOR="$accelerator" \
    -e NIDO_ACCEPT_INSTALL_VERSION_MODE="$install_version_mode" \
    -e NIDO_ACCEPT_TRANSPORT="$transport" \
    -e NIDO_ACCEPT_GENERAL_PROXY="${NIDO_ACCEPT_GENERAL_PROXY:-}" \
    "${NIDO_ACCEPT_DOCKER_IMAGE:-public.ecr.aws/docker/library/golang:1.23-bookworm}" \
    /bin/bash /inputs/harness/nido-public-release-inner.sh 2>&1 | tee "$output/acceptance.log"
fi
