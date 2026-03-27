# Phase 12: GA Readiness — COMPLETE

## Context

Production-readiness audit found **4 P0, 11 P1, 17 P2** issues across 15 areas. Core problems: CSRF on localhost TCP (body-free POSTs bypass CORS preflight), zero HTTP handler test coverage, unbounded log accumulation in browser, outdated README.

**Key architectural constraint:** Web UI is served from the same TCP listener it calls. Same-origin requests work without CORS. Cross-origin attacks use "simple requests" (no-body POST) that bypass preflight. Fix: custom CSRF header forces preflight on all mutating requests.

**Threat model reminder:**
- **Unix socket** → `socketMux` (full access, CLI only)
- **mTLS TCP** → `socketMux` (full access, authenticated by client cert)
- **Localhost TCP** → `tcpMux` (restricted routes + CSRF header required for mutations)

---

## Sub-Phase 12a: CSRF + Security + Tests

**5 issues, ~8 files**

### 12a-1: CSRF middleware for tcpMux
- **Files:** `internal/daemon/middleware.go`, `internal/daemon/server.go`
- **Problem:** 8 body-free POST routes on tcpMux are "simple requests" — no CORS preflight, any page on localhost can trigger them: `namespace/start`, `namespace/stop`, `apps/{name}/restart`, `apps/{name}/stop`, `apps/{name}/start`, `snapshots/export`, `diagnostics/fix`, plus `PUT` with `text/plain` Content-Type.
- **Fix:** Add `CSRFMiddleware` that requires `X-Citeck-CSRF: 1` header on all POST/PUT/DELETE requests. Custom headers always trigger CORS preflight → preflight rejected for unknown origins → request never sent. Apply to tcpMux handler chain only (socket and mTLS don't need it).
- **Note:** Same-origin requests from the Web UI can freely set custom headers without triggering preflight.

### 12a-2: Web UI add CSRF header
- **File:** `web/src/lib/api.ts`
- **Fix:** Add `'X-Citeck-CSRF': '1'` to all `fetch()` calls that use POST/PUT/DELETE methods. This includes `fetchJSON` helper and all direct `fetch` calls with mutating methods.

### 12a-3: `downloadAndImportSnapshot` use ssrfSafeClient
- **File:** `internal/daemon/routes_p2.go` (`downloadAndImportSnapshot`)
- **Problem:** Uses `snapshot.Download` (default httpClient) instead of `snapshot.DownloadWithClient(ssrfSafeClient, ...)`. URL comes from admin workspace config (lower risk), but inconsistent with `handleDownloadSnapshot` which uses ssrfSafeClient.
- **Fix:** Use `snapshot.DownloadWithClient(ssrfSafeClient, ...)` for consistency.

### 12a-4: HTTP handler tests
- **File:** new `internal/daemon/routes_test.go`
- **Problem:** Zero test coverage for HTTP route handlers. Security-critical logic (field whitelist, CSRF, SSRF, snapshot import) is completely untested.
- **Fix:** Test suite with httptest covering:
  - `handlePutAppConfig` field whitelist (image/volumes/cmd locked)
  - CSRF middleware blocks requests without header
  - CSRF middleware allows requests with header
  - `validateSnapshotURL` blocks private/loopback IPs
  - Validation error returns `ValidationErrorDto` with fields

### 12a-5: Two-mux boundary test
- **File:** `internal/daemon/routes_test.go`
- **Fix:** Register both muxes via `registerRoutes`, then verify socket-only routes (shutdown, exec, config write, reload, app config write, app file write) return 404 on tcpMux but 200/4xx on socketMux.

---

## Sub-Phase 12b: Stability + Recovery

**5 issues, ~5 files**

### 12b-1: Panic recovery middleware
- **File:** `internal/daemon/middleware.go`
- **Problem:** Panic in a handler or handler-spawned goroutine crashes the entire daemon. Go stdlib recovers panics in handler goroutines but logs to stderr only (not slog).
- **Fix:** `RecoveryMiddleware` wrapping all handlers. Catches panics, logs stack trace via `slog.Error`, returns 500 with `INTERNAL_ERROR` code. Apply to both socketMux and tcpMux.

### 12b-2: Logs page ring buffer
- **File:** `web/src/pages/Logs.tsx`
- **Problem:** `logs` state is unbounded string. Streaming follow appends: `setLogs(prev => prev + chunk)`. After hours of streaming, browser tab OOMs.
- **Fix:** Split to lines, keep last 50,000 lines. On chunk append: split combined string by `\n`, slice to last 50K, rejoin.

### 12b-3: SQLite schema versioning
- **File:** `internal/storage/sqlitestore.go`
- **Problem:** `migrate()` uses `CREATE TABLE IF NOT EXISTS` — does not add columns to existing tables on schema change. Future upgrades silently skip new columns.
- **Fix:** Add `schema_version` table. Migration runner: check current version, apply sequential ALTER TABLE statements for each version bump.

### 12b-4: Desktop DB file permissions
- **File:** `internal/storage/sqlitestore.go`
- **Problem:** SQLite DB file inherits umask (typically 0o644). Contains secrets in desktop mode. Other users on the machine can read it.
- **Fix:** `os.Chmod(dbPath, 0o600)` after initial `sql.Open` creates the file.

### 12b-5: Install wizard atomic config write
- **File:** `internal/cli/install.go` (line 202 only)
- **Problem:** `os.WriteFile(nsCfgPath, data, 0o600)` — crash mid-write corrupts namespace config.
- **Fix:** `fsutil.AtomicWriteFile(nsCfgPath, data, 0o600)`. Do NOT change line 353 (systemd unit write — intentional, no atomicity requirement).

---

## Sub-Phase 12c: CLI + Observability

**5 issues, ~5 files**

### 12c-1: Shell completion
- **File:** `internal/cli/root.go`
- **Fix:** Add cobra's built-in `completion` subcommand for bash/zsh/fish/powershell. One line: `rootCmd.AddCommand(newCompletionCmd())` or let cobra auto-register.

### 12c-2: `citeck_build_info` Prometheus metric
- **File:** `internal/daemon/routes.go` (`handleMetrics`)
- **Fix:** Add `citeck_build_info{version="...",commit="..."}` gauge (always value 1). Version comes from `d.version` (injected via ldflags).

### 12c-3: Error codes on key call sites
- **Files:** `internal/daemon/routes.go`, `internal/daemon/routes_p2.go`
- **Problem:** 112 `writeError` calls, only 4 use `writeErrorCode`. API consumers can't distinguish errors programmatically.
- **Fix:** Convert ~15 high-value sites to `writeErrorCode`:
  - `findApp == nil` → `APP_NOT_FOUND`
  - `runtime == nil` / `nsConfig == nil` → `NOT_CONFIGURED`
  - `snapshotMu.TryLock` fail → `SNAPSHOT_IN_PROGRESS`
  - Config parse error → `INVALID_CONFIG`
  - Volume delete while running → `NAMESPACE_RUNNING`
  - App already running → `APP_ALREADY_RUNNING`
- Do NOT convert generic errors ("failed to read body", "internal error") — these don't benefit from codes.

### 12c-4: Include daemon.log in system dump
- **File:** `internal/daemon/routes.go` (`writeSystemDumpZip`)
- **Fix:** Read `config.DaemonLogPath()` and its rotated variants (`.1`, `.2`, `.3`), add to ZIP under `daemon-logs/` directory. Cap each file at 2MB.

### 12c-5: Runtime log level control
- **Files:** `internal/daemon/server.go`, `internal/daemon/routes.go`
- **Fix:** Use `slog.LevelVar` instead of default level. Add `PUT /api/v1/daemon/loglevel` endpoint (socket-only) accepting `{"level":"debug"}`. Levels: debug, info, warn, error.

---

## Sub-Phase 12d: Documentation

**4 issues, ~4 files**

### 12d-1: Rewrite README.md
- **File:** `README.md`
- **Problem:** Entire README describes the old Kotlin Gradle app. Build commands, paths, architecture — all wrong for the Go binary.
- **Fix:** Complete rewrite: project description, install (binary download + `citeck install`), build from source (`make build`), architecture overview, CLI usage, Web UI, configuration.

### 12d-2: API reference
- **File:** new `docs/api.md`
- **Fix:** Document all `/api/v1/*` endpoints: method, path, request/response body, error codes, curl examples. Group by resource: daemon, namespace, apps, config, secrets, snapshots, diagnostics, events, health, metrics.

### 12d-3: Configuration reference
- **File:** new `docs/config.md`
- **Fix:** Document `daemon.yml` (server.webui, reconciler, docker) and `namespace.yml` (authentication, proxy, tls, bundleRef, pgadmin, mongodb) with all fields, defaults, and examples.

### 12d-4: Operator runbook
- **File:** new `docs/operations.md`
- **Fix:** Operational procedures: log locations, secret rotation, upgrade path, backup/restore from snapshot, debugging startup failures, mTLS cert management, common error codes.

---

## Sub-Phase 12e: Web UI Polish

**4 issues, ~4 files**

### 12e-1: Dashboard loading skeleton
- **File:** `web/src/pages/Dashboard.tsx`
- **Problem:** Shows plain "Loading..." text during initial load.
- **Fix:** Skeleton placeholder matching the Dashboard layout (info panel skeleton + table skeleton with shimmer animation).

### 12e-2: Welcome page error handling
- **File:** `web/src/pages/Welcome.tsx` (line 54)
- **Problem:** `handleOpenNamespace` catch block silently swallows start errors. User sees Dashboard with no indication of failure.
- **Fix:** Show error toast/banner when `postNamespaceStart()` fails. Don't navigate to Dashboard on error.

### 12e-3: Snapshot export integrity checksum
- **Files:** `internal/snapshot/export.go`, `internal/daemon/routes_p2.go`
- **Fix:** After export, compute SHA256 of the ZIP file. Write `<snapshot>.sha256` sidecar file alongside the ZIP. On HTTP download, set `X-Checksum-SHA256` response header.

### 12e-4: Namespace config apiVersion field
- **File:** `internal/namespace/config.go`
- **Fix:** Add `APIVersion string` field to `NamespaceConfig`. Default when absent/empty: `"v1"` (implicit). Write `apiVersion: v1` in `MarshalNamespaceConfig`. Future config schema changes bump this version; `LoadNamespaceConfig` can detect and migrate.

---

## Implicit Rules

- All new mutating endpoints (POST/PUT/DELETE) added in 12b/c/e automatically inherit CSRF protection from the tcpMux middleware (12a-1). No per-handler wiring needed.
- Socket-only endpoints (12c-5 log level) are registered on `socketMux` only — no CSRF required.

---

## Execution Order

```
12a (CSRF + security + tests) → 12b (stability) → deploy + test on server
→ 12c (CLI + observability) → 12d (docs) → 12e (UI polish)
```

Each sub-phase: implement → `go test -race ./internal/...` → code review → fix issues.

After 12b: build, deploy to server, test CSRF protection + panic recovery + Logs streaming.

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. CSRF: `curl -X POST http://127.0.0.1:8088/api/v1/namespace/start` → 403 (no CSRF header)
3. CSRF: `curl -X POST -H "X-Citeck-CSRF: 1" http://127.0.0.1:8088/api/v1/namespace/start` → 200 (header present)
4. CSRF: Web UI namespace start → works (same-origin, header added by api.ts)
5. Two-mux: `POST /daemon/shutdown` on TCP → 404 (socket-only)
6. Panic: trigger panic in handler → 500 response, daemon stays running
7. Logs: stream logs for 10 minutes → browser memory stays bounded
8. Prometheus: `curl /metrics` → includes `citeck_build_info{version="..."} 1`
9. Error codes: `GET /api/v1/namespace` (no namespace) → `{"code":"NOT_CONFIGURED",...}`
10. System dump: ZIP contains `daemon-logs/daemon.log`
