GO ?= go
HOST_OS = $(shell "$(GO)" env GOOS)
HOST_ARCH = $(shell "$(GO)" env GOARCH)
TARGET_OS ?= $(HOST_OS)
TARGET_ARCH ?= $(HOST_ARCH)
VERSION := $(shell cat VERSION)
BINARY = build/$(TARGET_OS)-$(TARGET_ARCH)/cdet
PACKAGE = dist/cdet-$(TARGET_OS)-$(TARGET_ARCH).tar.gz
RELEASE_PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test fmt-check check dist smoke release verify-release release-check check-gds
build:
	CGO_ENABLED=0 GOOS=$(TARGET_OS) GOARCH=$(TARGET_ARCH) "$(GO)" build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w -X=github.com/MiracleHYH/CDEToolkit/internal/stk.Version=$(VERSION)' -o "$(BINARY)" ./cmd/cdet

# Each Go package contains exactly cdet, with no legacy installer or user data.
dist: build
	"$(GO)" run ./cmd/cdet-dist pack --binary "$(BINARY)" --platform "$(TARGET_OS)/$(TARGET_ARCH)"
	"$(GO)" run ./cmd/cdet-dist verify --archive "$(PACKAGE)" --platform "$(TARGET_OS)/$(TARGET_ARCH)"

smoke: dist
	"$(GO)" run ./cmd/cdet-dist verify --archive "$(PACKAGE)" --platform "$(TARGET_OS)/$(TARGET_ARCH)" --smoke

test:
	"$(GO)" test ./...

fmt-check:
	@stk_unformatted=$$("$(shell "$(GO)" env GOROOT)/bin/gofmt" -l cmd internal); test -z "$$stk_unformatted" || { printf 'gofmt required:\n%s\n' "$$stk_unformatted"; exit 1; }
	sh -n scripts/install.sh

check: fmt-check
	"$(GO)" vet ./...
	"$(GO)" test -race ./...
	$(MAKE) build TARGET_OS=$(HOST_OS) TARGET_ARCH=$(HOST_ARCH)
	bash tests/go-cli-isolation.sh "$(CURDIR)/build/$(HOST_OS)-$(HOST_ARCH)/cdet"

release:
	@set -e; for platform in $(RELEASE_PLATFORMS); do $(MAKE) dist TARGET_OS="$${platform%/*}" TARGET_ARCH="$${platform#*/}"; done
	"$(GO)" run ./cmd/cdet-dist installer

verify-release:
	@set -e; for platform in $(RELEASE_PLATFORMS); do "$(GO)" run ./cmd/cdet-dist verify --archive "dist/cdet-$${platform%/*}-$${platform#*/}.tar.gz" --platform "$$platform"; done
	"$(GO)" run ./cmd/cdet-dist installer --verify

# A tag is passed through the environment, never interpolated into shell code.
release-check:
	"$(GO)" run ./cmd/cdet-dist check-tag --tag "$${STK_RELEASE_TAG:?set STK_RELEASE_TAG to vVERSION}" --repository .
	$(MAKE) check
	$(MAKE) release
	$(MAKE) verify-release
	"$(GO)" run ./cmd/cdet-dist verify --archive "dist/cdet-$(HOST_OS)-$(HOST_ARCH).tar.gz" --platform "$(HOST_OS)/$(HOST_ARCH)" --smoke

# GDS uses only copied scripts, fake nvcc/nvidia-smi and a CPU fixture worker.
check-gds:
	bash tests/gds-isolation.sh
