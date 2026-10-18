#!/usr/bin/env bash
set -euo pipefail

# Both executables are built from this checkout. The second one is an inert
# fixture, never Mihomo: it supports only -v and -t and refuses startup.
binary="${1:?provide an absolute built Nido path}"
core="${2:?provide the built tests/fixtures/proxy-core executable}"
[[ "$binary" == /* && -f "$binary" && -x "$binary" && ! -L "$binary" ]]
[[ "$core" == /* && -f "$core" && -x "$core" && ! -L "$core" ]]
binary="$(cd "$(dirname "$binary")" && pwd -P)/$(basename "$binary")"
core="$(cd "$(dirname "$core")" && pwd -P)/$(basename "$core")"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-quickstart-cli.XXXXXX")"
scratch="$(cd "$scratch" && pwd -P)"
trap 'rm -rf -- "$scratch"' EXIT
mkdir "$scratch/home" "$scratch/tmp" "$scratch/forbidden"

env -i HOME="$scratch/home" TMPDIR="$scratch/tmp" PATH="$scratch/forbidden:/usr/bin:/bin" \
  LANG=C LC_ALL=C TZ=UTC GORACE=halt_on_error=1 \
  GIT_CONFIG_GLOBAL="$scratch/home/.gitconfig" GIT_CONFIG_NOSYSTEM=1 \
  NIDO_FIXTURE_FORBIDDEN_LOG="$scratch/forbidden.log" \
  /bin/bash -euo pipefail -s -- "$binary" "$core" "$scratch" <<'ISOLATED'
binary="$1"; core="$2"; scratch="$3"
root="$scratch/software root"
codex_home="$HOME/codex"
for tool in curl wget systemctl supervisorctl sudo mihomo gds git; do
  cat > "$scratch/forbidden/$tool" <<'FORBIDDEN'
#!/bin/sh
printf '%s\n' "$0" >> "$NIDO_FIXTURE_FORBIDDEN_LOG"
exit 98
FORBIDDEN
  chmod 0700 "$scratch/forbidden/$tool"
done
mkdir -p "$codex_home/sessions" "$HOME/.config/mihomo"
printf 'KEEP=literal\n' > "$codex_home/.env"
printf 'AUTH-SENTINEL\n' > "$codex_home/auth.json"
printf 'DB-SENTINEL\n' > "$codex_home/state.sqlite"
printf 'SESSION-SENTINEL\n' > "$codex_home/sessions/one.jsonl"
printf 'EXTERNAL-PROXY\n' > "$HOME/.config/mihomo/config.yaml"
printf '# SHELL-SENTINEL\n' > "$HOME/.bashrc"
printf '[user]\n name = Isolation Fixture\n' > "$HOME/.gitconfig"
cp -R "$HOME" "$scratch/before"
"$binary" --root "$root" init --yes >/dev/null
nido() { "$binary" --root "$root" "$@"; }
nido codex setup --home "$codex_home" --yes >/dev/null

# Install + full quick start through the actual CLI, with no -yes bypass.
printf '%s\n' \
  'https://provider.invalid/sub?token=visible-token' \
  'Daily' 17890 y 'Download' 17891 y \
  'ss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode' \
  17892 'https://isp-check.invalid/check' y 'Relay' |
  nido proxy install --artifact "$core" > "$scratch/install.log" 2> "$scratch/preview.log"
grep -Fq visible-token "$scratch/preview.log"
grep -Fq p-ass "$root/proxy/config.yaml"
grep -Fq 'MATCH,DIRECT' "$root/proxy/config.yaml"
grep -Fq 'type: url-test' "$root/proxy/config.yaml"
grep -Fq 'dialer-proxy: isp-relay' "$root/proxy/config.yaml"
nido proxy status --json > "$scratch/status.json"
grep -Fq '"listeners":false' "$scratch/status.json"
[[ ! -e "$root/proxy/process.json" ]]

# Listener names are editable; extra traditional ports are not binding choices.
sed 's/endpoint-proxy/user-renamed/g' "$root/proxy/config.yaml" > "$scratch/renamed.yaml"
mv "$scratch/renamed.yaml" "$root/proxy/config.yaml"
chmod 0600 "$root/proxy/config.yaml"
nido proxy endpoints --json > "$scratch/endpoints.json"
grep -Fq user-renamed "$scratch/endpoints.json"
nido proxy bind ai --endpoint user-renamed --yes >/dev/null
grep -Fq http://127.0.0.1:17890 "$codex_home/.env"
cmp "$HOME/.gitconfig" "$scratch/before/.gitconfig"
nido proxy bind daily --endpoint endpoint-download --yes >/dev/null
grep -Fq http://127.0.0.1:17891 "$HOME/.gitconfig"
cp "$HOME/.gitconfig" "$scratch/git-bound"
nido proxy bind --tool codex --proxy 'socks5h://user:p%40ss@127.0.0.1:17892' --yes >/dev/null
grep -Fq 'socks5h://user:p%40ss@127.0.0.1:17892' "$codex_home/.env"
cmp "$HOME/.gitconfig" "$scratch/git-bound"
cp "$root/config.toml" "$scratch/config-before-empty"
nido proxy bind download --yes --json > "$scratch/download.json"
grep -Fq no_targets "$scratch/download.json"
cmp "$root/config.toml" "$scratch/config-before-empty"

# No listeners: show no endpoint and still offer custom proxy configuration.
printf 'mixed-port: 17899\n' > "$root/proxy/config.yaml"
nido proxy endpoints --json > "$scratch/no-listeners.json"
grep -Fxq '[]' "$scratch/no-listeners.json"
printf '%s\n' custom 'http://127.0.0.1:17894' y |
  nido proxy bind --tool git > "$scratch/custom.log" 2> "$scratch/custom.err"
grep -Fq '没有可用的 listener' "$scratch/custom.err"
grep -Fq '自定义代理地址' "$scratch/custom.err"
grep -Fq http://127.0.0.1:17894 "$HOME/.gitconfig"
nido proxy bind --tool git --proxy off --yes >/dev/null
nido proxy bind --tool codex --proxy off --yes >/dev/null
cmp "$HOME/.gitconfig" "$scratch/before/.gitconfig"
cmp "$codex_home/.env" "$scratch/before/codex/.env"
cmp "$codex_home/auth.json" "$scratch/before/codex/auth.json"
cmp "$codex_home/state.sqlite" "$scratch/before/codex/state.sqlite"
diff -r "$codex_home/sessions" "$scratch/before/codex/sessions"
cmp "$HOME/.config/mihomo/config.yaml" "$scratch/before/.config/mihomo/config.yaml"
cmp "$HOME/.bashrc" "$scratch/before/.bashrc"
[[ ! -e "$NIDO_FIXTURE_FORBIDDEN_LOG" && ! -e "$root/proxy/process.json" ]]
printf 'PASS: Proxy quick start, node URI, editable listeners, category/tool/custom bindings and data preservation; inert core only.\n'
printf 'Proxy quick start CLI 隔离验证通过\n'
ISOLATED
