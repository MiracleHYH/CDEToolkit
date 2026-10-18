#!/bin/sh
# Build only source files in a pinned compiler/libc image. No host GPU, network,
# service entry, user configuration, authentication or sessions are mounted.
set -eu

gds_go=${GO:-go}
gds_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$gds_root"
gds_sdk=${GDS_RELEASE_GOROOT:-$("$gds_go" env GOROOT)}
gds_image=public.ecr.aws/docker/library/golang@sha256:2015125fe4a3d97f9ba4ad55d458c2d1f025fdf9cb73d8f423a29e448d59da5e
gds_version=$(cat GDS_VERSION)
gds_go_version=$(cat .go-version)
case "$gds_sdk" in /*) ;; *) printf 'GDS_RELEASE_GOROOT must be an absolute Linux amd64 SDK path\n' >&2; exit 1 ;; esac
[ -x "$gds_sdk/bin/go" ]
"$gds_go" run ./cmd/nido-dist gds-check-tag --tag "gds-v$gds_version"
mkdir -p build
gds_stage=$(mktemp -d "$gds_root/build/gds-release.XXXXXX")
trap 'rm -rf "$gds_stage"' EXIT HUP INT TERM
mkdir "$gds_stage/source" "$gds_stage/first" "$gds_stage/second"
cp -R cmd internal "$gds_stage/source/"
cp go.mod go.sum "$gds_stage/source/"
GOPROXY=off GOSUMDB=off "$gds_go" mod vendor -o "$gds_stage/source/vendor"
for gds_attempt in first second; do
  docker run --rm --platform linux/amd64 --network none --read-only \
    --cap-drop ALL --security-opt no-new-privileges \
    --user "$(id -u):$(id -g)" \
    --tmpfs /tmp:rw,nosuid,nodev,mode=1777 \
    --mount "type=bind,src=$gds_stage/source,dst=/src,readonly" \
    --mount "type=bind,src=$gds_sdk,dst=/toolchain,readonly" \
    --mount "type=bind,src=$gds_stage/$gds_attempt,dst=/out" \
    --workdir /src \
    --env HOME=/tmp/home --env TMPDIR=/tmp --env GOCACHE=/tmp/cache \
    --env GOPATH=/tmp/go --env GOPROXY=off --env GOSUMDB=off \
    --env GOTOOLCHAIN=local --env GOFLAGS=-mod=vendor \
    --env CGO_ENABLED=1 --env GOOS=linux --env GOARCH=amd64 --env GOAMD64=v1 \
    --env CC=gcc --env LC_ALL=C --env TZ=UTC --env SOURCE_DATE_EPOCH=0 \
    --env "GDS_VERSION=$gds_version" --env "GDS_GO_VERSION=$gds_go_version" \
    "$gds_image" /bin/sh -ec '
      test "$(/toolchain/bin/go version)" = "go version go$GDS_GO_VERSION linux/amd64"
      test "$(getconf GNU_LIBC_VERSION)" = "glibc 2.31"
      /toolchain/bin/go build -trimpath -buildvcs=false -buildmode=exe \
        -ldflags="-s -w -X=main.version=$GDS_VERSION" -o /out/gds ./cmd/gds
      /out/gds --version --json > /out/metadata.json
    '
done
cmp "$gds_stage/first/gds" "$gds_stage/second/gds"
cmp "$gds_stage/first/metadata.json" "$gds_stage/second/metadata.json"
cat "$gds_stage/first/metadata.json"
"$gds_go" run ./cmd/nido-dist gds-pack --binary "$gds_stage/first/gds"
"$gds_go" run ./cmd/nido-dist gds-verify --binary dist/gds-linux-amd64
printf 'GDS release builds are byte-identical (Go %s, pinned glibc 2.31 image).\n' "$gds_go_version"
