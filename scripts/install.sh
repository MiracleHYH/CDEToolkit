#!/bin/sh
# Release template: Go embeds the verified Linux amd64 executable below.
# This boundary only unpacks a temporary program; every installation decision
# belongs to the same Go TUI used by nido. No second download or Shell menu.
set -eu
fail() { printf 'nido: %s\n' "$*" >&2; exit 1; }
case "$(uname -s)/$(uname -m)" in Linux/x86_64|Linux/amd64) ;; *) fail 'supported platform: Linux amd64 only' ;; esac
release_version='@NIDO_VERSION@'
release_sha256='@NIDO_SHA256@'
release_size='@NIDO_SIZE@'
compressed_size='@NIDO_GZIP_SIZE@'
case "$release_version" in *@*) fail 'source template is not a release installer' ;; esac
for tool in base64 gzip mktemp wc; do command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"; done
if command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then checksum_tool=shasum
else fail 'sha256sum or shasum is required'; fi
temporary=$(mktemp -d "${TMPDIR:-/tmp}/nido-install.XXXXXX")
cleanup() { rm -rf -- "$temporary"; }
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
canonical_temporary=$(CDPATH= cd -- "$temporary" && pwd -P) || fail 'cannot resolve the installer temporary directory'
temporary=$canonical_temporary
# Limits apply only to decoding children, never to Go or the invoking shell.
if ! (
    ulimit -f 131072 || exit 1
    base64 -d <<'NIDO_EMBEDDED_GZIP'
@NIDO_PAYLOAD@
NIDO_EMBEDDED_GZIP
) > "$temporary/nido.gz"; then fail 'embedded payload is damaged or exceeds its size limit'; fi
[ "$(wc -c < "$temporary/nido.gz")" -eq "$compressed_size" ] || fail 'embedded payload size mismatch'
if ! (
    ulimit -f 262144 || exit 1
    gzip -dc "$temporary/nido.gz"
) > "$temporary/nido"; then fail 'embedded executable is damaged or exceeds its size limit'; fi
[ "$(wc -c < "$temporary/nido")" -eq "$release_size" ] || fail 'embedded executable size mismatch'
if [ "$checksum_tool" = sha256sum ]; then actual=$(sha256sum "$temporary/nido")
else actual=$(shasum -a 256 "$temporary/nido"); fi
actual=${actual%% *}
[ "$actual" = "$release_sha256" ] || fail 'embedded executable SHA-256 mismatch'
chmod 0755 "$temporary/nido"
[ "$("$temporary/nido" version)" = "nido $release_version" ] || fail 'embedded executable version mismatch'
# /dev/null prevents the child from consuming curl|sh source. The Go UI obtains
# its controlling terminal itself, and scripts use explicit --yes/--dry-run.
"$temporary/nido" bootstrap-install "$@" </dev/null
