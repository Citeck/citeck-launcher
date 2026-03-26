# Phase 7: Production Hardening

37 issues across 3 sub-phases. Each sub-phase is independently testable and deployable.

## Sub-phase 7a: Critical Fixes (6 issues)

Must be fixed before any production deployment. Each is a clear bug or missing feature that breaks real usage.

### 7a-1: Version string not forwarded to daemon HTTP API

**Files:** `cmd/citeck/main.go`, `internal/cli/root.go`, `internal/daemon/server.go`, `internal/daemon/routes.go`

**Problem:** `handleDaemonStatus` and system dump return hardcoded `"dev"` instead of the build version injected via ldflags.

**Fix:**
- Add `Version string` field to `Daemon` struct
- Add `Version string` to `StartOptions`
- Thread version from `cli.Execute(version)` → `newStartCmd` → `daemon.Start(opts)` → `Daemon.Version`
- Use `d.Version` in `handleDaemonStatus` (line 34) and `handleSystemDump` (line 513)

### 7a-2: TCP server has no HTTP timeouts

**Files:** `internal/daemon/server.go:374`

**Problem:** `d.tcpServer = &http.Server{Handler: tcpHandler}` — no ReadTimeout, WriteTimeout, IdleTimeout. Slowloris DoS and idle connection accumulation on the public Web UI port.

**Fix:** Copy the same timeout config from the Unix socket server:
```go
d.tcpServer = &http.Server{
    Handler:        tcpHandler,
    ReadTimeout:    30 * time.Second,
    WriteTimeout:   0, // 0 for SSE streaming — use per-handler timeouts for non-SSE
    IdleTimeout:    120 * time.Second,
    MaxHeaderBytes: 1 << 20,
}
```
Note: WriteTimeout must be 0 (or very large) because SSE connections are long-lived. Non-SSE handlers should use `http.TimeoutHandler` wrapper if needed.

### 7a-3: CORS middleware defined but not wired

**Files:** `internal/daemon/server.go`, `internal/daemon/middleware.go:52`

**Problem:** `CORSMiddleware` exists but is never registered. SSE endpoint manually sets `Access-Control-Allow-Origin: *` but all other endpoints have no CORS headers. Web UI dev mode (vite on :5173 calling :8088) fails.

**Fix:**
- Wire `CORSMiddleware` into the TCP mux (not Unix socket — Unix is same-origin by definition)
- Make CORS origin reflect the request Origin header when token auth is active (not `*`)
- Delete the manual CORS header in the SSE handler (now handled by middleware)

### 7a-4: Snapshot auto-import races with namespace auto-start

**Files:** `internal/daemon/server.go:293-299`

**Problem:** `runtime.Start(appDefs)` fires unconditionally before `go importSnapshotIfNeeded(...)`. Containers mount empty volumes before import fills them.

**Fix:**
- If `nsCfg.Snapshot != ""`, run `importSnapshotIfNeeded` synchronously (blocking) before `runtime.Start`
- Add a timeout (10 minutes for large snapshots)
- Log progress
- Only if import succeeds or is already done, proceed with `runtime.Start`

### 7a-5: SnapshotDef missing JSON struct tags

**Files:** `internal/bundle/resolver.go:84-90`

**Problem:** `SnapshotDef` has no `json` tags. Go serializes as `ID`, `Name`, `URL`, `Size`, `SHA256` (uppercase). Frontend expects lowercase `id`, `name`, `url`, `size`. Workspace snapshots display as blank rows in the UI.

**Fix:** Add `json:"id"`, `json:"name"`, `json:"url"`, `json:"size"`, `json:"sha256"` tags to all fields of `SnapshotDef`.

### 7a-6: Bundle resolution failure is silent — daemon starts with 0 apps

**Files:** `internal/daemon/server.go:186-191`

**Problem:** When `resolver.Resolve()` fails (network down, bad YAML), daemon falls back to `EmptyBundleDef` and starts with zero apps. Health check says OK. No user feedback.

**Fix:**
- Store the bundle resolve error in `Daemon` struct
- Surface it in `handleGetNamespace` response (new `BundleError string` field in NamespaceDto)
- Surface it in `handleDaemonStatus` and health check (healthy=false when bundle failed)
- Log at ERROR level with actionable message

---

## Sub-phase 7b: Production Readiness (14 issues)

Required for reliable production operation. Fixes runtime correctness, data safety, and install UX.

### 7b-1: Reused containers not health-checked after hash match (#7)

**Files:** `internal/namespace/runtime.go:698-703`

**Fix:** After hash-match reuse, schedule a fast Docker inspect to verify container is running. If not, reset to `AppStatusReadyToPull`.

### 7b-2: Install wizard silently discards passwords (#8)

**Files:** `internal/cli/install.go:55-71`

**Fix:** Store `user:password` pairs correctly. The generator at `generator.go:419` already uses `u + ":" + u` format — the install should save both parts so the generator can use actual passwords. Change `nsCfg.Authentication.Users` to store `user:password` format, update generator to split on `:`.

### 7b-3: `cert status` ignores LE certs (#9)

**Files:** `internal/cli/cert.go:36-39`

**Fix:** Read cert path from namespace config (`proxy.tls.certPath`). If not set, try `fullchain.pem` then `server.crt` as fallbacks.

### 7b-4: Snapshot upload blocks HTTP handler (#10)

**Files:** `internal/daemon/routes_p2.go:697-766`

**Fix:** Mirror the export pattern: accept upload, save to temp file, launch import as goroutine, report progress via SSE. Return 202 Accepted immediately.

### 7b-5: Wizard tlsMode `letsencrypt` not propagated (#11)

**Files:** `internal/daemon/routes_p2.go:266-268`

**Fix:** In `handleCreateNamespace`, check `req.TLSMode`:
- `"self-signed"` → generate cert, set certPath/keyPath
- `"letsencrypt"` → set `nsCfg.Proxy.TLS.LetsEncrypt = true`
- `"custom"` → use provided paths

### 7b-6: Uninstall doesn't wait for daemon stop (#12)

**Files:** `internal/cli/uninstall.go:31-82`

**Fix:** After `systemctl stop citeck`, poll for socket gone (up to 30s). If still active, warn and abort. Or use `citeck stop --shutdown` with wait.

### 7b-7: `doStop` has no deadline (#13)

**Files:** `internal/namespace/runtime.go:1162-1184`

**Fix:** Accept a context with deadline (from `doShutdown`'s 30s context). Pass to `StopAndRemoveContainer`. If Docker is unresponsive, containers are abandoned after deadline.

### 7b-8: State file write not atomic (#14)

**Files:** `internal/namespace/state.go:25`

**Fix:** Write to `state-{nsID}.json.tmp` then `os.Rename` to `state-{nsID}.json`. Atomic on POSIX.

### 7b-9: Install: no Docker presence check (#15)

**Files:** `internal/cli/install.go`

**Fix:** At top of `runInstall`, check Docker socket reachable (`net.DialTimeout("unix", "/var/run/docker.sock", 2s)`). If not, print actionable error with install instructions. Also check docker group membership for non-root.

### 7b-10: Install: no port conflict check (#16)

**Files:** `internal/cli/install.go`

**Fix:** After port selection, bind-test the port (`net.Listen("tcp", ":"+port)`). If busy, warn and re-prompt.

### 7b-11: Config validation missing (#17)

**Files:** `internal/namespace/config.go:96-105`

**Fix:** Add `ValidateNamespaceConfig(cfg)` function:
- Port range 1-65535
- Host non-empty when TLS enabled
- BundleRef non-empty
- Users non-empty for BASIC auth
- TLS cert/key paths exist when not letsEncrypt
Call from `ParseNamespaceConfig`, `handleCreateNamespace`, and `handlePutConfig`.

### 7b-12: `handleAppStart` uses RestartApp for never-started apps (#18)

**Files:** `internal/daemon/routes.go:285-306`, `internal/namespace/runtime.go`

**Fix:** Add `StartApp(name)` method to runtime that checks current status:
- If `READY_TO_PULL` or `PULL_FAILED` or `START_FAILED` → re-enter `pullAndStartApp`
- If `RUNNING` → no-op
- If `STOPPED` → re-enter `pullAndStartApp`
Use `StartApp` from `handleAppStart` instead of `RestartApp`.

### 7b-13: Diagnostics status string mismatch (#19)

**Files:** `internal/cli/diagnose.go`, `internal/daemon/routes_p2.go:475-560`

**Fix:** Standardize on `"warning"` everywhere (matching CLI `formatCheckIcon`). Update daemon handler strings.

### 7b-14: `apply --wait` SSE race (#20)

**Files:** `internal/cli/apply.go:172-210`

**Fix:** Subscribe to SSE stream BEFORE calling `ReloadNamespace`. Check initial state after subscribing. Same pattern as `snapshotWithWait`.

---

## Sub-phase 7c: Quality & Polish (17 issues)

Improves robustness, developer experience, and code cleanliness.

### 7c-1: START_FAILED → eternal STALLED (#21)

**Fix:** In reconciler, add retry for `AppStatusStartFailed` and `AppStatusPullFailed` with exponential backoff (1min, 2min, 4min, max 30min). Reset retry counter on manual restart.

### 7c-2: `doStop` without graceful ordering (#22)

**Fix:** Use `GracefulShutdownOrder()` in `doStop`. Stop proxy first, then webapps in parallel, then infra in parallel.

### 7c-3: Dead middleware code (#23)

**Fix:** Implement `LoggingMiddleware` (method, path, status, duration, slog.Info). Wire both CORS and Logging middleware into mux. Delete no-op stubs.

### 7c-4: OperationHistory without rotation (#24)

**Fix:** Cap at 1000 entries. On `Record()`, if file exceeds cap, truncate to last 500 entries (or rename to `.old`).

### 7c-5: No tests for runtime/ACME (#25)

**Fix:** Add unit tests:
- `runtime_test.go`: hash matching (same hash → reuse, different → recreate), `doRegenerate` state transitions
- `acme/renewal_test.go`: renewal threshold logic (> 50% remaining → skip, < 50% → renew), `renewalInterval` (short-lived vs normal cert)
- `appdef_test.go`: `GetHash` determinism, `GetHashInput` format

### 7c-6: Logs: no --since/--timestamps (#26)

**Fix:** Add `--since`, `--until`, `--timestamps` flags to `logs` command. Forward to Docker API via query params.

### 7c-7: Exec: no TTY/interactive mode (#27)

**Fix:** For local (Unix socket) exec: bypass HTTP API, call Docker exec directly with TTY+stdin. For remote: document limitation. Add `--interactive/-i` flag.

### 7c-8: Clean: no orphan networks (#28)

**Fix:** In `findOrphans`, also scan `docker network ls --filter label=citeck.launcher=true`. Remove networks not matching any running namespace.

### 7c-9: Clean: no interactive confirm (#29)

**Fix:** When `--execute` without `--yes`: show interactive prompt instead of error. Use same `promptYesNo` pattern from install.go.

### 7c-10: Docker network prefix match bug (#30)

**Fix:** After `NetworkList` with name filter, verify `networks[0].Name == name` exactly before using the result. If no exact match, treat as not found.

### 7c-11: Desktop mode selects wrong namespace (#31)

**Fix:** In `server.go:106-131`, read `launcher_state` table from SQLiteStore if available. Use stored `workspace_id`/`namespace_id` as preferred defaults.

### 7c-12: Token generate doesn't reload daemon (#32)

**Fix:** After writing token file, try to connect to daemon and call a new `POST /api/v1/daemon/reload-config` endpoint (or reuse existing reload). If daemon not running, print note.

### 7c-13: Form missing snapshot/template fields (#33)

**Fix:** Add `snapshot` (select, options from `/api/v1/workspace/snapshots`) and `template` (select, options from `/api/v1/templates`) fields to `NamespaceCreateSpec`. Wire `handleCreateNamespace` to use them.

### 7c-14: CloudConfig version resets on restart (#34)

**Fix:** Persist version counter in state file. Load on startup, increment on config change.

### 7c-15: `updateStats` blocks cmdCh processing (#35)

**Fix:** Run `updateStats` in a separate goroutine (not in runLoop). Use a result channel to deliver stats back, or update directly under a brief lock. This unblocks cmdCh processing.

### 7c-16: Wizard sends empty bundleRepo (#36)

**Fix:** In `Wizard.tsx`, populate bundle selector from `/api/v1/bundles` response. In `handleCreateNamespace`, if `bundleRepo`/`bundleKey` empty, use the first available bundle from workspace config (matching install.go behavior).

### 7c-17: `doStop` swallows container stop errors (#37)

**Fix:** Log errors from `StopAndRemoveContainer` at `slog.Warn` level. Don't fail the stop operation, but make errors visible.

---

## Execution Order

1. **7a (6 issues):** ~2-3 hours. All independent, can be done in parallel. Build + deploy + verify on server.
2. **7b (14 issues):** ~4-6 hours. Some dependencies (7b-11 config validation is used by 7b-5, 7b-10). Build + test suite + deploy.
3. **7c (17 issues):** ~6-8 hours. All independent. Build + test suite + final review.

## Verification

After each sub-phase:
1. `go build && go vet && go test ./...` — all pass
2. Deploy to test server (45.15.158.227)
3. `citeck install` → fresh namespace → all apps RUNNING
4. `citeck reload` after config change → smart regenerate (only changed containers restart)
5. Playwright: HTTPS dashboard loads, Keycloak login works
6. `citeck snapshot export/import` cycle → data preserved
7. `citeck stop && citeck start` → graceful lifecycle

## Key Architectural Decisions to Preserve

- **SSE (not WebSocket)** for real-time events
- **Bind-mount volumes** (not Docker named volumes)
- **Smart regenerate** via hash comparison (docker-compose up style)
- **3-phase doStart**: resolve (no lock) → remove stale (no lock) → update state (lock)
- **JWTSecret** generated per-instance, persisted to `conf/jwt-secret`
- **StablePort** via sorted webapp names + counter (infra has fixed ports)
- **ImageDigest** resolved from local Docker cache before hash comparison
- **ACME profile** via custom JWS for IP certs (Go stdlib doesn't support profiles yet)
- **HTTPS scheme** for external hosts even without local TLS (assumed behind reverse proxy)
