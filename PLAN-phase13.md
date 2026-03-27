# Phase 13: Production Hardening for Scale

## Context

Deep production readiness audit found **1 critical, 2 high, 7 medium, 14 low** issues across security, reliability, observability, API quality, and Web UI. The most severe finding: raw secrets (passwords, tokens, JWT keys) are returned unmasked by the REST API and displayed in plain text in the Web UI.

**Goal:** Fix all P0/P1/P2 issues. Leave P3 (low) issues documented for future consideration.

---

## Sub-Phase 13a: Security (P0/P1)

**4 issues, ~6 files**

### 13a-1: Mask secrets in API response (CRITICAL)
- **Files:** `internal/daemon/routes.go` (`handleAppInspect`), move `maskSecretEnv` from `internal/cli/describe.go` to `internal/daemon/` (shared between routes and CLI)
- **Problem:** `handleAppInspect` returns raw Docker env vars including `POSTGRES_PASSWORD`, `KC_BOOTSTRAP_ADMIN_PASSWORD`, `ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET`, `OIDC_CLIENT_SECRET`. Any API caller (web or CLI) sees all secrets in plain text.
- **Fix:** Apply `maskSecretEnv` server-side in `handleAppInspect` before building the response DTO. Keys ending with `_PASSWORD`, `_SECRET`, `_TOKEN`, `_KEY` get value replaced with `***`. Move function to `internal/daemon/`, import from `cli/describe.go`.
- **Also mask** in `writeSystemDumpZip`: the per-app logs in the ZIP may contain secrets printed at container startup.

### 13a-2: HTTP security headers middleware (HIGH)
- **File:** `internal/daemon/middleware.go`, `internal/daemon/server.go`
- **Problem:** No browser security headers. Web UI vulnerable to clickjacking, MIME-sniffing, resource injection.
- **Fix:** Add `SecurityHeadersMiddleware` applied to `tcpHandler` in `server.go`:
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Strict-Transport-Security: max-age=63072000` (only when mTLS/HTTPS active)
- **Note:** Apply before CORSMiddleware so headers are set even on preflight responses.

### 13a-3: TLS 1.3 minimum for mTLS
- **File:** `internal/daemon/server.go` (`setupMTLS`, line ~633)
- **Problem:** `MinVersion: tls.VersionTLS12` permits weaker cipher suites. All certs are self-signed for this daemon — no TLS 1.2 compatibility needed.
- **Fix:** Change to `MinVersion: tls.VersionTLS13`.

### 13a-4: Mask secrets in system dump namespace.yml
- **File:** `internal/daemon/routes.go` (`handleSystemDump`)
- **Problem:** System dump ZIP includes `namespace.yml` which contains user passwords in `authentication.users` (e.g., `admin:admin`). The YAML is pre-marshaled under configMu lock, but passwords are not masked.
- **Fix:** Before marshaling `namespace.yml` for the dump, replace password portions in `authentication.users` with `***`. Apply masking to the copy only (not the live config).

---

## Sub-Phase 13b: Reliability (P1/P2)

**4 issues, ~4 files**

### 13b-1: Wire `daemon.yml` `docker.stopTimeout`
- **Files:** `internal/daemon/server.go` (line ~331), `internal/namespace/runtime.go`
- **Problem:** `docker.stopTimeout` in `daemon.yml` is declared (config/daemon.go:40) but never forwarded to the runtime. `runtime.go:1350` uses only `app.Def.StopTimeout`. Operators who set it see no effect.
- **Fix:** In `server.go`, pass `daemonCfg.Docker.StopTimeout` to runtime via `runtime.SetDefaultStopTimeout(seconds)`. In `runtime.go`, use it as fallback when `app.Def.StopTimeout == 0`. If neither is set, default to 10s.

### 13b-2: `restartApp` — use independent context for stop phase
- **File:** `internal/namespace/runtime.go` (restartApp, line ~643)
- **Problem:** Container stop in restart uses `runCtx` which is cancelled on daemon shutdown. If shutdown arrives during restart, Docker stop call is cancelled — container may be left in partial state.
- **Fix:** Use `context.WithTimeout(context.Background(), stopTimeout)` for the stop call within restart. The start phase should still use `runCtx` (cancelled on shutdown is correct for start).

### 13b-3: Socket server access logging
- **File:** `internal/daemon/server.go` (line ~421)
- **Problem:** `Handler: RecoveryMiddleware(socketMux)` — socket requests (exec, shutdown, config write, reload) are not access-logged. CLI-to-daemon operations invisible for audit.
- **Fix:** Wrap with `LoggingMiddleware`:
  ```go
  Handler: RecoveryMiddleware(LoggingMiddleware(socketMux)),
  ```

### 13b-4: Bind-mount `MkdirAll` error propagation
- **File:** `internal/docker/client.go` (lines 188, 193)
- **Problem:** Two `os.MkdirAll` calls for bind-mount directories silently ignore errors. If dir creation fails (permissions, read-only FS), Docker returns a less informative error.
- **Fix:** Return `MkdirAll` error with a clear message: `"create bind-mount directory %s: %w"`.

---

## Sub-Phase 13c: Observability (P2)

**4 issues, ~4 files**

### 13c-1: HTTP request metrics (counter + histogram)
- **Files:** `internal/daemon/middleware.go` (new `MetricsMiddleware` or extend `LoggingMiddleware`), `internal/daemon/routes.go` (`handleMetrics`)
- **Problem:** No HTTP request count or latency metrics. Cannot set SLOs or alert on error rates.
- **Fix:** Track per-request metrics in middleware using `sync.Map` of counters and histogram buckets. Normalize paths (replace `{name}` with `:name` to avoid label cardinality explosion). Expose in `handleMetrics`:
  - `citeck_http_requests_total{method, path, status}`
  - `citeck_http_request_duration_seconds_bucket{path, le}` (buckets: 0.01, 0.05, 0.1, 0.5, 1, 5, 30)
  - `citeck_http_request_duration_seconds_count{path}`
  - `citeck_http_request_duration_seconds_sum{path}`

### 13c-2: Operations history — add caller identity
- **File:** `internal/namespace/history.go` (`OperationRecord` struct)
- **Problem:** Operations JSONL has no caller identity. Cannot determine who triggered an action.
- **Fix:** Add `RequestID string` and `ClientCN string` fields to `OperationRecord`. Pass from HTTP handler context via a `context.WithValue` key set in `LoggingMiddleware`.

### 13c-3: History rotation — use `fsutil.AtomicWriteFile`
- **File:** `internal/namespace/history.go` (`rotateIfNeeded`, line ~84)
- **Problem:** Uses `os.WriteFile` + `os.Rename`. No fsync before rename. Inconsistent with codebase convention (`fsutil.AtomicWriteFile` used everywhere else for data integrity).
- **Fix:** Replace `os.WriteFile(tmpPath, ...)` + `os.Rename(tmpPath, ...)` with single `fsutil.AtomicWriteFile(path, data, 0o644)`.

### 13c-4: SSE event drop counter metric
- **File:** `internal/daemon/server.go` (`broadcastEvent`, line ~680)
- **Problem:** Dropped events are logged as warning but not counted. Cannot alert on persistent slow consumers.
- **Fix:** Add `sseDropped atomic.Int64` field to `Daemon`. Increment on each drop. Expose as `citeck_sse_events_dropped_total` gauge in `handleMetrics`.

---

## Sub-Phase 13d: API + CLI Polish (P2)

**4 issues, ~5 files**

### 13d-1: `citeck validate` command
- **File:** new `internal/cli/validate.go`
- **Problem:** No way to validate `namespace.yml` without starting the daemon. Operators discover config errors only after start.
- **Fix:** Add `citeck validate [file]` that:
  1. Parses and validates namespace config via `namespace.ParseNamespaceConfig`
  2. Validates daemon config if `--daemon` flag passed
  3. Prints validation results (field-level errors)
  4. Exit code 0 on success, 1 on error
- Register in `root.go`.

### 13d-2: App name validation in route handlers
- **File:** `internal/daemon/routes.go` (all app-scoped handlers)
- **Problem:** `r.PathValue("name")` is passed directly to `findApp()` without format validation. `validNameRegex` exists (line 708) but is only used in `handleDeleteVolume`. Defense-in-depth gap.
- **Fix:** Extract `validateAppName(name) error` helper using `validNameRegex`. Call at top of each app-scoped handler before `findApp()`. Return 400 `INVALID_REQUEST` for invalid names.

### 13d-3: Error codes on remaining writeError sites
- **Files:** `internal/daemon/routes.go`, `internal/daemon/routes_p2.go`
- **Problem:** Several error responses still use `writeError` without machine-readable codes:
  - `handleDeleteNamespace` running check (line 130): uses `writeError` instead of `writeErrorCode` with `NAMESPACE_RUNNING`
  - `handleExportSnapshot` namespace must be stopped (line 676): no error code
  - Various 500 errors expose internal messages
- **Fix:** Convert remaining high-value `writeError` sites to `writeErrorCode`. For 500 errors: log detailed error via `slog.Error`, return generic `"internal error"` to client.

### 13d-4: Internal error message suppression on 500
- **Files:** `internal/daemon/routes.go`, `internal/daemon/routes_p2.go`
- **Problem:** `writeError(w, 500, err.Error())` in ~15 places surfaces internal messages (file paths, Docker error strings).
- **Fix:** Add `writeInternalError(w, err)` helper that: (1) logs `slog.Error("handler error", "err", err)`, (2) writes `writeErrorCode(w, 500, api.ErrCodeInternalError, "internal error")`. Replace all `writeError(w, 500, err.Error())` calls.

---

## Sub-Phase 13e: Web UI Hardening (P2/P3)

**4 issues, ~4 files**

### 13e-1: Env var display improvements in AppDetail
- **File:** `web/src/pages/AppDetail.tsx` (line ~125)
- **Problem:** After server-side masking (13a-1), sensitive env vars show `KEY=***`. But long non-secret values (JWT public keys, base64 configs) break layout with `break-all`. Also no visual distinction between masked and unmasked values.
- **Fix:** Use `overflow-hidden text-ellipsis` with hover tooltip for long values. Style masked values (`***`) with muted color to visually distinguish. Keep layout compact.

### 13e-2: AppDetail loading state
- **File:** `web/src/pages/AppDetail.tsx`
- **Problem:** Shows "Loading..." text during Docker inspect call. No skeleton matching the Dashboard pattern.
- **Fix:** Add skeleton placeholders during `inspect === null` state, matching Dashboard skeleton style (shimmer animation).

### 13e-3: Toast notification system
- **Files:** new `web/src/components/Toast.tsx`, new `web/src/lib/toast.ts` (zustand store)
- **Problem:** No consistent notification system. Errors show inline in some pages, silently swallowed in others. SSE reconnect is invisible.
- **Fix:** Simple toast component:
  - Zustand store: `addToast(message, type)`, auto-dismiss after 5s
  - Types: `success`, `error`, `info`
  - Positioned bottom-right, stacked
  - Use for: start/stop results, export/import completion, SSE reconnect notification

### 13e-4: SSE reconnect notification
- **File:** `web/src/lib/store.ts` (gap detection, line ~62)
- **Problem:** On SSE reconnect after gap detection, state reloads silently. User doesn't know connection was lost.
- **Fix:** After gap detection triggers `fetchData()`, show toast: "Connection restored, state refreshed". Requires 13e-3 toast system.

---

## Implicit Rules

- All new endpoints inherit CSRF protection (localhost TCP) or mTLS (non-localhost) from existing middleware chain.
- All new file writes use `fsutil.AtomicWriteFile`.
- All new error responses use `writeErrorCode` with constants from `api/dto.go`.
- Socket-only endpoints are registered on `socketMux` only.
- `maskSecretEnv` suffixes: `_PASSWORD`, `_SECRET`, `_TOKEN`, `_KEY` (case-insensitive).

---

## Execution Order

```
13a (security) → 13b (reliability) → deploy + test on server
→ 13c (observability) → 13d (API + CLI) → 13e (Web UI)
```

Each sub-phase: implement → `go test -race ./internal/...` → code review → fix issues.

After 13b: build, deploy to server, test security headers + masked secrets + socket logging.

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. `curl .../apps/emodel/inspect` — env vars with `_PASSWORD`, `_SECRET`, `_TOKEN`, `_KEY` show `***`
3. Response headers: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` present on all TCP responses
4. `curl -I https://host:8088/` — includes `Strict-Transport-Security` (HTTPS only)
5. TLS handshake with TLS 1.2 client → rejected
6. System dump ZIP: namespace.yml has `admin:***` not `admin:admin`
7. `citeck validate namespace.yml` — validates config, prints field errors, exit code 1 on error
8. Socket access log: `slog.Info "HTTP request" method=POST path=/api/v1/daemon/shutdown remote=@`
9. Prometheus: `citeck_http_requests_total{method="POST",path="/api/v1/namespace/start",status="200"} 1`
10. Prometheus: `citeck_sse_events_dropped_total 0`
11. Web UI toast appears on namespace start/stop
12. Web UI toast on SSE reconnect: "Connection restored"
