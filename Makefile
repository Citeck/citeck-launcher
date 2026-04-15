# Dev builds carry a compact UTC timestamp so `citeck version --short` is
# unique per build — lets you verify you're talking to the freshly-scp'd
# binary on a test server without re-checking git commits. Releases override
# this via `make VERSION=v2.1.0 ...` (CI builds with `go build -ldflags`
# directly so they bypass the Makefile default entirely).
VERSION ?= dev-$(shell date -u +%Y%m%d-%H%M%S)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILDDIR := build/bin
BINARY   := $(BUILDDIR)/citeck-server
DESKTOP  := $(BUILDDIR)/citeck-desktop
GO_BUILD_FLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.gitCommit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"
WEBDIST  := internal/daemon/webdist

# Go tools path
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GOLANGCI_LINT=$(GOBIN)/golangci-lint

.PHONY: all build build-fast build-web build-desktop test test-unit test-race test-coverage \
        lint fmt tidy tools clean help dev-daemon dev-desktop

all: test build

help:
	@echo "Usage:"
	@echo "  make build          - Build Go binary + embed React web UI"
	@echo "  make build-fast     - Build Go only (skip web rebuild)"
	@echo "  make build-desktop  - Build desktop (Wails) binary"
	@echo "  make test           - Run all tests (Go + Vitest)"
	@echo "  make test-unit      - Go unit tests only (./internal/...)"
	@echo "  make test-race      - Go tests with race detector + timeout"
	@echo "  make test-coverage  - Go tests with coverage report"
	@echo "  make lint           - Run Go + Web linters"
	@echo "  make fmt            - Format Go code"
	@echo "  make tidy           - Tidy Go modules"
	@echo "  make tools          - Install dev tools (golangci-lint)"
	@echo "  make clean          - Remove build artifacts"

build: build-web
	@mkdir -p $(BUILDDIR)
	go build $(GO_BUILD_FLAGS) -o $(BINARY) ./cmd/citeck

build-fast:
	@mkdir -p $(BUILDDIR) $(WEBDIST)
	@test -f $(WEBDIST)/index.html || echo '<html><body>Run "make build" to include web UI</body></html>' > $(WEBDIST)/index.html
	go build $(GO_BUILD_FLAGS) -o $(BINARY) ./cmd/citeck

build-web:
	cd web && pnpm run build
	rm -rf $(WEBDIST)
	cp -r web/dist $(WEBDIST)

build-desktop: build-web
	@mkdir -p $(BUILDDIR)
	CGO_ENABLED=1 go build -tags desktop $(GO_BUILD_FLAGS) -o $(DESKTOP) ./cmd/citeck-desktop

test:
	go test -race ./...
	cd web && pnpm test

test-unit:
	go test -race ./internal/...

test-race:
	go test -race -timeout=120s -count 1 ./...

test-coverage:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-integration:
	go test -tags=integration ./tests/...

lint:
	$(GOLANGCI_LINT) run ./...
	cd web && pnpm lint

fmt:
	go fmt ./...

tidy:
	go mod tidy

tools:
	@echo "Installing dev tools..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

dev-daemon:
	go run ./cmd/citeck start --foreground &
	cd web && pnpm dev

dev-desktop:
	cd web && pnpm run build
	@mkdir -p $(BUILDDIR)
	CGO_ENABLED=1 go build -o $(DESKTOP) ./cmd/citeck-desktop
	./$(DESKTOP)

clean:
	rm -rf $(BUILDDIR)
	rm -rf $(WEBDIST)
	rm -rf web/dist
	rm -f coverage.out coverage.html
