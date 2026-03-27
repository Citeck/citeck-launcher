# Phase 11: Production Readiness

## Context

Production audit found **3 P0, 15 P1, 13 P2** issues. Core problem areas: unauthenticated dangerous endpoints on TCP listener, missing write timeouts, observability gaps, Logs page polling flood, no API-level error codes.

**Key principle:** Localhost TCP listener must have the same security posture as a public-facing API — any browser tab on the machine can reach it via JavaScript.

**Threat model:** Two distinct TCP modes:
- **Localhost (127.0.0.1)** — no auth, but reachable from any local browser tab via fetch(). Must restrict dangerous endpoints.
- **Non-localhost (0.0.0.0)** — mTLS required. Client is authenticated by cert CN. All endpoints accessible.

---

## Sub-Phase 11a: P0 Security (Localhost Attack Surface)

**4 issues, ~5 files**

### 11a-1: Restrict dangerous endpoints by listener type
- **File:** `internal/daemon/server.go` (route registration)
- **Problem:** `POST /daemon/shutdown` and `POST /apps/{name}/exec` are reachable from the localhost TCP listener. Any browser tab on localhost can call them via fetch().
- **Fix:** Create two muxes: `socketMux` (all routes) and `tcpMux` (safe routes only). Register shutdown and exec only on `socketMux`. The TCP `tcpMux` serves the Web UI + read-only APIs + app lifecycle (start/stop/restart).
- **Exception:** When mTLS is active (non-localhost listener), use `socketMux` for TCP too — the client is authenticated by certificate, so all endpoints are safe.
- **Routes restricted to socket-only (localhost TCP):** `daemon/shutdown`, `apps/{name}/exec`

### 11a-2: Fix CORS to validate exact origin, not prefix
- **File:** `internal/daemon/middleware.go`
- **Problem:** `matchCORSOrigin` allows `http://localhost:ANY_PORT` — a malicious page on localhost:3000 can CSRF the daemon.
- **Fix:** Build allowed origins from the actual daemon listen address. If listen is `127.0.0.1:8088`, allow `http://127.0.0.1:8088` only. For mTLS, allow `https://HOST:PORT`. Pass the listen address to `CORSMiddleware`.
- **Note:** Vite dev mode uses a proxy (`/api → localhost:8088`), NOT CORS. Restricting origins does not break the dev workflow.

### 11a-3: Cap exec output size + add request body limit
- **File:** `internal/daemon/routes.go` (`handleAppExec`)
- **Problem:** Exec output is unbounded (OOM risk). Request body has no limit.
- **Fix:** Limit exec output to 1MB via `io.LimitReader` on the Docker exec output stream. Add `http.MaxBytesReader(w, r.Body, 64*1024)` on request body (64KB is more than enough for a command array).

### 11a-4: Restrict `handlePutAppConfig` — prevent arbitrary image/volume injection
- **File:** `internal/daemon/routes.go` (`handlePutAppConfig`)
- **Problem:** Accepts arbitrary `ApplicationDef` YAML — caller can set any Docker image, mount host paths, inject env vars. Combined with unauthenticated localhost TCP, this is a container escape vector.
- **Fix:** Whitelist mutable fields (environments, resources, cmd, ports). Reject changes to image, volumes, initContainers. Alternatively, restrict this endpoint to socket-only like exec.

---

## Sub-Phase 11b: HTTP Server Hardening

**5 issues, ~4 files**

### 11b-1: Add per-handler write timeouts (not global 0)
- **File:** `internal/daemon/server.go`, `internal/daemon/routes.go`
- **Problem:** `WriteTimeout: 0` on both HTTP servers. Non-streaming handlers have no write deadline — slowloris risk.
- **Fix:** Set `WriteTimeout: 30s` globally on both servers. For streaming handlers (SSE events, log follow, system dump ZIP), use `http.ResponseController` to extend/disable the deadline at the start of the handler. This is the standard Go 1.20+ pattern.
- **Streaming handlers that need override:** `handleEvents`, `handleAppLogsFollow`, `writeSystemDumpZip`

### 11b-2: Log remote address + mTLS CN in HTTP access logs
- **File:** `internal/daemon/middleware.go` (`LoggingMiddleware`)
- **Fix:** Add `r.RemoteAddr` to the log line. For mTLS connections (`r.TLS != nil`), also log `r.TLS.PeerCertificates[0].Subject.CommonName`. Simple, high-value change.

### 11b-3: Fix health endpoint to reflect actual app state
- **File:** `internal/daemon/routes.go` (`handleHealth`)
- **Problem:** Returns `healthy: true` when 15 of 19 apps are `START_FAILED`.
- **Fix:** Three states: `healthy` (all apps running), `degraded` (some apps failed/starting), `unhealthy` (namespace STALLED or no apps running). This matches k8s health probe semantics.

### 11b-4: SSRF protection on snapshot download URL
- **File:** `internal/daemon/routes_p2.go` (`handleDownloadSnapshot`)
- **Fix:** Validate URL scheme (`http`/`https` only). After DNS resolution, check resolved IP against RFC1918, loopback, link-local, and cloud metadata ranges (`169.254.169.254`). Use a custom `http.Transport` with `DialContext` that inspects the resolved address.

### 11b-5: Atomic file writes for generated namespace files
- **File:** `internal/daemon/routes.go` (`handleReloadNamespace` line 157), `internal/daemon/server.go` (startup)
- **Fix:** Replace `os.WriteFile(destPath, content, 0o644)` with `fsutil.AtomicWriteFile` for all generated config files. Prevents partial writes on crash during reload.

---

## Sub-Phase 11c: Reliability & Performance

**6 issues, ~5 files**

### 11c-1: Switch Logs page to streaming (not polling)
- **File:** `web/src/pages/Logs.tsx`
- **Problem:** Polls REST endpoint every 2s via `setInterval`. Creates constant Docker API load per open tab.
- **Fix:** Use the existing streaming endpoint (`GET /apps/{name}/logs?follow=true&tail=N`) via `fetch()` with `ReadableStream`. The backend already streams via chunked response (`handleAppLogsFollow`). Only use the non-streaming endpoint for initial load. Remove the 2s `setInterval`.
- **Note:** This is NOT SSE/EventSource — it's chunked `text/plain`. Use `response.body.getReader()`.

### 11c-2: Virtual list for log rendering
- **File:** `web/src/pages/Logs.tsx`
- **Problem:** Renders up to 2000+ DOM elements with no virtualization.
- **Fix:** Use `@tanstack/react-virtual` for windowed rendering. Only render visible lines (typically 50-100 at a time). Preserves search highlighting and scroll behavior.

### 11c-3: Cache registry tokens in resolver
- **File:** `internal/daemon/server.go` (`makeTokenLookup`, `makeRegistryAuthFunc`)
- **Problem:** `store.ListSecrets()` (full disk scan) called on every image pull.
- **Fix:** Pre-fetch all registry secrets into a `map[string]RegistryAuth` when creating the resolver. Immutable for the pull session. Rebuild on next reload.

### 11c-4: Propagate context to HTTP probes
- **File:** `internal/namespace/runtime.go` (`httpProbeCheck`)
- **Fix:** Pass `ctx` to `http.NewRequestWithContext()` inside the probe. Probe cancels when namespace stop is requested, instead of hanging until per-probe timeout expires.

### 11c-5: Direct map lookup in `findApp` instead of O(n) scan
- **File:** `internal/daemon/routes.go`, `internal/namespace/runtime.go`
- **Problem:** `findApp` copies entire app slice then linear scans on every HTTP request.
- **Fix:** Add `Runtime.FindApp(name string) (*AppRuntime, bool)` with direct map lookup under RLock. Replace all `findApp(name)` call sites.

### 11c-6: SQLiteStore connection pool limit
- **File:** `internal/storage/sqlitestore.go`
- **Fix:** Add `db.SetMaxOpenConns(1)` after `sql.Open()`. Serializes all database access. For a low-traffic desktop app this is correct; prevents concurrent write deadlocks.

---

## Sub-Phase 11d: Observability & API Polish

**5 issues, ~5 files**

### 11d-1: Add machine-readable error codes to API
- **File:** `internal/daemon/server.go` (`writeError`), `internal/api/dto.go`
- **Fix:** Add `Code string` to `ErrorDto`. Define constants: `APP_NOT_FOUND`, `NAMESPACE_STOPPED`, `SNAPSHOT_IN_PROGRESS`, `INVALID_CONFIG`, etc. Pass code through `writeError(w, code, httpStatus, msg)`.

### 11d-2: Prometheus-compatible metrics endpoint
- **File:** new `internal/daemon/routes_metrics.go`
- **Fix:** Add `GET /metrics` endpoint in Prometheus exposition format (`text/plain`). Hand-write the output (no dependency). Export: `citeck_apps_total`, `citeck_apps_running`, `citeck_apps_failed`, `citeck_namespace_status`, `citeck_uptime_seconds`, per-app status gauges.

### 11d-3: Daemon log rotation
- **File:** `internal/daemon/server.go` (log setup)
- **Fix:** Add a simple rotating `io.Writer` (or use `lumberjack`) at 50MB/3 files. Matches Docker container log rotation already configured (`json-file 50m/3`).

### 11d-4: SSE event reliability — include sequence numbers
- **File:** `internal/daemon/routes.go` (`handleEvents`, `broadcastEvent`), `internal/api/dto.go`, `web/src/lib/store.ts`
- **Fix:** Add atomic `Seq int64` counter. Each `broadcastEvent` increments it and sets `evt.Seq = seq`. Frontend detects gaps and triggers `fetchData()` to catch up. Eliminates "missed event → stale UI" without requiring complex retry/replay.

### 11d-5: Add X-Request-ID header + log it
- **File:** `internal/daemon/middleware.go`
- **Fix:** Generate short random ID per request (8 hex chars). Set as `X-Request-Id` response header. Include in the `LoggingMiddleware` access log line. No per-handler changes needed — 90% of the correlation value for minimal code.

---

## Sub-Phase 11e: Remaining P1 + Select P2

**6 issues, ~6 files**

### 11e-1: Desktop socket permissions
- **File:** `internal/daemon/server.go`
- **Problem:** Desktop mode uses `0o666` (world-readable) socket — any user can exec/shutdown.
- **Fix:** Use `0o600` on all platforms. Vite dev mode uses HTTP proxy, not the socket.

### 11e-2: Web UI Error Boundary
- **File:** `web/src/App.tsx` or new `ErrorBoundary.tsx`
- **Fix:** Wrap all routes in a React Error Boundary that catches render errors and shows "Something went wrong" with a reload button.

### 11e-3: SSE reconnect race condition
- **File:** `web/src/lib/store.ts`
- **Problem:** Two reconnects can race and create two concurrent EventSource connections (goroutine leak on server).
- **Fix:** Use a reconnect generation counter. Increment on disconnect. Before creating new EventSource, check counter matches. Close old stream explicitly.

### 11e-4: Docker `Names[0]` bounds check
- **File:** `internal/docker/client.go` (`CleanupStaleContainers`)
- **Fix:** Add `len(ctr.Names) > 0` guard before accessing `ctr.Names[0]`.

### 11e-5: Validate `daemon.yml` listen address at load time
- **File:** `internal/config/daemon.go`
- **Fix:** Parse listen address with `net.SplitHostPort`. Reject invalid formats. Log warning if non-localhost without mTLS certs.

### 11e-6: Routes reorganization by resource domain
- **File:** `internal/daemon/routes.go`, `internal/daemon/routes_p2.go`
- **Fix:** Split into: `routes_daemon.go` (status, shutdown, events, health, metrics, dump), `routes_namespace.go` (CRUD, config, reload), `routes_apps.go` (list, restart, stop, start, exec, logs, config, inspect), `routes_snapshots.go` (list, export, import, download), `routes_secrets.go` (CRUD). Pure file reorganization, no logic changes.

---

## Deferred (not in Phase 11)

These items were identified in the audit but are deferred because they require larger architectural changes or have lower production impact:

- **Secrets encryption at rest** — would need an encryption key management system (SOPS, Vault, KMS). Currently mitigated by filesystem permissions (0o600). Desktop mode SQLite could use SQLCipher but that changes the driver.
- **Cloud Config JWT secret exposure** — the 8761 port is bound to 127.0.0.1 and is only accessed by containers via Docker host networking. The threat requires local process access AND knowledge that the port exists. Low risk in the single-tenant deployment model.
- **Pagination on list endpoints** — practical concern only at 100+ items. Current deployments have ~20 apps, ~5 secrets, ~3 snapshots. Add when needed.
- **Request ID propagation into handlers** — 11d-5 adds it to access logs. Full propagation (logger-per-request via context) would be a larger refactor. Add if needed for debugging.

---

## Execution Order

```
11a (P0 security) → 11b (HTTP hardening) → 11c (reliability/perf) → 11d (observability) → 11e (remaining)
```

Each sub-phase: implement → `go test -race ./internal/...` → code review → fix issues.

After 11b: deploy to server, test CORS + exec restriction + health endpoint.
After 11c: deploy to server, test streaming logs + Prometheus metrics.

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. Security: `curl -X POST http://127.0.0.1:8088/api/v1/daemon/shutdown` → 404 (not on tcpMux)
3. Security: `curl -X POST http://127.0.0.1:8088/api/v1/apps/proxy/exec` → 404 (not on tcpMux)
4. CORS: page on `http://localhost:3000` fetch to `http://127.0.0.1:8088` → CORS rejected
5. CORS: Web UI on `http://127.0.0.1:8088` → same-origin, works without CORS
6. mTLS: `curl --cert admin.crt --key admin.key https://server:8088/api/v1/daemon/shutdown` → works (mTLS = full access)
7. Health: namespace with failed apps → `{"status":"degraded","healthy":false}`
8. SSRF: `POST /snapshots/download {"url":"http://169.254.169.254/"}` → 400 blocked
9. Logs: open Logs page → single chunked streaming connection, no polling
10. Prometheus: `curl /metrics` → `text/plain` with `citeck_apps_total 20`
11. Log rotation: daemon.log rotated at 50MB, 3 retained files
12. SSE: kill connection, frontend reconnects, detects seq gap, fetches fresh state
