# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher manages Citeck ECOS namespaces and Docker containers. It is a single Go binary (~14MB) that serves as both CLI and daemon, with an embedded React Web UI. The original Kotlin code (Compose Desktop, Gradle) is kept as reference implementation.

## Build & Development Commands

### Go + Web UI (primary)

```bash
make build                    # Build Go binary + embed React web UI
make build-fast               # Build Go only (skip web rebuild)
make test                     # Run all tests (Go + Vitest)
go test ./...                 # Go tests only
go test ./internal/...        # Go unit tests only
cd web && npx vitest run      # React component tests
cd web && npx playwright test # E2E browser tests
golangci-lint run             # Go linter
cd web && npm run lint        # Web linter
./citeck start --foreground   # Run daemon with web UI on :8088
```

### Kotlin reference (read-only, not built)

```bash
./gradlew :app:run            # Run Compose Desktop app
./gradlew test                # Run Kotlin tests
./gradlew ktlintFormat        # Auto-fix Kotlin lint
```

The Kotlin code in `core/`, `cli/`, `app/` directories is reference-only. All new development is in Go + React.

## Architecture

### Go Daemon + CLI (`internal/`)

| Package | Purpose |
|---|---|
| `internal/cli/` | Cobra CLI commands (start, stop, status, apply, etc.) |
| `internal/daemon/` | HTTP server, API routes, middleware (auth, CORS) |
| `internal/namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `internal/docker/` | Docker SDK wrapper (containers, images, exec, logs, probes) |
| `internal/bundle/` | Bundle definitions and resolution from git repos |
| `internal/git/` | Git clone/pull via os/exec |
| `internal/config/` | Filesystem paths, daemon config, workspace loading |
| `internal/client/` | DaemonClient (Unix socket + TCP transport) |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs) |
| `internal/appdef/` | Application definition models |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/history/` | Operation history (JSONL) |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`.

- `web/src/pages/` — Dashboard, AppDetail, Logs, Config
- `web/src/components/` — AppTable, StatusBadge
- `web/src/lib/` — API client (fetch), WebSocket client, Zustand store

### Entry Point

`cmd/citeck/main.go` — CLI entry point (cobra root command).

### Embedded Resources (`internal/appfiles/`)

Contains default configuration templates for managed services: Alfresco, PostgreSQL, PgAdmin, Proxy, Keycloak (embedded via `go:embed`).

### Kotlin Reference (`core/`, `cli/`, `app/`)

Original Kotlin implementation. Read-only reference for understanding business logic. Key files:
- `core/namespace/gen/NamespaceGenerator.kt` — container generation logic
- `core/namespace/NamespaceConfig.kt` — config model
- `core/namespace/runtime/` — state machine, app lifecycle

## Code Style

### Go
- Standard `gofmt` formatting
- `golangci-lint` for linting
- Tabs for indentation (Go standard)

### Web (React/TypeScript)
- Tailwind CSS 4 for styling
- ESLint for linting

### Kotlin (reference only)
- ktlint via `.editorconfig`, wildcard imports allowed
- 4-space indentation, LF line endings, UTF-8

## Key Dependencies

### Go
- **CLI**: spf13/cobra
- **Docker**: docker/docker/client (official SDK)
- **WebSocket**: coder/websocket
- **YAML**: gopkg.in/yaml.v3
- **CLI output**: charmbracelet/lipgloss
- **Testing**: stretchr/testify
- **HTTP**: net/http (stdlib, Go 1.22+ routing)
- **Logging**: log/slog (stdlib)
- **Embed**: embed (stdlib, for web UI + appfiles)

### Web UI
- **Framework**: React 19 + TypeScript
- **Build**: Vite
- **Styles**: Tailwind CSS 4
- **State**: Zustand
- **Testing**: Vitest + Testing Library
- **E2E**: Playwright

## Agent Plan — Go Rewrite (V3)

Go rewrite is **complete** (phases 1-8, 10 DONE). Phase 9 (Desktop app) deferred.

**Current focus:** Web UI feature parity with Compose Desktop.
- See `~/.claude/plans/snoopy-herding-gosling.md` for the Web UI plan
- **`AGENT_PLAN_V3.md`** — original rewrite plan (phases 1-10)
- **`AGENT_INSTRUCTIONS.md`** — reference guide for agents
- **`PROGRESS.md`** — tracks completed work

### Completed V3 Phases
- Phase 1: Go scaffold + CLI skeleton ✅
- Phase 2: Web UI scaffold (React + Vite + Tailwind) ✅
- Phase 3: Port daemon core (namespace, Docker, bundles) ✅
- Phase 4: Full CLI + apply + diff ✅
- Phase 5: Full web dashboard ✅
- Phase 6: Liveness + self-healing ✅
- Phase 7: Remote daemon + auth ✅
- Phase 8: Advanced features (cert, clean) ✅
- Phase 9: Desktop app (Wails v3) — DEFERRED
- Phase 10: Distribution (goreleaser, install script, systemd) ✅

### Architecture
```
citeck (single Go binary ~14MB) — daemon + CLI + embedded React Web UI
Web UI on http://localhost:8088 — full management dashboard
```

## CI/CD

GitHub Actions release workflow (`.github/workflows/release.yml`): triggered by `v*.*.*` tags, builds on Linux/Windows/macOS (x64 + arm64), creates GitHub release.
