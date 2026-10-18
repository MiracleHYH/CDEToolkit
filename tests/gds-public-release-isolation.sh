#!/usr/bin/env bash
set -euo pipefail

# Public-release CPU smoke test. This is deliberately separate from offline
# source tests and is never an installation into the caller's HOME or PATH.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行\n' >&2; exit 2; }
fixtures="${NIDO_TEST_RELEASE_INPUTS:?set NIDO_TEST_RELEASE_INPUTS to a public release fixture directory}"
output="${NIDO_TEST_RELEASE_OUTPUT:?set NIDO_TEST_RELEASE_OUTPUT to a new evidence directory}"
version="${NIDO_TEST_RELEASE_VERSION:?set NIDO_TEST_RELEASE_VERSION explicitly}"
gds_version="${NIDO_TEST_RELEASE_GDS_VERSION:?set NIDO_TEST_RELEASE_GDS_VERSION explicitly}"
stable='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$version" =~ $stable && "$gds_version" =~ $stable ]] || { printf '版本必须是稳定 X.Y.Z\n' >&2; exit 2; }
[[ "$fixtures" == /* && -d "$fixtures" && ! -L "$fixtures" ]] || exit 2
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
for tool in bwrap timeout sha256sum tar file readlink cmp diff find curl awk; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
awk_tool="$(readlink -f "$(command -v awk)")"
[[ "$awk_tool" == /usr/* && -f "$awk_tool" && -x "$awk_tool" ]] || exit 2
[[ -x /usr/bin/curl && -d /etc/ssl && -f /etc/resolv.conf && -f /etc/hosts ]] || exit 2
for name in install.sh nido-previous.tar.gz expected-gds; do
  [[ -f "$fixtures/$name" && ! -L "$fixtures/$name" ]] || { printf '缺少普通公开 fixture：%s\n' "$name" >&2; exit 2; }
done
if [[ -e "$fixtures/gds-local-old" || -L "$fixtures/gds-local-old" ]]; then
  [[ -f "$fixtures/gds-local-old" && ! -L "$fixtures/gds-local-old" ]] || exit 2
fi
for name in expected-gds gds-local-old; do
  [[ -f "$fixtures/$name" ]] || continue
  description="$(LC_ALL=C file -b "$fixtures/$name")"
  [[ "$description" == *'ELF 64-bit'* && "$description" == *'executable'* && "$description" == *'x86-64'* ]] || { printf '%s 必须为原生 Linux amd64 ELF\n' "$name" >&2; exit 2; }
done

umask 077
mkdir "$output"
output="$(cd "$output" && pwd -P)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-public-release.XXXXXX")"
scratch="$(cd "$scratch" && pwd -P)"
cleanup() {
  result=$?
  if [[ -d "$scratch/work/evidence" ]]; then
    # Do not copy installed programs, private HOME fixtures or symlink targets.
    find "$scratch/work/evidence" -maxdepth 1 -type f \( -name '*.json' -o -name '*.txt' -o -name '*.log' -o -name '*.sha256' \) -exec cp -- {} "$output/" \; || true
  fi
  rm -rf -- "$scratch"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/inputs" "$scratch/work"
for name in install.sh nido-previous.tar.gz expected-gds gds-local-old; do
  [[ -f "$fixtures/$name" ]] || continue
  # Only these explicitly staged public files cross the namespace boundary.
  cp -- "$fixtures/$name" "$scratch/inputs/$name"
done
(cd "$scratch/inputs" && sha256sum ./*) > "$output/inputs.sha256"
printf 'host_uid=%s\nhost_kernel=%s\nhost_machine=%s\nnido_version=%s\ngds_version=%s\n' "$(id -u)" "$(uname -r)" "$(uname -m)" "$version" "$gds_version" > "$output/environment.txt"

cat > "$scratch/runner.sh" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
umask 077
[[ "$(id -u)" != 0 && "$(uname -m)" == x86_64 ]]
[[ ! -e /dev/nvidiactl && ! -e /dev/nvidia0 && ! -e /dev/dri && ! -e /sys && ! -e /run && ! -e /home && ! -e /root ]]
[[ "$(readlink /proc/self/exe)" == /usr/bin/readlink ]]
mkdir -p /work/evidence /work/guard /work/home/.codex/sessions /work/home/.config/mihomo /work/old /work/before
printf 'namespace_uid=%s\n' "$(id -u)" > /work/evidence/namespace.txt
for ns in mnt pid user; do printf '%s=%s\n' "$ns" "$(readlink "/proc/self/ns/$ns")" >> /work/evidence/namespace.txt; done
find /dev -maxdepth 1 -printf '%f\n' | sort > /work/evidence/devices.txt

# PATH traps catch component or service execution. Private /dev, /proc and the
# absence of /run independently prevent access to host GPU and service sockets.
for tool in systemctl supervisorctl sudo codex mihomo gds nvidia-smi nvcc docker podman service rc-service launchctl apt apt-get yum dnf npm pip; do
  cat > "/work/guard/$tool" <<'FORBIDDEN'
#!/bin/sh
printf '%s\n' "$0" >> /work/evidence/forbidden-commands.log
exit 98
FORBIDDEN
  chmod 0755 "/work/guard/$tool"
done

# The bootstrap may download only the explicit/latest official Nido asset and its
# checksum. Reject extra URLs, curl configuration, and arbitrary output paths.
cat > /work/guard/curl <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
original=("$@")
source_url= output_path=
deny() { printf 'rejected bootstrap curl arguments\n' >> /work/evidence/forbidden-commands.log; exit 98; }
while (($#)); do
  case "$1" in
    -q|--http1.1|--fail|--silent|--show-error|--location|--tlsv1.2) shift ;;
    --proto|--proto-redir) [[ $# -ge 2 && "$2" == '=https' ]] || deny; shift 2 ;;
    --retry) [[ $# -ge 2 && "$2" == 2 ]] || deny; shift 2 ;;
    --connect-timeout) [[ $# -ge 2 && "$2" == 15 ]] || deny; shift 2 ;;
    --max-time) [[ $# -ge 2 && "$2" == 300 ]] || deny; shift 2 ;;
    --max-filesize) [[ $# -ge 2 && "$2" == 268435456 ]] || deny; shift 2 ;;
    --output)
      [[ $# -ge 2 && -z "$output_path" ]] || deny
      output_path="$2"
      [[ "$output_path" =~ ^/tmp/nido-install\.[A-Za-z0-9]+/(nido-linux-amd64\.tar\.gz|checksum)$ ]] || deny
      shift 2 ;;
    https://github.com/MiracleHYH/CDEToolkit/releases/download/v"$NIDO_TEST_RELEASE_VERSION"/nido-linux-amd64.tar.gz|https://github.com/MiracleHYH/CDEToolkit/releases/download/v"$NIDO_TEST_RELEASE_VERSION"/nido-linux-amd64.tar.gz.sha256)
      [[ -z "$source_url" ]] || deny; source_url="$1"; shift ;;
    https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/nido-linux-amd64.tar.gz|https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/nido-linux-amd64.tar.gz.sha256)
      [[ -z "$source_url" ]] || deny; source_url="$1"; shift ;;
    *) deny ;;
  esac
done
[[ -n "$source_url" && -n "$output_path" ]] || deny
printf '%s\n' "$source_url" >> /work/evidence/nido-downloads.log
exec /usr/bin/curl "${original[@]}"
CURL
chmod 0755 /work/guard/curl

printf 'AUTH-SENTINEL\n' > "$HOME/.codex/auth.json"
printf 'SESSION-SENTINEL\n' > "$HOME/.codex/sessions/one.jsonl"
printf 'DATABASE-SENTINEL\n' > "$HOME/.codex/state.sqlite"
printf 'model = "preserved"\n' > "$HOME/.codex/config.toml"
printf 'HTTPS_PROXY=http://user-proxy.invalid:7890\nKEEP=value\n' > "$HOME/.codex/.env"
printf 'secret: EXTERNAL-PROXY-SENTINEL\n' > "$HOME/.config/mihomo/config.yaml"
printf '# USER-SHELL-SENTINEL\n' > "$HOME/.bashrc"
printf '# USER-ZSH-SENTINEL\n' > "$HOME/.zshrc"
cp -R "$HOME/.codex" /work/before/codex
cp -R "$HOME/.config" /work/before/config
cp "$HOME/.bashrc" /work/before/bashrc
cp "$HOME/.zshrc" /work/before/zshrc

[[ "$(tar -tzf /inputs/nido-previous.tar.gz)" == nido ]]
tar -xOzf /inputs/nido-previous.tar.gz nido > /work/old/nido
chmod 0755 /work/old/nido
file /work/old/nido | grep -Eq 'ELF 64-bit.*x86-64'
[[ "$(/work/old/nido version)" == 'nido 0.4.0' ]]
root=/work/software/nido
/work/old/nido --root "$root" init --yes > /work/evidence/old-init.log
printf '\n# USER-NIDO-CONFIG-COMMENT\n' >> "$root/config.toml"
printf 'USER-NIDO-DATA\n' > "$root/user-data.txt"
cp "$root/config.toml" /work/before/nido-config.toml
cp "$root/env.sh" /work/before/nido-env.sh
cp "$root/user-data.txt" /work/before/nido-user-data.txt

assert_preserved() {
  cmp /work/before/nido-config.toml "$root/config.toml"
  cmp /work/before/nido-env.sh "$root/env.sh"
  cmp /work/before/nido-user-data.txt "$root/user-data.txt"
  diff -r /work/before/codex "$HOME/.codex"
  diff -r /work/before/config "$HOME/.config"
  cmp /work/before/bashrc "$HOME/.bashrc"
  cmp /work/before/zshrc "$HOME/.zshrc"
  [[ ! -e /work/evidence/forbidden-commands.log ]]
  for name in process.json worker.json state.json; do
    [[ ! -e "$root/gds/$name" && ! -L "$root/gds/$name" ]]
  done
}
assert_gds_config() {
  cmp /work/before/gds-config.toml "$root/gds/config.toml"
  cmp /work/before/gds-unknown.txt "$root/gds/user-notes.txt"
  assert_preserved
}
assert_official_gds() {
  cmp /inputs/expected-gds "$root/gds/gds"
  grep -Fq '"version":"'"$NIDO_TEST_RELEASE_GDS_VERSION"'"' "$root/gds/installed.json"
  grep -Fq '"release_tag":"gds-v'"$NIDO_TEST_RELEASE_GDS_VERSION"'"' "$root/gds/installed.json"
  digest="$(sha256sum /inputs/expected-gds)"; digest="${digest%% *}"
  grep -Fq '"sha256":"'"$digest"'"' "$root/gds/installed.json"
  assert_gds_config
}
nido() { "$root/bin/nido" --root "$root" "$@"; }

# The public bootstrap downloads Nido into the namespace's private root and
# must preserve the pre-existing TOML bytes. GDS later uses its official API.
sh /inputs/install.sh --version "$NIDO_TEST_RELEASE_VERSION" --root "$root" --yes > /work/evidence/nido-upgrade.log
[[ "$(nido version)" == "nido $NIDO_TEST_RELEASE_VERSION" ]]
assert_preserved
[[ ! -e "$root/gds" && ! -e "$root/proxy" && ! -e "$root/codex" ]]
sh /inputs/install.sh --root "$root" --yes > /work/evidence/nido-repeat.log
[[ "$(nido version)" == "nido $NIDO_TEST_RELEASE_VERSION" ]]
assert_preserved
[[ "$(wc -l < /work/evidence/nido-downloads.log)" == 4 ]]

# Configure only a fabricated UUID. No driver capability probe or allocation
# is permitted anywhere in this test; the only GDS execution is metadata.
nido gds configure --gpu GPU-12345678-1234-1234-1234-123456789abc --yes --json > /work/evidence/gds-configure.json
printf '\n# USER-GDS-CONFIG-COMMENT\n' >> "$root/gds/config.toml"
printf 'USER-GDS-NOTES\n' > "$root/gds/user-notes.txt"
cp "$root/gds/config.toml" /work/before/gds-config.toml
cp "$root/gds/user-notes.txt" /work/before/gds-unknown.txt
nido gds install --dry-run --json > /work/evidence/gds-preview.json
[[ ! -e "$root/gds/gds" && ! -e "$root/gds/installed.json" ]]
assert_gds_config
nido gds install --yes --json > /work/evidence/gds-install.json
assert_official_gds
cp "$root/gds/installed.json" /work/evidence/gds-installed-receipt.json
"$root/gds/gds" --version --json > /work/evidence/gds-metadata.json
grep -Fq '"native":true' /work/evidence/gds-metadata.json
grep -Fq '"product":"gds"' /work/evidence/gds-metadata.json
grep -Fq '"version":"'"$NIDO_TEST_RELEASE_GDS_VERSION"'"' /work/evidence/gds-metadata.json

nido gds update --check --json > /work/evidence/gds-check.json
grep -Fxq '{"current":"'"$NIDO_TEST_RELEASE_GDS_VERSION"'","latest":"gds-v'"$NIDO_TEST_RELEASE_GDS_VERSION"'","available":false}' /work/evidence/gds-check.json
assert_official_gds
nido gds update "$NIDO_TEST_RELEASE_GDS_VERSION" --yes --json > /work/evidence/gds-explicit-update.json
assert_official_gds
nido gds update --yes --json > /work/evidence/gds-default-update.json
assert_official_gds

local_upgrade=false
if [[ -f /inputs/gds-local-old ]]; then
  nido gds update --artifact /inputs/gds-local-old --yes --json > /work/evidence/gds-local-old.json
  cmp /inputs/gds-local-old "$root/gds/gds"
  grep -Fq '"version":"0.0.0"' "$root/gds/installed.json"
  assert_gds_config
  nido gds update --check --json > /work/evidence/gds-old-check.json
  grep -Fxq '{"current":"0.0.0","latest":"gds-v'"$NIDO_TEST_RELEASE_GDS_VERSION"'","available":true}' /work/evidence/gds-old-check.json
  nido gds update --yes --json > /work/evidence/gds-local-to-official.json
  assert_official_gds
  local_upgrade=true
fi

nido gds remove --yes --json > /work/evidence/gds-remove.json
[[ ! -e "$root/gds/gds" && ! -L "$root/gds/gds" ]]
assert_gds_config
nido gds status --json > /work/evidence/gds-final-status.json
grep -Fq '"installed":false' /work/evidence/gds-final-status.json
grep -Fq '"runtime":"stopped"' /work/evidence/gds-final-status.json
grep -Fq '"worker":"stopped"' /work/evidence/gds-final-status.json
grep -Fq '"invocation":"exited"' /work/evidence/gds-final-status.json
cp "$root/gds/invocation.json" /work/evidence/gds-final-invocation.json
for entry in /proc/[0-9]*/exe; do
  executable="$(readlink "$entry" 2>/dev/null || true)"
  case "$executable" in "$root/gds/"*|/inputs/expected-gds|/inputs/gds-local-old) printf '仍有 GDS 进程：%s\n' "$executable" >&2; exit 1 ;; esac
done
printf 'No GDS monitor, worker, probe or service remains running.\n' > /work/evidence/processes-final.txt
printf '{"nido":"%s","gds":"%s","ordinary_uid":%s,"local_to_official_upgrade":%s,"private_home":true,"private_pid":true,"gpu_devices":false,"real_service_started":false,"preserved_sentinels":true}\n' "$NIDO_TEST_RELEASE_VERSION" "$NIDO_TEST_RELEASE_GDS_VERSION" "$(id -u)" "$local_upgrade" > /work/evidence/result.json
sha256sum "$root/bin/nido" /inputs/expected-gds /work/before/nido-config.toml /work/before/nido-env.sh /work/before/gds-config.toml > /work/evidence/verified.sha256
printf '官方 Nido 升级/重复安装、GDS 下载校验安装/查询/更新/卸载及数据保留验证通过。\n'
INNER

flags=(--unshare-all --share-net --die-with-parent --new-session --clearenv --cap-drop ALL
  --ro-bind /usr /usr --ro-bind /lib /lib --ro-bind /lib64 /lib64
  --symlink usr/bin /bin --symlink usr/sbin /sbin
  --dir /test-bin --symlink "$awk_tool" /test-bin/awk
  --dir /etc --ro-bind /etc/ssl /etc/ssl --ro-bind /etc/resolv.conf /etc/resolv.conf --ro-bind /etc/hosts /etc/hosts
  --ro-bind "$scratch/inputs" /inputs --ro-bind "$scratch/runner.sh" /runner.sh
  --bind "$scratch/work" /work --proc /proc --dev /dev --tmpfs /tmp --chdir /work
  --setenv HOME /work/home --setenv TMPDIR /tmp --setenv PATH /work/guard:/test-bin:/usr/bin:/bin
  --setenv LANG C --setenv LC_ALL C --setenv TZ UTC
  --setenv NIDO_TEST_RELEASE_VERSION "$version" --setenv NIDO_TEST_RELEASE_GDS_VERSION "$gds_version")
# Unlike offline tests, this explicit public-release check shares networking.
# It mounts no host HOME, GPU device, service socket, credentials or data.
timeout --kill-after=5s 30m bwrap "${flags[@]}" /bin/bash /runner.sh 2>&1 | tee "$output/run.log"
[[ -f "$scratch/work/evidence/result.json" ]]
printf '普通用户 Linux amd64 官方发布隔离验证通过；证据：%s\n' "$output"
