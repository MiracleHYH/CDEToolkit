GO ?= go
HOST_OS = $(shell "$(GO)" env GOOS)
HOST_ARCH = $(shell "$(GO)" env GOARCH)
TARGET_OS ?= $(HOST_OS)
TARGET_ARCH ?= $(HOST_ARCH)
VERSION := $(shell cat VERSION)
BINARY = build/$(TARGET_OS)-$(TARGET_ARCH)/nido
PACKAGE = dist/nido-$(TARGET_OS)-$(TARGET_ARCH).tar.gz
RELEASE_PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test fmt-check check dist smoke release verify-release release-check check-gds check-proxy-real check-proxy-linux check-proxy-linux-race check-proxy-linux-native
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
	@nido_unformatted=$$("$(shell "$(GO)" env GOROOT)/bin/gofmt" -l cmd internal); test -z "$$nido_unformatted" || { printf 'gofmt required:\n%s\n' "$$nido_unformatted"; exit 1; }
	sh -n scripts/install.sh
	bash -n tests/proxy-linux-isolation.sh tests/proxy-linux-bwrap.sh

check: fmt-check
	"$(GO)" vet ./...
	"$(GO)" test -race ./...
	$(MAKE) build TARGET_OS=$(HOST_OS) TARGET_ARCH=$(HOST_ARCH)
	bash tests/nido-cli-isolation.sh "$(CURDIR)/build/$(HOST_OS)-$(HOST_ARCH)/nido"

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

# GDS uses only copied scripts, fake nvcc/nvidia-smi and a CPU fixture worker.
check-gds:
	bash tests/gds-isolation.sh

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
