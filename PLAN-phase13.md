# Phase 13: Production Hardening for Scale

## Context

Deep production readiness audit found **1 critical, 2 high, 7 medium, 14 low** issues across security, reliability, observability, API quality, and Web UI. The most severe finding: raw secrets (passwords, tokens, JWT keys) are returned unmasked by the REST API and displayed in plain text in the Web UI.

**Goal:** Fix all P0/P1/P2 issues. Leave P3 (low) issues documented for future consideration.

---

## Sub-Phase 13a: Security (P0/P1)

**4 issues, ~6 files**

### 13a-1: Mask secrets in API response (CRITICAL)
- **Files:** `internal/daemon/routes.go` (`handleAppInspect`), move `maskSecretEnv` from `internal/cli/describe.go` to a shared package (e.g., `internal/daemon/` or new `internal/envutil/`)
- **Problem:** `handleAppInspect` returns raw Docker env vars including `POSTGRES_PASSWORD`, `KC_BOOTSTRAP_ADMIN_PASSWORD`, `ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET`, `OIDC_CLIENT_SECRET`. Any API caller (web or CLI) sees all secrets.
- **Fix:** Apply `maskSecretEnv` server-side in `handleAppInspect` before building the response DTO. Keys ending with `_PASSWORD`, `_SECRET`, `_TOKEN`, `_KEY` get value replaced with `***`. CLI `describe.go` should import and use the shared function.

### 13a-2: HTTP security headers middleware (HIGH)
- **File:** `internal/daemon/middleware.go`, `internal/daemon/server.go`
- **Problem:** No browser security headers (X-Frame-Options, X-Content-Type-Options, CSP, Referrer-Policy). Web UI is vulnerable to clickjacking, MIME-sniffing, and resource injection.
- **Fix:** Add `SecurityHeadersMiddleware` applied to `tcpHandler` in `server.go`:
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Strict-Transport-Security: max-age=63072000` (only when mTLS/HTTPS active)

### 13a-3: TLS 1.3 minimum for mTLS
- **File:** `internal/daemon/server.go` (`setupMTLS`)
- **Problem:** `MinVersion: tls.VersionTLS12` permits weaker cipher suites. All certs are self-signed for this daemon — no compatibility need for TLS 1.2.
- **Fix:** Change to `MinVersion: tls.VersionTLS13`.

### 13a-4: Mask secrets in system dump
- **File:** `internal/daemon/routes.go` (`writeSystemDumpZip`)
- **Problem:** System dump ZIP includes `namespace.yml` which may contain user passwords in `authentication.users`. Also includes per-app logs which could contain secrets at startup.
- **Fix:** Mask `authentication.users` values in the namespace.yml written to the dump (replace password portion with `***`). Document in operator runbook that system dumps may contain sensitive data.

---

## Sub-Phase 13b: Reliability (P1/P2)

**4 issues, ~4 files**

### 13b-1: Wire `daemon.yml` `docker.stopTimeout`
- **File:** `internal/daemon/server.go`, `internal/namespace/runtime.go`
- **Problem:** `docker.stopTimeout` in `daemon.yml` is declared but never forwarded to the runtime. Operators who set it see no effect.
- **Fix:** In `server.go`, pass `daemonCfg.Docker.StopTimeout` to runtime via `runtime.SetDefaultStopTimeout(seconds)`. In `runtime.go`, use it as fallback when `app.Def.StopTimeout == 0`.

### 13b-2: `restartApp` — use independent context for stop phase
- **File:** `internal/namespace/runtime.go` (`restartApp` or `doStop`)
- **Problem:** The stop phase uses `runCtx` which is cancelled on daemon shutdown. If shutdown signal arrives during restart, the Docker stop call is cancelled — container may be left in a partial state.
- **Fix:** Use `context.WithTimeout(context.Background(), stopTimeout)` for the container stop call within restart operations.

### 13b-3: Socket server access logging
- **File:** `internal/daemon/server.go`
- **Problem:** All Unix socket requests (exec, shutdown, config write, reload) are not access-logged. CLI-to-daemon operations are invisible for audit.
- **Fix:** Wrap `socketMux` with `LoggingMiddleware` before `RecoveryMiddleware`:
  ```go
  d.server = &http.Server{
      Handler: RecoveryMiddleware(LoggingMiddleware(socketMux)),
  ```

### 13b-4: Bind-mount `MkdirAll` error propagation
- **File:** `internal/docker/client.go` (`CreateContainer`, bind-mount dir creation)
- **Problem:** `os.MkdirAll` errors are silently ignored. If dir creation fails, Docker returns a less informative error.
- **Fix:** Return the `MkdirAll` error to the caller with a clear message.

---

## Sub-Phase 13c: Observability (P2)

**4 issues, ~4 files**

### 13c-1: HTTP request metrics (counter + histogram)
- **File:** `internal/daemon/middleware.go` (`LoggingMiddleware`), `internal/daemon/routes.go` (`handleMetrics`)
- **Problem:** No HTTP request count or latency metrics. Cannot set SLOs or alert on error rates.
- **Fix:** Add atomic counters in `LoggingMiddleware` for request count by method+path+status and a simple bucketed histogram for latency. Expose in `handleMetrics`:
  - `citeck_http_requests_total{method, path, status}`
  - `citeck_http_request_duration_seconds_bucket{path, le}`
  - `citeck_http_request_duration_seconds_count{path}`
  - `citeck_http_request_duration_seconds_sum{path}`

### 13c-2: Operations history — add caller identity
- **File:** `internal/namespace/history.go`
- **Problem:** Operations JSONL records have no caller identity. Cannot determine who triggered an action.
- **Fix:** Add `RequestID` and `ClientCN` fields to `OperationRecord`. Pass from HTTP handler context through `runtime.Start/Stop/Restart` methods.

### 13c-3: History rotation — use `fsutil.AtomicWriteFile`
- **File:** `internal/namespace/history.go` (`rotateIfNeeded`)
- **Problem:** Uses `os.WriteFile` + `os.Rename` instead of `fsutil.AtomicWriteFile`. Inconsistent with codebase convention, no fsync before rename.
- **Fix:** Replace with `fsutil.AtomicWriteFile`.

### 13c-4: SSE event drop counter metric
- **File:** `internal/daemon/server.go` (`broadcastEvent`)
- **Problem:** Dropped events are logged but not counted. Cannot alert on persistent slow consumers.
- **Fix:** Add `atomic.Int64` counter, expose as `citeck_sse_events_dropped_total` in metrics.

---

## Sub-Phase 13d: API + CLI Polish (P2)

**4 issues, ~5 files**

### 13d-1: `citeck validate` command
- **File:** new `internal/cli/validate.go`
- **Problem:** No way to validate `namespace.yml` without starting the daemon. Operators must start the daemon to discover config errors.
- **Fix:** Add `citeck validate [file]` that parses and validates the config, resolves the bundle reference (dry-run), and prints results. Exit code 0 on success, 1 on error.

### 13d-2: App name validation in route handlers
- **File:** `internal/daemon/routes.go` (all app-scoped handlers)
- **Problem:** App name from path params is not validated before `findApp()`. Defense-in-depth gap.
- **Fix:** Add a shared `validateAppName` helper that checks against `validNameRegex`. Apply at the top of each app-scoped handler before `findApp()`. Return 400 with `INVALID_REQUEST` for invalid names.

### 13d-3: Namespace delete — check for running containers
- **Files:** `internal/daemon/routes_p2.go` (`handleDeleteNamespace`)
- **Problem:** Delete proceeds even if namespace is running. Containers may be orphaned.
- **Fix:** Check namespace status before delete. If RUNNING or STARTING, return 409 with `NAMESPACE_RUNNING` and a message to stop first.

### 13d-4: Internal error message suppression
- **Files:** `internal/daemon/routes.go`, `internal/daemon/routes_p2.go`
- **Problem:** Some `writeError(w, 500, err.Error())` calls surface internal messages (file paths, Docker errors) to API consumers.
- **Fix:** For 500 errors, log the detailed error server-side via `slog.Error`, return generic `"internal error"` message to the client. Keep detailed messages for 400-level errors (user-actionable).

---

## Sub-Phase 13e: Web UI Hardening (P2/P3)

**4 issues, ~3 files**

### 13e-1: Env var masking + toggle in AppDetail
- **File:** `web/src/pages/AppDetail.tsx`
- **Problem:** Even after server-side masking (13a-1), UX should support show/hide toggle for env vars (like password fields). Long values break layout.
- **Fix:** Add a "Show values" toggle button (default: hidden). Display `KEY=***` by default, show full value only when toggled. Use `overflow-hidden text-ellipsis` with tooltip on hover for long values.

### 13e-2: AppDetail loading state
- **File:** `web/src/pages/AppDetail.tsx`
- **Problem:** No loading indicator while Docker inspect call completes.
- **Fix:** Add skeleton/spinner during `inspect === null` state, matching Dashboard skeleton pattern.

### 13e-3: SSE reconnect event replay
- **File:** `web/src/lib/store.ts`
- **Problem:** On SSE reconnect after gap detection, full state reload happens but any ephemeral events (one-time error messages, snapshot progress) are lost.
- **Fix:** Document this as a known limitation. Add a small toast notification "Connection restored, state refreshed" so the user knows a gap occurred.

### 13e-4: Web UI — toast notification system
- **Files:** new `web/src/components/Toast.tsx`, updates to pages
- **Problem:** Errors, success messages, and status changes have no consistent notification system. Some pages use inline error divs, others silently swallow errors.
- **Fix:** Add a simple toast component (zustand store for toasts, auto-dismiss after 5s). Use it for: start/stop results, export/import completion, error messages, SSE reconnect notification.

---

## Implicit Rules

- All new endpoints inherit CSRF protection (localhost TCP) or mTLS (non-localhost) from existing middleware chain.
- All new file writes use `fsutil.AtomicWriteFile`.
- All new error responses use `writeErrorCode` with constants from `api/dto.go`.
- Socket-only endpoints are registered on `socketMux` only.

---

## Execution Order

```
13a (security) → 13b (reliability) → 13c (observability)
→ 13d (API + CLI) → 13e (Web UI)
```

Each sub-phase: implement → `go test -race ./internal/...` → code review → fix issues.

After 13b: build, deploy to server, test.

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. `curl .../apps/emodel/inspect` — env vars with `_PASSWORD`, `_SECRET`, `_TOKEN`, `_KEY` show `***`
3. Web UI App Detail — passwords masked by default, toggle to reveal
4. Response headers: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff` present
5. `citeck validate namespace.yml` — validates config, prints errors
6. Prometheus: `citeck_http_requests_total{method="POST",path="/api/v1/namespace/start",status="200"} 1`
7. Socket server access log: `slog.Info "HTTP request" method=POST path=/api/v1/daemon/shutdown`
8. Namespace delete while running → 409 `NAMESPACE_RUNNING`
