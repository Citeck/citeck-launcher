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
| `internal/cli/` | Cobra CLI commands (start, stop, status, apply, migrate, etc.) |
| `internal/daemon/` | HTTP server, API routes (SSE events, config, volumes), middleware |
| `internal/namespace/` | Config parsing, container generator, runtime state machine, reconciler |
| `internal/docker/` | Docker SDK wrapper (containers, images, exec, logs via stdcopy, probes) |
| `internal/bundle/` | Bundle definitions and resolution from git repos |
| `internal/git/` | Git clone/pull via go-git (pure Go, with token auth, hard-reset, reclone) |
| `internal/config/` | Filesystem paths, daemon config (daemon.yml), workspace dir scanner |
| `internal/storage/` | Store interface + FileStore (server) + SQLiteStore (desktop) |
| `internal/h2migrate/` | H2 MVStore read-only parser, LZF decompressor, H2→SQLite migration |
| `internal/client/` | DaemonClient (Unix socket + TCP transport) |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs), path constants |
| `internal/appdef/` | Application definition models (ApplicationDef, ApplicationKind) |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/actions/` | Namespace lifecycle actions (pull, start, stop executors) |
| `internal/form/` | Form field specs, built-in field definitions, validation |
| `internal/snapshot/` | Volume snapshot export/import (ZIP + tar.xz) |
| `internal/namespace/nsactions/` | Action executors wired to Docker + runtime |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`. Darcula/Lens dark theme.

**Pages:**
- `Dashboard.tsx` — namespace info panel + grouped app table
- `AppDetail.tsx` — container info, ports, volumes, env, per-app YAML config editor
- `Logs.tsx` — full log viewer (7-pattern level detection, ANSI strip, regex search, follow, copy/download)
- `Config.tsx` — namespace.yml viewer/editor with YAML highlighting
- `Volumes.tsx` — Docker volume management (namespace-scoped, list/delete)
- `DaemonLogs.tsx` — launcher daemon logs viewer
- `Welcome.tsx` — namespace list, quick start buttons, create/delete
- `Wizard.tsx` — multi-step namespace creation (8 steps)
- `Secrets.tsx` — secret CRUD with type selector and test button
- `Diagnostics.tsx` — system health checks with fix actions

**Components:**
- `AppTable.tsx` — grouped table (Core/Extensions/Additional/ThirdParty), lucide-react icons
- `TabBar.tsx` — IDE-style tab navigation
- `ConfirmModal.tsx` — reusable confirm dialog (always mounted, showModal/close)
- `NamespaceControls.tsx` — Start/Stop/Reload with confirm
- `StatusBadge.tsx` — color-coded status labels
- `ContextMenu.tsx` — right-click context menu with items/dividers
- `FormDialog.tsx` — spec-driven form dialog (text/number/password/select/checkbox)
- `JournalDialog.tsx` — data table dialog with search, selection, custom buttons

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
- **SQLite**: modernc.org/sqlite (pure Go, no CGO — desktop mode storage)

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
Full feature parity with Kotlin Compose Desktop namespace screen. 18 commits, 5 review rounds (53 issues fixed).

### Web UI Phase 2 — COMPLETE (2026-03-25)
Full Web UI feature set: Welcome screen, wizard, secrets, diagnostics, context menus, form framework, journal browser, snapshot import/export, light theme, Playwright E2E.

### Phase 3: Architecture Gap Closure — COMPLETE (2026-03-25)
Actions service, go-git, form validation, bind-mount volumes, snapshot export/import, runtime hardening (stalled recovery, stale cleanup, socket lock). Platform deploys 19/19.

### Phase 4: CLI Completion + Production Hardening — COMPLETE (2026-03-25)
Snapshot URL download (HTTP resume, SHA256, retry), CLI clean/apply/diff/status --watch, git hardening (hard-reset, reclone on corruption, URL change detection), dead code cleanup. 3 code review passes.

### Phase 5: Kotlin Parity — PLANNED
See `~/.claude/plans/snappy-cuddling-popcorn.md` for full plan (30 gaps, 8 sub-phases).
Order: 5-pre (generator parity, CloudConfigServer, registry auth) → 5A (wire reconciler/auth/history) → 5C (state persistence) → 5B (deployment hash) → 5D (pull policy) → 5E (install) → 5F (ACME) → 5G (validation).
After Phase 5: delete Kotlin code (`core/`, `cli/`, `app/`, `gradle/`).

### Key Technical Decisions
- SSE (not WebSocket) for real-time events
- TCP bound to 127.0.0.1 (security)
- stdcopy.StdCopy for Docker log demuxing
- Namespace-scoped volume operations (bind-mounts, not Docker named volumes)
- Two storage backends: flat files (server) / SQLite (desktop)
- Desktop mode via explicit `--desktop` flag only
- H2 MVStore read-only parser in Go for migration
- Shared secrets at launcher level, not per-workspace
- go-git (pure Go) for git operations — no external git binary required
- Snapshot download with HTTP resume, SHA256 verification, retry (3 attempts)
- `reflect.DeepEqual` for config diff (not string comparison)
- filepath.Join everywhere (no fmt.Sprintf for paths)

### Known Gaps (will be closed in Phase 5)

**Unwired code (exists but not connected):**
- Reconciler + liveness probes: NOT wired in daemon startup
- Token auth middleware: NOT applied to TCP listener
- OperationHistory: NOT called anywhere

**Missing features vs Kotlin:**
- CloudConfigServer (port 8761): NOT implemented — breaks "stop in launcher, debug locally" developer workflow. Kotlin serves extCloudConfig with localhost URLs (published ports) for local debugging
- Docker registry auth: `PullImage()` sends no credentials — enterprise repos (harbor.citeck.ru) fail with 401
- Runtime state not persisted (detached apps, edited defs, cached bundle lost on restart)
- Deployment hash: containers always recreated on start (Kotlin keeps unchanged ones)
- Pull policy: all images re-pulled every time (Kotlin only re-pulls snapshot images)
- Generator gaps: no Alfresco, no globalDefaultWebappProps, no springProfiles, no debugPort JDWP, no webappProps.cloudConfig merge, no workspace-level infra image overrides, no namespace template application on creation, no license/bundle-key injection
- Snapshot auto-import on daemon startup: `snapshot` field in namespace.yml is dead config
- Workspace config not re-read on reload
- `citeck install` + ACME/Let's Encrypt not implemented
- `config validate` only checks YAML syntax, no semantic validation
- Log startup condition (`cond.Log`) ignored in `waitForStartup()`

### Other References
- **`PROGRESS.md`** — tracks completed work

## CI/CD

GitHub Actions release workflow (`.github/workflows/release.yml`): triggered by `v*.*.*` tags, builds on Linux/Windows/macOS (x64 + arm64), creates GitHub release.
