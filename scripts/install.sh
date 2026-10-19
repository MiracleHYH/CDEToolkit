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
yes=0
root_explicit=0
shell_setup=''
interactive=0
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
                --root) root=$2; root_explicit=1 ;;
                --github-proxy) github_proxy=$2; github_proxy_explicit=1 ;;
            esac
            shift 2 ;;
        --dry-run) dry_run=1; shift ;;
        --yes) yes=1; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
case "$(uname -s)" in Linux) os=linux ;; *) fail 'supported platform: Linux amd64 only' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; *) fail 'supported platform: Linux amd64 only' ;; esac

# Read choices from the controlling terminal, never from a curl|sh program's
# standard input. Script mode remains unchanged without a terminal.
ask() {
    printf '%s [%s]: ' "$1" "$2" >&3
    IFS= read -r answer <&3 || fail 'interaction cancelled; nothing installed'
    case "$answer" in q|Q|quit|exit) fail 'installation cancelled; nothing installed' ;; esac
    answer_supplied=0
    [ -z "$answer" ] || answer_supplied=1
    [ -n "$answer" ] || answer=$2
}
if [ "$yes" = 0 ] && [ "$dry_run" = 0 ] && ( : <> /dev/tty ) 2>/dev/null; then
    exec 3<> /dev/tty
    interactive=1
    printf 'Nido 安装：只安装管理工具，不安装或启动组件。输入 q 可退出。\n' >&3
    if [ "$root_explicit" = 0 ]; then
        ask '安装目录' "$root"
        root=$answer
    fi
    if [ "$github_proxy_explicit" = 0 ]; then
        ask 'GitHub 下载：auto / direct / HTTPS 前缀；回车本次用 auto，保留已保存策略' "$github_proxy"
        github_proxy=$answer
        if [ "$answer_supplied" = 1 ]; then github_proxy_explicit=1; fi
    fi
    detected_shell=${SHELL:-}
    detected_shell=${detected_shell##*/}
    profile=''
    case "$detected_shell" in
        bash) profile=$HOME/.bashrc ;;
        zsh) profile=${ZDOTDIR:-$HOME}/.zshrc ;;
    esac
    if [ -n "$profile" ]; then
        ask "是否将 nido 接入 $profile？y/N" N
        case "$answer" in y|Y) shell_setup=$detected_shell ;; esac
    else
        printf '未识别到 Bash/Zsh；安装后显示手动加载命令。\n' >&3
    fi
fi
case "$root" in '~') root=$HOME ;; '~/'*) root=$HOME/${root#\~/} ;; esac
while [ "$root" != / ] && [ "${root%/}" != "$root" ]; do root=${root%/}; done
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
if printf '%s' "$root" | LC_ALL=C grep -q '[[:cntrl:]:]'; then fail 'root cannot contain control characters or colons'; fi
if [ "$version" != latest ]; then
    stable_version "$version" || fail 'version must be X.Y.Z or latest'
fi

asset=nido-$os-$arch.tar.gz
base=https://github.com/MiracleHYH/CDEToolkit/releases
if [ "$version" = latest ]; then base=$base/latest/download; else base=$base/download/v$version; fi
if [ "$dry_run" = 1 ]; then
    printf 'Package: %s/%s\nInstall: %s\nGitHub transport: %s\n' "$base" "$asset" "$root" "$github_proxy"
    exit 0
fi
if [ "$interactive" = 1 ]; then
    printf '安装目录：%s\nGitHub 下载：%s\n' "$root" "$github_proxy" >&3
    if [ -n "$shell_setup" ]; then printf 'Shell 接入：%s\n' "$profile" >&3
    else printf 'Shell 接入：不修改配置文件\n' >&3; fi
    ask '开始安装？y/N' N
    case "$answer" in y|Y) ;; *) fail 'installation cancelled; nothing installed' ;; esac
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
actual_root=$("$temporary/nido" --root "$root" path) || fail 'cannot resolve installed Nido directory'
if [ -n "$shell_setup" ]; then
    if ! "$actual_root/bin/nido" --root "$actual_root" shell setup "$shell_setup" --yes; then
        printf 'nido: Nido 已安装，但 Shell 接入未完成；原配置保留，请使用下方手动加载命令。\n' >&2
    fi
fi
printf 'Installed nido %s; no components or services were started.\n' "$installed_version"
printf 'Installation directory: %s\n' "$actual_root"
printf 'Activate in the current terminal (the installer cannot change its parent shell):\n'
quoted_environment=$(printf '%s' "$actual_root/env.sh" | sed "s/'/'\\\"'\\\"'/g")
printf ". '%s'\n" "$quoted_environment"
