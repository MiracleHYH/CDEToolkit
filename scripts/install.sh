#!/bin/sh
# Bootstrap only Nido. Components and external tools are never installed here.
set -eu

fail() { printf 'nido: %s\n' "$*" >&2; exit 1; }
stable_version() {
    # grep works one line at a time, so reject control characters separately.
    case "$1" in *'
'*) return 1 ;; esac
    if printf '%s' "$1" | LC_ALL=C grep -q '[[:cntrl:]]'; then return 1; fi
    printf '%s\n' "$1" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
}
usage() {
    printf '%s\n' 'Usage: sh install.sh [--version X.Y.Z] [--root DIR] [--github-proxy auto|direct|HTTPS_PREFIX] [--yes] [--dry-run]' \
        'NIDO_HOME selects the software directory (default ~/.nido).' \
        'NIDO_GITHUB_PROXY selects GitHub asset transport (default auto).'
}

version=${NIDO_VERSION:-latest}
root=${NIDO_HOME:-${HOME:?HOME is required}/.nido}
dry_run=0
github_proxy=auto
github_proxy_explicit=0
if [ "${NIDO_GITHUB_PROXY+x}" = x ]; then
    github_proxy=${NIDO_GITHUB_PROXY:-direct}
    github_proxy_explicit=1
fi
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--root|--github-proxy)
            [ "$#" -ge 2 ] || fail "$1 needs a value"
            case "$1" in
                --version) version=$2 ;;
                --root) root=$2 ;;
                --github-proxy) github_proxy=$2; github_proxy_explicit=1 ;;
            esac
            shift 2 ;;
        --dry-run) dry_run=1; shift ;;
        --yes) shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
# An accelerator receives a full public GitHub URL. Never interpret this as a
# general HTTP proxy or permit credentials, query strings or insecure schemes.
case "$github_proxy" in
    auto|direct) ;;
    https://*)
        while [ "${github_proxy%/}" != "$github_proxy" ]; do github_proxy=${github_proxy%/}; done
        case "$github_proxy" in *'@'*|*'?'*|*'#'*|*'\'*|*'
'*) fail 'GitHub prefix must be HTTPS without credentials, query or fragment' ;; esac
        if printf '%s' "$github_proxy" | LC_ALL=C grep -q '[[:space:][:cntrl:]]'; then fail 'invalid GitHub prefix'; fi
        authority=${github_proxy#https://}
        authority=${authority%%/*}
        printf '%s\n' "$authority" | LC_ALL=C grep -Eq '^([A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(:[0-9]+)?$' || fail 'invalid GitHub prefix host'
        port=''
        case "$authority" in
            \[*\]*) rest=${authority#*]}; if [ -n "$rest" ]; then port=${rest#:}; fi ;;
            *:*) port=${authority##*:} ;;
        esac
        if [ -n "$port" ]; then
            printf '%s\n' "$port" | awk 'length($0) <= 5 && $0 + 0 >= 1 && $0 + 0 <= 65535 { ok=1 } END { exit !ok }' || fail 'invalid GitHub prefix port'
        fi ;;
    *) fail 'GitHub transport must be auto, direct or an HTTPS prefix' ;;
esac
case "$root" in /*) ;; *) fail 'root must be an absolute path';; esac
[ "$root" != / ] || fail 'root cannot be /'
carriage_return=$(printf '\r')
case "$root" in *'
'*|*"$carriage_return"*) fail 'root cannot contain line breaks';; esac
if [ "$version" != latest ]; then
    stable_version "$version" || fail 'version must be X.Y.Z or latest'
fi
case "$(uname -s)" in Linux) os=linux ;; *) fail 'supported platform: Linux amd64 only' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; *) fail 'supported platform: Linux amd64 only' ;; esac

asset=nido-$os-$arch.tar.gz
base=https://github.com/MiracleHYH/CDEToolkit/releases
if [ "$version" = latest ]; then base=$base/latest/download; else base=$base/download/v$version; fi
if [ "$dry_run" = 1 ]; then
    printf 'Package: %s/%s\nInstall: %s\nGitHub transport: %s\n' "$base" "$asset" "$root" "$github_proxy"
    exit 0
fi
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
if command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum_tool=shasum
else fail 'sha256sum or shasum is required'; fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/nido-install.XXXXXX")
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
canonical_temporary=$(CDPATH= cd -- "$temporary" && pwd -P) || fail 'cannot resolve the installer temporary directory'
temporary=$canonical_temporary
fetch() {
    curl -q --globoff --http1.1 --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --tlsv1.2 --connect-timeout "$3" --max-time "$4" --max-filesize 268435456 \
        --output "$2" "$1"
}
download() {
    case "$github_proxy" in
        direct) fetch "$1" "$2" 10 180 && return 0 ;;
        auto)
            fetch "$1" "$2" 4 180 && return 0
            # Small fixed fallback list; each request is bounded and starts a
            # fresh output. No mirror affects Codex or arbitrary network calls.
            for prefix in https://gh-proxy.org https://ghfast.top; do
                printf 'nido: GitHub unavailable; trying %s\n' "$prefix" >&2
                fetch "$prefix/$1" "$2" 5 90 && return 0
            done ;;
        *) fetch "$github_proxy/$1" "$2" 10 180 && return 0 ;;
    esac
    fail 'download failed; check GitHub transport and whether a Nido Release exists'
}
download "$base/$asset" "$temporary/$asset"
download "$base/$asset.sha256" "$temporary/checksum"
expected=$(awk -v name="$asset" 'NR == 1 && NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == name { value=$1; next } { bad=1 } END { if (NR != 1 || bad || value == "") exit 1; print value }' "$temporary/checksum") || fail 'invalid checksum file'
if [ "$checksum_tool" = sha256sum ]; then actual=$(sha256sum "$temporary/$asset")
else actual=$(shasum -a 256 "$temporary/$asset"); fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing installed'
# Keep the archive list bounded and check tar's exit status independently.
# Command substitution inside [ ... ] would otherwise discard a tar failure.
if ! (
    ulimit -f 1 || exit 1
    tar -tzf "$temporary/$asset"
) > "$temporary/members"; then
    fail 'cannot list release archive; nothing installed'
fi
[ "$(wc -c < "$temporary/members")" -eq 5 ] && [ "$(cat "$temporary/members")" = nido ] || fail 'release must contain only nido'
# Stream one member instead of extracting archive paths into the filesystem.
# Bound only the extraction child, never the caller or the installed command.
# POSIX shells use 512-byte blocks; bash variants may use 1024-byte blocks.
if ! (
    ulimit -f 524288 || exit 1
    tar -xOzf "$temporary/$asset" nido
) > "$temporary/nido"; then
    fail 'release extraction failed or exceeded its file-size limit; nothing installed'
fi
[ -s "$temporary/nido" ] || fail 'empty executable'
chmod 0755 "$temporary/nido"
reported=$("$temporary/nido" version)
case "$reported" in 'nido '*) installed_version=${reported#nido } ;; *) fail 'not a Nido executable' ;; esac
stable_version "$installed_version" || fail 'not a stable release executable'
if [ "$version" != latest ] && [ "$installed_version" != "$version" ]; then fail 'release version mismatch'; fi

# Go owns the small installation boundary and refuses unowned existing entries.
# This never installs components, alters external Codex or edits shell profiles.
if [ "$github_proxy_explicit" = 1 ]; then
    "$temporary/nido" --root "$root" init --yes --github-proxy "$github_proxy"
else
    # Existing preferences remain owned by Nido, not a shell TOML parser.
    "$temporary/nido" --root "$root" init --yes
fi
printf 'Installed nido %s; no components or services were started.\n' "$installed_version"
printf 'Load the generated environment file shown above, or add its source command to your shell profile.\n'
