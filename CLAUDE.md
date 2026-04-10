# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher manages Citeck namespaces and Docker containers. It is a single Go binary (~18MB) that serves as both CLI and daemon, with an embedded React Web UI on `http://127.0.0.1:7088`.

### History

This is a **Go rewrite** (v2.0) of the original Kotlin/JVM launcher (v1.x). The Kotlin source is in the same repo under tags `v1.0.0`–`v1.3.9` (branch `master` before Go rewrite). Use `git show v1.3.8:path/to/file` to read Kotlin source.

**Kotlin launcher key details:**
- Built with Gradle, Kotlin, Compose Desktop (JVM)
- Storage: H2 MVStore (`storage.db`) — binary key-value store with compressed chunks
- Secrets: AES-256-GCM encrypted with master password (PBKDF2-HMAC-SHA256, 1M iterations, 16-byte salt)
- Encrypted payload: `EncryptedStorage { key: KeyParams, alg: 0, iv: byte[], tagLen: 128, data: byte[] }`
- Entity storage: JSON serialized to ByteArray in MVStore maps like `entities/{wsId}!{entityType}`
- Namespace configs stored in H2 (not as YAML files) under `entities/{wsId}!namespace`
- Secrets stored encrypted in `secrets!data` map → key `"storage"` → encrypted blob containing all auth secrets
- State: `launcher!state` (selectedWorkspace), `workspace-state!{wsId}` (selectedNamespace)
- Key source files: `Database.kt`, `SecretsEncryptor.kt`, `SecretsStorage.kt`, `EncryptedStorage.kt`, `EntitiesService.kt`

## Build & Development Commands

### Go + Web UI (primary)

```bash
make build                    # Build Go binary + embed React web UI → build/bin/citeck
make build-fast               # Build Go only (skip web rebuild) → build/bin/citeck
make build-desktop            # Build desktop (Wails) binary → build/bin/citeck-desktop
make test                     # Run all tests (Go + Vitest)
make test-unit                # Go unit tests only (./internal/...)
make test-race                # Go tests with race detector + 120s timeout
make test-coverage            # Go coverage report → coverage.html
make lint                     # Run Go (golangci-lint) + Web (eslint) linters
make fmt                      # Format Go code
make tidy                     # go mod tidy
make tools                    # Install golangci-lint v2.11.4
make clean                    # Remove build artifacts
build/bin/citeck start --foreground   # Run daemon with web UI on 127.0.0.1:7088
./build/bin/citeck-desktop            # Run desktop app (Wails webview)
```

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
| `internal/storage/` | Store interface + FileStore (server) + SQLiteStore (desktop) + SecretService (AES-256-GCM encryption) |
| `internal/h2migrate/` | H2 MVStore read-only parser, LZF decompressor, H2→SQLite migration |
| `internal/client/` | DaemonClient (Unix socket + mTLS TCP transport) |
| `internal/output/` | Text/JSON output formatter, tables, colors |
| `internal/api/` | Shared API types (DTOs), path constants |
| `internal/appdef/` | Application definition models (ApplicationDef, ApplicationKind) |
| `internal/appfiles/` | Embedded resource files (go:embed) |
| `internal/actions/` | Namespace lifecycle actions (pull, start, stop executors) |
| `internal/form/` | Form field specs, built-in field definitions, validation |
| `internal/snapshot/` | Volume snapshot export/import (ZIP + tar.xz) |
| `internal/tlsutil/` | TLS cert utilities (self-signed, client cert, CA pool loader) |
| `internal/fsutil/` | Atomic file write (temp+fsync+rename), RotatingWriter (log rotation), CleanLogHandler (human-readable slog) |
| `internal/acme/` | ACME/Let's Encrypt client + auto-renewal service |
| `internal/namespace/nsactions/` | Action executors wired to Docker + runtime |

### Web UI (`web/`)

React 19 + Vite + TypeScript + Tailwind CSS 4. Embedded into Go binary via `go:embed`. Darcula/Lens dark theme.

**Pages:**
- `Dashboard.tsx` — sidebar + app table + right drawer overlay + bottom panel
- `AppDetail.tsx` — full-page fallback (composes AppDrawerContent + AppConfigEditor)
- `Logs.tsx` — thin wrapper for LogViewer
- `Config.tsx` — health checks + ConfigEditor
- `Volumes.tsx` — Docker volume management (namespace-scoped, list/delete)
- `DaemonLogs.tsx` — thin wrapper for DaemonLogsViewer
- `Welcome.tsx` — namespace list, quick start buttons, create/delete
- `Wizard.tsx` — multi-step namespace creation (8 steps, language-aware)
- `Secrets.tsx` — secret CRUD with type selector, test button, encryption status, inline unlock
- `Diagnostics.tsx` — system health checks with fix actions

**Components:**
- `AppTable.tsx` — grouped table with panel actions (openDrawer, openBottomTab)
- `BottomPanel.tsx` — IDE-style bottom panel (lazy mount, drag-resize, collapse)
- `RightDrawer.tsx` — overlay drawer with slide animation
- `LogViewer.tsx` — log viewer (virtual list, regex search, level filter, streaming, active prop)
- `ConfigEditor.tsx` — namespace.yml viewer/editor with YAML highlighting
- `DaemonLogsViewer.tsx` — daemon logs streaming (fetch-based, replaces polling)
- `AppDrawerContent.tsx` — app inspect details + action buttons (logs, config, restart)
- `AppConfigEditor.tsx` — per-app YAML config + mounted files editor
- `YamlViewer.tsx` — shared YAML syntax highlighter
- `TabBar.tsx` — IDE-style tab navigation + language selector + theme toggle
- `StatusBadge.tsx` — color-coded status with dot indicator and i18n display names
- `NamespaceControls.tsx` — Start/Stop/Reload with confirm
- `ConfirmModal.tsx` — reusable confirm dialog (always mounted, showModal/close)
- `Toast.tsx` — toast notifications (theme-aware colors)
- `ErrorBoundary.tsx` — React error boundary with reload button
- `ContextMenu.tsx` — right-click context menu with items/dividers
- `FormDialog.tsx` — spec-driven form dialog (text/number/password/select/checkbox)
- `JournalDialog.tsx` — data table dialog with search, selection, custom buttons

**Lib:**
- `api.ts` — REST API client (fetchWithTimeout, CSRF, exported API_BASE)
- `store.ts` — Zustand dashboard store (SSE events, exponential backoff reconnect)
- `panels.ts` — Zustand panel store (drawer, bottom tabs, height persistence)
- `i18n.ts` — i18n store (8 locales, lazy loading, t() + useTranslation())
- `websocket.ts` — SSE EventSource wrapper (not WebSocket despite filename)
- `tabs.ts` — Tab state management (zustand)
- `toast.ts` — Toast notification store (zustand, auto-dismiss)
- `types.ts` — TypeScript interfaces matching Go DTOs

**Hooks:**
- `useResizeHandle.ts` — pointer-capture drag hook for bottom panel resize
- `useContextMenu.ts` — context menu state management

### Entry Point

`cmd/citeck/main.go` — CLI entry point (cobra root command).

## Code Style

### Go
- Standard `gofmt` formatting
- `golangci-lint` v2.11.4 with 21 linters (`.golangci.yml`): dupl, errorlint, gochecknoinits, gocritic, gocyclo, gosec, govet (shadow), ineffassign, misspell (US), modernize, nakedret, nestif, prealloc, revive, staticcheck, testifylint, unconvert, unparam, unused, wrapcheck. **Always run `make tools` before linting to get the pinned CI version** — newer gosec taint analysis catches more G703/G706 false positives than 2.7.2.
- Tabs for indentation (Go standard)
- Custom slog handler (`fsutil.CleanLogHandler`): `2026-04-01T02:58:51Z INFO  Message key=value`

### Web (React/TypeScript)
- Tailwind CSS 4 for styling
- ESLint for linting
- lucide-react for icons

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

## Key Technical Decisions

- **SSE** (not WebSocket) for real-time events
- **Single-mux** architecture: all routes on `socketMux`, shared by Unix socket, localhost TCP, and mTLS TCP
- **mTLS** for non-localhost Web UI (client certs in `conf/webui-ca/`, dynamic pool reload)
- **CSRF** via custom header (`X-Citeck-CSRF`) on localhost TCP only
- **Smart regenerate** via deployment hash comparison (like `docker-compose up`) — unchanged containers keep running
- **3-phase doStart**: I/O outside lock → remove stale → update state under lock
- **Graceful shutdown**: phased stop groups (proxy → webapps → keycloak → infra)
- **Detach mode**: `Runtime.Detach()` exits without stopping containers for zero-downtime binary upgrades; Docker owns containers, not the launcher (same principle as kubelet restarts)
- **`GetHashInput` stability** is a hard compatibility contract across versions — changes require migration
- **Secrets**: AES-256-GCM per-secret encryption via `SecretService`; system secrets (JWT, OIDC, admin password) via `resolveOneSystemSecret` pattern
- **Admin password**: generated once on first server-mode start; same password for Keycloak (master + ecos-app), RabbitMQ, PgAdmin; desktop mode always uses "admin"
- **Admin password change** via `citeck setup admin-password`: kcadm.sh for Keycloak, rabbitmqctl for RabbitMQ, setup.py for PgAdmin (all runtime, no container restart); webapps reloaded for RABBITMQ_PASSWORD env update
- **Two storage backends**: flat files (server) / SQLite (desktop); desktop mode via explicit `--desktop` flag
- **go-git** (pure Go) for git operations — no external git binary required
- **ACME** profiles via custom JWS; LE works with IPs via shortlived profile (~6 day certs)
- **Reconciler**: exponential backoff retry for failed apps (1m → 30m max); liveness probes with 3-failure threshold
- **install.sh** is a thin bootstrap (~200 lines): fetch + exec. All lifecycle logic (install/upgrade/rollback/SIGKILL preserve/systemd drop-in) lives in Go (`internal/cli/installer_lifecycle.go`)
- **CloudConfigServer** skipped in server mode (webapps disable it via env)

For detailed phase-by-phase history, see `PROGRESS.md`.

## CI/CD

GitHub Actions:
- **Release workflow** (`.github/workflows/release-go.yml`): triggered by `v*.*.*` tags, builds Linux amd64 server binary, creates draft GitHub release. Uses `go-version-file: go.mod`.
- **CI workflow** (`.github/workflows/ci.yml`): triggered on push/PR to master and `release/**` branches. Runs `go vet`, `golangci-lint v2.11.4`, `go test -race`, `pnpm vitest run`, full server build.
- **Linting**: `.golangci.yml` v2 format, 21 linters, G104 excluded (cleanup errors), test files relaxed for dupl/gosec/unparam.
