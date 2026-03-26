# Phase 8: Production-Grade Hardening

Deep audit identified **57 unique issues** across 25 files. Grouped into 4 sub-phases by blast radius.

## Sub-phase 8a: Critical Safety (12 issues)

Must fix before any production deployment at scale. Race conditions, security, data loss.

### 8a-1: eventCb called under r.mu — lock inversion risk

**Files:** `internal/namespace/runtime.go:617-644`

**Problem:** `setStatus` and `setAppStatus` call `r.eventCb(...)` while holding `r.mu`. The callback acquires `d.eventMu`. Any future code path acquiring `eventMu→r.mu` will deadlock.

**Fix:** Add a buffered event channel `eventCh chan api.EventDto` to Runtime. `setStatus`/`setAppStatus` push events into the channel (non-blocking) instead of calling `eventCb` directly. A dedicated goroutine drains the channel and calls `eventCb` outside any lock. This avoids modifying all ~20 call sites:
```go
// In Runtime struct:
eventCh chan api.EventDto // buffered, drained by dispatchLoop goroutine

// In setStatus (called with r.mu held):
select {
case r.eventCh <- evt:
default: // drop if full, same as current broadcastEvent behavior
}

// dispatchLoop (started by doStart, stopped by doStop):
for evt := range r.eventCh {
    if cb := r.eventCb.Load(); cb != nil { (*cb)(evt) }
}
```

### 8a-2: SetEventCallback has no synchronization — data race

**Files:** `internal/namespace/runtime.go:274-276`

**Problem:** `r.eventCb` written without lock, read under `r.mu`. Race if called after `Start()`.

**Fix:** Use `atomic.Pointer[EventCallback]` for lock-free read/write. This is consistent with 8a-1 which moves the eventCb call outside `r.mu` — using `r.mu` to protect eventCb would conflict with that change. Atomic pointer provides safe concurrent access without any mutex.

### 8a-3: Daemon fields read without configMu

**Files:** `internal/daemon/routes.go:49-57, 519-543, 861`, `internal/daemon/server.go:659`

**Problem:** `d.nsConfig`, `d.bundleError` read without `configMu.RLock()`. Concurrent reload writes these under `configMu.Lock()`.

**Fix:** Add `d.configMu.RLock()/RUnlock()` in `handleGetNamespace`, `buildDumpData`, `handleHealth` (routes.go), and `activeConfigPath` (server.go).

### 8a-4: pullSem not released via defer — permanent depletion on panic

**Files:** `internal/namespace/runtime.go:840-869`

**Problem:** Semaphore slot acquired at line 841, released at line 869. Any panic between them permanently depletes the slot. With capacity 4, 4 panics = all pulls blocked forever.

**Fix:** `defer func() { <-r.pullSem }()` immediately after acquire.

### 8a-5: Socket chmod 0o666 — world-writable in server mode

**Files:** `internal/daemon/server.go:419`

**Problem:** Any local process can exec commands in containers, stop namespace, delete volumes.

**Fix:** `0o600` in server mode, `0o666` only in desktop mode.

### 8a-6: StreamEvents uses httpClient (30s timeout) — SSE always drops after 30s

**Files:** `internal/client/client.go:295`

**Problem:** `c.httpClient.Do(req)` has 30s Timeout. SSE is long-lived. All `--wait`, `--watch` commands break after 30s.

**Fix:** Use `c.streamClient.Do(req)` (no timeout).

### 8a-7: os.Exit inside RunE bypasses defers — resource leaks

**Files:** `internal/cli/apply.go:315`, `internal/cli/exec.go:55`, `internal/cli/diagnose.go:149`, `internal/cli/config.go:99`

**Problem:** `os.Exit()` inside `RunE` skips `defer c.Close()`.

**Fix:** Return typed `ExitCodeError`; handle in `Execute()`:
```go
type ExitCodeError struct { Code int; Err error }
func (e ExitCodeError) Error() string { return e.Err.Error() }
// in Execute(): if errors.As(err, &ece) { os.Exit(ece.Code) }
```

### 8a-8: handlePutConfig — non-atomic write corrupts config on crash

**Files:** `internal/daemon/routes.go:430-434`

**Fix:** Write to temp file + `os.Rename`.

### 8a-9: FileStore.SaveSecret — non-atomic write

**Files:** `internal/storage/filestore.go:124`

**Fix:** Same as 8a-8.

### 8a-10: GetHashInput mutates Ports slice in place — data race

**Files:** `internal/appdef/appdef.go:130`

**Problem:** `sort.Strings(d.Ports)` mutates shared struct. Two concurrent `GetHash` calls race.

**Fix:** Copy before sort: `ports := append([]string(nil), d.Ports...)`.

### 8a-11: No timeout on git clone/pull — blocks daemon startup indefinitely

**Files:** `internal/git/repo.go:125, 171`

**Fix:** Accept `context.Context`, pass to `CloneOptions.Context` / `PullOptions.Context`.

### 8a-12: ACME fullchain.pem written with os.Create (0o666 pre-umask)

**Files:** `internal/acme/client.go:177`

**Fix:** `os.OpenFile(chainPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)`.

---

## Sub-phase 8b: Runtime Robustness (16 issues)

Fixes that prevent silent failures, resource leaks, and Docker edge cases.

### 8b-1: Shutdown goroutines untracked — snapshot export/import outlive daemon

**Files:** `internal/daemon/server.go:490-520`, `internal/daemon/routes_p2.go:675-693, 762-775`

**Fix:** Add `bgWg sync.WaitGroup` to Daemon; track all `d.bgCtx` goroutines; `d.bgWg.Wait()` in doShutdown after bgCancel.

### 8b-2: Second SIGTERM has no force-quit escape hatch

**Files:** `internal/daemon/server.go:466-473`

**Fix:** After first signal triggers shutdown, a second signal calls `os.Exit(1)`.

### 8b-3: Unix socket server WriteTimeout 120s kills SSE after 2 min

**Files:** `internal/daemon/server.go:407-412`

**Fix:** Set `WriteTimeout: 0` on the Unix socket server (same as TCP).

### 8b-4: CloudConfigServer has no HTTP timeouts

**Files:** `internal/daemon/cloudconfig.go:47-51`

**Fix:** Add `ReadTimeout: 10s, WriteTimeout: 10s, IdleTimeout: 60s`.

### 8b-5: Container stop timeout hardcoded 10s — PostgreSQL needs 30-60s

**Files:** `internal/docker/client.go:290`

**Fix:** Add `StopTimeout int` field to `ApplicationDef` (0 = use default). Generator sets defaults by kind: 30s for infra (postgres, rabbitmq, zookeeper), 10s for webapps. `StopAndRemoveContainer` accepts the timeout from the caller. Global default is configurable via `docker.stopTimeout` in `daemon.yml` (see 8d-11).

### 8b-6: Phase 2 container removal errors discarded — next create fails with "name in use"

**Files:** `internal/namespace/runtime.go:752-756`

**Fix:** Log errors; on removal failure, retry with `ForceRemove: true`.

### 8b-7: Init container cleanup uses cancelled context

**Files:** `internal/namespace/runtime.go:1036-1049`

**Fix:** Use `context.Background()` with 10s timeout for cleanup calls.

### 8b-8: Docker daemon restart not detected — reconciler logs errors indefinitely

**Files:** `internal/docker/client.go` (throughout)

**Fix:** In reconciler, detect `client.IsErrConnectionFailed(err)`; log once, back off, attempt reconnect.

### 8b-9: OOM-killed containers restarted without logging the OOM event

**Files:** `internal/namespace/reconciler.go:88-95`

**Fix:** After detecting missing container, inspect for `State.OOMKilled`; if true, log WARNING + emit event.

### 8b-10: doStop has 2min total timeout shared across all groups — first group can starve others

**Files:** `internal/namespace/runtime.go:1271`

**Fix:** 30s per group (total ~2min), not shared.

### 8b-11: doRegenerate — brief window where Apps() returns empty

**Files:** `internal/namespace/runtime.go:1231-1239`

**Fix:** Move `r.apps = make(...)` inside doStart Phase 3 lock, immediately before populating.

### 8b-12: Concurrent git clone/pull on same directory — no per-directory mutex

**Files:** `internal/git/repo.go`

**Fix:** Add `singleflight.Group` keyed on `DestDir` (from `golang.org/x/sync/singleflight` — new direct dep, but `golang.org/x/*` packages are already used: crypto, net, sys, time).

### 8b-13: Auth errors trigger reclone loop — burns API rate limits

**Files:** `internal/git/repo.go:204`

**Fix:** Check error for "authentication"/"unauthorized"; return error without reclone.

### 8b-14: Actions queue overflow spawns unbounded goroutines

**Files:** `internal/actions/actions.go:223`

**Fix:** Block caller (with context) or drop with explicit `ErrQueueFull`.

### 8b-15: ACME concurrent renewal — cert/key mismatch

**Files:** `internal/acme/renewal.go`

**Fix:** Add `atomic.Bool isRenewing`; skip checkAndRenew if already running.

### 8b-16: ACME rate limit not handled — domain locked out for a week

**Files:** `internal/acme/client.go`

**Fix:** Parse `rateLimited` error; persist last failure timestamp; exponential backoff with 1h minimum.

---

## Sub-phase 8c: CLI Correctness & UX (17 issues)

### 8c-1: Exit codes defined but unused — scripts can't distinguish error types

**Fix:** Apply correct exit codes: `ExitDaemonNotRunning` when daemon unreachable, `ExitConfigError` for bad config, `ExitNotFound` for missing app, `ExitTimeout` for timeouts.

### 8c-2: `stop` has no `--wait` / `--timeout` flags

**Fix:** Add `--wait` + `--timeout`, subscribe SSE, wait for `namespace_status→STOPPED`.

### 8c-3: `stop` exits 0 when daemon not running

**Fix:** Exit 0 with stderr warning "daemon is not running" — idempotent, matches `kubectl delete` pattern. Note: this is an exception to 8c-1 — `stop` is idempotent by design, other commands (`status`, `logs`, `exec`) should use `ExitDaemonNotRunning`.

### 8c-4: `restart` has no `--wait` flag

**Fix:** Add `--wait` + `--timeout`, wait for app status → RUNNING.

### 8c-5: `promptChoice` returns raw invalid input silently

**Files:** `internal/cli/install.go:228-242`

**Fix:** Re-prompt until valid choice entered (loop).

### 8c-6: `clean` uses `fmt.Scanln` (inconsistent, breaks with piped input)

**Fix:** Use `bufio.NewScanner(os.Stdin)` like install/uninstall.

### 8c-7: `diagnose` hardcodes `/var/run/docker.sock` — ignores DOCKER_HOST

**Fix:** Create Docker client and call Ping instead.

### 8c-8: `logs` follow mode silently swallows read errors

**Fix:** Print non-EOF errors to stderr; exit non-zero.

### 8c-9: `describe` exposes secrets in env vars

**Fix:** Mask `*_PASSWORD`, `*_SECRET`, `*_TOKEN`, `*_KEY` values as `***`.

### 8c-10: `token generate` restart note outside PrintResult — broken JSON mode

**Fix:** Move inside text callback.

### 8c-11: `cert letsencrypt` overwrites existing cert without backup/confirm

**Fix:** Check existing cert; require `--force` to overwrite; backup old cert.

### 8c-12: `apply` reads file twice (TOCTOU)

**Fix:** Single `os.ReadFile`, then `ParseNamespaceConfig(data)`.

### 8c-13: `diff --file` missing prints usage and exits 0

**Fix:** `cmd.MarkFlagRequired("file")` or return error.

### 8c-14: `install` plaintext passwords in 0644 config file

**Fix:** Write namespace.yml with `0o600`.

### 8c-15: `install` only tries ifconfig.me for IP detection

**Fix:** Try 3 services in order: ifconfig.me, api.ipify.org, checkip.amazonaws.com.

### 8c-16: `reload` exits 0 when result.Success == false

**Fix:** Return error if `!result.Success`.

### 8c-17: `version` lacks build commit and timestamp

**Fix:** Add `gitCommit`, `buildDate` ldflags.

---

## Sub-phase 8d: DoS Protection & Observability (12 issues)

### 8d-1: Unlimited SSE subscriber accumulation

**Fix:** Cap at 100 subscribers; return 503 when exceeded.

### 8d-2: `tail` parameter unbounded on /apps/{name}/logs and /daemon/logs

**Fix:** Cap at 10000 lines.

### 8d-3: Snapshot multipart upload 512MB in-memory buffer

**Fix:** Set `ParseMultipartForm(32 << 20)` (32MB); Go spills to disk automatically.

### 8d-4: handleLockToggle + handleRenameSnapshot — no body size limit

**Fix:** Use `readJSON(r, &body)` (which uses `io.LimitReader`).

### 8d-5: CORS wildcard `*` — localhost CSRF

**Fix:** Default allowed origins: `http://127.0.0.1:*`, `http://localhost:*`. Configurable via `server.cors.allowedOrigins` in `daemon.yml` (for custom dev setups, e.g. Vite on :5173). Validate `Origin` header against the list; reflect matching origin (not `*`).

### 8d-6: Token comparison not constant-time

**Fix:** `subtle.ConstantTimeCompare([]byte(parts[1]), []byte(token)) == 1`.

### 8d-7: No rate limiting on TCP listener

**Fix:** Add rate limiter (100 req/s per IP). Use `golang.org/x/time/rate` — already an indirect dependency in go.mod.

### 8d-8: Snapshot operations have no concurrency guard

**Fix:** Per-namespace `sync.Mutex` for import/export.

### 8d-9: ZIP bomb — no aggregate extraction size limit

**Fix:** Track cumulative bytes written; abort at 50GB.

### 8d-10: No disk space check before snapshot import/export

**Fix:** Estimate needed space; check available; fail early.

### 8d-11: Reconciler/pull/stop intervals not configurable in daemon.yml

**Fix:** Expose `reconciler.interval`, `reconciler.livenessPeriod`, `docker.pullConcurrency`, `docker.stopTimeout` in `DaemonConfig`.

### 8d-12: No metrics endpoint (Prometheus/OpenMetrics)

**Fix:** Add `/metrics` endpoint using `github.com/prometheus/client_golang` (new dependency). Metrics:
- `citeck_namespace_status` gauge
- `citeck_app_status` gauge per app
- `citeck_app_restarts_total` counter
- `citeck_image_pull_duration_seconds` histogram
- `citeck_reconciler_runs_total` counter
- `citeck_http_requests_total` counter with method/path/status labels

Note: adds ~2MB to binary. Endpoint only registered on TCP listener (not Unix socket).

---

## Execution Order

1. **8a (12 issues):** Race conditions + security + data integrity. Build + test after each.
   - 8a-1 + 8a-2 must be done together (event dispatch + eventCb synchronization).
2. **8b (16 issues):** Runtime robustness, Docker edge cases, ACME. Build + test.
   - 8b-5 (stop timeout parameter) before 8b-10 (per-group timeout) — both touch doStop callers.
   - 8b-11 (doRegenerate empty window) touches Phase 3 lock in doStart — coordinate with 8a-1 (event dispatch changes).
3. **8c (17 issues):** CLI correctness, exit codes, UX. Build + test.
   - 8c-1 (exit codes) depends on 8a-7 (ExitCodeError type) being done first.
4. **8d (12 issues):** DoS protection, observability, tuning. Build + test.
   - 8d-11 (daemon.yml tunables) must be done before 8d-12 (metrics) — metrics use configurable intervals.

## Verification

After each sub-phase:
1. `go build && go vet && go test ./...`
2. `golangci-lint run` (race detector, unused, errcheck)
3. Deploy to test server
4. Fresh `citeck install` → all apps RUNNING
5. `citeck reload` → smart regenerate
6. `citeck stop && citeck start` → graceful lifecycle
7. Concurrent client stress test (10 SSE + 100 API calls)
