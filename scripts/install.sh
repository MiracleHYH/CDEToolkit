#!/bin/sh
# Bootstrap only Nido. Components and external tools are never installed here.
set -eu

fail() { printf 'nido: %s\n' "$*" >&2; exit 1; }
usage() {
    printf '%s\n' 'Usage: sh install.sh [--version X.Y.Z] [--root DIR] [--yes] [--dry-run]' \
        'NIDO_HOME selects the software directory (default ~/.nido).'
}

version=${NIDO_VERSION:-latest}
root=${NIDO_HOME:-${HOME:?HOME is required}/.nido}
dry_run=0
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--root)
            [ "$#" -ge 2 ] || fail "$1 needs a value"
            case "$1" in --version) version=$2 ;; --root) root=$2 ;; esac
            shift 2 ;;
        --dry-run) dry_run=1; shift ;;
        --yes) shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
case "$root" in /*) ;; *) fail 'root must be an absolute path';; esac
[ "$root" != / ] || fail 'root cannot be /'
carriage_return=$(printf '\r')
case "$root" in *'
'*|*"$carriage_return"*) fail 'root cannot contain line breaks';; esac
if [ "$version" != latest ]; then
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'version must be X.Y.Z or latest'
fi
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) fail 'supported systems: Linux and macOS' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail 'supported architectures: amd64 and arm64' ;; esac

asset=nido-$os-$arch.tar.gz
base=https://github.com/MiracleHYH/CDEToolkit/releases
if [ "$version" = latest ]; then base=$base/latest/download; else base=$base/download/v$version; fi
if [ "$dry_run" = 1 ]; then
    printf 'Package: %s/%s\nInstall: %s\n' "$base" "$asset" "$root"
    exit 0
fi
command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
if command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum_tool=shasum
else fail 'sha256sum or shasum is required'; fi

temporary=$(mktemp -d "${TMPDIR:-/tmp}/nido-install.XXXXXX")
temporary=$(CDPATH= cd -- "$temporary" && pwd -P)
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
download() {
    curl -q --http1.1 --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --tlsv1.2 --retry 2 --connect-timeout 15 --max-time 300 --max-filesize 268435456 \
        --output "$2" "$1" || fail 'download failed; check network access and whether a Nido Release exists'
}
download "$base/$asset" "$temporary/$asset"
download "$base/$asset.sha256" "$temporary/checksum"
expected=$(awk -v name="$asset" 'NR == 1 && NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == name { value=$1; next } { bad=1 } END { if (NR != 1 || bad || value == "") exit 1; print value }' "$temporary/checksum") || fail 'invalid checksum file'
if [ "$checksum_tool" = sha256sum ]; then actual=$(sha256sum "$temporary/$asset")
else actual=$(shasum -a 256 "$temporary/$asset"); fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'SHA-256 mismatch; nothing installed'
[ "$(tar -tzf "$temporary/$asset")" = nido ] || fail 'release must contain only nido'
# Stream one member instead of extracting archive paths into the filesystem.
tar -xOzf "$temporary/$asset" nido > "$temporary/nido"
[ -s "$temporary/nido" ] || fail 'empty executable'
chmod 0755 "$temporary/nido"
reported=$("$temporary/nido" version)
case "$reported" in 'nido '*) installed_version=${reported#nido } ;; *) fail 'not a Nido executable' ;; esac
printf '%s\n' "$installed_version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail 'not a stable release executable'
if [ "$version" != latest ] && [ "$installed_version" != "$version" ]; then fail 'release version mismatch'; fi

# Go owns the small installation boundary and refuses unowned existing entries.
# This never installs components, alters external Codex or edits shell profiles.
"$temporary/nido" --root "$root" init --yes
printf 'Installed nido %s; no components or services were started.\n' "$installed_version"
printf 'Load the generated environment file shown above, or add its source command to your shell profile.\n'
