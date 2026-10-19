#!/usr/bin/env bash
set -euo pipefail

# All upstreams are local read-only fixtures. No real Conda/Python or network.
NIDO_TEST_BINARY="${1:?provide the built nido binary}"
[[ "$NIDO_TEST_BINARY" = /* && -x "$NIDO_TEST_BINARY" ]]
NIDO_TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/nido-conda-cli.XXXXXX")"
NIDO_TEST_ROOT="$(cd "$NIDO_TEST_ROOT" && pwd -P)"
trap 'rm -rf -- "$NIDO_TEST_ROOT"' EXIT
export HOME="$NIDO_TEST_ROOT/user" TMPDIR="$NIDO_TEST_ROOT/tmp"
export PATH="$NIDO_TEST_ROOT/fixtures:/usr/bin:/bin"
unset NIDO_HOME CDET_PREFIX CDET_STORE CDET_HOME STK_HOME CODEX_HOME CODEX_SQLITE_HOME CODEX_INSTALL_DIR
unset CONDARC CONDA_EXE XDG_CONFIG_HOME XDG_CONFIG_DIRS XDG_CACHE_HOME XDG_DATA_HOME XDG_DATA_DIRS
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
while IFS= read -r nido_inherited_name; do
  case "$nido_inherited_name" in CONDA_*|PIP_*|_NIDO_CONDA_*) unset "$nido_inherited_name" ;; esac
done < <(compgen -e)
unset nido_inherited_name

root="$NIDO_TEST_ROOT/software path/nido"
export NIDO_FIXTURE_CALLS="$NIDO_TEST_ROOT/upstream-calls"
export NIDO_FIXTURE_FORBIDDEN="$NIDO_TEST_ROOT/forbidden-calls"
mkdir -p "$HOME/.config/pip" "$HOME/.pip" "$NIDO_TEST_ROOT/fixtures" "$TMPDIR" "$NIDO_TEST_ROOT/public-output"
cat > "$HOME/.condarc" <<'CONFIG'
# USER-CONDA-HEADER
channels:
  - conda-forge
  - defaults
  - https://private.invalid/team
channel_priority: strict
ssl_verify: true
# USER-CONDA-FOOTER
CONFIG
cp "$HOME/.condarc" "$HOME/custom.yaml"
cat > "$HOME/custom-pip.conf" <<'CONFIG'
# USER-PIP-HEADER
[global]
index-url = https://original-global.invalid/simple
cert = /user/certificate.pem
# USER-PIP-CACHE-COMMENT
cache-dir = /previous/pip-cache

[install]
index-url = https://original-install.invalid/simple
# USER-PIP-FOOTER
CONFIG
printf '[global]\ntimeout = 77\n' > "$HOME/.config/pip/pip.conf"
printf '[install]\ntimeout = 88\n' > "$HOME/.pip/pip.conf"
printf '# USER-BASH-SENTINEL\n' > "$HOME/.bashrc"
printf '# USER-ZSH-SENTINEL\n' > "$HOME/.zshrc"
chmod 0600 "$HOME/.condarc" "$HOME/custom.yaml"
chmod 0640 "$HOME/custom-pip.conf"
# The fixture baseline contains credentials from another config source. Its
# explicit Conda destination must be private before restoring those fields.
chmod 0600 "$HOME/custom.yaml"
cp -R "$HOME" "$NIDO_TEST_ROOT/user.before"
export CONDARC="$HOME/custom.yaml" PIP_CONFIG_FILE="$HOME/custom-pip.conf"

for tool in python3 pip pip3 curl wget sudo systemctl; do
  cat > "$NIDO_TEST_ROOT/fixtures/$tool" <<'FIXTURE'
#!/bin/sh
printf '%s\n' "$0 $*" >> "$NIDO_FIXTURE_FORBIDDEN"
exit 98
FIXTURE
  chmod 0755 "$NIDO_TEST_ROOT/fixtures/$tool"
done
cat > "$NIDO_TEST_ROOT/fixtures/conda" <<'FIXTURE'
#!/bin/sh
set -eu
printf 'conda %s\n' "$*" >> "$NIDO_FIXTURE_CALLS"
case "$*" in
  'config --show channels default_channels custom_channels custom_multichannels channel_alias migrated_channel_aliases migrated_custom_channels --json')
    [ "${NIDO_FIXTURE_FAIL_CAPTURE-}" != 1 ] || exit 93
    fixture_channels='"conda-forge","defaults","https://private.invalid/team"'
    if [ "${NIDO_FIXTURE_FULL_URL_CHANNELS-}" = 1 ]; then
      fixture_channels='"https://original.invalid/cloud/conda-forge","defaults","https://private.invalid/team"'
    fi
    cat <<JSON
{"channels":[$fixture_channels],"default_channels":["https://baseline:CONDA-BASELINE-SECRET@original.invalid/pkgs/main","https://original.invalid/pkgs/r","https://original.invalid/pkgs/msys2"],"custom_multichannels":{"defaults":["https://baseline:CONDA-BASELINE-SECRET@original.invalid/pkgs/main","https://original.invalid/pkgs/r","https://original.invalid/pkgs/msys2"]},"custom_channels":{"conda-forge":"https://original.invalid/cloud/conda-forge","bioconda":"https://original.invalid/cloud/bioconda","private":"https://private.invalid/cloud/private"},"channel_alias":"https://original.invalid/cloud","migrated_channel_aliases":[],"migrated_custom_channels":{}}
JSON
    ;;
  *'--show'*'envs_dirs'*'pkgs_dirs'*'--json')
    # Validate only: native directory changes must already be in the user file.
    native_file="$HOME/.condarc"
    envs=$(sed -n '/^envs_dirs:/{n;s/^[[:space:]]*-[[:space:]]*//;p;}' "$native_file")
    pkgs=$(sed -n '/^pkgs_dirs:/{n;s/^[[:space:]]*-[[:space:]]*//;p;}' "$native_file")
    if grep -q mirrors.tuna.tsinghua.edu.cn "${CONDARC:?}"; then
      base=https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs
      cloud=https://mirrors.tuna.tsinghua.edu.cn/anaconda/cloud
    else
      base=https://repo.anaconda.com/pkgs
      cloud=https://conda.anaconda.org
    fi
    cat <<JSON
{"channels":["conda-forge","defaults","https://private.invalid/team"],"default_channels":["$base/main","$base/r","$base/msys2"],"custom_multichannels":{"defaults":["$base/main","$base/r","$base/msys2"]},"custom_channels":{"conda-forge":"$cloud/conda-forge","bioconda":"$cloud/bioconda","private":"https://private.invalid/cloud/private"},"channel_alias":"https://original.invalid/cloud","migrated_channel_aliases":[],"migrated_custom_channels":{},"envs_dirs":["$envs"],"pkgs_dirs":["$pkgs"]}
JSON
    ;;
  *) exit 96 ;;
esac
FIXTURE
cat > "$NIDO_TEST_ROOT/fixtures/python" <<'FIXTURE'
#!/bin/sh
set -eu
printf 'python %s\n' "$*" >> "$NIDO_FIXTURE_CALLS"
case "$*" in
  '-m pip config list')
    [ "${NIDO_FIXTURE_FAIL_CAPTURE-}" != 1 ] || exit 93
    [ "${NIDO_FIXTURE_FAIL_PIP_CAPTURE-}" != 1 ] || exit 93
    printf "global.index-url='https://original-global.invalid/simple'\n"
    printf "install.index-url='https://baseline:PIP-BASELINE-SECRET@original-install.invalid/simple'\n"
    if [ "${PIP_INDEX_URL+x}" = x ]; then
      printf ":env:.index-url='%s'\n" "$PIP_INDEX_URL"
    fi
    if [ "${NIDO_FIXTURE_PIP_EXTRA-}" = 1 ]; then
      printf "global.extra-index-url='https://secondary.invalid/simple'\n"
    fi
    ;;
  '-m pip cache dir')
    sed -n 's/^cache-dir[[:space:]]*=[[:space:]]*//p' "${PIP_CONFIG_FILE:?}"
    ;;
  *) exit 96 ;;
esac
FIXTURE
chmod 0755 "$NIDO_TEST_ROOT/fixtures/conda" "$NIDO_TEST_ROOT/fixtures/python"

run_nido() {
  local nido_result nido_output
  nido_output=$(mktemp "$NIDO_TEST_ROOT/public-output/call.XXXXXX")
  if "$NIDO_TEST_BINARY" --root "$root" "$@" > "$nido_output" 2>&1; then
    nido_result=0
  else
    nido_result=$?
  fi
  cat "$nido_output"
  return "$nido_result"
}
expect_failure() {
  if run_nido "$@" > "$NIDO_TEST_ROOT/expected-failure.log"; then
    printf 'unexpected success: %s\n' "$*" >&2
    exit 1
  fi
}
environment_value() {
  env -i HOME="$HOME" PATH=/usr/bin:/bin /bin/sh -c '. "$1"; case "$2" in CONDARC) printf "%s" "${CONDARC-}" ;; PIP_INDEX_URL) printf "%s" "${PIP_INDEX_URL-}" ;; *) exit 96 ;; esac' fixture "$root/env.sh" "$1"
}
mirror_file() {
  run_nido conda status --json | sed -n 's/.*"mirror_file":"\([^"]*\)".*/\1/p'
}
file_mode() {
  if [[ "$(uname -s)" = Darwin ]]; then
    /usr/bin/stat -f %Lp "$1"
  else
    stat -c %a "$1"
  fi
}

# Local inspection and init never query upstream tools, even when fixtures exist.
run_nido conda status --json > "$NIDO_TEST_ROOT/before.json"
run_nido conda mirrors --json > "$NIDO_TEST_ROOT/mirrors-before.json"
[[ ! -e "$root" && ! -e "$NIDO_FIXTURE_CALLS" ]]
run_nido init --yes > "$NIDO_TEST_ROOT/init.log"
run_nido conda status > "$NIDO_TEST_ROOT/status.log"
run_nido conda show --json > "$NIDO_TEST_ROOT/show.json"
run_nido conda mirrors > "$NIDO_TEST_ROOT/mirrors.log"
[[ ! -e "$NIDO_FIXTURE_CALLS" && ! -e "$root/conda" ]]

# A failed first capture cannot publish a partial baseline or switch mirrors.
cp -R "$root" "$NIDO_TEST_ROOT/capture-failure.before"
export NIDO_FIXTURE_FAIL_CAPTURE=1
expect_failure conda setup all --yes
expect_failure conda mirror pip tuna --yes
diff -r "$NIDO_TEST_ROOT/capture-failure.before" "$root"
unset NIDO_FIXTURE_FAIL_CAPTURE
export NIDO_FIXTURE_FAIL_PIP_CAPTURE=1
expect_failure conda setup all --yes
diff -r "$NIDO_TEST_ROOT/capture-failure.before" "$root"
unset NIDO_FIXTURE_FAIL_PIP_CAPTURE
run_nido conda setup all --dry-run > "$NIDO_TEST_ROOT/setup-preview.log"
diff -r "$NIDO_TEST_ROOT/capture-failure.before" "$root"

# Existing defaults are never reread; all adds only the missing tool.
run_nido conda setup conda --yes > "$NIDO_TEST_ROOT/setup-conda.log"
cp "$NIDO_FIXTURE_CALLS" "$NIDO_TEST_ROOT/calls.after-conda"
export NIDO_FIXTURE_FAIL_CAPTURE=1
run_nido conda setup conda --yes > "$NIDO_TEST_ROOT/setup-repeat.log"
cmp "$NIDO_FIXTURE_CALLS" "$NIDO_TEST_ROOT/calls.after-conda"
unset NIDO_FIXTURE_FAIL_CAPTURE
run_nido conda setup --yes > "$NIDO_TEST_ROOT/setup-all.log"
cp "$NIDO_FIXTURE_CALLS" "$NIDO_TEST_ROOT/calls.after-all"
run_nido conda setup all --yes > "$NIDO_TEST_ROOT/setup-all-repeat.log"
cmp "$NIDO_FIXTURE_CALLS" "$NIDO_TEST_ROOT/calls.after-all"
run_nido conda mirrors --json > "$NIDO_TEST_ROOT/mirrors-captured.json"
grep -Fq default "$NIDO_TEST_ROOT/mirrors-captured.json"

# Dry runs do not modify Nido, native files, or create data directories.
cp -R "$root" "$NIDO_TEST_ROOT/dry.before"
cp "$HOME/custom.yaml" "$NIDO_TEST_ROOT/dry-conda.before"
cp "$HOME/custom-pip.conf" "$NIDO_TEST_ROOT/dry-pip.before"
run_nido conda mirror conda tuna --dry-run > "$NIDO_TEST_ROOT/dry-conda.log"
run_nido conda mirror pip ustc --dry-run > "$NIDO_TEST_ROOT/dry-pip.log"
run_nido conda envs "$HOME/not-created/envs" --dry-run > "$NIDO_TEST_ROOT/dry-envs.log"
run_nido conda cache conda "$HOME/not-created/pkgs" --dry-run > "$NIDO_TEST_ROOT/dry-pkgs.log"
run_nido conda cache pip "$HOME/not-created/pip" --dry-run > "$NIDO_TEST_ROOT/dry-cache.log"
run_nido conda reset --dry-run > "$NIDO_TEST_ROOT/dry-reset.log"
diff -r "$NIDO_TEST_ROOT/dry.before" "$root"
cmp "$NIDO_TEST_ROOT/dry-conda.before" "$HOME/custom.yaml"
cmp "$NIDO_TEST_ROOT/dry-pip.before" "$HOME/custom-pip.conf"
[[ ! -e "$HOME/not-created" ]]

# Before choosing a mirror, native Conda changes respect the explicit CONDARC.
run_nido conda envs "$HOME/custom-envs" --yes > "$NIDO_TEST_ROOT/custom-envs.log"
grep -Fq "$HOME/custom-envs" "$HOME/custom.yaml"
cmp "$HOME/.condarc" "$NIDO_TEST_ROOT/user.before/.condarc"
[[ "$(file_mode "$HOME/custom.yaml")" = 600 ]]

# Default survives process/Shell changes and does not mean official or off.
run_nido conda mirror conda default --yes > "$NIDO_TEST_ROOT/default-conda.log"
run_nido conda mirror pip default --yes > "$NIDO_TEST_ROOT/default-pip.log"
default_overlay="$(mirror_file)"
[[ "$(environment_value CONDARC)" = "$HOME/custom.yaml" ]]
[[ "$default_overlay" = "$root"/conda/mirror-*.yaml && -f "$default_overlay" ]]
grep -Fq CONDA-BASELINE-SECRET "$default_overlay"
[[ "$(environment_value PIP_INDEX_URL)" = https://baseline:PIP-BASELINE-SECRET@original-install.invalid/simple ]]
cp "$default_overlay" "$NIDO_TEST_ROOT/default-overlay.before"
run_nido conda status --json > "$NIDO_TEST_ROOT/default-status.json"
run_nido conda show > "$NIDO_TEST_ROOT/default-show.log"
run_nido conda mirrors --json > "$NIDO_TEST_ROOT/default-mirrors.json"
run_nido conda mirror conda default --dry-run > "$NIDO_TEST_ROOT/default-conda-preview.json"
run_nido conda mirror pip default --dry-run > "$NIDO_TEST_ROOT/default-pip-preview.json"

# Aliases choose official; built-in mirrors retain deterministic overlay paths.
run_nido conda mirror conda conda --yes > "$NIDO_TEST_ROOT/alias-conda.log"
official_overlay="$(mirror_file)"
grep -Fq https://repo.anaconda.com/pkgs/main "$official_overlay"
run_nido conda mirror conda official --yes > "$NIDO_TEST_ROOT/official.log"
[[ "$(mirror_file)" = "$official_overlay" && "$(environment_value CONDARC)" = "$HOME/custom.yaml" ]]
run_nido conda mirror pip pypi --yes > "$NIDO_TEST_ROOT/alias-pypi.log"
[[ "$(environment_value PIP_INDEX_URL)" = https://pypi.org/simple ]]
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/tuna.log"
tuna_overlay="$(mirror_file)"
[[ "$tuna_overlay" != "$official_overlay" && "$tuna_overlay" != "$default_overlay" ]]
cp "$tuna_overlay" "$NIDO_TEST_ROOT/tuna.before"
run_nido conda mirror conda official --yes > "$NIDO_TEST_ROOT/switch-official.log"
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/tuna-repeat.log"
[[ "$(mirror_file)" = "$tuna_overlay" && "$(environment_value CONDARC)" = "$HOME/custom.yaml" ]]
cmp "$NIDO_TEST_ROOT/tuna.before" "$tuna_overlay"
cmp "$NIDO_TEST_ROOT/default-overlay.before" "$default_overlay"
for mirror in official tuna ustc https://packages.example.com/python/simple; do
  run_nido conda mirror pip "$mirror" --yes > "$NIDO_TEST_ROOT/pip-mirror.log"
  case "$mirror" in
    official) expected_index=https://pypi.org/simple ;;
    tuna) expected_index=https://pypi.tuna.tsinghua.edu.cn/simple ;;
    ustc) expected_index=https://mirrors.ustc.edu.cn/pypi/simple ;;
    *) expected_index="$mirror" ;;
  esac
  [[ "$(environment_value PIP_INDEX_URL)" = "$expected_index" ]]
done

# An Nido mirror selects the standard user Conda file and synchronizes the known
# original explicit file. Native paths never belong to immutable overlays.
cp "$HOME/.condarc" "$NIDO_TEST_ROOT/targets-standard.before"
cp "$HOME/custom.yaml" "$NIDO_TEST_ROOT/targets-custom.before"
run_nido conda envs "$HOME/preview-synced-envs" --dry-run --json > "$NIDO_TEST_ROOT/native-files-preview.json"
grep -Fq "$HOME/.condarc" "$NIDO_TEST_ROOT/native-files-preview.json"
grep -Fq "$HOME/custom.yaml" "$NIDO_TEST_ROOT/native-files-preview.json"
cmp "$NIDO_TEST_ROOT/targets-standard.before" "$HOME/.condarc"
cmp "$NIDO_TEST_ROOT/targets-custom.before" "$HOME/custom.yaml"
run_nido conda envs '~/native envs' --yes > "$NIDO_TEST_ROOT/native-envs.log"
run_nido conda cache conda "$HOME/native pkgs" --yes > "$NIDO_TEST_ROOT/native-pkgs.log"
run_nido conda cache pip "$HOME/native pip" --yes > "$NIDO_TEST_ROOT/native-pip.log"
grep -Fq "$HOME/native envs" "$HOME/.condarc"
grep -Fq "$HOME/native pkgs" "$HOME/.condarc"
grep -Fq "$HOME/native envs" "$HOME/custom.yaml"
grep -Fq "$HOME/native pkgs" "$HOME/custom.yaml"
grep -Fxq "cache-dir = $HOME/native pip" "$HOME/custom-pip.conf"
cmp "$NIDO_TEST_ROOT/tuna.before" "$tuna_overlay"
[[ "$(file_mode "$HOME/.condarc")" = 600 && "$(file_mode "$HOME/custom.yaml")" = 600 && "$(file_mode "$HOME/custom-pip.conf")" = 640 ]]
for line in '# USER-CONDA-HEADER' '# USER-CONDA-FOOTER' 'channel_priority: strict' 'ssl_verify: true' 'https://private.invalid/team'; do
  grep -Fq "$line" "$HOME/.condarc"
done
sed '/^cache-dir[[:space:]]*=/d' "$HOME/custom-pip.conf" > "$NIDO_TEST_ROOT/pip-remaining.after"
sed '/^cache-dir[[:space:]]*=/d' "$NIDO_TEST_ROOT/user.before/custom-pip.conf" > "$NIDO_TEST_ROOT/pip-remaining.before"
cmp "$NIDO_TEST_ROOT/pip-remaining.before" "$NIDO_TEST_ROOT/pip-remaining.after"
[[ ! -e "$HOME/native envs" && ! -e "$HOME/native pkgs" && ! -e "$HOME/native pip" ]]
if grep -Eq '^export (CONDA_ENVS_PATH|CONDA_PKGS_DIRS|PIP_CACHE_DIR)=' "$root/env.sh"; then
  printf 'native paths were exported as shell overrides\n' >&2
  exit 1
fi

# After source, the explicit file is remembered in the mirror restoration state.
# Both native targets still receive edits while CONDARC points at the overlay.
(
  . "$root/env.sh"
  [[ "$CONDARC" = "$HOME/custom.yaml" ]]
  run_nido conda envs "$HOME/loaded-mirror-envs" --yes > "$NIDO_TEST_ROOT/loaded-native-envs.log"
  grep -Fq "$HOME/loaded-mirror-envs" "$HOME/.condarc"
  grep -Fq "$HOME/loaded-mirror-envs" "$HOME/custom.yaml"
)
[[ ! -e "$HOME/loaded-mirror-envs" ]]

# Existing environment overrides are conflicts, not silently replaced settings.
cp "$root/config.toml" "$NIDO_TEST_ROOT/conflict-config.before"
cp "$HOME/.condarc" "$NIDO_TEST_ROOT/conflict-conda.before"
cp "$HOME/custom-pip.conf" "$NIDO_TEST_ROOT/conflict-pip.before"
export CONDA_ENVS_PATH="$HOME/override-envs"
expect_failure conda envs "$HOME/blocked-envs" --yes
unset CONDA_ENVS_PATH
export CONDA_PKGS_DIRS="$HOME/override-pkgs"
expect_failure conda cache conda "$HOME/blocked-pkgs" --yes
unset CONDA_PKGS_DIRS
export PIP_CACHE_DIR="$HOME/override-pip"
expect_failure conda cache pip "$HOME/blocked-pip" --yes
unset PIP_CACHE_DIR
cmp "$NIDO_TEST_ROOT/conflict-config.before" "$root/config.toml"
cmp "$NIDO_TEST_ROOT/conflict-conda.before" "$HOME/.condarc"
cmp "$NIDO_TEST_ROOT/conflict-pip.before" "$HOME/custom-pip.conf"

# A second unsafe target must fail preflight before changing the first file.
mv "$HOME/custom.yaml" "$HOME/custom.yaml.real"
ln -s "$HOME/custom.yaml.real" "$HOME/custom.yaml"
expect_failure conda envs "$HOME/unsafe-second-target" --yes
cmp "$NIDO_TEST_ROOT/conflict-config.before" "$root/config.toml"
cmp "$NIDO_TEST_ROOT/conflict-conda.before" "$HOME/.condarc"
rm "$HOME/custom.yaml"
mv "$HOME/custom.yaml.real" "$HOME/custom.yaml"

printf 'NOT-A-DIRECTORY\n' > "$NIDO_TEST_ROOT/plain-file"
expect_failure conda mirror conda ustc --yes
expect_failure conda mirror pip http://insecure.invalid/simple --yes
expect_failure conda mirror pip https://missing-simple.invalid --yes
expect_failure conda mirror pip https://user:secret@private.invalid/simple --yes
expect_failure conda envs relative/path --yes
expect_failure conda envs "$NIDO_TEST_ROOT/plain-file" --yes
expect_failure conda cache conda "$root/cache" --yes
expect_failure conda cache pip "$HOME/trailing-pip " --yes

# Mirror source tracks only mirror variables. Native storage values remain
# native and user storage environment variables are never saved/restored.
cp "$NIDO_FIXTURE_CALLS" "$NIDO_TEST_ROOT/calls.after-presets"
(
  export CONDARC="$HOME/custom.yaml" PIP_INDEX_URL=https://before.invalid/simple
  export CONDA_ENVS_PATH=user-envs CONDA_PKGS_DIRS=user-pkgs PIP_CACHE_DIR=user-pip
  . "$root/env.sh"
  . "$root/env.sh"
  [[ "$CONDARC" = "$HOME/custom.yaml" && "$CONDA_ENVS_PATH" = user-envs ]]
  [[ "$CONDA_PKGS_DIRS" = user-pkgs && "$PIP_CACHE_DIR" = user-pip ]]
  [[ "$PIP_CONFIG_FILE" = "$HOME/custom-pip.conf" ]]
  run_nido conda mirror conda off --yes > "$NIDO_TEST_ROOT/off-conda.log"
  run_nido conda mirror pip off --yes > "$NIDO_TEST_ROOT/off-pip.log"
  . "$root/env.sh"
  [[ "$CONDARC" = "$HOME/custom.yaml" && "$PIP_INDEX_URL" = https://before.invalid/simple ]]
  [[ "$CONDA_ENVS_PATH" = user-envs && "$CONDA_PKGS_DIRS" = user-pkgs && "$PIP_CACHE_DIR" = user-pip ]]
)

# Reset and per-path off preserve native values and the persisted default.
cp "$HOME/.condarc" "$NIDO_TEST_ROOT/native-conda.before-reset"
cp "$HOME/custom-pip.conf" "$NIDO_TEST_ROOT/native-pip.before-reset"
run_nido conda cache conda off --yes > "$NIDO_TEST_ROOT/off-pkgs.log"
run_nido conda cache pip off --yes > "$NIDO_TEST_ROOT/off-cache.log"
run_nido conda envs off --yes > "$NIDO_TEST_ROOT/off-envs.log"
run_nido conda reset --yes > "$NIDO_TEST_ROOT/reset.log"
cmp "$NIDO_TEST_ROOT/native-conda.before-reset" "$HOME/.condarc"
cmp "$NIDO_TEST_ROOT/native-pip.before-reset" "$HOME/custom-pip.conf"
run_nido conda mirror conda default --yes > "$NIDO_TEST_ROOT/default-after-reset.log"
run_nido conda mirror pip default --yes > "$NIDO_TEST_ROOT/pip-default-after-reset.log"
[[ "$(mirror_file)" = "$default_overlay" && "$(environment_value CONDARC)" = "$HOME/custom.yaml" ]]
[[ "$(environment_value PIP_INDEX_URL)" = https://baseline:PIP-BASELINE-SECRET@original-install.invalid/simple ]]
cmp "$NIDO_TEST_ROOT/default-overlay.before" "$default_overlay"
cmp "$NIDO_TEST_ROOT/calls.after-presets" "$NIDO_FIXTURE_CALLS"

# Explicit validation checks actual native paths using only fake upstreams.
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/validate-conda.log"
run_nido conda mirror pip tuna --yes > "$NIDO_TEST_ROOT/validate-pip.log"
run_nido conda envs "$HOME/validate-envs" --yes > "$NIDO_TEST_ROOT/validate-envs.log"
run_nido conda cache conda "$HOME/validate-pkgs" --yes > "$NIDO_TEST_ROOT/validate-pkgs.log"
run_nido conda cache pip "$HOME/validate-pip" --yes > "$NIDO_TEST_ROOT/validate-cache.log"
expect_failure conda validate
run_nido conda validate --dry-run > "$NIDO_TEST_ROOT/validate-preview.log"
(
  . "$root/env.sh"
  run_nido conda validate --json > "$NIDO_TEST_ROOT/validate.json"
  export NIDO_FIXTURE_PIP_EXTRA=1
  expect_failure conda validate
  grep -Fq extra-index-url "$NIDO_TEST_ROOT/expected-failure.log"
  unset NIDO_FIXTURE_PIP_EXTRA
  PIP_INDEX_URL=https://changed-by-user.invalid/simple
  run_nido conda reset --yes > "$NIDO_TEST_ROOT/final-reset.log"
  . "$root/env.sh"
  [[ "$PIP_INDEX_URL" = https://changed-by-user.invalid/simple ]]
)
grep -Fq "$HOME/validate-envs" "$HOME/.condarc"
grep -Fxq "cache-dir = $HOME/validate-pip" "$HOME/custom-pip.conf"
[[ ! -e "$HOME/validate-envs" && ! -e "$HOME/validate-pkgs" && ! -e "$HOME/validate-pip" ]]

# A separate root exercises automatic first-use capture without setup.
root="$NIDO_TEST_ROOT/automatic-nido"
run_nido init --yes > "$NIDO_TEST_ROOT/auto-init.log"
run_nido conda mirror pip tuna --yes > "$NIDO_TEST_ROOT/auto-pip.log"
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/auto-conda.log"
run_nido conda mirror pip default --yes > "$NIDO_TEST_ROOT/auto-default.log"
[[ "$(environment_value PIP_INDEX_URL)" = https://baseline:PIP-BASELINE-SECRET@original-install.invalid/simple ]]

# A baseline expressed as an original full source URL is rewritten natively on
# preset selection, then explicitly restored by default. Reset does not undo it.
root="$NIDO_TEST_ROOT/full-url-nido"
export CONDARC="$HOME/full-source.yaml" NIDO_FIXTURE_FULL_URL_CHANNELS=1
cat > "$CONDARC" <<'CONFIG'
# FULL-SOURCE-HEADER
channels:
  - https://original.invalid/cloud/conda-forge
  - defaults
  - https://private.invalid/team
channel_priority: strict
CONFIG
chmod 0600 "$CONDARC"
run_nido init --yes > "$NIDO_TEST_ROOT/full-init.log"
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/full-tuna.log"
for native_file in "$HOME/.condarc" "$HOME/full-source.yaml"; do
  grep -Eq '^[[:space:]]*-[[:space:]]+conda-forge$' "$native_file"
  if grep -Fq https://original.invalid/cloud/conda-forge "$native_file"; then
    printf 'original full source survived native preset selection\n' >&2
    exit 1
  fi
done
run_nido conda mirror conda default --yes > "$NIDO_TEST_ROOT/full-default.log"
grep -Fq https://original.invalid/cloud/conda-forge "$HOME/.condarc"
grep -Fq https://original.invalid/cloud/conda-forge "$HOME/full-source.yaml"
run_nido conda mirror conda tuna --yes > "$NIDO_TEST_ROOT/full-tuna-repeat.log"
cp "$HOME/full-source.yaml" "$NIDO_TEST_ROOT/full-before-reset"
run_nido conda reset --yes > "$NIDO_TEST_ROOT/full-reset.log"
cmp "$NIDO_TEST_ROOT/full-before-reset" "$HOME/full-source.yaml"
grep -Fq '# FULL-SOURCE-HEADER' "$HOME/full-source.yaml"
unset NIDO_FIXTURE_FULL_URL_CHANNELS

# Native mutations are the only expected changes to pre-existing user files.
for kept in .config/pip/pip.conf .pip/pip.conf .bashrc .zshrc; do
  cmp "$NIDO_TEST_ROOT/user.before/$kept" "$HOME/$kept"
done
if grep -R -E '(CONDA|PIP)-BASELINE-SECRET' "$NIDO_TEST_ROOT/public-output" >/dev/null; then
  printf 'default mirror credentials leaked into ordinary CLI output\n' >&2
  exit 1
fi
[[ ! -e "$NIDO_FIXTURE_FORBIDDEN" ]]
printf 'Conda / pip 持久 default 与原生目录 CLI 隔离验证通过\n'
