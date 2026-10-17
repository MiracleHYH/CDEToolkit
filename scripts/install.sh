#!/bin/sh
# Download only the CLI. Directory selection and all managed writes live in Go.
set -eu

fail() { printf 'cdet: %s\n' "$*" >&2; exit 1; }
usage() {
    printf '%s\n' 'Usage: sh install.sh [--version X.Y.Z] [--prefix DIR] [--store DIR] [--yes] [--dry-run]' \
        'Two destinations: CDET_PREFIX (default ~/.cdet), CDET_STORE (default ~/.cdet-store).'
}

version=${CDET_VERSION:-latest}
prefix=${CDET_PREFIX:-}
store=${CDET_STORE:-}
accept_defaults=0
dry_run=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--prefix|--store)
            [ "$#" -ge 2 ] || fail "$1 needs a value"
            case "$1" in --version) version=$2 ;; --prefix) prefix=$2 ;; --store) store=$2 ;; esac
            shift 2 ;;
        --dry-run) dry_run=1; shift ;;
        --yes) accept_defaults=1; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done

if [ "$version" != latest ]; then
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'version must be X.Y.Z or latest'
fi
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) fail 'supported systems: Linux and macOS' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail 'supported architectures: amd64 and arm64' ;; esac

asset=cdet-$os-$arch.tar.gz
base=https://github.com/MiracleHYH/CDEToolkit/releases
if [ "$version" = latest ]; then base=$base/latest/download; else base=$base/download/v$version; fi
if [ "$dry_run" = 1 ]; then
    printf 'Package: %s/%s\nInstall: %s\nComponents: %s\n' "$base" "$asset" "${prefix:-$HOME/.cdet}" "${store:-$HOME/.cdet-store}"
    exit 0
fi
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
if command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum_tool=shasum
else fail 'sha256sum or shasum is required'; fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/cdet-install.XXXXXX")
temporary=$(CDPATH= cd -- "$temporary" && pwd -P)
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --tlsv1.2 --retry 2 --connect-timeout 15 --max-time 300 --max-filesize 268435456 \
        --output "$2" "$1" || fail 'download failed; check network access and whether a Release exists'
}
download "$base/$asset" "$temporary/$asset"
download "$base/$asset.sha256" "$temporary/checksum"
expected=$(awk -v name="$asset" 'NR == 1 && NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == name { value=$1; next } { bad=1 } END { if (NR != 1 || bad || value == "") exit 1; print value }' "$temporary/checksum") || fail 'invalid checksum file'
if [ "$checksum_tool" = sha256sum ]; then actual=$(sha256sum "$temporary/$asset")
else actual=$(shasum -a 256 "$temporary/$asset"); fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing installed'
[ "$(tar -tzf "$temporary/$asset")" = cdet ] || fail 'release must contain only cdet'
# Stream one member instead of extracting archive paths into the filesystem.
tar -xOzf "$temporary/$asset" cdet > "$temporary/cdet"
[ -s "$temporary/cdet" ] || fail 'empty executable'
chmod 0755 "$temporary/cdet"
reported=$("$temporary/cdet" version)
case "$reported" in 'cdet '*) installed_version=${reported#cdet } ;; *) fail 'not a CDEToolkit executable' ;; esac
printf '%s\n' "$installed_version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'not a stable release executable'
if [ "$version" != latest ] && [ "$installed_version" != "$version" ]; then fail 'release version mismatch'; fi

# Go asks only for missing destinations using /dev/tty (also works with curl|sh).
# Noninteractive callers supply both roots or explicitly accept defaults.
set -- init
[ -z "$prefix" ] || set -- "$@" --prefix "$prefix"
[ -z "$store" ] || set -- "$@" --store "$store"
[ "$accept_defaults" = 0 ] || set -- "$@" --yes
"$temporary/cdet" "$@"
printf 'Installed cdet %s; no components or services were started.\n' "$installed_version"
