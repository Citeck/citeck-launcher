# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Citeck Launcher manages Citeck ECOS namespaces and Docker containers. It is a single Go binary (~14MB) that serves as both CLI and daemon, with an embedded React Web UI on `http://127.0.0.1:7088`.

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
make tools                    # Install golangci-lint v2.7.2
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
- `golangci-lint` v2.7.2 with 21 linters (`.golangci.yml`): dupl, errorlint, gochecknoinits, gocritic, gocyclo, gosec, govet (shadow), ineffassign, misspell (US), modernize, nakedret, nestif, prealloc, revive, staticcheck, testifylint, unconvert, unparam, unused, wrapcheck
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

## Current Status

### Web UI Phase 1 — COMPLETE (2026-03-25)
Full dashboard, app table, logs, config editor.

### Web UI Phase 2 — COMPLETE (2026-03-25)
Full Web UI feature set: Welcome screen, wizard, secrets, diagnostics, context menus, form framework, journal browser, snapshot import/export, light theme, Playwright E2E.

### Phase 3: Architecture Gap Closure — COMPLETE (2026-03-25)
Actions service, go-git, form validation, bind-mount volumes, snapshot export/import, runtime hardening (stalled recovery, stale cleanup, socket lock). Platform deploys 19/19.

### Phase 4: CLI Completion + Production Hardening — COMPLETE (2026-03-25)
Snapshot URL download (HTTP resume, SHA256, retry), CLI clean/apply/diff/status --watch, git hardening (hard-reset, reclone on corruption, URL change detection), dead code cleanup. 3 code review passes.

### Phase 5: Full Parity — COMPLETE (2026-03-26)
All 25 P0/P1 gaps closed across 8 sub-phases.

### Phase 6: Final Parity + Kotlin Removal — COMPLETE (2026-03-26)
14 backend gaps + 7 web UI gaps closed. Kotlin code removed.

### Server Deployment Testing — COMPLETE (2026-03-26)
Tested on remote server (community 2025.12). 13 gaps found and fixed:
- Docker container log rotation (json-file 50m/3 files)
- Pull stall prevention via heartbeat
- Smart regenerate: doRegenerate() keeps unchanged containers running (docker-compose up style)
- 3-phase doStart: resolve digests (no lock) → remove stale (WaitGroup, no lock) → update state (lock)
- Deterministic webapp ports (sorted names), fixed infra ports (zk=17018, alf=17019)
- ImageDigest resolved before hash comparison, excluded from hash (set after pull)
- Unified GetHash/GetHashInput; JWTSecret generated per-instance
- Let's Encrypt: full ACME integration + IP cert via shortlived profile + auto-renewal
- HTTPS scheme for external hosts without local TLS (reverse proxy assumed)
- Snapshot CLI (list/export/import), `citeck start` delegates to running daemon via Unix socket

### Phase 7: Production Hardening — COMPLETE (2026-03-26)
37 issues fixed across 3 sub-phases + 2 review passes (15 additional fixes).
Version forwarding, TCP timeouts, CORS wiring, reconciler retry with backoff,
graceful shutdown groups, config validation, StartApp method, atomic state writes,
ACME renewal tests, network orphan cleanup, self-signed cert auto-generation.

### Phase 8: Production-Grade Hardening — COMPLETE (2026-03-26)
57 issues across 4 sub-phases (8a–8d). 2 code review passes (15 additional fixes).

### Server Deployment Testing Round 2 — COMPLETE (2026-03-26)
Tested on remote server with community 2025.12 (second round). Found and fixed:
- JWT secret size (32→64 bytes for HS512 compatibility)
- TLS cert stale directory detection and root cause fix (docker/client.go parent dir creation)
- ACME cert hostname mismatch on host change (CertMatchesHost with expiry check)
- Self-signed cert missing on reload path
- Deduplication: tlsutil package, ensureSelfSignedCert shared helper
- 8 new tests (CertMatchesHost, GenerateSelfSignedCert)

### Phase 9: Production Hardening for Scale — COMPLETE (2026-03-27)
12 issues across 3 sub-phases + 1 code review pass (4 additional fixes).
- 9a: Atomic writes — shared `fsutil.AtomicWriteFile` (temp+fsync+rename)
- 9b: Security — snapshot input validation, log memory limit
- 9c: Concurrency — reconciler 3-phase lock, timeouts, ACME rate limit, OIDC secret

### Phase 10: mTLS for Web UI + Production Hardening — COMPLETE (2026-03-27)
25 issues across 5 sub-phases + 2 code review passes.
- 10a: P0 shutdown safety — appWg/reconcileWg wait before close(eventCh), bgWg tracking for snapshot download
- 10b: mTLS infrastructure — GenerateClientCert, LoadCACertPool, atomic selfcert, WebUICADir/WebUITLSDir paths, cert CLI (generate/list/revoke)
- 10c: mTLS server+client — tls.RequireAndVerifyClientCert for non-localhost, dynamic cert pool reload via GetConfigForClient, CLI --tls-cert/--tls-key/--server-cert/--insecure flags, install wizard auto-generates client cert
- 10d: P1 bugs — channel-based waitForDeps (replaces statusCond), StopApp re-lookup, RestartApp timeout context, phased doShutdown, ACME server timeouts, snapshot vol.Name sanitization, rate limiter reduced to 1000 entries
- 10e: P2 fixes — fsutil.AtomicWriteFile for config, MaxBytesReader for snapshot upload, blocking Stop via stopCh, WaitForContainerExit pre-check, system dump through struct marshal, Logs.tsx debounced search, Welcome.tsx error state, SSE backoff reset on open, Config.tsx beforeunload

### Key Technical Decisions
- SSE (not WebSocket) for real-time events
- TCP bound to 127.0.0.1 by default; non-localhost requires mTLS (client certs in webui-ca/, dynamic pool reload)
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
- Smart regenerate via deployment hash comparison (like docker-compose up)
- 3-phase doStart: I/O outside lock, state update inside lock
- JWTSecret generated per-instance, persisted to `conf/jwt-secret`
- ACME profiles via custom JWS (Go stdlib doesn't support profiles yet)
- HTTPS scheme for external hosts even without local TLS
- Graceful shutdown: phased stop groups (proxy → webapps → keycloak → infra)
- Reconciler: exponential backoff retry for failed apps (1m → 30m max)
- Config validation at parse time (port range, TLS host, LE host, auth users)

### Server Deployment Testing Round 3 — COMPLETE (2026-03-27)
Tested on remote server with community 2025.12 (clean deployment). Found and fixed:
- Stale Unix socket detection: `DetectTransport` now dials socket instead of `os.Stat` (client/transport.go)
- `citeck start --foreground` no longer fails when stale socket exists
- All 19 services start correctly (BASIC and Keycloak auth modes)
- Host switching (launcher2.sipaha.ru ↔ launcher.sipaha.ru) with automatic LE cert obtainment
- Snapshot export/import cycle verified
- Playwright browser testing: HTTPS + Keycloak OIDC login + full dashboard

### Key Technical Decisions (Phase 10)
- mTLS for non-localhost Web UI: self-signed client certs in `conf/webui-ca/`, server cert in `conf/webui-tls/`
- Dynamic cert pool reload via `tls.Config.GetConfigForClient` (no daemon restart on cert add/revoke)
- CLI `--tls-cert/--tls-key/--server-cert/--insecure` flags; auto-discover from env vars and local confdir
- Channel-based `statusNotify` (replaces `sync.Cond` — eliminates lock inversion in `waitForDeps`)
- Dedicated `stopCh` (buffered 1) for stop signal — cannot be dropped unlike `cmdCh` send
- Token auth removed entirely — mTLS is the only auth mechanism for non-localhost

### Phase 11: Production Readiness — COMPLETE (2026-03-27)
26 issues across 5 sub-phases + 3 code review passes (19 additional fixes).
- 11a: P0 security — single-mux architecture (socketMux for all transports), CORS exact origin validation, exec output cap (1MB), PutAppConfig field whitelist
- 11b: HTTP hardening — WriteTimeout 30s + ResponseController for streaming, access logs with remote addr/CN/X-Request-Id, health 3-state (healthy/degraded/unhealthy), SSRF protection (DNS resolution + blocked IP ranges + ssrfSafeClient with DialContext), atomic file writes
- 11c: Reliability — streaming logs (chunked ReadableStream, not polling), virtual list (@tanstack/react-virtual), registry auth pre-cached, context-aware HTTP probes, FindApp O(1) map lookup, SQLite MaxOpenConns(1)
- 11d: Observability — machine-readable error codes, Prometheus /metrics (text exposition), daemon log rotation (50MB/3 files, fsutil.RotatingWriter), SSE sequence numbers + gap detection, X-Request-Id (8 hex, crypto/rand), SSE fetchData debounced
- 11e: Remaining — socket permissions 0o600, React ErrorBoundary, SSE reconnect generation counter, Docker Names[0] bounds check, daemon.yml listen validation

### Key Technical Decisions (Phase 11)
- Single-mux architecture: all routes on `socketMux`, shared by Unix socket, localhost TCP, and mTLS TCP. Localhost TCP is trusted (desktop thin client); non-localhost requires mTLS. CSRF protects localhost TCP from cross-origin attacks.
- CORS exact origin:port validation (no prefix matching); OPTIONS rejected for unknown origins
- SSRF double defense: validateSnapshotURL (pre-check) + ssrfSafeClient (DialContext re-validation at connect time, prevents DNS rebinding)
- WriteTimeout 30s globally; streaming handlers disable via `http.ResponseController.SetWriteDeadline(time.Time{})`
- `statusRecorder.Unwrap()` for proper ResponseController chain through middleware
- Prometheus metrics hand-written (no dependency); label values escaped per exposition spec
- Daemon log rotation via `fsutil.RotatingWriter` (thread-safe, Close() on shutdown)
- WebUI mTLS server cert issued for 100 years (36500 days)
- PutAppConfig whitelist: only env, resources, probes mutable; image/volumes/cmd/ports locked (defense-in-depth)

### Phase 16: Secrets Encryption + Desktop Phase 2 — COMPLETE (2026-03-31)
30+ commits across 9 steps + 3 code review rounds (17 issues fixed) + lint cleanup (347→0 warnings).

**P0: Secrets Encryption:**
- `SecretService` (AES-256-GCM, PBKDF2-HMAC-SHA256 1M iterations, per-secret random 12-byte IV)
- Secrets NEVER stored plaintext on disk — Kotlin import auto-encrypts with same password
- `secretReader`/`secretWriter` interfaces for uniform access through encryption layer
- API: `GET /secrets/status`, `POST /secrets/unlock`, `POST /secrets/setup-password`
- Dashboard: multi-step master password dialog (kotlin-decrypt / setup-password / unlock)
- Secrets page: encryption badge, locked warning, inline unlock form
- Sentinel errors: `ErrSecretsLocked`, `ErrAlreadyEncrypted`, `ErrCorruptedKeystore`
- 15 crypto tests (round-trip, wrong password, locked state, restart simulation, raw DB verification)

**P1: Desktop Bug Fixes:**
- Citeck logo for window + tray icons (go:embed)
- LogViewer: first-chunk replace (no REST+stream duplication)
- DaemonLogsViewer: streaming via `?follow=true` (replaces 5s polling)
- Desktop proxy: direct HTTP client via Unix socket (replaces httputil.ReverseProxy which dropped body with ContentLength=0 from Wails AssetServer)
- TCP listener skipped in desktop mode (Wails proxies through socket)
- Informative error page: daemon error + startup logs on failure, auto-refresh
- DevTools menu item in system tray
- Stale WAL/SHM cleanup on SQLiteStore open
- .sh file permissions: explicit chmod after write

**Lint Cleanup:**
- `.golangci.yml` with 21 linters (from citeck-ci reference project)
- Makefile: `-s -w` ldflags, fmt/tidy/coverage/tools targets, pinned golangci-lint v2.7.2
- All 347 warnings fixed across 142 files

### Key Technical Decisions (Phase 16)
- Per-secret encryption (not single blob like Kotlin) — changing one secret doesn't re-encrypt all
- `SecretService` wraps `SQLiteStore`, does NOT change `Store` interface — server mode unaffected
- Verify token (`"citeck-secrets-v1"` encrypted with derived key) validates password without decrypting all secrets
- Desktop mode: no TCP listener, Wails → direct HTTP client → Unix socket → daemon
- Wails AssetServer sends POST body with ContentLength=0 (streamed) → httputil.ReverseProxy drops body → replaced with manual HTTP client that buffers body
- `DaemonStatus` (atomic fields + LogBuffer) shared between daemon loop and proxy for informative error display
- `CleanLogHandler` for human-readable slog output: ISO 8601 UTC, no quoted keys, padded level
- Bundle resolver: GIT_TOKEN fallback when bundleRepo.AuthType is empty (Kotlin migration compat)

### Other References
- **`PROGRESS.md`** — tracks completed work (historical)

### Phase 12: GA Readiness — COMPLETE (2026-03-27)
23 issues across 5 sub-phases + 3 code review passes (8 additional fixes).
- 12a: CSRF + security — CSRFMiddleware (X-Citeck-CSRF header) on tcpMux, Web UI CSRF header on all POST/PUT/DELETE, ssrfSafeClient for downloadAndImportSnapshot, HTTP handler tests, two-mux boundary test
- 12b: Stability — RecoveryMiddleware (panic → 500 + slog), Logs page 50K line ring buffer, SQLite schema versioning (schema_version table), DB chmod 0o600 after migrate, install wizard atomic config write
- 12c: CLI + observability — shell completion (bash/zsh/fish/powershell), citeck_build_info Prometheus metric, error codes on ~15 high-value sites, daemon.log in system dump ZIP, runtime log level control (PUT /api/v1/daemon/loglevel, socket-only)
- 12d: Documentation — README.md rewrite (Go binary), API reference (docs/api.md), config reference (docs/config.md), operator runbook (docs/operations.md)
- 12e: UI polish — Dashboard loading skeleton, Welcome page error handling (show error + keep list), snapshot export SHA256 sidecar, namespace config apiVersion field

### Key Technical Decisions (Phase 12)
- CSRF via custom header (X-Citeck-CSRF) forces CORS preflight → preflight rejected for unknown origins → no body-free POST attacks
- CSRF on tcpMux only; socket and mTLS don't need it (already authenticated)
- RecoveryMiddleware outermost on TCP path (catches panics in all middleware layers)
- SQLite schema versioning: sequential migrations with schema_version table; errors.Is(sql.ErrNoRows) for version check
- slog.LevelVar for runtime log level control without daemon restart
- MarshalNamespaceConfig shallow-copies config to avoid mutating live state

### Phase 13: Production Hardening for Scale — COMPLETE (2026-03-27)
20 issues across 5 sub-phases + 2 code review passes (6 additional fixes).
- 13a: Security — `api.MaskSecretEnv` (shared, server-side in handleAppInspect), SecurityHeadersMiddleware (X-Frame-Options, CSP, HSTS), TLS 1.3 minimum for mTLS, maskNamespaceConfigSecrets in system dump
- 13b: Reliability — daemon.yml `docker.stopTimeout` wired to runtime, restartApp independent stop context (context.Background), socket server access logging, bind-mount MkdirAll error propagation
- 13c: Observability — HTTP request metrics (counter + histogram, hand-coded Prometheus), OperationRecord caller identity (RequestID, ClientCN), history rotation via fsutil.AtomicWriteFile + mutex, SSE drop counter metric
- 13d: API + CLI — `citeck validate` command, validateAppName on all app-scoped handlers, error codes on remaining sites (NAMESPACE_RUNNING), writeInternalError helper (log + generic 500)
- 13e: Web UI — env var display (masked values muted, ellipsis), AppDetail loading skeleton, toast notification system (zustand + auto-dismiss), SSE reconnect toast

### Key Technical Decisions (Phase 13)
- `api.MaskSecretEnv` in shared `internal/api/secrets.go` — used by both CLI and daemon, no duplication
- SecurityHeadersMiddleware signature `func(bool, http.Handler) http.Handler` — matches codebase convention
- Histogram buckets: exclusive recording (break on first match), cumulative output in writePrometheus
- `writeInternalError` logs full error via slog.Error, returns generic "internal error" to client — prevents internal path/message leakage
- `numHistogramBuckets` const with init() panic guard — prevents silent array size mismatch
- History rotateMu protects concurrent rotateIfNeeded calls

### Phase 14: Production Hardening at Scale — COMPLETE (2026-03-27)
20 issues across 4 sub-phases + 2 code review passes (11 additional fixes).
- 14a: Backend reliability — SSE heartbeat (15s keepalive), safe reclone (clone to .tmp, atomic swap), namespace ID collision (O_EXCL, 409), concurrent reload guard (TryLock, 409)
- 14b: Web UI reliability — Logs string[] storage + incremental level detection, ReDoS structural check, fetchJSON error body parsing, fetchWithTimeout (30s AbortController), no skeleton flash on SSE refresh, DaemonLogs visibility-aware polling (5s), middleware Flush() for streaming
- 14c: Test coverage & CI — runtime behavioral tests (5 tests with docker.RuntimeClient mock), RotatingWriter tests, -race in Makefile, pre-merge CI workflow (go vet + golangci-lint + go test -race + vitest), removed stale Kotlin workflow
- 14d: UX polish — AppDetail AbortController for stale fetches, restart error toast, Config fetch error display, SSE lastSeq reset on reconnect, follow mode skips initial REST fetch

### Key Technical Decisions (Phase 14)
- `docker.RuntimeClient` interface extracted for testability — daemon uses `*docker.Client` directly for richer method set
- Runtime behavioral tests use `statusNotify` channel (event-driven, not polling)
- ReDoS: structural nested-quantifier check (`NESTED_QUANTIFIER_RE`) instead of timing heuristic
- `fetchWithTimeout` wrapper with signal composition for all non-streaming requests
- Middleware `Flush()` added to `recoveryWriter`/`statusRecorder` — fixes SSE/log streaming through middleware chain
- CI: golangci-lint + go test -race + vitest; release workflow uses `go-version-file: go.mod`

### Phase 15: Lens-Inspired UI Redesign — COMPLETE (2026-03-28)
5 sub-phases, 2 code review passes (5 additional fixes).
- 15a: Panel infrastructure — `lib/panels.ts` (Zustand panel store), `useResizeHandle.ts` (pointer-capture drag), `BottomPanel.tsx` (lazy mount, tab strip, collapse), `RightDrawer.tsx` (overlay with slide animation)
- 15b: Extract reusable components — `LogViewer.tsx` (from Logs.tsx, +compact/active props), `ConfigEditor.tsx` (from Config.tsx), `DaemonLogsViewer.tsx` (from DaemonLogs.tsx), `AppDrawerContent.tsx` (inspect + actions), `AppConfigEditor.tsx` (YAML + files editor)
- 15c: Dashboard integration — sidebar + app table + right drawer overlay + bottom panel, AppTable click → openDrawer/openBottomTab (no navigation), TabBar settings → bottom panel, sidebar gear icon for ns config, panel reset on Dashboard unmount
- 15d: Polish — Escape key (close drawer → collapse panel), height persistence in localStorage, active-tab-only streaming, drawer row highlight
- 15e: Server mode — `desktop` field in DaemonStatusDto, conditional Welcome screen (server mode skips Welcome at root)

### Key Technical Decisions (Phase 15)
- Bottom panel renders inside Dashboard only (not globally) — clean unmount, no stale state
- Lazy tab mounting via `mountedRef` (Set<string>) — tab component created on first activation, stays mounted until closed
- `active` prop on LogViewer/DaemonLogsViewer controls streaming — `active=false` aborts stream, `active=true` resumes
- `bottomPanelOpen` gates `active` in BottomPanel — collapsed panel pauses all streaming
- `resetPanels()` called in Welcome on namespace switch — prevents stale drawer/tabs surviving namespace change
- Stopped namespace shows full app catalog from `d.appDefs` via `appDefsToStoppedApps()` — apps always visible like k8s desired state
- RightDrawer is absolute-positioned inside content area (not portal) — doesn't interfere with bottom panel
- `pointercancel` handled in useResizeHandle — prevents stuck resize state on touch interruption
- Server mode detection via single `getDaemonStatus()` call on app mount

## CI/CD

GitHub Actions:
- **Release workflow** (`.github/workflows/release-go.yml`): triggered by `v*.*.*` tags, builds on Linux/Windows/macOS (x64 + arm64), creates GitHub release. Uses `go-version-file: go.mod`.
- **CI workflow** (`.github/workflows/ci.yml`): triggered on push/PR to master. Runs `go vet`, `golangci-lint v2.7.2`, `go test -race ./internal/...`, `npx vitest run`.
- **Linting**: `.golangci.yml` v2 format, 21 linters, G104 excluded (cleanup errors), test files relaxed for dupl/gosec/unparam.
