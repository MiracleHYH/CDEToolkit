GO ?= go
HOST_OS = $(shell "$(GO)" env GOOS)
HOST_ARCH = $(shell "$(GO)" env GOARCH)
TARGET_OS ?= $(HOST_OS)
TARGET_ARCH ?= $(HOST_ARCH)
VERSION := $(shell cat VERSION)
GDS_VERSION := $(shell cat GDS_VERSION)
BINARY = build/$(TARGET_OS)-$(TARGET_ARCH)/nido
PACKAGE = dist/nido-$(TARGET_OS)-$(TARGET_ARCH).tar.gz
RELEASE_PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test fmt-check check dist smoke release verify-release release-check gds-release gds-release-check gds-verify-release check-gds check-gds-linux-native check-gds-legacy build-gds check-proxy-real check-proxy-linux check-proxy-linux-race check-proxy-linux-native check-proxy-quickstart-linux-native check-proxy-quickstart-real-linux check-proxy-public-release check-nido-public-release
build:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) "$(GO)" build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w -X=github.com/MiracleHYH/CDEToolkit/internal/nido.Version=$(VERSION)' -o "$(BINARY)" ./cmd/nido

# Each release package contains only nido, never user data or legacy scripts.
dist: build
	"$(GO)" run ./cmd/nido-dist pack --binary "$(BINARY)" --platform "$(TARGET_OS)/$(TARGET_ARCH)"
	"$(GO)" run ./cmd/nido-dist verify --archive "$(PACKAGE)" --platform "$(TARGET_OS)/$(TARGET_ARCH)"

smoke: dist
	"$(GO)" run ./cmd/nido-dist verify --archive "$(PACKAGE)" --platform "$(TARGET_OS)/$(TARGET_ARCH)" --smoke

test:
	"$(GO)" test ./...

fmt-check:
	@nido_unformatted=$$("$(shell "$(GO)" env GOROOT)/bin/gofmt" -l cmd internal tests/fixtures/proxy-core); test -z "$$nido_unformatted" || { printf 'gofmt required:\n%s\n' "$$nido_unformatted"; exit 1; }
	sh -n scripts/install.sh
	sh -n scripts/gds-release-build.sh
	bash -n tests/proxy-linux-isolation.sh tests/proxy-linux-bwrap.sh tests/gds-linux-native-isolation.sh tests/gds-public-release-isolation.sh
	bash -n tests/proxy-quickstart-linux-native.sh tests/proxy-quickstart-cli-isolation.sh tests/proxy-quickstart-real-linux.sh
	bash -n tests/proxy-public-release-isolation.sh
	bash -n tests/conda-cli-isolation.sh
	bash -n tests/nido-public-release-isolation.sh tests/nido-public-release-inner.sh

check: fmt-check
	"$(GO)" vet ./...
	"$(GO)" test -race ./...
	$(MAKE) build TARGET_OS=$(HOST_OS) TARGET_ARCH=$(HOST_ARCH)
	bash tests/nido-cli-isolation.sh "$(CURDIR)/build/$(HOST_OS)-$(HOST_ARCH)/nido"
	bash tests/conda-cli-isolation.sh "$(CURDIR)/build/$(HOST_OS)-$(HOST_ARCH)/nido"
	@proxy_fixture_dir=$$(mktemp -d); trap 'rm -rf -- "$$proxy_fixture_dir"' EXIT; \
	CGO_ENABLED=0 "$(GO)" build -trimpath -buildvcs=false -o "$$proxy_fixture_dir/fake-mihomo" ./tests/fixtures/proxy-core && \
	bash tests/proxy-quickstart-cli-isolation.sh "$(CURDIR)/build/$(HOST_OS)-$(HOST_ARCH)/nido" "$$proxy_fixture_dir/fake-mihomo"

release:
	@set -e; for platform in $(RELEASE_PLATFORMS); do $(MAKE) dist TARGET_OS="$${platform%/*}" TARGET_ARCH="$${platform#*/}"; done
	"$(GO)" run ./cmd/nido-dist installer

verify-release:
	@set -e; for platform in $(RELEASE_PLATFORMS); do "$(GO)" run ./cmd/nido-dist verify --archive "dist/nido-$${platform%/*}-$${platform#*/}.tar.gz" --platform "$$platform"; done
	"$(GO)" run ./cmd/nido-dist installer --verify

# A tag is passed through the environment, never interpolated into shell code.
release-check:
	"$(GO)" run ./cmd/nido-dist check-tag --tag "$${NIDO_RELEASE_TAG:?set NIDO_RELEASE_TAG to vVERSION}" --repository .
	$(MAKE) check
	$(MAKE) release
	$(MAKE) verify-release
	"$(GO)" run ./cmd/nido-dist verify --archive "dist/nido-$(HOST_OS)-$(HOST_ARCH).tar.gz" --platform "$(HOST_OS)/$(HOST_ARCH)" --smoke

# Current GDS tests use fake drivers, clocks and temporary CPU subprocesses.
check-gds:
	"$(GO)" test -race -count=1 ./internal/gdsapi ./internal/gdsutil ./internal/gdsruntime/... ./internal/components/gds ./cmd/gds
	"$(GO)" test -race -count=1 ./internal/nido -run '^TestGDS'

# Ordinary native amd64 user; needs existing bwrap, SDK, GCC and offline vendor.
# NIDO_TEST_GDS_OUTPUT must name a fresh evidence directory. No host fallback.
check-gds-linux-native:
	GO="$(GO)" bash tests/gds-linux-native-isolation.sh

# Explicit historical test; this is not the Nido GDS backend.
check-gds-legacy:
	bash tests/gds-isolation.sh

# Native optional artifact; ordinary Nido builds remain CGO_ENABLED=0.
# Run on Linux amd64 with a C compiler, or provide an explicit cross CC.
build-gds:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOAMD64=v1 "$(GO)" build -mod=readonly -trimpath -buildvcs=false -buildmode=exe -ldflags='-s -w -X=main.version=$(GDS_VERSION)' -o build/gds-linux-amd64 ./cmd/gds

# Formal raw GDS assets use the current pinned Go SDK with the pinned Bullseye
# libc/compiler image. GDS_RELEASE_GOROOT must be a Linux amd64 SDK on macOS.
# Two clean, offline, CPU-only builds must be byte-identical before packaging.
gds-release:
	GO="$(GO)" sh scripts/gds-release-build.sh

gds-verify-release:
	"$(GO)" run ./cmd/nido-dist gds-verify --binary dist/gds-linux-amd64 --smoke

gds-release-check:
	"$(GO)" run ./cmd/nido-dist gds-check-tag --tag "$${GDS_RELEASE_TAG:?set GDS_RELEASE_TAG to gds-vVERSION}" --repository .
	$(MAKE) check-gds
	$(MAKE) gds-release
	$(MAKE) gds-verify-release

# Opt in with a previously verified standalone core. This target never downloads
# a program; the test runs only in temporary HOME/root and loopback fixtures.
# NIDO_TEST_MIHOMO_UPDATE optionally adds a separately verified upgrade core.
check-proxy-real:
	@test -n "$${NIDO_TEST_MIHOMO:-}" || { printf 'set NIDO_TEST_MIHOMO to an absolute verified Mihomo path\n' >&2; exit 1; }
	"$(GO)" test -count=1 -v ./internal/nido -run '^TestProxyRealMihomo(CrossVersion)?$$'

# Cross-build with the existing host Go and execute inside a disposable native
# Linux container. No host mounts, external network, core download, or daemon setup.
check-proxy-linux:
	GO="$(GO)" bash tests/proxy-linux-isolation.sh

# NIDO_TEST_LINUX_BIN_DIR contains static Linux nido/nido.test/proxy.test,
# all built from this checkout with -race and the engine's native architecture.
check-proxy-linux-race:
	GO="$(GO)" NIDO_TEST_LINUX_RACE=1 bash tests/proxy-linux-isolation.sh

# Run only on a native Linux host with existing bubblewrap and static inputs.
# Set NIDO_TEST_LINUX_RACE=1 for three prebuilt race programs; no host fallback.
check-proxy-linux-native:
	GO="$(GO)" bash tests/proxy-linux-bwrap.sh

# Existing ordinary-user Linux amd64 SDK/GCC/bwrap plus a vendored source tree;
# no real Mihomo, network downloads, system services or host user data.
check-proxy-quickstart-linux-native:
	bash tests/proxy-quickstart-linux-native.sh

# Explicit real-core quick start E2E; existing native static race artifacts only.
# Private filesystem/PID/network; no source, host HOME, libraries or downloads.
check-proxy-quickstart-real-linux:
	GO="$(GO)" bash tests/proxy-quickstart-real-linux.sh

# Explicit public HTTPS acceptance; verified assets, private HOME/PID namespace,
# and an inert core only. Never part of the offline check/release targets.
check-proxy-public-release:
	bash tests/proxy-public-release-isolation.sh

# Explicit public HTTPS acceptance against independently verified new and old
# releases; all installation, upgrade and Conda fixtures use temporary homes.
check-nido-public-release:
	bash tests/nido-public-release-isolation.sh
