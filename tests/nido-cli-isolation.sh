#!/usr/bin/env bash
set -euo pipefail
# This is an automation contract. Real keyboard interaction is covered by PTY
# suites; never feed production menus numbered lines or y/N answers here.
exec </dev/null

NIDO_TEST_BINARY="${1:?provide the built nido binary}"
[[ "$NIDO_TEST_BINARY" = /* && -x "$NIDO_TEST_BINARY" ]]
NIDO_TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/nido-cli-test.XXXXXX")"
NIDO_TEST_ROOT="$(cd "$NIDO_TEST_ROOT" && pwd -P)"
trap 'rm -rf -- "$NIDO_TEST_ROOT"' EXIT
export HOME="$NIDO_TEST_ROOT/user" PATH="$NIDO_TEST_ROOT/forbidden:/usr/bin:/bin" TMPDIR="$NIDO_TEST_ROOT/tmp"
unset NIDO_HOME CDET_PREFIX CDET_STORE CDET_HOME STK_HOME CODEX_HOME CODEX_SQLITE_HOME CODEX_INSTALL_DIR XDG_CONFIG_HOME
unset NIDO_GITHUB_PROXY NIDO_NETWORK_MODE NIDO_PROXY NIDO_VERSION
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
unset CONDARC CONDA_EXE CONDA_ENVS_PATH CONDA_PKGS_DIRS CONDA_CHANNELS CONDA_DEFAULT_CHANNELS CONDA_CUSTOM_CHANNELS CONDA_CUSTOM_MULTICHANNELS CONDA_CHANNEL_ALIAS
unset PIP_CONFIG_FILE PIP_INDEX_URL PIP_EXTRA_INDEX_URL PIP_NO_INDEX PIP_CACHE_DIR PIP_NO_CACHE_DIR
export NIDO_FIXTURE_FORBIDDEN_LOG="$NIDO_TEST_ROOT/forbidden-calls"
root="$NIDO_TEST_ROOT/software path/nido"
codex_home="$HOME/.codex"
mkdir -p "$codex_home/sessions" "$HOME/.config/mihomo" "$NIDO_TEST_ROOT/forbidden" "$TMPDIR"
for tool in curl systemctl supervisorctl sudo codex mihomo gds conda pip python python3; do
  cat > "$NIDO_TEST_ROOT/forbidden/$tool" <<'FIXTURE'
#!/bin/sh
printf 'forbidden\n' >> "$NIDO_FIXTURE_FORBIDDEN_LOG"
exit 98
FIXTURE
  chmod 0755 "$NIDO_TEST_ROOT/forbidden/$tool"
done
printf 'AUTH-SENTINEL\n' > "$codex_home/auth.json"
printf 'SESSION-SENTINEL\n' > "$codex_home/sessions/one.jsonl"
printf 'DATABASE-SENTINEL\n' > "$codex_home/state.sqlite"
printf 'model = "preserved"\n' > "$codex_home/config.toml"
printf 'HTTPS_PROXY=http://user-proxy.invalid:7890\nKEEP=value\n' > "$codex_home/.env"
printf 'secret: EXTERNAL-PROXY-SENTINEL\n' > "$HOME/.config/mihomo/config.yaml"
printf '# USER-SHELL-SENTINEL\n' > "$HOME/.bashrc"
printf '# USER-ZSH-SENTINEL\n' > "$HOME/.zshrc"
cp -R "$codex_home" "$NIDO_TEST_ROOT/codex.before"
cp "$HOME/.config/mihomo/config.yaml" "$NIDO_TEST_ROOT/proxy.before"
cp "$HOME/.bashrc" "$NIDO_TEST_ROOT/bash.before"
cp "$HOME/.zshrc" "$NIDO_TEST_ROOT/zsh.before"

"$NIDO_TEST_BINARY" version > "$NIDO_TEST_ROOT/version.log"
/usr/bin/grep -Eq '^nido [0-9]+\.[0-9]+\.[0-9]+$' "$NIDO_TEST_ROOT/version.log"
"$NIDO_TEST_BINARY" --root "$root" status --json > "$NIDO_TEST_ROOT/status-before.json"
[[ ! -e "$root" ]]
"$NIDO_TEST_BINARY" --root "$root" init --yes > "$NIDO_TEST_ROOT/init.log"
[[ -x "$root/bin/nido" && -f "$root/config.toml" && -f "$root/env.sh" ]]
[[ ! -e "$root/bin/codex" && ! -e "$root/codex" && ! -e "$root/proxy" && ! -e "$root/gds" ]]
cp "$root/config.toml" "$NIDO_TEST_ROOT/config.before"
"$root/bin/nido" --root "$root" init --yes > "$NIDO_TEST_ROOT/repeat.log"
cmp "$root/config.toml" "$NIDO_TEST_ROOT/config.before"
"$root/bin/nido" --root "$root" status --json > "$NIDO_TEST_ROOT/status-after.json"
cmp "$NIDO_TEST_ROOT/status-before.json" "$NIDO_TEST_ROOT/status-after.json"
for shell in bash zsh; do
  "$root/bin/nido" --root "$root" env "$shell" > "$NIDO_TEST_ROOT/env-$shell.sh"
  /bin/sh -n "$NIDO_TEST_ROOT/env-$shell.sh"
  if /usr/bin/grep -Eq 'CODEX_HOME|CODEX_SQLITE_HOME|HTTP_PROXY|HTTPS_PROXY|ALL_PROXY' "$NIDO_TEST_ROOT/env-$shell.sh"; then
    printf 'unconfigured external environment was exported\n' >&2
    exit 1
  fi
  env -i HOME="$HOME" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; command -v nido' fixture "$NIDO_TEST_ROOT/env-$shell.sh" > "$NIDO_TEST_ROOT/path-$shell.log"
  /usr/bin/grep -Fxq "$root/bin/nido" "$NIDO_TEST_ROOT/path-$shell.log"
done
# The generated environment is an explicit opt-in, never an automatic profile edit.
env -i HOME="$HOME" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; command -v nido' fixture "$root/env.sh" > "$NIDO_TEST_ROOT/path-file.log"
/usr/bin/grep -Fxq "$root/bin/nido" "$NIDO_TEST_ROOT/path-file.log"

# Exercise external integration only with fixture executables and a mock
# official installer. Never download or execute real upstream code.
rm "$NIDO_TEST_ROOT/forbidden/codex"
external="$NIDO_TEST_ROOT/external codex"
mkdir -p "$external/packages/standalone/fixture/bin" "$HOME/.local/bin"
cat > "$external/packages/standalone/fixture/bin/codex" <<'FIXTURE'
#!/bin/sh
[ "$#" = 1 ] && [ "$1" = --version ] || exit 96
printf 'codex-cli 1.0.0\n'
FIXTURE
chmod 0755 "$external/packages/standalone/fixture/bin/codex"
ln -s "$external/packages/standalone/fixture/bin/codex" "$HOME/.local/bin/codex"
printf 'KEEP=literal' > "$external/.env"
printf 'EXTERNAL-AUTH\n' > "$external/auth.json"
printf 'EXTERNAL-DB\n' > "$external/state.sqlite"
mkdir -p "$external/sessions"
printf 'EXTERNAL-SESSION\n' > "$external/sessions/one.jsonl"
cp -R "$external" "$NIDO_TEST_ROOT/external.before"
export PATH="$NIDO_TEST_ROOT/forbidden:$HOME/.local/bin:/usr/bin:/bin"
"$root/bin/nido" --root "$root" codex setup --home "$external" --yes > "$NIDO_TEST_ROOT/associate.log"
diff -r "$NIDO_TEST_ROOT/external.before" "$external"
"$root/bin/nido" --root "$root" env bash > "$NIDO_TEST_ROOT/linked-env.sh"
env -i HOME="$HOME" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; printf "%s\n" "$CODEX_HOME"' fixture "$NIDO_TEST_ROOT/linked-env.sh" > "$NIDO_TEST_ROOT/linked-home.log"
/usr/bin/grep -Fxq "$external" "$NIDO_TEST_ROOT/linked-home.log"
[[ ! -e "$root/bin/codex" && ! -e "$root/codex" ]]
"$root/bin/nido" --root "$root" codex proxy http://127.0.0.1:17890 --yes > "$NIDO_TEST_ROOT/proxy-set.log"
/usr/bin/grep -Fq 'http://127.0.0.1:17890' "$external/.env"
"$root/bin/nido" --root "$root" codex proxy off --yes > "$NIDO_TEST_ROOT/proxy-off.log"
cmp "$external/.env" "$NIDO_TEST_ROOT/external.before/.env"
"$root/bin/nido" --root "$root" codex proxy http://127.0.0.1:17890 --yes > "$NIDO_TEST_ROOT/proxy-repeat.log"

export NIDO_FIXTURE_USER_HOME="$HOME" NIDO_FIXTURE_COMPONENT="$NIDO_TEST_ROOT/fixture-codex"
export NIDO_FIXTURE_CURL_LOG="$NIDO_TEST_ROOT/curl-calls"
cat > "$NIDO_FIXTURE_COMPONENT" <<'FIXTURE'
#!/bin/sh
[ "$#" = 1 ] && [ "$1" = --version ] || exit 96
printf 'codex-cli 2.0.0\n'
FIXTURE
chmod 0755 "$NIDO_FIXTURE_COMPONENT"
cat > "$NIDO_TEST_ROOT/forbidden/curl" <<'FIXTURE'
#!/bin/sh
set -eu
last=
for arg do last=$arg; done
[ "$HOME" != "$NIDO_FIXTURE_USER_HOME" ]
[ "$http_proxy" = http://127.0.0.1:1 ]
[ "$https_proxy" = "$http_proxy" ] && [ "$all_proxy" = "$http_proxy" ]
[ "$HTTP_PROXY" = "$http_proxy" ] && [ "$HTTPS_PROXY" = "$http_proxy" ] && [ "$ALL_PROXY" = "$http_proxy" ]
[ "$no_proxy" = localhost,127.0.0.1,::1 ] && [ "$NO_PROXY" = "$no_proxy" ]
printf '%s\n' "$last" >> "$NIDO_FIXTURE_CURL_LOG"
case "$last" in
  https://chatgpt.com/codex/install.sh)
    /bin/cat <<'INSTALLER'
set -eu
[ "$CODEX_NON_INTERACTIVE" = 1 ]
[ "$HOME" != "$NIDO_FIXTURE_USER_HOME" ]
[ "$CODEX_INSTALL_DIR" = "$NIDO_FIXTURE_USER_HOME/.local/bin" ]
[ "$HTTP_PROXY" = http://127.0.0.1:1 ]
[ "$NO_PROXY" = localhost,127.0.0.1,::1 ]
[ -d "$CODEX_HOME" ]
/bin/mkdir -p "$CODEX_INSTALL_DIR"
# Remove only our fixture symlink: a real installation remains upstream-owned.
/bin/rm -f "$CODEX_INSTALL_DIR/codex"
fixture_package="$CODEX_HOME/packages/standalone/updated-fixture/bin"
/bin/mkdir -p "$fixture_package"
/bin/cp "$NIDO_FIXTURE_COMPONENT" "$fixture_package/codex"
/bin/chmod 0755 "$fixture_package/codex"
/bin/ln -s "$fixture_package/codex" "$CODEX_INSTALL_DIR/codex"
printf '# mock installer shell edit\n' >> "$HOME/.bashrc"
INSTALLER
    ;;
  *) exit 97 ;;
esac
FIXTURE
chmod 0755 "$NIDO_TEST_ROOT/forbidden/curl"
"$root/bin/nido" --root "$root" codex update --dry-run > "$NIDO_TEST_ROOT/update-preview.log"
[[ ! -e "$NIDO_FIXTURE_CURL_LOG" ]]
# Codex commands reference Nido Proxy, independently of GitHub Accelerate
# and the Codex runtime .env block. The mock downloader never opens a socket.
"$root/bin/nido" --root "$root" download proxy http://127.0.0.1:1 --yes > "$NIDO_TEST_ROOT/nido-proxy.log"
"$root/bin/nido" --root "$root" download set https://accelerator.invalid --yes > "$NIDO_TEST_ROOT/accelerate.log"
cp "$external/.env" "$NIDO_TEST_ROOT/runtime-proxy.before"
"$root/bin/nido" --root "$root" codex update --yes > "$NIDO_TEST_ROOT/update.log"
cmp "$external/.env" "$NIDO_TEST_ROOT/runtime-proxy.before"
/usr/bin/grep -Fq 'https://chatgpt.com/codex/install.sh' "$NIDO_FIXTURE_CURL_LOG"
[[ $(wc -l < "$NIDO_FIXTURE_CURL_LOG") -eq 1 ]]
"$HOME/.local/bin/codex" --version > "$NIDO_TEST_ROOT/updated-version.log"
/usr/bin/grep -Fxq 'codex-cli 2.0.0' "$NIDO_TEST_ROOT/updated-version.log"
"$root/bin/nido" --root "$root" codex detach --yes > "$NIDO_TEST_ROOT/detach.log"
# The upstream fixture may add program packages; application data and the
# original package remain untouched by Nido's integration and detachment.
for kept in .env auth.json state.sqlite; do
  cmp "$NIDO_TEST_ROOT/external.before/$kept" "$external/$kept"
done
diff -r "$NIDO_TEST_ROOT/external.before/sessions" "$external/sessions"
diff -r "$NIDO_TEST_ROOT/external.before/packages/standalone/fixture" "$external/packages/standalone/fixture"
[[ -x "$HOME/.local/bin/codex" ]]

# A separate new HOME is installed externally; Nido never creates an internal
# Codex data directory. Remove only the fixture command to model a fresh tool.
rm "$HOME/.local/bin/codex"
fresh="$NIDO_TEST_ROOT/new external codex"
"$root/bin/nido" --root "$root" codex install --home "$fresh" --proxy http://127.0.0.1:1 --yes > "$NIDO_TEST_ROOT/install.log"
[[ $(wc -l < "$NIDO_FIXTURE_CURL_LOG") -eq 2 ]]
[[ -d "$fresh" && -x "$HOME/.local/bin/codex" && ! -e "$root/codex" && ! -e "$root/bin/codex" ]]
"$root/bin/nido" --root "$root" codex detach --yes > "$NIDO_TEST_ROOT/detach-new.log"
[[ -d "$fresh" && -x "$HOME/.local/bin/codex" ]]
"$root/bin/nido" --root "$root" env bash > "$NIDO_TEST_ROOT/detached-env.sh"
! /usr/bin/grep -q CODEX_HOME "$NIDO_TEST_ROOT/detached-env.sh"
diff -r "$NIDO_TEST_ROOT/codex.before" "$codex_home"
cmp "$HOME/.config/mihomo/config.yaml" "$NIDO_TEST_ROOT/proxy.before"
cmp "$HOME/.bashrc" "$NIDO_TEST_ROOT/bash.before"
cmp "$HOME/.zshrc" "$NIDO_TEST_ROOT/zsh.before"
[[ ! -e "$NIDO_FIXTURE_FORBIDDEN_LOG" && ! -e "$HOME/.cdet" && ! -e "$HOME/.cdet-store" ]]
printf 'Nido 单目录 CLI 隔离验证通过\n'
