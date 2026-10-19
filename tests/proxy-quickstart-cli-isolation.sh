#!/usr/bin/env bash
set -euo pipefail

# Current mode checks this checkout; legacy is explicitly selected only when
# verifying frozen releases predating the Nido API listener binding. The fourth
# argument independently selects legacy full-YAML previews for frozen releases;
# current source requires a private summary and an explicit final confirmation.
# The second executable is inert: only -v and -t, never Mihomo startup.
binary="${1:?provide an absolute built Nido path}"
core="${2:?provide the built tests/fixtures/proxy-core executable}"
binding_mode="${3:-current}"
quickstart_mode="${4:-current}"
[[ "$binding_mode" == current || "$binding_mode" == legacy ]]
[[ "$quickstart_mode" == current || "$quickstart_mode" == legacy ]]
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
  /bin/bash -euo pipefail -s -- "$binary" "$core" "$scratch" "$binding_mode" "$quickstart_mode" <<'ISOLATED'
binary="$1"; core="$2"; scratch="$3"; binding_mode="$4"; quickstart_mode="$5"
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

prompted_nido() {
  local output=$1 diagnostic=$2 command
  shift 2
  if [[ "$quickstart_mode" == current ]]; then
    command -v script >/dev/null || { printf 'current quick start requires existing util-linux script\n' >&2; return 2; }
    printf -v command '%q ' "$binary" --root "$root" "$@"
    printf -v command '%s > %q' "$command" "$output"
    # A real child PTY is required by current source. Input echo stays off so
    # credentials from fixtures cannot leak through the terminal transcript.
    script -q -e -E never -c "$command" /dev/null > "$diagnostic" 2>&1
  else
    nido "$@" > "$output" 2> "$diagnostic"
  fi
}

# Install + full quick start through the actual CLI, with no -yes bypass.
quickstart_input() {
  printf '%s\n' \
    'https://provider.invalid/sub?token=visible-token' \
    'Daily' 17890 y 'Download' 17891 y \
    'ss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode' \
    17892 'https://isp-check.invalid/check' y 'Relay'
  if [[ "$quickstart_mode" == current ]]; then printf 'y\n'; fi
}
quickstart_input |
  prompted_nido "$scratch/install.log" "$scratch/preview.log" proxy install --artifact "$core"
if [[ "$quickstart_mode" == current ]]; then
  grep -Fq 'Proxy 配置摘要' "$scratch/preview.log"
  grep -Fq '保存 Proxy 配置（保持停止）' "$scratch/preview.log"
  for marker in 17890 17891 17892; do grep -Fq "$marker" "$scratch/preview.log"; done
  for private in provider.invalid visible-token 'ss://' p-ass '192.0.2.1' isp-check.invalid; do
    ! grep -Fq "$private" "$scratch/preview.log"
    ! grep -Fq "$private" "$scratch/install.log"
  done
else
  grep -Fq visible-token "$scratch/preview.log"
  grep -Fq p-ass "$scratch/preview.log"
fi
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
cp "$root/config.toml" "$scratch/config-before-download"
cp "$codex_home/.env" "$scratch/codex-before-download"
if [[ "$binding_mode" == legacy ]]; then
  nido proxy bind download --yes --json > "$scratch/download.json"
  grep -Fq no_targets "$scratch/download.json"
  [[ ! -e "$root/download-proxy.toml" ]]
else
  nido proxy bind download --endpoint endpoint-download --yes --json > "$scratch/download.json"
  grep -Fq applied "$scratch/download.json"
  grep -Fq nido-api-proxy "$root/download-proxy.toml"
  grep -Fq endpoint-download "$root/download-proxy.toml"
  [[ "$(stat -c %a "$root/download-proxy.toml")" == 600 ]]
  nido download show --json > "$scratch/download-show.json"
  grep -Fq '"api_listener":"endpoint-download"' "$scratch/download-show.json"
  nido proxy bind download --proxy off --yes >/dev/null
  [[ -f "$root/download-proxy.toml" ]]
  ! grep -Fq endpoint-download "$root/download-proxy.toml"
fi
cmp "$root/config.toml" "$scratch/config-before-download"
cmp "$codex_home/.env" "$scratch/codex-before-download"
cmp "$HOME/.gitconfig" "$scratch/git-bound"

# No listeners: show no endpoint and still offer custom proxy configuration.
printf 'mixed-port: 17899\n' > "$root/proxy/config.yaml"
nido proxy endpoints --json > "$scratch/no-listeners.json"
grep -Fxq '[]' "$scratch/no-listeners.json"
printf '%s\n' custom 'http://127.0.0.1:17894' y |
  prompted_nido "$scratch/custom.log" "$scratch/custom.err" proxy bind --tool git
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
