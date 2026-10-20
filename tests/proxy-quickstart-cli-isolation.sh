#!/usr/bin/env bash
set -euo pipefail

# Proxy-only lifecycle and Quick Start contract. The optional legacy UI
# argument exists solely for separately labelled frozen-release verification.
# Current source requires private summaries and explicit confirmation.
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
umask 077
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

prompted_nido() {
  local output=$1 diagnostic=$2 command terminal_dir terminal_pid status index ready label keys plan_mode=${fixture_plan:-false}
  shift 2
  if [[ "$quickstart_mode" == current ]]; then
    command -v script >/dev/null || { printf 'current quick start requires the existing system script utility\n' >&2; return 2; }
    terminal_dir="$(mktemp -d "$scratch/terminal.XXXXXX")"
    # Capture private input before allocating a PTY. BSD script lacks -E never;
    # no input reaches either implementation before child stty disables echo.
    cat > "$terminal_dir/input"
    chmod 0600 "$terminal_dir/input"
    mkfifo "$terminal_dir/feed"
    cat > "$terminal_dir/child.sh" <<'PTY_CHILD'
#!/bin/bash
set -euo pipefail
output=$1; status_file=$2
shift 2
stty -echo rows 50 cols 160
printf 'NIDO_PRIVATE_PTY_READY\n' >&2
set +e
"$@" > "$output"
status=$?
printf '%s\n' "$status" > "$status_file"
exit "$status"
PTY_CHILD
    case "$(uname -s)" in
      Linux)
        printf -v command '%q ' /bin/bash "$terminal_dir/child.sh" "$output" "$terminal_dir/status" "$binary" --root "$root" "$@"
        script -q -e -E never -c "$command" /dev/null < "$terminal_dir/feed" > "$diagnostic" 2>&1 &
        ;;
      Darwin)
        # BSD script requires a terminal on its own stdin. macOS's bundled
        # Expect supplies only that outer PTY; script still owns the child PTY.
        [[ -x /usr/bin/expect ]] || { printf 'BSD script requires the existing macOS Expect adapter\n' >&2; return 2; }
        cat > "$terminal_dir/adapter.exp" <<'PTY_ADAPTER'
set timeout 10
log_user 0
fconfigure stdin -translation binary -encoding binary -blocking 0
fconfigure stdout -translation lf -encoding utf-8
set child_args $argv
spawn -noecho /usr/bin/script -q /dev/null /bin/bash {*}$child_args
expect {
  -exact "NIDO_PRIVATE_PTY_READY" { puts -nonewline stdout $expect_out(buffer); flush stdout }
  timeout { close; wait; exit 124 }
  eof { wait; exit 125 }
}
proc forward_input {} {
  global spawn_id
  set bytes [read stdin]
  if {[string length $bytes]} { send -- $bytes }
  if {[eof stdin]} { fileevent stdin readable {} }
}
set completed 0
set expired 0
match_max 65536
expect_background {
  -re {.+} { puts -nonewline stdout $expect_out(buffer); flush stdout }
  eof { set completed 1 }
}
fileevent stdin readable forward_input
after 60000 { set expired 1; set completed 1 }
vwait completed
if {$expired} { close; wait; exit 124 }
set result [wait]
exit [lindex $result 3]
PTY_ADAPTER
        /usr/bin/expect "$terminal_dir/adapter.exp" "$terminal_dir/child.sh" "$output" "$terminal_dir/status" "$binary" --root "$root" "$@" < "$terminal_dir/feed" > "$diagnostic" 2>&1 &
        ;;
      *) printf 'unsupported script implementation\n' >&2; return 2 ;;
    esac
    terminal_pid=$!
    exec 3> "$terminal_dir/feed"
    ready=false
    for ((index=0; index<200; index++)); do
      if grep -Fq NIDO_PRIVATE_PTY_READY "$diagnostic"; then ready=true; break; fi
      if ! kill -0 "$terminal_pid" 2>/dev/null; then break; fi
      sleep 0.05
    done
    if [[ "$ready" != true ]]; then
      exec 3>&-
      kill "$terminal_pid" 2>/dev/null || true
      wait "$terminal_pid" 2>/dev/null || true
      printf 'private child PTY did not become ready\n' >&2
      return 2
    fi
    if [[ "$plan_mode" == true ]]; then
        # Feed real terminal keys only after the public UI has rendered each
        # question. Bulk answers can be lost between asynchronous prompts.
        while IFS=$'\t' read -r label keys; do
          [[ -n "$label" && -n "$keys" ]] || return 2
          ready=false
          for ((index=0; index<200; index++)); do
            if grep -Fq -- "$label" "$diagnostic"; then ready=true; break; fi
            if ! kill -0 "$terminal_pid" 2>/dev/null; then break; fi
            sleep 0.05
          done
          if [[ "$ready" != true ]]; then
            exec 3>&-
            kill "$terminal_pid" 2>/dev/null || true
            wait "$terminal_pid" 2>/dev/null || true
            printf 'public TUI prompt did not arrive: %s\n' "$label" >&2
            return 2
          fi
          printf '%b' "$keys" >&3
        done < "$terminal_dir/input"
    else
      cat "$terminal_dir/input" >&3
    fi
    for ((index=0; index<600; index++)); do
      [[ -s "$terminal_dir/status" ]] && break
      if ! kill -0 "$terminal_pid" 2>/dev/null; then break; fi
      sleep 0.05
    done
    exec 3>&-
    if [[ ! -s "$terminal_dir/status" ]]; then
      kill "$terminal_pid" 2>/dev/null || true
      wait "$terminal_pid" 2>/dev/null || true
      printf 'private child PTY did not complete within its bound\n' >&2
      return 2
    fi
    # BSD script does not propagate the child status; use the private receipt.
    status="$(cat "$terminal_dir/status")"
    for ((index=0; index<200; index++)); do
      if ! kill -0 "$terminal_pid" 2>/dev/null; then break; fi
      sleep 0.05
    done
    if kill -0 "$terminal_pid" 2>/dev/null; then
      kill "$terminal_pid" 2>/dev/null || true
      wait "$terminal_pid" 2>/dev/null || true
      printf 'system script did not release the completed PTY\n' >&2
      return 2
    fi
    wait "$terminal_pid" || true
    [[ "$status" =~ ^[0-9]+$ && "$status" -le 255 ]] || return 2
    return "$status"
  else
    nido "$@" > "$output" 2> "$diagnostic"
  fi
}

prompted_nido_plan() {
  local fixture_plan=true
  prompted_nido "$@"
}

if [[ "$quickstart_mode" == current ]]; then
  # The optional post-install step defaults to Skip, without a config or start.
  original_root="$root"
  root="$scratch/default skip root"
  nido init --yes >/dev/null
  printf '%s\t%s\n' '安装 Proxy' '\033[B\r' 'Mihomo 已安装，尚未配置或启动' '\r' |
    prompted_nido_plan "$scratch/skip-install.log" "$scratch/skip-preview.log" proxy install --artifact "$core"
  grep -Fq 'Skip（默认）' "$scratch/skip-preview.log"
  ! grep -Fq 'Provider URL' "$scratch/skip-preview.log"
  [[ -x "$root/proxy/mihomo" && ! -e "$root/proxy/config.yaml" && ! -e "$root/proxy/config.yaml.back" && ! -e "$root/proxy/process.json" ]]
  root="$original_root"
fi

# Install + full quick start through the actual CLI, with no -yes bypass.
quickstart_input() {
  printf '%s\n' \
    'https://provider.invalid/sub?token=visible-token' \
    'Daily' 17890 y 'Download' 17891 y \
    'ss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode' \
    17892 'https://isp-check.invalid/check' y 'Relay'
}
quickstart_plan() {
  local initial_stage=$1 final_keys=${2:-'\033[B\r'}
  # Ctrl+A then Ctrl+K clears a fallback from the beginning of a text field.
  # Ctrl+U only deletes before the cursor, leaving a fallback intact here.
  # printf %b requires \0NNN: short octal escapes can consume a following
  # numeric answer (\01317890 becomes Y7890 instead of Ctrl+K then 17890).
  if [[ $initial_stage = install ]]; then
    printf '%s\t%s\n' '安装 Proxy' '\033[B\r' 'Mihomo 已安装，尚未配置或启动' '\033[B\r'
  fi
  printf '%s\t%s\n' \
    'Provider URL' '\0001\0013https://provider.invalid/sub?token=visible-token\r' \
    '日常代理 filter' '\0001\0013Daily\r' \
    '日常代理端口' '\0001\001317890\r' \
    '是否启用独立下载入口' '\033[B\r' \
    '下载 filter' '\0001\0013Download\r' \
    '下载端口' '\0001\001317891\r' \
    '是否启用 ISP' '\033[B\r' \
    'ISP 节点链接' '\0001\0013ss://aes-128-gcm:p%2Dass@192.0.2.1:8388#ISP%2Dnode\r' \
    'ISP 端口' '\0001\001317892\r' \
    'ISP 是否需要前置代理' '\033[B\r' \
    'ISP 前置 filter' '\0001\0013Relay\r' \
    'ISP 前置测试地址' '\0001\0013https://isp-check.invalid/check\r' \
    '回环管理地址' '\r' \
    '保存 Proxy 配置（保持停止）' "$final_keys"
}
if [[ "$quickstart_mode" == current ]]; then
  quickstart_plan install |
    prompted_nido_plan "$scratch/install.log" "$scratch/preview.log" proxy install --artifact "$core"
else
  quickstart_input | prompted_nido "$scratch/install.log" "$scratch/preview.log" proxy install --artifact "$core"
fi
if [[ "$quickstart_mode" == current ]]; then
  grep -Fq 'Proxy 配置摘要' "$scratch/preview.log"
  grep -Fq '保存 Proxy 配置（保持停止）' "$scratch/preview.log"
  for marker in 17890 17891 17892; do grep -Fq "$marker" "$scratch/preview.log"; done
  for private in provider.invalid visible-token 'ss://' p%2Dass p-ass '192.0.2.1'; do
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
if [[ "$quickstart_mode" == current ]]; then
  grep -Fq 'dialer-proxy: ISP-Relay' "$root/proxy/config.yaml"
else
  grep -Fq 'dialer-proxy: isp-relay' "$root/proxy/config.yaml"
fi
nido proxy status --json > "$scratch/status.json"
grep -Fq '"listeners":false' "$scratch/status.json"
[[ ! -e "$root/proxy/process.json" ]]
if [[ "$quickstart_mode" == current ]]; then
  cp "$root/proxy/config.yaml" "$scratch/config-before-repeat"
  quickstart_plan configure | prompted_nido_plan "$scratch/repeat.log" "$scratch/repeat-preview.log" proxy configure
  cmp "$root/proxy/config.yaml" "$scratch/config-before-repeat"
  [[ ! -e "$root/proxy/config.yaml.back" && ! -e "$root/proxy/process.json" ]]
  # Esc cancels the public confirmation without replacing config or backup.
  if quickstart_plan configure '\033' |
    prompted_nido_plan "$scratch/cancel.log" "$scratch/cancel-preview.log" proxy configure; then
    printf 'Esc unexpectedly approved Quick Start\n' >&2
    exit 1
  fi
  cmp "$root/proxy/config.yaml" "$scratch/config-before-repeat"
  [[ ! -e "$root/proxy/config.yaml.back" && ! -e "$root/proxy/process.json" ]]
  for transcript in repeat-preview.log cancel-preview.log; do
    for private in provider.invalid visible-token 'ss://' p%2Dass p-ass '192.0.2.1'; do
      ! grep -Fq "$private" "$scratch/$transcript"
    done
  done
fi

# Proxy does not configure its consumers. Keep both external tool sentinels
# unchanged while removing/reinstalling the component with native data intact.
mkdir -p "$root/proxy/providers" "$root/proxy/data"
printf 'PROVIDER-CACHE-SENTINEL\n' > "$root/proxy/providers/subscription.yaml"
printf 'RUNTIME-DATA-SENTINEL\n' > "$root/proxy/data/keep"
printf 'LAST-CONFIG-SENTINEL\n' > "$root/proxy/config.yaml.back"
cp "$root/proxy/config.yaml" "$scratch/config-before-remove"
nido proxy remove --yes > "$scratch/remove.json"
[[ ! -e "$root/proxy/mihomo" && ! -e "$root/proxy/process.json" ]]
cmp "$root/proxy/config.yaml" "$scratch/config-before-remove"
grep -Fxq PROVIDER-CACHE-SENTINEL "$root/proxy/providers/subscription.yaml"
grep -Fxq RUNTIME-DATA-SENTINEL "$root/proxy/data/keep"
grep -Fxq LAST-CONFIG-SENTINEL "$root/proxy/config.yaml.back"
nido proxy install --artifact "$core" --yes > "$scratch/reinstall.json"
[[ -x "$root/proxy/mihomo" && ! -e "$root/proxy/process.json" ]]
cmp "$root/proxy/config.yaml" "$scratch/config-before-remove"
grep -Fxq PROVIDER-CACHE-SENTINEL "$root/proxy/providers/subscription.yaml"
grep -Fxq RUNTIME-DATA-SENTINEL "$root/proxy/data/keep"
grep -Fxq LAST-CONFIG-SENTINEL "$root/proxy/config.yaml.back"
cmp "$HOME/.gitconfig" "$scratch/before/.gitconfig"
cmp "$codex_home/.env" "$scratch/before/codex/.env"
cmp "$codex_home/auth.json" "$scratch/before/codex/auth.json"
cmp "$codex_home/state.sqlite" "$scratch/before/codex/state.sqlite"
diff -r "$codex_home/sessions" "$scratch/before/codex/sessions"
cmp "$HOME/.config/mihomo/config.yaml" "$scratch/before/.config/mihomo/config.yaml"
cmp "$HOME/.bashrc" "$scratch/before/.bashrc"
[[ ! -e "$NIDO_FIXTURE_FORBIDDEN_LOG" && ! -e "$root/proxy/process.json" ]]
if [[ "$quickstart_mode" == current ]]; then printf 'Proxy quick start 默认 Skip 与重复配置验证通过\n'; fi
if [[ "$quickstart_mode" == current ]]; then printf 'Proxy 公共 TUI 方向键、密钥隐藏与 Esc 取消验证通过\n'; fi
printf 'Proxy 配置、缓存与数据保留及卸载重装隔离验证通过\n'
printf 'PASS: Proxy quick start, node URI, component removal/reinstall and external data preservation; inert core only.\n'
printf 'Proxy quick start CLI 隔离验证通过\n'
ISOLATED
