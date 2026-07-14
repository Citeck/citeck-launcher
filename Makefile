# Dev builds carry a compact UTC timestamp so `citeck version --short` is
# unique per build — lets you verify you're talking to the freshly-scp'd
# binary on a test server without re-checking git commits. Releases override
# this via `make VERSION=v2.2.1 ...` (CI builds with `go build -ldflags`
# directly so they bypass the Makefile default entirely).
VERSION ?= dev-$(shell date -u +%Y%m%d-%H%M%S)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Use bash for all recipes so they run identically on Linux, macOS, and the
# Windows runner (Git Bash) — the release-* targets rely on POSIX tools.
SHELL := bash
BUILDDIR := dist/bin
BINARY   := $(BUILDDIR)/citeck-server
DESKTOP  := $(BUILDDIR)/citeck-launcher
GO_BUILD_FLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.gitCommit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"
WEBDIST  := internal/daemon/webdist

# Go tools path
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GOLANGCI_LINT=$(GOBIN)/golangci-lint

.PHONY: all check build build-fast build-web build-desktop run test test-unit test-race test-coverage \
        test-e2e lint fmt tidy tools clean help dev-daemon dev-desktop web-deps deadcode \
        release-server release-desktop-linux release-desktop-windows release-desktop-macos

all: test build

# ===== Full local gate — run THIS before committing/tagging =====
# One command that runs every linter, compiler, and test the release pipeline
# gates on (mirrors .github/workflows/test.yml step-for-step) PLUS web eslint,
# so "green here" means "green in CI". Fail-fast: stops at the first failure.
# NOTE: plain pushes to master DO NOT run CI — the v*.*.* release tag is the
# first gate — so run `make check` locally before pushing a release.
# Prereqs: `make tools` once (pinned golangci-lint); the deadcode gate needs
# CGO + GTK3 dev headers (libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev),
# the same packages the CI test job installs.
check:
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { echo "ERROR: $(GOLANGCI_LINT) not found — run 'make tools' first"; exit 1; }
	@mkdir -p $(WEBDIST)
	@test -f $(WEBDIST)/index.html || echo '<html></html>' > $(WEBDIST)/index.html
	@echo "==> [1/10] go vet"
	go vet ./...
	@echo "==> [2/10] golangci-lint (v2.11.4 pinned)"
	$(GOLANGCI_LINT) run ./...
	@echo "==> [3/10] go test -race -cover ./internal/... (slow: namespace ~160s)"
	set -o pipefail; go test -race -cover ./internal/... | tee /tmp/citeck-cover.txt
	@echo "==> [4/10] coverage floors"
	bash scripts/ci/coverage-floor.sh /tmp/citeck-cover.txt
	@echo "==> [5/10] govulncheck (reachable-vuln gate)"
	bash scripts/ci/govulncheck.sh
	@echo "==> [6/10] deadcode (needs CGO + GTK3 headers)"
	bash scripts/ci/deadcode.sh
	@echo "==> [7/10] web: install (frozen) + vitest + prod audit + eslint"
	cd web && pnpm install --frozen-lockfile && pnpm vitest run && pnpm audit --prod --audit-level high && pnpm lint
	@echo "==> [8/10] build server binary (tsc + vite + go build via 'make build')"
	$(MAKE) build
	@echo "==> [9/10] cross-compile check (linux/arm64)"
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/citeck-check-arm64 ./cmd/citeck && rm -f /tmp/citeck-check-arm64
	@echo "==> [10/10] PASS — full local gate green (superset of CI)"

help:
	@echo "Usage:"
	@echo "  make check          - FULL local gate (CI superset + eslint); run before committing/tagging"
	@echo "  make build          - Build Go binary + embed React web UI"
	@echo "  make build-fast     - Build Go only (skip web rebuild)"
	@echo "  make build-desktop  - Build desktop (Wails) binary"
	@echo "  make run            - Build desktop binary and run it"
	@echo "  make test           - Run all tests (Go + Vitest)"
	@echo "  make test-unit      - Go unit tests only (./internal/...)"
	@echo "  make test-race      - Go tests with race detector + timeout"
	@echo "  make test-coverage  - Go tests with coverage report"
	@echo "  make test-e2e       - Web UI Playwright e2e (needs a running daemon, see target)"
	@echo "  make lint           - Run Go + Web linters"
	@echo "  make deadcode       - Dead-code analysis vs scripts/ci/deadcode-allowlist.txt"
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

# Install web deps only when node_modules is missing — keeps incremental
# builds fast while letting a fresh checkout `make run` with zero setup.
web-deps:
	@test -d web/node_modules || (cd web && pnpm install)

build-web: web-deps
	cd web && pnpm run build   # Vite outputs straight into $(WEBDIST) (see web/vite.config.ts)

# Linux desktop build uses Wails GTK3 backend (GTK4 + WebKitGTK 6.0 path is
# not yet shipping in mainstream distros). Other OSes ignore the gtk3 tag.
DESKTOP_TAGS := desktop,gtk3

build-desktop: build-web
	@mkdir -p $(BUILDDIR)
	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" $(GO_BUILD_FLAGS) -o $(DESKTOP) ./cmd/citeck-desktop

run: build-desktop
	./$(DESKTOP)

# ===== Release artifacts (CI calls these; build logic lives under packaging/) =====
# Everything lands in dist/. VERSION/ARCH/GOOS/GOARCH come from the environment.
# release-server is cross-OS (GOOS/GOARCH); the desktop scripts live in their
# per-OS packaging subfolder next to that OS's config.
release-server: build-web
	bash packaging/release-server.sh
release-desktop-linux: build-web
	bash packaging/linux/release.sh
release-desktop-windows: build-web
	bash packaging/windows/release.sh
release-desktop-macos: build-web
	bash packaging/macos/release.sh

test:
	go test -race ./...
	cd web && pnpm test

test-unit:
	go test -race ./internal/...

test-race:
	go test -race -timeout=120s -count 1 ./...

test-coverage:
	@mkdir -p dist
	go test -coverprofile=dist/coverage.out ./internal/...
	go tool cover -html=dist/coverage.out -o dist/coverage.html
	@echo "Coverage report: dist/coverage.html"

test-integration:
	go test -tags=integration ./tests/...

# Web UI end-to-end tests (Playwright, web/tests/). PREREQUISITE: a daemon
# serving the web UI at http://127.0.0.1:7088 must already be running (see
# web/playwright.config.ts baseURL). The server-mode Web UI is not offered in
# production, so bind it via the dev/E2E hatch:
#   CITECK_SERVER_WEBUI=1 dist/bin/citeck-server start --foreground   # port 7088
# Browser binaries: cd web && pnpm exec playwright install chromium
# CI does not run this target yet (no daemon harness — backlog).
test-e2e: web-deps
	cd web && pnpm run test:e2e

lint:
	$(GOLANGCI_LINT) run ./...
	cd web && pnpm lint

# Same gate CI runs (see .github/workflows/test.yml): fails on any unreachable
# function not in scripts/ci/deadcode-allowlist.txt.
deadcode:
	bash scripts/ci/deadcode.sh

fmt:
	go fmt ./...

tidy:
	go mod tidy

tools:
	@echo "Installing dev tools..."
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

dev-daemon: web-deps
	go run ./cmd/citeck start --foreground &
	cd web && pnpm dev

dev-desktop: web-deps
	cd web && pnpm run build
	@mkdir -p $(BUILDDIR)
	CGO_ENABLED=1 go build -tags "$(DESKTOP_TAGS)" -o $(DESKTOP) ./cmd/citeck-desktop
	./$(DESKTOP)

clean:
	rm -rf dist $(WEBDIST)
