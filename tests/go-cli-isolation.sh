#!/usr/bin/env bash
set -euo pipefail

STK_TEST_BINARY="${1:?provide the built cdet binary}"
[[ "$STK_TEST_BINARY" = /* && -x "$STK_TEST_BINARY" ]]
STK_TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/stk-cli-test.XXXXXX")"
STK_TEST_ROOT="$(cd "$STK_TEST_ROOT" && pwd -P)"
trap 'rm -rf -- "$STK_TEST_ROOT"' EXIT
export HOME="$STK_TEST_ROOT/user" PATH=/usr/bin:/bin
unset CDET_HOME STK_HOME CODEX_HOME HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
local_root="$STK_TEST_ROOT/fast/.local"
codex_home="$local_root/.codex"
stk_home="$local_root/stk"
mkdir -p "$codex_home/packages/standalone/v1/bin" "$codex_home/bin" "$codex_home/sessions" "$local_root/external" "$HOME" "$STK_TEST_ROOT/fast/proj"
cat > "$codex_home/packages/standalone/v1/bin/codex" <<'FIXTURE'
#!/bin/sh
printf 'CODEX_HOME=%s\nHTTP_PROXY=%s\nARGS=%s\n' "$CODEX_HOME" "${HTTP_PROXY:-}" "$*"
FIXTURE
cat > "$local_root/external/mihomo" <<'FIXTURE'
#!/bin/sh
printf 'fixture mihomo\n'
FIXTURE
chmod 0755 "$codex_home/packages/standalone/v1/bin/codex" "$local_root/external/mihomo"
ln -s /retired/.local/.codex/packages/standalone/v1 "$codex_home/packages/standalone/current"
ln -s /retired/.local/.codex/packages/standalone/current/bin/codex "$codex_home/bin/codex"
printf 'AUTH-SENTINEL\n' > "$codex_home/auth.json"
printf 'SESSION-SENTINEL\n' > "$codex_home/sessions/one.jsonl"
printf 'SQLITE-SENTINEL\n' > "$codex_home/state.sqlite"
printf 'model = "preserved"\n' > "$codex_home/config.toml"
printf 'mixed-port: 7890\npassword: SECRET-SENTINEL\n' > "$local_root/proxy.yaml"
printf '[ -z "$PS1" ] && return\n# user shell configuration\n' > "$HOME/.bashrc"
cp "$codex_home/auth.json" "$STK_TEST_ROOT/auth.before"
cp "$codex_home/sessions/one.jsonl" "$STK_TEST_ROOT/session.before"
cp "$codex_home/state.sqlite" "$STK_TEST_ROOT/db.before"
cp "$local_root/proxy.yaml" "$STK_TEST_ROOT/proxy.before"

args=(--home "$stk_home" setup proxy codex --root "local=$local_root" --root "projects=$STK_TEST_ROOT/fast/proj" --binary "proxy=$local_root/external/mihomo" --binary "codex=$codex_home/packages/standalone/v1/bin/codex" --codex-home "$codex_home" --proxy-config "$local_root/proxy.yaml" --shell-file "$HOME/.bashrc")
"$STK_TEST_BINARY" "${args[@]}" --dry-run --json > "$STK_TEST_ROOT/plan.json"
[[ ! -e "$stk_home" ]]
! /usr/bin/grep -q SECRET-SENTINEL "$STK_TEST_ROOT/plan.json"
"$STK_TEST_BINARY" "${args[@]}" > "$STK_TEST_ROOT/setup.log"
"$STK_TEST_BINARY" "${args[@]}" > "$STK_TEST_ROOT/repeat.log"
/usr/bin/grep -q '完成 0 项' "$STK_TEST_ROOT/repeat.log"
"$stk_home/bin/codex" 'one argument' > "$STK_TEST_ROOT/exec.log"
/usr/bin/grep -Fq "CODEX_HOME=$codex_home" "$STK_TEST_ROOT/exec.log"
/usr/bin/grep -Fq 'HTTP_PROXY=http://127.0.0.1:7890' "$STK_TEST_ROOT/exec.log"
/bin/bash --noprofile --norc -c '. "$1"; command -v codex' _ "$HOME/.bashrc" > "$STK_TEST_ROOT/path.log"
/usr/bin/grep -Fxq "$stk_home/bin/codex" "$STK_TEST_ROOT/path.log"
mv "$STK_TEST_ROOT/fast" "$STK_TEST_ROOT/fast-new"
new_home="$STK_TEST_ROOT/fast-new/.local/stk"
new_codex="$STK_TEST_ROOT/fast-new/.local/.codex"
if "$new_home/bin/codex" --version > "$STK_TEST_ROOT/before-repair-exec.log" 2>&1; then
  printf 'component executed before rebuilding relocated paths\n' >&2
  exit 1
fi
if "$new_home/cdet" --home "$new_home" env bash > "$STK_TEST_ROOT/before-repair-env.log" 2>&1; then
  printf 'stale environment emitted before repair\n' >&2
  exit 1
fi
if "$new_home/cdet" --home "$new_home" doctor --json > "$STK_TEST_ROOT/before-repair-doctor.json"; then
  printf 'doctor accepted unrepaired relocated paths\n' >&2
  exit 1
fi
/usr/bin/grep -q '路径待重建' "$STK_TEST_ROOT/before-repair-doctor.json"
"$new_home/cdet" --home "$new_home" repair --dry-run > "$STK_TEST_ROOT/repair-preview.log"
[[ ! -e "$STK_TEST_ROOT/fast" ]]
"$new_home/cdet" --home "$new_home" repair > "$STK_TEST_ROOT/repair.log"
"$new_home/cdet" --home "$new_home" repair > "$STK_TEST_ROOT/repair-repeat.log"
/usr/bin/grep -q '完成 0 项' "$STK_TEST_ROOT/repair-repeat.log"
"$new_home/bin/codex" --version > "$STK_TEST_ROOT/new-exec.log"
/usr/bin/grep -Fq "CODEX_HOME=$new_codex" "$STK_TEST_ROOT/new-exec.log"
"$new_home/cdet" --home "$new_home" doctor --exec-check > "$STK_TEST_ROOT/doctor.log"
cmp "$STK_TEST_ROOT/auth.before" "$new_codex/auth.json"
cmp "$STK_TEST_ROOT/session.before" "$new_codex/sessions/one.jsonl"
cmp "$STK_TEST_ROOT/db.before" "$new_codex/state.sqlite"
cmp "$STK_TEST_ROOT/proxy.before" "$STK_TEST_ROOT/fast-new/.local/proxy.yaml"
[[ ! -e "$STK_TEST_ROOT/fast" ]]
printf 'Go CLI isolated adoption, PATH, relocation, idempotency and data preservation: PASS\n'

# Lifecycle operations also use fixtures only. The adopted binary is retained
# when a user explicitly switches to a toolkit-owned copy.
original_codex="$new_codex/packages/standalone/v1/bin/codex"
cp "$original_codex" "$STK_TEST_ROOT/codex-before"
if "$STK_TEST_BINARY" --home "$new_home" update codex > "$STK_TEST_ROOT/external-update.log" 2>&1; then
  printf 'external update unexpectedly accepted\n' >&2
  exit 1
fi
"$STK_TEST_BINARY" --home "$new_home" update codex --takeover --install "codex=$original_codex" --dry-run > "$STK_TEST_ROOT/update-preview.log"
[[ ! -e "$new_home/tools/codex/codex" ]]
"$STK_TEST_BINARY" --home "$new_home" update codex --takeover --install "codex=$original_codex" > "$STK_TEST_ROOT/update.log"
"$new_home/bin/codex" --version > "$STK_TEST_ROOT/owned-exec.log"
/usr/bin/grep -Fq "CODEX_HOME=$new_codex" "$STK_TEST_ROOT/owned-exec.log"
cmp "$STK_TEST_ROOT/codex-before" "$original_codex"
"$STK_TEST_BINARY" --home "$new_home" update cdet --install "cdet=$STK_TEST_BINARY" > "$STK_TEST_ROOT/self-update.log"
printf 'USER-CACHE\n' > "$new_home/run/proxy/keep.db"
"$STK_TEST_BINARY" --home "$new_home" uninstall codex --dry-run > "$STK_TEST_ROOT/uninstall-preview.log"
[[ -x "$new_home/tools/codex/codex" ]]
"$STK_TEST_BINARY" --home "$new_home" uninstall codex > "$STK_TEST_ROOT/uninstall-codex.log"
[[ ! -e "$new_home/tools/codex/codex" && ! -e "$new_home/bin/codex" && -x "$new_home/bin/mihomo" ]]
"$STK_TEST_BINARY" --home "$new_home" uninstall cdet > "$STK_TEST_ROOT/uninstall-stk.log"
[[ ! -e "$new_home/cdet" && ! -e "$new_home/env.sh" && ! -e "$new_home/bin/mihomo" ]]
! /usr/bin/grep -q 'server-toolkit-go environment' "$HOME/.bashrc"
/usr/bin/grep -q '# user shell configuration' "$HOME/.bashrc"
[[ -f "$new_home/run/proxy/keep.db" ]]
cmp "$STK_TEST_ROOT/auth.before" "$new_codex/auth.json"
cmp "$STK_TEST_ROOT/session.before" "$new_codex/sessions/one.jsonl"
cmp "$STK_TEST_ROOT/db.before" "$new_codex/state.sqlite"
cmp "$STK_TEST_ROOT/proxy.before" "$STK_TEST_ROOT/fast-new/.local/proxy.yaml"
cmp "$STK_TEST_ROOT/codex-before" "$original_codex"
"$STK_TEST_BINARY" --home "$new_home" uninstall cdet > "$STK_TEST_ROOT/uninstall-repeat.log"
/usr/bin/grep -q '完成 0 项' "$STK_TEST_ROOT/uninstall-repeat.log"
printf 'Go CLI isolated update, takeover, self-update and data-preserving uninstall: PASS\n'

# A separate fixture exercises a complete update batch and its explicit
# rollback, including self update, without executing either candidate program.
rollback_home="$STK_TEST_ROOT/rollback-manager"
rollback_codex="$STK_TEST_ROOT/rollback-data"
mkdir -p "$rollback_codex"
printf 'ROLLBACK-AUTH\n' > "$rollback_codex/auth.json"
printf '#!/bin/sh\nprintf first\\n\n' > "$STK_TEST_ROOT/codex-first"
printf '#!/bin/sh\nprintf second\\n\n' > "$STK_TEST_ROOT/codex-second"
chmod 0755 "$STK_TEST_ROOT/codex-first" "$STK_TEST_ROOT/codex-second"
"$STK_TEST_BINARY" --home "$rollback_home" install codex --install "codex=$STK_TEST_ROOT/codex-first" --codex-home "$rollback_codex" > "$STK_TEST_ROOT/rollback-setup.log"
"$STK_TEST_BINARY" --home "$rollback_home" update codex --install "codex=$STK_TEST_ROOT/codex-second" > "$STK_TEST_ROOT/rollback-update.log"
"$STK_TEST_BINARY" --home "$rollback_home" rollback --dry-run --json > "$STK_TEST_ROOT/rollback-preview.json"
cmp "$STK_TEST_ROOT/codex-second" "$rollback_home/tools/codex/codex"
"$STK_TEST_BINARY" --home "$rollback_home" rollback > "$STK_TEST_ROOT/rollback.log"
cmp "$STK_TEST_ROOT/codex-first" "$rollback_home/tools/codex/codex"
printf '#!/bin/sh\nprintf fixture-manager\\n\n' > "$STK_TEST_ROOT/next-manager"
"$STK_TEST_BINARY" --home "$rollback_home" update cdet --install "cdet=$STK_TEST_ROOT/next-manager" > "$STK_TEST_ROOT/self-update-fixture.log"
"$STK_TEST_BINARY" --home "$rollback_home" rollback > "$STK_TEST_ROOT/self-rollback.log"
cmp "$STK_TEST_BINARY" "$rollback_home/cdet"
/usr/bin/grep -Fxq 'ROLLBACK-AUTH' "$rollback_codex/auth.json"
printf 'Go CLI isolated component rollback, self rollback and authentication preservation: PASS\n'
