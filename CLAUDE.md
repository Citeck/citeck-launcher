# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher manages Citeck ECOS namespaces and Docker containers. It is a single Go binary (~14MB) that serves as both CLI and daemon, with an embedded React Web UI on `http://127.0.0.1:8088`. The original Kotlin code (Compose Desktop, Gradle) is kept as reference implementation.

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
./citeck start --foreground   # Run daemon with web UI on 127.0.0.1:8088
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
| `internal/daemon/` | HTTP server, API routes (SSE events, config, volumes), middleware |
| `internal/namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `internal/docker/` | Docker SDK wrapper (containers, images, exec, logs via stdcopy, probes) |
| `internal/bundle/` | Bundle definitions and resolution from git repos |
| `internal/git/` | Git clone/pull via os/exec |
| `internal/config/` | Filesystem paths, daemon config, workspace loading |
| `internal/client/` | DaemonClient (Unix socket + TCP transport) |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs), path constants |
| `internal/appdef/` | Application definition models (ApplicationDef, ApplicationKind) |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/history/` | Operation history (JSONL) |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`. Darcula/Lens dark theme.

**Pages:**
- `Dashboard.tsx` — namespace info panel + grouped app table
- `AppDetail.tsx` — container info, ports, volumes, env, per-app YAML config editor
- `Logs.tsx` — full log viewer (7-pattern level detection, ANSI strip, regex search, follow, copy/download)
- `Config.tsx` — namespace.yml viewer/editor with YAML highlighting
- `Volumes.tsx` — Docker volume management (namespace-scoped, list/delete)
- `DaemonLogs.tsx` — launcher daemon logs viewer

**Components:**
- `AppTable.tsx` — grouped table (Core/Extensions/Additional/ThirdParty), lucide-react icons
- `TabBar.tsx` — IDE-style tab navigation
- `ConfirmModal.tsx` — reusable confirm dialog (always mounted, showModal/close)
- `NamespaceControls.tsx` — Start/Stop/Reload with confirm
- `StatusBadge.tsx` — color-coded status labels

**Lib:**
- `api.ts` — REST API client (fetch wrapper)
- `store.ts` — Zustand dashboard store (SSE events, exponential backoff reconnect)
- `websocket.ts` — SSE EventSource wrapper (not WebSocket despite filename)
- `tabs.ts` — Tab state management (zustand)
- `types.ts` — TypeScript interfaces matching Go DTOs

### Entry Point

`cmd/citeck/main.go` — CLI entry point (cobra root command).

### Kotlin Reference (`core/`, `cli/`, `app/`)

Original Kotlin implementation. Read-only reference for understanding business logic. Key files:
- `core/namespace/gen/NamespaceGenerator.kt` — container generation logic
- `core/namespace/NamespaceConfig.kt` — config model
- `core/namespace/runtime/` — state machine, app lifecycle
- `app/src/main/kotlin/ru/citeck/launcher/view/` — UI (forms, logs, dialogs, context menus)

## Code Style

### Go
- Standard `gofmt` formatting
- `golangci-lint` for linting
- Tabs for indentation (Go standard)

### Web (React/TypeScript)
- Tailwind CSS 4 for styling
- ESLint for linting
- lucide-react for icons

### Kotlin (reference only)
- ktlint via `.editorconfig`, wildcard imports allowed
- 4-space indentation, LF line endings, UTF-8

## Key Dependencies

### Go
- **CLI**: spf13/cobra
- **Docker**: docker/docker/client (official SDK) + docker/docker/pkg/stdcopy
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
- **Icons**: lucide-react
- **State**: Zustand
- **Testing**: Vitest + Testing Library
- **E2E**: Playwright

## Current Status

### Web UI Phase 1 — COMPLETE (2026-03-25)
Full feature parity with Kotlin Compose Desktop namespace screen. 18 commits, 5 review rounds (53 issues fixed). See `PROGRESS.md` for details.

### Web UI Phase 2 — IN PROGRESS
See `~/.claude/plans/snoopy-herding-gosling.md` for the full plan:
- E0: Desktop data compatibility (H2 migration, dual storage backends, `--desktop` flag)
- E1: Welcome Screen + namespace list
- E2: Dynamic form framework (FormSpec → React)
- E3: Namespace install wizard
- E4: Journal/Entity browser
- E5: Context menus
- F1: Shared secrets (global git tokens)
- F2: Diagnostics page
- F3: Snapshot import/export
- F4: UI polish (frontend-design agent, light theme)
- F5: Playwright E2E test suite

### Key Technical Decisions
- SSE (not WebSocket) for real-time events
- TCP bound to 127.0.0.1 (security)
- stdcopy.StdCopy for Docker log demuxing
- Namespace-scoped volume operations
- Two storage backends: flat files (server) / SQLite (desktop)
- Desktop mode via explicit `--desktop` flag only
- H2 MVStore read-only parser in Go for migration
- Shared secrets at launcher level, not per-workspace

### Other References
- **`AGENT_PLAN_V3.md`** — original Go rewrite plan (phases 1-10)
- **`AGENT_INSTRUCTIONS.md`** — reference guide for agents
- **`PROGRESS.md`** — tracks completed work

## CI/CD

GitHub Actions release workflow (`.github/workflows/release.yml`): triggered by `v*.*.*` tags, builds on Linux/Windows/macOS (x64 + arm64), creates GitHub release.
