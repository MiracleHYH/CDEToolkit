#!/usr/bin/env bash
set -euo pipefail

CDET_TEST_BINARY="${1:?provide the built cdet binary}"
[[ "$CDET_TEST_BINARY" = /* && -x "$CDET_TEST_BINARY" ]]
CDET_TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/cdet-cli-test.XXXXXX")"
CDET_TEST_ROOT="$(cd "$CDET_TEST_ROOT" && pwd -P)"
trap 'rm -rf -- "$CDET_TEST_ROOT"' EXIT
export HOME="$CDET_TEST_ROOT/user" PATH="$CDET_TEST_ROOT/forbidden:/usr/bin:/bin" TMPDIR="$CDET_TEST_ROOT/tmp"
unset CDET_PREFIX CDET_STORE CDET_HOME STK_HOME CODEX_HOME CODEX_SQLITE_HOME XDG_CONFIG_HOME
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
# Go metadata requests cannot leave the isolated test. A failed version
# lookup is displayed; program installation uses the fake curl below.
export HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1
export CDET_FIXTURE_CURL_LOG="$CDET_TEST_ROOT/curl-calls"
export CDET_FIXTURE_FORBIDDEN_LOG="$CDET_TEST_ROOT/forbidden-calls"
prefix="$CDET_TEST_ROOT/mount/cli"
store="$CDET_TEST_ROOT/mount/components"
codex_source="$HOME/.codex"
proxy_source="$HOME/.config/mihomo"
mkdir -p "$codex_source/packages/standalone/v1/bin" "$codex_source/bin" "$codex_source/sessions" "$proxy_source" "$CDET_TEST_ROOT/forbidden" "$TMPDIR"
# Only these tiny, locally compiled programs are installed and executed.
cat > "$CDET_TEST_ROOT/component.c" <<'FIXTURE'
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
int main(int argc, char **argv) {
  if (argc > 1 && !strcmp(argv[1], "--version")) {
    printf("codex-cli %d.0.0\n", CDET_FIXTURE_VERSION);
    return 0;
  }
  if (argc > 1 && !strcmp(argv[1], "-v")) {
    puts("Mihomo Meta v1.0.0 fixture");
    return 0;
  }
#if CDET_FIXTURE_VERSION == 2
  puts("fixture version two");
#elif CDET_FIXTURE_VERSION == 99
  fputs("forbidden component execution\n", stderr);
  return 99;
#else
  const char *home = getenv("CODEX_HOME");
  printf("CODEX_HOME=%s\nARG=%s\n", home ? home : "", argc > 1 ? argv[1] : "");
#endif
  return 0;
}
FIXTURE
cc -DCDET_FIXTURE_VERSION=1 "$CDET_TEST_ROOT/component.c" -o "$codex_source/packages/standalone/v1/bin/codex"
ln -s "$codex_source/packages/standalone/v1/bin/codex" "$codex_source/bin/codex"
cc -DCDET_FIXTURE_VERSION=99 "$CDET_TEST_ROOT/component.c" -o "$proxy_source/mihomo"
export CDET_FIXTURE_CODEX="$CDET_TEST_ROOT/official-fixture-codex"
cp "$codex_source/packages/standalone/v1/bin/codex" "$CDET_FIXTURE_CODEX"
# Never fetch or execute the real upstream installer. This curl only returns
# a local script that copies the fixture to the explicit managed target.
cat > "$CDET_TEST_ROOT/forbidden/curl" <<'FIXTURE'
#!/bin/sh
set -eu
last=
for arg do last=$arg; done
[ "$last" = https://chatgpt.com/codex/install.sh ] || exit 97
printf 'fixture curl\n' >> "$CDET_FIXTURE_CURL_LOG"
/bin/cat <<'INSTALLER'
set -eu
[ "$CODEX_NON_INTERACTIVE" = 1 ]
[ "$HOME" != "$CODEX_HOME" ]
/bin/mkdir -p "$CODEX_INSTALL_DIR"
/bin/cp "$CDET_FIXTURE_CODEX" "$CODEX_INSTALL_DIR/codex"
/bin/chmod 0755 "$CODEX_INSTALL_DIR/codex"
# Deliberately emulate upstream Shell edits to prove HOME is isolated.
printf '# fake installer profile\n' >> "$HOME/.bashrc"
INSTALLER
FIXTURE
chmod 0755 "$CDET_TEST_ROOT/forbidden/curl"
for tool in systemctl supervisorctl sudo gds; do
  cat > "$CDET_TEST_ROOT/forbidden/$tool" <<'FIXTURE'
#!/bin/sh
printf 'forbidden\n' >> "$CDET_FIXTURE_FORBIDDEN_LOG"
exit 98
FIXTURE
  chmod 0755 "$CDET_TEST_ROOT/forbidden/$tool"
done
printf 'AUTH-SENTINEL\n' > "$codex_source/auth.json"
printf 'SESSION-SENTINEL\n' > "$codex_source/sessions/one.jsonl"
printf 'DATABASE-SENTINEL\n' > "$codex_source/state.sqlite"
printf 'WAL-SENTINEL\n' > "$codex_source/state.sqlite-wal"
printf 'model = "preserved"\n' > "$codex_source/config.toml"
printf 'mixed-port: 7890\nsecret: PROXY-SECRET-SENTINEL\n' > "$proxy_source/config.yaml"
printf '# USER-SHELL-SENTINEL\n' > "$HOME/.bashrc"
cp "$codex_source/auth.json" "$CDET_TEST_ROOT/auth.before"
cp "$codex_source/state.sqlite" "$CDET_TEST_ROOT/db.before"
cp "$codex_source/config.toml" "$CDET_TEST_ROOT/config.before"
cp "$proxy_source/config.yaml" "$CDET_TEST_ROOT/proxy.before"
cp "$HOME/.bashrc" "$CDET_TEST_ROOT/shell.before"
base=(--prefix "$prefix" --store "$store")
"$CDET_TEST_BINARY" "${base[@]}" init --dry-run --json > "$CDET_TEST_ROOT/init-plan.json"
[[ ! -e "$prefix" && ! -e "$store" ]]
"$CDET_TEST_BINARY" "${base[@]}" init > "$CDET_TEST_ROOT/init.log"
[[ ! -e "$prefix/bin/codex" && ! -e "$store/codex" && ! -e "$store/gds" ]]
"$CDET_TEST_BINARY" "${base[@]}" install codex proxy --from "codex=$codex_source" --from "proxy=$proxy_source" --dry-run --json > "$CDET_TEST_ROOT/import-plan.json"
! /usr/bin/grep -Eq 'AUTH-SENTINEL|PROXY-SECRET-SENTINEL|SESSION-SENTINEL' "$CDET_TEST_ROOT/import-plan.json"
[[ ! -e "$store/codex" ]]
[[ ! -e "$CDET_FIXTURE_CURL_LOG" ]]
"$CDET_TEST_BINARY" "${base[@]}" install codex proxy --from "codex=$codex_source" --from "proxy=$proxy_source" > "$CDET_TEST_ROOT/import.log"
"$CDET_TEST_BINARY" "${base[@]}" install codex proxy > "$CDET_TEST_ROOT/repeat.log"
/usr/bin/grep -q '完成 0 项' "$CDET_TEST_ROOT/repeat.log"
"$prefix/bin/codex" 'one argument' > "$CDET_TEST_ROOT/codex.log"
/usr/bin/grep -Fxq "CODEX_HOME=$store/codex" "$CDET_TEST_ROOT/codex.log"
/usr/bin/grep -Fxq 'ARG=one argument' "$CDET_TEST_ROOT/codex.log"
cmp "$CDET_TEST_ROOT/auth.before" "$store/codex/auth.json"
cmp "$CDET_TEST_ROOT/db.before" "$store/codex/state.sqlite"
cmp "$CDET_TEST_ROOT/config.before" "$store/codex/config.toml"
cmp "$CDET_TEST_ROOT/proxy.before" "$store/proxy/config.yaml"
[[ "$(readlink "$store/codex/bin/codex")" = ../packages/standalone/v1/bin/codex ]]
[[ ! -e "$store/codex/versions" && ! -e "$store/codex/current" && ! -e "$store/codex/data" ]]
[[ -f "$store/.cdet/bin/codex" && ! -e "$store/.cdet/programs" ]]
cmp "$CDET_FIXTURE_CODEX" "$store/.cdet/bin/codex"
mv "$codex_source" "$codex_source.retained"
"$prefix/bin/codex" 'source no longer present' > "$CDET_TEST_ROOT/source-independent.log"
mv "$CDET_TEST_ROOT/mount" "$CDET_TEST_ROOT/mount-new"
export CDET_PREFIX="$prefix" CDET_STORE="$store"
prefix="$CDET_TEST_ROOT/mount-new/cli"
store="$CDET_TEST_ROOT/mount-new/components"
if "$prefix/bin/codex" test > "$CDET_TEST_ROOT/before-repair.log" 2>&1; then
  printf 'component ran before repair\n' >&2
  exit 1
fi
"$prefix/bin/cdet" repair --dry-run > "$CDET_TEST_ROOT/repair-preview.log"
[[ ! -e "$CDET_TEST_ROOT/mount" ]]
"$prefix/bin/cdet" repair > "$CDET_TEST_ROOT/repair.log"
"$prefix/bin/cdet" repair > "$CDET_TEST_ROOT/repair-repeat.log"
"$prefix/bin/codex" 'after mount move' > "$CDET_TEST_ROOT/relocated.log"
/usr/bin/grep -Fxq "CODEX_HOME=$store/codex" "$CDET_TEST_ROOT/relocated.log"
new_store="$CDET_TEST_ROOT/store-moved-alone"
mv "$store" "$new_store"
if "$prefix/bin/cdet" status > "$CDET_TEST_ROOT/missing-store.log" 2>&1; then
  printf 'missing store silently replaced\n' >&2
  exit 1
fi
[[ ! -e "$store" ]]
"$prefix/bin/cdet" repair --store "$new_store" > "$CDET_TEST_ROOT/rebind.log"
store="$new_store"
"$prefix/bin/codex" 'after store move' > "$CDET_TEST_ROOT/rebound.log"
/usr/bin/grep -Fxq "CODEX_HOME=$store/codex" "$CDET_TEST_ROOT/rebound.log"
cc -DCDET_FIXTURE_VERSION=2 "$CDET_TEST_ROOT/component.c" -o "$CDET_FIXTURE_CODEX"
cp "$CDET_FIXTURE_CURL_LOG" "$CDET_TEST_ROOT/curl.before-preview"
"$prefix/bin/cdet" update codex --dry-run --json > "$CDET_TEST_ROOT/update-preview.json"
cmp "$CDET_FIXTURE_CURL_LOG" "$CDET_TEST_ROOT/curl.before-preview"
"$prefix/bin/cdet" update codex --yes > "$CDET_TEST_ROOT/update.log"
"$prefix/bin/codex" > "$CDET_TEST_ROOT/v2.log"
/usr/bin/grep -Fxq 'fixture version two' "$CDET_TEST_ROOT/v2.log"
# An upstream-owned program may replace itself without invalidating toolkit
# entry ownership. No historical checksum lock or rollback warehouse exists.
cp "$codex_source.retained/packages/standalone/v1/bin/codex" "$store/.cdet/bin/codex"
"$prefix/bin/codex" 'upstream replacement' > "$CDET_TEST_ROOT/self-updated.log"
/usr/bin/grep -Fxq 'ARG=upstream replacement' "$CDET_TEST_ROOT/self-updated.log"
printf 'unknown data\n' > "$store/codex/unknown"
printf 'unknown program-directory file\n' > "$store/.cdet/bin/keep-user"
"$prefix/bin/cdet" remove codex > "$CDET_TEST_ROOT/remove.log"
[[ ! -e "$prefix/bin/codex" && ! -e "$store/.cdet/bin/codex" && -f "$store/codex/unknown" ]]
[[ -f "$store/.cdet/bin/keep-user" ]]
"$prefix/bin/cdet" remove self > "$CDET_TEST_ROOT/remove-self.log"
[[ ! -e "$prefix/bin/cdet" && -f "$store/proxy/config.yaml" && ! -e "$store/gds" ]]
cmp "$CDET_TEST_ROOT/auth.before" "$codex_source.retained/auth.json"
cmp "$CDET_TEST_ROOT/auth.before" "$store/codex/auth.json"
cmp "$CDET_TEST_ROOT/db.before" "$store/codex/state.sqlite"
cmp "$CDET_TEST_ROOT/config.before" "$store/codex/config.toml"
cmp "$CDET_TEST_ROOT/proxy.before" "$proxy_source/config.yaml"
cmp "$CDET_TEST_ROOT/proxy.before" "$store/proxy/config.yaml"
cmp "$CDET_TEST_ROOT/shell.before" "$HOME/.bashrc"
[[ ! -e "$CDET_FIXTURE_FORBIDDEN_LOG" && ! -e "$CDET_TEST_ROOT/mount" ]]
printf 'CDEToolkit 双目录 CLI 隔离验证通过\n'
