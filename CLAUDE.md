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
- ZooKeeper: `ZOO_4LW_COMMANDS_WHITELIST=srvr,mntr,ruok,stat` — `srvr` required by zkServer.sh health, `mntr` required by observer ZK monitor

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

**Quality & Reliability:**
- `.golangci.yml` with 21 linters (from citeck-ci reference project), 0 warnings
- Makefile: `-s -w` ldflags, fmt/tidy/coverage/tools targets, pinned golangci-lint v2.11.4
- Per-page ErrorBoundary — page crash shows inline error + retry, rest of app stays alive
- Locale completeness test (`import.meta.glob`) — auto-discovers all locale files, verifies all keys present
- All 8 locales (en/ru/de/es/fr/ja/pt/zh) fully translated for secrets/migration/encryption keys
- Defensive null checks on API DTOs (namespace.apps ?? [])

### Key Technical Decisions (Phase 16)
- Per-secret encryption (not single blob like Kotlin) — changing one secret doesn't re-encrypt all
- `SecretService` wraps `SQLiteStore`, does NOT change `Store` interface — server mode unaffected
- Verify token (`"citeck-secrets-v1"` encrypted with derived key) validates password without decrypting all secrets
- Desktop mode: no TCP listener, Wails → direct HTTP client → Unix socket → daemon
- Wails AssetServer sends POST body with ContentLength=0 (streamed) → httputil.ReverseProxy drops body → replaced with manual HTTP client that buffers body
- `DaemonStatus` (atomic fields + LogBuffer) shared between daemon loop and proxy for informative error display
- `CleanLogHandler` for human-readable slog output: ISO 8601 UTC, no quoted keys, padded level
- Bundle resolver: GIT_TOKEN fallback when bundleRepo.AuthType is empty (Kotlin migration compat)
- Per-page `<ErrorBoundary inline>` — crash in one page doesn't kill the whole app
- Locale completeness test via `import.meta.glob` — auto-discovers locale files, no manual imports
- Stale WAL/SHM cleanup on SQLiteStore open — prevents disk I/O errors after crash

### Server Deployment Testing Round 4 — COMPLETE (2026-04-01)
Bundle version upgrade + host/auth switching tested on remote server. 5 bugs fixed + cached bundle fallback + CLI per-app commands.

**Bug fixes:**
- Workspace config fallback: `loadWorkspaceConfig()` returned empty struct instead of nil, preventing fallback to `_workspace` repo with `path: community` setting
- `.sh` file permissions on reload: generated scripts written with 0644 instead of 0755 on reload path (initial startup was correct)
- Bundle ref display: `runtime.config` not updated on reload → status always showed original bundle version
- Daemon shutdown hang: `Start()` returned nil instead of `ErrShutdownRequested` after HTTP-triggered shutdown → process never exited
- Route constant: `handleDaemonLogs` route used hardcoded string instead of `api.DaemonLogs` constant

**Cached bundle fallback (Kotlin parity):**
- `NsPersistedState.CachedBundle` stores last successfully resolved `bundle.Def`
- Written after every successful resolve (if changed), via `persistState()`
- On startup/reload: if `Resolve()` fails → load cached bundle from state file → use it with WARN log
- Verified: `community:2099.99` (non-existent) → fell back to cached `2026.1` with 14 apps

**CLI per-app commands:**
- `citeck start [app]` — start single app (removes from detached) or namespace
- `citeck stop [app]` — stop single app (marks as detached) or namespace
- `citeck logs [app]` — daemon logs if no app, container logs if app given
- Client methods: `StopApp`, `StartApp`, `GetDaemonLogs`, `StreamDaemonLogs`

**Verified scenarios (Playwright):**
- Bundle upgrade: community:2025.12 → community:2026.1 (14 apps, 2 new: integrations + ecos-project-tracker)
- Host switch: domain (LE cert) ↔ IP (self-signed) — only proxy+keycloak recreated
- Auth switch: KEYCLOAK → BASIC — keycloak removed, proxy+emodel recreated
- Auth switch: BASIC → KEYCLOAK — keycloak added, proxy+emodel recreated

### Key Technical Decisions (Server Test 4)
- `loadWorkspaceConfig()` returns nil (not empty struct) when no file found — enables fallback chain
- `.sh` permissions handled identically in startup (server.go) and reload (routes.go) paths
- Atomic `Regenerate(apps, cfg, bundleDef)` — config + cached bundle update in dispatch loop (no UpdateConfig + Regenerate race)
- `daemon.Start()` always returns `ErrShutdownRequested` after Serve() exits — caller handles as clean exit
- `signal.Stop(sigCh)` cleanup in CLI log streaming — proper OS signal deregistration
- Cached bundle stored per-namespace in `state-{nsID}.json` — same scope as Kotlin's H2 `namespace-runtime-state`

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

### Phase 17: Self-Healing Runtime — COMPLETE (2026-04-06)
11 commits across backend + web UI + tests + review fixes.

**Liveness probes:**
- All services get `LivenessProbe`: webapps (HTTP `/management/health`), postgres/observer-postgres (`pg_isready`), zookeeper (HTTP `/commands/ruok` on admin port 8080), rabbitmq (`rabbitmq-diagnostics check_running`), mongo (`db.adminCommand('ping')`), keycloak (HTTP `/health/live`), observer (HTTP `/health`)
- Skip: mailhog, pgadmin, onlyoffice, proxy
- Failure counting: 3 consecutive failures (FailureThreshold=3) before restart (not single-failure)
- `checkLiveness` runs in STALLED state too (previously stopped for all apps when one died)
- HTTP probes use container IP via Docker API (not curl-in-container)

**Startup timeouts reduced:**
- Webapps/observer/proxy: 30 × 10s = 5 min (was 360 × 10s = 1 hour fallback)
- Postgres/observer-postgres/keycloak: 60 × 10s = 10 min

**Restart tracking:**
- `RestartCount` per app (in `AppRuntime` and API `AppDto`)
- `RestartEvent` ring buffer (100 events) with reason/detail/diagnostics path
- Persisted in `state-{nsID}.json` — survives daemon restart, cleared on namespace stop
- API: `GET /namespace/restart-events`, SSE: `restart_event` type

**Pre-restart diagnostics:**
- Thread dump (`jcmd 1 Thread.print`) for Java apps + last 500 container log lines
- Saved to `volumes/{nsID}/diagnostics/{app}/{timestamp}.txt` (auto-cleanup >7 days)
- Captured BEFORE container restart (while container is still alive)
- API: `GET /diagnostics-file?path=...` with path traversal protection

**Configuration:**
- `daemon.yml` → `reconciler.livenessEnabled` (bool, default true)
- `namespace.yml` → `webapps.{name}.livenessDisabled` (bool, default false)

**Web UI:**
- Restart count badge (recycle symbol + count) on app status in AppTable
- Restart Events bottom panel tab (time, app, reason badge, detail)
- Auto-refresh via SSE → totalRestarts derived from namespace store

### Key Technical Decisions (Phase 17)
- `ContainerLogs` added to `RuntimeClient` interface (not type-asserted to `*docker.Client`)
- Restart state cleared in `doStop` (not `doStart`) — survives daemon restart recovery, cleared on explicit namespace stop
- `handleDiagnosticsFile` path traversal protection: `HasPrefix(absPath, diagDir + os.PathSeparator)`
- Liveness failures cleaned in `setAppStatus` when app leaves RUNNING (covers all transition paths)
- `cleanupOldDiagnostics` runs every reconcile cycle (60s) — single `ReadDir` when no diagnostics dir exists

### Phase 18: Bundle Upgrade + Image Cleanup + Docs — COMPLETE (2026-04-07)

**Bundle upgrade:**
- `POST /api/v1/namespace/upgrade` endpoint — update bundleRef in namespace.yml + reload
- `doReload()` extracted from `handleReloadNamespace` for reuse by upgrade handler
- `citeck upgrade [ref]` CLI command, `citeck upgrade --list` shows available versions
- Client methods: `UpgradeNamespace`, `ListBundles`
- Web UI: upgrade button (ArrowUpCircle) in Dashboard sidebar, FormDialog version picker
- All 8 locales updated with `upgrade.*` i18n keys

**Image cleanup:**
- `citeck clean --images --execute` prunes dangling Docker images after confirmation
- `PruneUnusedImages` added to Docker client (ImagesPrune with dangling filter)

**Snapshot export improvements:**
- `citeck snapshot export --dir /path/` — daemon writes directly to specified directory
- Auto stop/start: if namespace is running, CLI stops it, exports, then starts back
- Interactive prompts for output dir and stop confirmation (`--yes` skips all)
- `snapshotAndWait` replaces `snapshotWithWait` — always synchronous, no dead `wait` param

**CLI namespace guard:**
- `citeck start` in server mode requires `namespace.yml` — fails with "Run citeck install" message

**Self-update:**
- `citeck self-update` — download latest from GitHub Releases, verify SHA256, atomic replace
- `citeck self-update --file <path>` — offline update from local binary
- `citeck self-update rollback` — revert to previous version (backup auto-created)
- Stops daemon before binary replace, starts after; `--check` for version check only
- Dev build guard: refuses self-update from `version=dev`
- **REMOVED in 2.1.0**, replaced by `install.sh` one-liner — same flow (download → swap → rollback) but preserves running platform containers across the swap via detach/SIGKILL path.

**P12 browser certificates:**
- `citeck webui cert` and `citeck install` auto-generate `.p12` file for Web UI browser import (mTLS)
- Written to current directory as `citeck-webui-{name}.p12`; private key no longer printed to console
- `go-pkcs12` library (pure Go, no openssl dependency)

**CLI improvements:**
- `citeck start/stop -d` — detach mode (fire-and-forget, like docker-compose)
- `citeck stop` — synchronous by default, shows live progress per app (TTY-aware)
- `citeck restart` — restart entire namespace (no arg) or single app
- `citeck upgrade --dry-run` — preview bundle change, verify target exists
- `citeck snapshot import` — auto stop/start (same as export)
- `--format json` global flag (renamed from `--output` to avoid ambiguity with file paths)

**Observer:**
- Default image `citeck/observer:1.1.0` (fallback when not in bundle)
- All ports explicit: SERVER_PORT, OTLP_GRPC_PORT, OTLP_HTTP_PORT, LOG_RECEIVER_UDP_PORT
- DISCOVERY_APP_NAME and UDP port mapping added

**Web UI log:**
- Shows machine IP (via `net.InterfaceAddrs`) instead of raw `0.0.0.0` in startup log

**Bundle listing fix:**
- `handleListBundles` now checks `data/repo/{path}` first (offline-imported bundles), then falls back to `data/bundles/{repo.ID}` (cloned repos)

### Phase 19: Install Wizard UX Overhaul + Live Status Unification — COMPLETE (2026-04-07)
22 commits across wizard rewrite, i18n, TLS auto-detection, live status, UX polish, Keycloak fix.

**CLI i18n system (`i18n.go`, `locales/*.json`):**
- `SupportedLocales` — single source of truth for all 8 languages (en/ru/zh/es/de/fr/pt/ja)
- JSON locale files embedded via `go:embed`, `t(key, args...)` with `{param}` interpolation
- Fallback chain: selected locale → en.json → raw key
- `TestLocaleCompleteness` — verifies all locale files have exactly the same keys as en.json
- Language selection first step in wizard — all subsequent prompts in selected language

**Wizard rewrite (`install.go`):**
- 7 prompts (was 12): Language, Welcome, Hostname, TLS (auto), Port, Auth, Release, Systemd, Start
- Removed: Display name (auto from hostname), PgAdmin (default off), Snapshot ID, separate Remote/Firewall questions
- TLS auto-detection: for non-localhost, tries LE staging → if OK uses LE, else self-signed fallback
- Quick connectivity pre-check (5s TCP dial) skips LE when offline
- `--offline` flag + `--workspace` implies offline: no network checks
- Multi-level release selection: top-level (latest per repo) + "Other version..." drill-down with Back navigation
- Release versions fetched from workspace repo (online) or local files (offline/workspace)
- Error when no releases found (offline without `--workspace`)
- `printAccessInfo()` — final block with platform URL, login credentials, self-signed cert warning
- `config.DetectOutboundIP()` extracted to shared package (used by cli + daemon)
- `parseUsers()` stores usernames only (generator creates password = username pairs)

**TLS auto-detection (`configureTLSAuto`, `acme.TryStaging`):**
- `acme.TryStaging(ctx, hostname)` — full ACME flow against LE staging server with ephemeral keys
- Works for both domains and IP addresses (IP uses shortlived profile)
- Online: staging OK → LE production, staging fail → self-signed with message
- Offline/no internet: self-signed immediately (5s dial timeout, no 60s hang)
- Only localhost skips to manual TLS menu

**Live status unification (`livestatus.go`):**
- `renderAppTable()` — shared table renderer using `output.FormatTable` + `ColorizeStatus`
- `streamLiveStatus` (start.go) and `streamStopStatus` (stop.go) refactored to use it
- TTY: clear + reprint table. Non-TTY: print summary only on change.

**Prompt helpers (`install_prompt.go`):**
- `promptNumber`, `promptText`, `promptYesNo` with TTY ANSI cleanup
- `isTTYOut()` / `clearLines(n)` — shared TTY detection and ANSI line erasure

**Bug fix: Keycloak liveness probe port 8080 → 9000**
- Keycloak 26+ moved `/health/live` to management interface on port 9000
- Port 8080 returns 404 → 3 consecutive failures → restart → infinite loop
- Verified on test server: port 9000 returns 200 OK, Keycloak stable after fix

### Key Technical Decisions (Phase 19)
- CLI i18n via JSON files + `go:embed` — matches web UI pattern, no external dependencies
- `SupportedLocales` as centralized list — wizard, tests, and future web UI sync use it
- TLS auto-detection at install time (not runtime) — config written once, daemon uses it
- LE staging validates domain/IP reachability without consuming production rate limits
- LE works with IPs via ACME shortlived profile (~6 day certs, 6h renewal interval)
- Release selection grouped by repo — top-level shows latest, "Other" drills into full version list
- `repoVersions.displayName()` eliminates duplicated label logic across picker functions
- Auth matching by exact `==` against translated label (not `strings.Contains` on brand name)
- Keycloak health on port 9000 (management interface) — Keycloak 26+ change

### Phase 20: Codebase Refactoring (File Splits + Dedup) — COMPLETE (2026-04-08)
6 tasks: 4 file splits + 1 cleanup + final verification. Pure structural refactoring with zero behavior changes.

**File splits:**
- `runtime.go` (1,754→317 lines) → `runtime_commands.go` (301), `runtime_orchestration.go` (348), `runtime_state.go` (169), `runtime_dto.go` (100), `runtime_app.go` (415), `runtime_probes.go` (160)
- `routes.go` (1,470 lines) → `routes_apps.go` (516), `routes_config.go` (404), `routes_system.go` (379), `routes_volumes.go` (66) — domain-specific route files, `doReload` moved to `server.go`
- `routes_p2.go` (1,418 lines) → `routes_ns.go` (363), `routes_secrets.go` (322), `routes_snapshots.go` (601), `routes_diagnostics.go` (134)

**Dedup/helpers:**
- `routes_helpers.go` (76 lines): `parseTailParam`, `requireRuntime`, `volumesDir`, `validateID`, `sanitizeName`, `activeNsID`, `safeIDPattern`
- Replaced 2 duplicated tail-parsing blocks and 5 duplicated nil-runtime guards in route handlers

**Cleanup:**
- `GracefulShutdownOrder` → `gracefulShutdownOrder` (no production callers, only test)

### Key Technical Decisions (Phase 20)
- Split boundaries follow domain ownership: types/constructors, commands, orchestration (runLoop/doStart/doStop), state/persistence, DTO, app lifecycle, probes/stats, config/control, system/observability, volumes, namespaces, secrets, snapshots, diagnostics
- `doReload` moved from routes.go to server.go — business logic separated from HTTP handlers
- Shared helpers in `routes_helpers.go` — cross-domain utilities (validation, parsing, guards)
- `handleReloadNamespace`/`handleUpgradeNamespace` nil guards NOT replaced with `requireRuntime` — they use a compound check (`runtime == nil || nsConfig == nil || bundleDef == nil`) under `configMu.RLock()`

### Phase 21: Wizard Rewrite + CLI Polish — COMPLETE (2026-04-08)
Install wizard reduced to 3 interactive steps, CLI i18n, bundle version parity, uninstall command.

**Wizard rewrite (3 steps):**
- Hostname (auto-detected via ipify), TLS (5 options: LE auto, LE force, self-signed, custom cert, disabled — with LE staging probe), Release (sorted newest-first, Kotlin BundleKey parity)
- Registry auth prompt for enterprise bundles
- Auto-configured: port (443/80), Keycloak SSO, systemd service
- `--offline` / `--workspace` for air-gapped installs

**Bundle versions:**
- Kotlin `BundleKey` parity: `repo:version` format, sorted newest-first
- Multi-level picker: latest per repo at top, "Other version..." drill-down

**IP detection:**
- External IP via ipify API (wizard hostname default)
- Local interfaces via `net.InterfaceAddrs` (daemon startup log)

**CLI i18n:**
- `ensureI18n()` reads `language` from `daemon.yml` — all CLI output localized
- Localized: `uninstall`, `start`, `status`, wizard prompts

**CLI commands:**
- `status --watch` — live table redraw (TTY-aware)
- `uninstall` — graceful namespace stop + "drop all data" confirmation
- `_workspace` directory renamed to `workspace`

**CI:**
- GTK/WebKit system deps added for desktop build lint

### Release 2.1.0 — COMPLETE (2026-04-09)
Zero-downtime binary upgrade, `install.sh` installer (replaces `citeck self-update`), huh TUI migration, `citeck setup` interactive config editor.

**Zero-downtime binary upgrade (detach mode):**
- New `Runtime.Detach()` / `Runtime.ShutdownDetached()` — exit runLoop without calling `doStop`. `doDetach` cancels `runCtx`, waits for reconciler/app goroutines, persists state with status preserved (next daemon auto-starts). **Never** calls `StopAndRemoveContainer` or `RemoveNetwork`.
- `Daemon.doShutdown(leaveRunning bool)` — `cloudCfgServer.Stop()` and `acmeRenewal.Stop()` run **before** the runtime shutdown so a late renewal callback cannot `RestartApp("proxy")` during detach (ACME renewal uses `context.Background()` so it wouldn't respect `runCtx` cancellation).
- `Detach()` returns `bool` — `false` when status is STOPPING (stop already in flight) so `shutdownAfter` degrades to the regular stop path instead of silently losing containers.
- HTTP: `POST /api/v1/daemon/shutdown?leave_running=true` with strict `strconv.ParseBool` (400 on any unrecognized value).
- `citeck stop --shutdown --leave-running` — validates combination (requires `--shutdown`, rejects app arg).
- Hash compatibility: `ApplicationDef.GetHashInput` is byte-identical between v2.0.0 and v2.1.0 (verified), so v2.1.0's `buildExistingContainerMap` reuses v2.0.0 containers with matching labels.

**install.sh installer (replaces `citeck self-update`):**
- One-liner from `raw.githubusercontent.com/Citeck/citeck-launcher/release/2.1.0/install.sh`
- Fallback chain: `--leave-running` (v2.1.0+) → SIGKILL preserve (v2.0.0) → full shutdown (last resort). SIGKILL is safe because Docker (not the launcher) owns the containers.
- systemd coordination: `sigkill_daemon_preserve_platform` masks the unit before killing so `Restart=on-failure` doesn't respawn the old binary during the swap window. `start_daemon` unmasks via `CITECK_SYSTEMD_MASKED` flag.
- `get_local_version` handles v2.0.0 (no `--short` flag) via fallback parse of "Citeck CLI X.Y.Z" line.
- `fetch_latest_version` skips semver pre-release identifiers (`*-*` pattern) instead of relying on GitHub's own `prerelease: true` flag, which the Citeck project currently sets for the entire v2.x series.
- `do_rollback` calls `start_daemon` after the binary swap (fixed mid-release — earlier it left systemd masked + daemon dead).
- `--file <path>` flag for offline / local-binary installs.

**huh TUI migration (all CLI user interactions):**
- `charmbracelet/huh` Select / Input / Confirm wrappers in `install_prompt.go`; 25 interactive prompts migrated across install wizard, registry auth, password reset, snapshot/clean/uninstall/workspace.
- `promptPassword` uses `huh.EchoModePassword`; master-password input stays on `golang.org/x/term` (huh doesn't support secure password reading without echo).
- `--yes` flag bypasses confirmations via `promptConfirm` helper; all callsites verified for correct default direction.

**`citeck setup` interactive config editor:**
- TUI-based settings editor with arrow-key navigation, history, and rollback.
- `reloadAndWait` shares live status streaming with `start`/`stop`; 3-option confirm dialog (apply+reload, apply only, cancel).
- DaemonFile target (language) only offers apply/cancel — reload doesn't help `daemon.yml` changes.
- `CurrentValue` display strings localized across all 8 locales.

**Bug fixes:**
- `citeck status --watch`: fixed two bugs — (1) pre-watch `PrintResult` leaked above the live table (untracked lines never cleared), (2) off-by-one in `ClearLines` because the rendered text didn't end with `\n`, so the cursor stayed at the end of the last row and the "up + clear" loop missed that line — each event added a fresh "zookeeper" row under the stale one. Fix: watch mode does all rendering itself (no pre-render), always terminates text with trailing `\n`, uses `strings.Count` without `+1`.
- CI/Makefile `golangci-lint` version mismatch: Makefile pinned v2.7.2, CI used v2.11.4. Newer gosec taint analysis caught G703 false positives on already-validated paths. Fixed Makefile to install v2.11.4 and suppressed 3 remaining false positives in `routes_snapshots.go` / `routes_volumes.go` with explicit justification comments.

**Key technical decisions:**
- SIGKILL-based preservation works because Docker owns containers, not the launcher — the launcher is just an orchestrator. Same principle as kubelet restarts in Kubernetes.
- systemd mask is the cleanest way to prevent `Restart=on-failure` respawn during an upgrade — `systemctl stop` would trigger SIGTERM → graceful shutdown → container stop, defeating the preserve-platform goal.
- `GetHashInput` stability is now a hard compatibility contract across versions — any future change must ship with either a migration path or documented "full-stop upgrade required".

## CI/CD

GitHub Actions:
- **Release workflow** (`.github/workflows/release-go.yml`): triggered by `v*.*.*` tags, builds Linux amd64 server binary, creates draft GitHub release. Uses `go-version-file: go.mod`.
- **CI workflow** (`.github/workflows/ci.yml`): triggered on push/PR to master and `release/**` branches. Runs `go vet`, `golangci-lint v2.11.4`, `go test -race`, `pnpm vitest run`, full server build.
- **Linting**: `.golangci.yml` v2 format, 21 linters, G104 excluded (cleanup errors), test files relaxed for dupl/gosec/unparam.
