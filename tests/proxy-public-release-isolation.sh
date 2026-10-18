#!/usr/bin/env bash
set -euo pipefail

# Explicit public-release acceptance. Inputs must already be verified official
# artifacts; this test downloads the genuine bootstrap payload into private HOME.
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || { printf '需要原生 Linux amd64\n' >&2; exit 2; }
[[ "$(id -u)" != 0 ]] || { printf '必须以普通用户执行\n' >&2; exit 2; }
inputs="${NIDO_TEST_PROXY_RELEASE_INPUTS:?provide a verified public fixture directory}"
output="${NIDO_TEST_PROXY_RELEASE_OUTPUT:?provide a new evidence directory}"
version="${NIDO_TEST_PROXY_RELEASE_VERSION:?provide the exact stable release version}"
previous="${NIDO_TEST_PROXY_PREVIOUS_VERSION:-}"
socks="${NIDO_TEST_PROXY_RELEASE_SOCKS:-}"
stable='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
[[ "$version" =~ $stable ]] || { printf '版本必须是稳定 X.Y.Z\n' >&2; exit 2; }
[[ "$inputs" == /* && -d "$inputs" && ! -L "$inputs" ]] || exit 2
[[ "$output" == /* && ! -e "$output" && ! -L "$output" ]] || { printf '证据目录必须为不存在的绝对路径\n' >&2; exit 2; }
if [[ -n "$socks" ]]; then
  [[ "$socks" =~ ^socks5h://127\.0\.0\.1:([1-9][0-9]{0,4})$ ]] && ((10#${BASH_REMATCH[1]} <= 65535)) || { printf '仅允许本轮专用 IPv4 回环 SOCKS 端口\n' >&2; exit 2; }
fi
for tool in bwrap timeout sha256sum tar file readlink cmp diff find curl awk wc; do
  command -v "$tool" >/dev/null || { printf '缺少已有命令：%s\n' "$tool" >&2; exit 2; }
done
awk_tool="$(readlink -f "$(command -v awk)")"
[[ "$awk_tool" == /usr/* && -f "$awk_tool" && -x "$awk_tool" ]] || exit 2
[[ -x /usr/bin/curl && -d /etc/ssl && -f /etc/resolv.conf && -f /etc/hosts ]] || exit 2
names=(install.sh nido-linux-amd64.tar.gz nido-linux-amd64.tar.gz.sha256 fake-mihomo proxy-quickstart-cli-isolation.sh)
if [[ -n "$previous" || -e "$inputs/nido-previous.tar.gz" || -L "$inputs/nido-previous.tar.gz" ]]; then
  [[ "$previous" =~ $stable && -f "$inputs/nido-previous.tar.gz" && ! -L "$inputs/nido-previous.tar.gz" ]] || { printf '旧包与精确旧版本必须同时提供\n' >&2; exit 2; }
  [[ "$previous" != "$version" ]] || { printf '旧版本必须与受验版本不同\n' >&2; exit 2; }
  names+=(nido-previous.tar.gz)
fi
for name in "${names[@]}"; do
  [[ -f "$inputs/$name" && ! -L "$inputs/$name" && -s "$inputs/$name" ]] || { printf '缺少普通已核验输入：%s\n' "$name" >&2; exit 2; }
  (( $(wc -c < "$inputs/$name") <= 268435456 )) || { printf '输入过大：%s\n' "$name" >&2; exit 2; }
done
expected="$(awk -v name=nido-linux-amd64.tar.gz 'NR == 1 && NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == name { value=$1; next } { bad=1 } END { if (NR != 1 || bad || value == "") exit 1; print value }' "$inputs/nido-linux-amd64.tar.gz.sha256")" || { printf '无效发行校验文件\n' >&2; exit 2; }
actual="$(sha256sum "$inputs/nido-linux-amd64.tar.gz")"; actual="${actual%% *}"
[[ "$actual" == "$expected" ]] || { printf '已核验包摘要不匹配\n' >&2; exit 2; }
checksum_digest="$(sha256sum "$inputs/nido-linux-amd64.tar.gz.sha256")"; checksum_digest="${checksum_digest%% *}"
for name in nido-linux-amd64.tar.gz nido-previous.tar.gz; do
  [[ -f "$inputs/$name" ]] || continue
  [[ "$(tar -tzf "$inputs/$name")" == nido ]] || { printf '发行包必须仅含 nido：%s\n' "$name" >&2; exit 2; }
done

umask 077
mkdir "$output"
output="$(cd "$output" && pwd -P)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/nido-proxy-public-release.XXXXXX")"
scratch="$(cd "$scratch" && pwd -P)"
cleanup() {
  result=$?
  printf 'exit_status=%s\n' "$result" > "$output/result.txt"
  if [[ -d "$scratch/work/evidence" ]]; then
    find "$scratch/work/evidence" -maxdepth 1 -type f \( -name '*.json' -o -name '*.txt' -o -name '*.log' -o -name '*.sha256' \) -exec cp -- {} "$output/" \; || true
  fi
  rm -rf -- "$scratch"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir "$scratch/inputs" "$scratch/work"
for name in "${names[@]}"; do cp -- "$inputs/$name" "$scratch/inputs/$name"; done
(cd "$scratch/inputs" && sha256sum ./*) > "$output/inputs.sha256"
printf 'host_uid=%s\nhost_kernel=%s\nhost_machine=%s\nnido_version=%s\nprevious_version=%s\nnetwork=shared; genuine HTTPS downloads only\nprivate_home=true\nreal_mihomo=false\n' "$(id -u)" "$(uname -r)" "$(uname -m)" "$version" "${previous:-not-requested}" > "$output/environment.txt"

cat > "$scratch/runner.sh" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
umask 077
[[ "$(id -u)" != 0 && "$(uname -m)" == x86_64 ]] || exit 1
[[ ! -e /sys && ! -e /run && ! -e /home && ! -e /root && ! -e /dev/nvidiactl && ! -e /dev/dri ]] || exit 1
mkdir -p /work/evidence /work/guard "$HOME/.codex/sessions" "$HOME/.config/mihomo" /work/before /work/package
printf 'namespace_uid=%s\n' "$(id -u)" > /work/evidence/namespace.txt
for ns in mnt pid user; do printf '%s=%s\n' "$ns" "$(readlink "/proc/self/ns/$ns")" >> /work/evidence/namespace.txt; done
for tool in wget systemctl supervisorctl sudo codex mihomo gds git docker podman service rc-service launchctl apt apt-get yum dnf npm pip; do
  cat > "/work/guard/$tool" <<'FORBIDDEN'
#!/bin/sh
printf '%s\n' "$0" >> /work/evidence/forbidden-commands.log
exit 98
FORBIDDEN
  chmod 0700 "/work/guard/$tool"
done

# Only the official exact-version/latest package and checksum are accepted.
# This bridge fetches genuine bytes and then compares them with the independently
# verified inputs. It never substitutes fixture content for a public response.
cat > /work/guard/curl <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
original=("$@")
deny() { printf 'rejected bootstrap curl arguments or response\n' >> /work/evidence/forbidden-commands.log; exit 98; }
prefix=(-q --http1.1 --fail --silent --show-error --location --proto '=https' --proto-redir '=https'
  --tlsv1.2 --retry 2 --connect-timeout 15 --max-time 300 --max-filesize 268435456 --output)
[[ ${#original[@]} == 22 && "${original[*]:0:20}" == "${prefix[*]}" ]] || deny
output_path="${original[20]}"
source_url="${original[21]}"
[[ "$output_path" =~ ^/tmp/nido-install\.[A-Za-z0-9]+/(nido-linux-amd64\.tar\.gz|checksum)$ ]] || deny
case "$source_url" in
  https://github.com/MiracleHYH/CDEToolkit/releases/download/v"$NIDO_TEST_PROXY_RELEASE_VERSION"/nido-linux-amd64.tar.gz|https://github.com/MiracleHYH/CDEToolkit/releases/download/v"$NIDO_TEST_PROXY_RELEASE_VERSION"/nido-linux-amd64.tar.gz.sha256|https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/nido-linux-amd64.tar.gz|https://github.com/MiracleHYH/CDEToolkit/releases/latest/download/nido-linux-amd64.tar.gz.sha256) ;;
  *) deny ;;
esac
command=(/usr/bin/curl -q)
[[ -z "$NIDO_TEST_PROXY_RELEASE_SOCKS" ]] || command+=(--proxy "$NIDO_TEST_PROXY_RELEASE_SOCKS")
command+=("${original[@]:1}")
"${command[@]}"
case "$source_url" in
  *.sha256) cmp /inputs/nido-linux-amd64.tar.gz.sha256 "$output_path" || deny ;;
  *) cmp /inputs/nido-linux-amd64.tar.gz "$output_path" || deny ;;
esac
printf '%s\n' "$source_url" >> /work/evidence/public-downloads.log
sha256sum "$output_path" >> /work/evidence/public-downloads.sha256
CURL
chmod 0700 /work/guard/curl

printf 'AUTH-SENTINEL\n' > "$HOME/.codex/auth.json"
printf 'SESSION-SENTINEL\n' > "$HOME/.codex/sessions/one.jsonl"
printf 'DATABASE-SENTINEL\n' > "$HOME/.codex/state.sqlite"
printf 'model = "preserved"\n' > "$HOME/.codex/config.toml"
printf 'HTTPS_PROXY=http://user-proxy.invalid:7890\nKEEP=value\n' > "$HOME/.codex/.env"
printf 'secret: EXTERNAL-PROXY-SENTINEL\n' > "$HOME/.config/mihomo/config.yaml"
printf '# USER-SHELL-SENTINEL\n' > "$HOME/.bashrc"
printf '# USER-ZSH-SENTINEL\n' > "$HOME/.zshrc"
printf '[user]\n name = Preserved User\n' > "$HOME/.gitconfig"
cp -R "$HOME/.codex" /work/before/codex
cp -R "$HOME/.config" /work/before/config
for name in .bashrc .zshrc .gitconfig; do cp "$HOME/$name" "/work/before/$name"; done
preserved() {
  diff -r /work/before/codex "$HOME/.codex"
  diff -r /work/before/config "$HOME/.config"
  for name in .bashrc .zshrc .gitconfig; do cmp "/work/before/$name" "$HOME/$name"; done
  [[ ! -e /work/evidence/forbidden-commands.log ]] || exit 1
}
tar -xOzf /inputs/nido-linux-amd64.tar.gz nido > /work/package/nido
chmod 0700 /work/package/nido
file /work/package/nido | tee /work/evidence/package-type.txt | grep -Eq 'ELF 64-bit.*x86-64'
[[ "$(/work/package/nido version)" == "nido $NIDO_TEST_PROXY_RELEASE_VERSION" ]] || exit 1
file /inputs/fake-mihomo | grep -Eq 'ELF 64-bit.*x86-64'
sha256sum /work/package/nido > /work/evidence/package-cli.sha256
assert_proxy_missing() {
  local status_file="$1"
  grep -Fq '"program":"missing"' "$status_file"
  grep -Fq '"config":"missing"' "$status_file"
  grep -Fq '"runtime":{"state":"stopped","config_changed":false,"listeners":false,' "$status_file"
}
assert_environment() {
  local root="$1" label="$2"
  env -i HOME="$HOME" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; command -v nido' fixture "$root/env.sh" > "/work/evidence/$label-environment.txt"
  [[ "$(cat "/work/evidence/$label-environment.txt")" == "$root/bin/nido" ]] || exit 1
}
check_install() {
  local root="$1" label="$2"
  [[ "$("$root/bin/nido" version)" == "nido $NIDO_TEST_PROXY_RELEASE_VERSION" ]] || exit 1
  cmp /work/package/nido "$root/bin/nido"
  "$root/bin/nido" --root "$root" --help > "/work/evidence/$label-help.txt"
  grep -Fq 'proxy bind --tool codex|git' "/work/evidence/$label-help.txt"
  "$root/bin/nido" --root "$root" proxy status --json > "/work/evidence/$label-status.json"
  assert_proxy_missing "/work/evidence/$label-status.json"
  assert_environment "$root" "$label"
  "$root/bin/nido" --root "$root" proxy endpoints --json > "/work/evidence/$label-endpoints.json"
  grep -Fxq '[]' "/work/evidence/$label-endpoints.json"
  "$root/bin/nido" --root "$root" proxy install --dry-run --json > "/work/evidence/$label-preview.json"
  [[ ! -e "$root/proxy" && ! -e "$root/codex" && ! -e "$root/gds" ]] || exit 1
  preserved
}
sh /inputs/install.sh --yes > /work/evidence/install-first.log
root="$HOME/.nido"
check_install "$root" first
printf '\n# USER-NIDO-CONFIG-COMMENT\n' >> "$root/config.toml"
printf 'USER-NIDO-DATA\n' > "$root/user-data.txt"
cp "$root/config.toml" /work/before/nido-config.toml
cp "$root/env.sh" /work/before/nido-env.sh
cp "$root/user-data.txt" /work/before/nido-user-data.txt
sh /inputs/install.sh --version "$NIDO_TEST_PROXY_RELEASE_VERSION" --root "$root" --yes > /work/evidence/install-repeat.log
check_install "$root" repeat
cmp /work/before/nido-config.toml "$root/config.toml"
cmp /work/before/nido-env.sh "$root/env.sh"
cmp /work/before/nido-user-data.txt "$root/user-data.txt"
sh /inputs/install.sh --root "$root" --yes > /work/evidence/install-latest.log
check_install "$root" latest
cmp /work/before/nido-config.toml "$root/config.toml"
cmp /work/before/nido-env.sh "$root/env.sh"
cmp /work/before/nido-user-data.txt "$root/user-data.txt"
custom='/work/custom software/nido'
sh /inputs/install.sh --version "$NIDO_TEST_PROXY_RELEASE_VERSION" --root "$custom" --yes > /work/evidence/install-custom.log
check_install "$custom" custom
downloads=8
upgrade=false
if [[ -n "$NIDO_TEST_PROXY_PREVIOUS_VERSION" ]]; then
  tar -xOzf /inputs/nido-previous.tar.gz nido > /work/package/previous
  chmod 0700 /work/package/previous
  file /work/package/previous | grep -Eq 'ELF 64-bit.*x86-64'
  [[ "$(/work/package/previous version)" == "nido $NIDO_TEST_PROXY_PREVIOUS_VERSION" ]] || exit 1
  oldroot=/work/upgrade/nido
  /work/package/previous --root "$oldroot" init --yes > /work/evidence/old-init.log
  printf '\n# USER-UPGRADE-CONFIG-COMMENT\n' >> "$oldroot/config.toml"
  printf 'USER-UPGRADE-DATA\n' > "$oldroot/user-data.txt"
  cp "$oldroot/config.toml" /work/before/old-config.toml
  cp "$oldroot/env.sh" /work/before/old-env.sh
  cp "$oldroot/user-data.txt" /work/before/old-user-data.txt
  sh /inputs/install.sh --version "$NIDO_TEST_PROXY_RELEASE_VERSION" --root "$oldroot" --yes > /work/evidence/install-upgrade.log
  check_install "$oldroot" upgrade
  cmp /work/before/old-config.toml "$oldroot/config.toml"
  cmp /work/before/old-env.sh "$oldroot/env.sh"
  cmp /work/before/old-user-data.txt "$oldroot/user-data.txt"
  downloads=10
  upgrade=true
fi
[[ "$(wc -l < /work/evidence/public-downloads.log)" == "$downloads" ]] || exit 1
bash /inputs/proxy-quickstart-cli-isolation.sh "$root/bin/nido" /inputs/fake-mihomo > /work/evidence/quickstart-cli.log 2>&1
grep -Fxq 'Proxy quick start CLI 隔离验证通过' /work/evidence/quickstart-cli.log
preserved
for entry in /proc/[0-9]*/exe; do
  executable="$(readlink "$entry" 2>/dev/null || true)"
  case "$executable" in /work/package/*|*/bin/nido|/inputs/fake-mihomo) printf '仍有测试进程：%s\n' "$executable" >&2; exit 1 ;; esac
done
printf 'All test CLI processes exited; no real Mihomo or system service was started.\n' > /work/evidence/processes-final.txt
printf '{"version":"%s","public_downloads":%s,"previous_upgrade":%s,"private_home":true,"preserved_sentinels":true,"real_mihomo":false,"system_service":false}\n' "$NIDO_TEST_PROXY_RELEASE_VERSION" "$downloads" "$upgrade" > /work/evidence/public-result.json
printf 'PASS: official Nido package, public install, editable quick start and category/tool binding; inert core only.\n'
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
  --setenv NIDO_TEST_PROXY_RELEASE_VERSION "$version"
  --setenv NIDO_TEST_PROXY_PREVIOUS_VERSION "$previous"
  --setenv NIDO_TEST_PROXY_RELEASE_SOCKS "$socks")
timeout --kill-after=5s 30m bwrap "${flags[@]}" /bin/bash /runner.sh 2>&1 | tee "$output/run.log"
downloads=8
upgrade=false
[[ -z "$previous" ]] || { downloads=10; upgrade=true; }
result_json='{"version":"'"$version"'","public_downloads":'"$downloads"',"previous_upgrade":'"$upgrade"',"private_home":true,"preserved_sentinels":true,"real_mihomo":false,"system_service":false}'
[[ -f "$scratch/work/evidence/public-result.json" && "$(cat "$scratch/work/evidence/public-result.json")" == "$result_json" ]] || { printf '缺少精确验收结果\n' >&2; exit 1; }
base=https://github.com/MiracleHYH/CDEToolkit/releases
for route in latest/download "download/v$version" latest/download "download/v$version"; do
  printf '%s/%s/nido-linux-amd64.tar.gz\n%s/%s/nido-linux-amd64.tar.gz.sha256\n' "$base" "$route" "$base" "$route"
done > "$scratch/expected-downloads.txt"
if [[ -n "$previous" ]]; then
  printf '%s/download/v%s/nido-linux-amd64.tar.gz\n%s/download/v%s/nido-linux-amd64.tar.gz.sha256\n' "$base" "$version" "$base" "$version" >> "$scratch/expected-downloads.txt"
fi
cmp "$scratch/expected-downloads.txt" "$scratch/work/evidence/public-downloads.log"
awk -v archive="$expected" -v checksum="$checksum_digest" -v count="$downloads" '
  NF != 2 { bad=1 }
  NR % 2 == 1 { if ($1 != archive || $2 !~ /^\/tmp\/nido-install\.[A-Za-z0-9]+\/nido-linux-amd64\.tar\.gz$/) bad=1 }
  NR % 2 == 0 { if ($1 != checksum || $2 !~ /^\/tmp\/nido-install\.[A-Za-z0-9]+\/checksum$/) bad=1 }
  END { if (NR != count || bad) exit 1 }
' "$scratch/work/evidence/public-downloads.sha256"
grep -Fxq 'Proxy quick start CLI 隔离验证通过' "$scratch/work/evidence/quickstart-cli.log"
grep -Fxq 'PASS: official Nido package, public install, editable quick start and category/tool binding; inert core only.' "$output/run.log"
[[ ! -e "$scratch/work/evidence/forbidden-commands.log" ]] || exit 1
printf 'PASS: 普通用户 Linux amd64 公网发行隔离验证；证据：%s\n' "$output"
