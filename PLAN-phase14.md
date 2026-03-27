# Phase 14: Production Hardening at Scale

## Context

Deep audit across all Go packages, Web UI, CLI client, tests, and CI/CD. After cross-referencing audit findings against actual code, **3 of the 5 originally reported "critical" concurrency bugs turned out to be false positives** — the runtime's `appWg.Wait()` in `doRegenerate`/`doStop` correctly drains all goroutines before map replacement. The `waitForDeps` goroutine exits cleanly via `ctx.Done()` on namespace stop.

**Actual findings:** 4 high (reliability/UX), 4 high (Web UI), 5 medium, 5 low.

**Goal:** Fix all high issues. Harden Web UI for scale. Add runtime test coverage. Improve CI.

---

## Sub-Phase 14a: Backend Reliability (HIGH)

**4 issues, ~4 files**

### 14a-1: SSE heartbeat for load balancer compatibility
- **File:** `internal/daemon/routes.go` (`handleEvents`)
- **Problem:** No keep-alive events sent. TCP intermediaries (load balancers, NAT gateways, cloud proxies) with idle-timeout policies (typically 60–120s) drop the SSE connection silently. Client discovers the gap only when the next real event would have fired — could be minutes later. Standard SSE practice is periodic heartbeat.
- **Fix:** Add a 15s ticker. On tick, write `: keepalive\n\n` (SSE comment — ignored by EventSource API but resets TCP idle timer). Reset ticker on each real event send. Cancel ticker when client disconnects or context is done.

### 14a-2: `reclone` deletes repo before verifying new clone succeeds
- **File:** `internal/git/repo.go` (`reclone`)
- **Problem:** `os.RemoveAll(opts.DestDir)` then `doClone(ctx, opts)`. If clone fails (network timeout, auth error, disk full), the bundle directory is now empty. The daemon cannot resolve any app definitions until the next successful sync (up to `pullPeriod` later, default 5m). All containers stop getting updates.
- **Fix:** Clone to a temp directory (`opts.DestDir + ".tmp"`) in the same filesystem. On success: `os.RemoveAll(old)` + `os.Rename(tmp, dest)`. On failure: `os.RemoveAll(tmp)`, keep old directory intact, return the error. Log a warning so the operator knows the repo is stale.

### 14a-3: Namespace ID collision in desktop mode — silent overwrite
- **File:** `internal/daemon/routes_p2.go` (`handleCreateNamespace`)
- **Problem:** `sanitizeName("My NS")` and `sanitizeName("MY NS")` both produce `"my-ns"`. The handler writes `namespace.yml` to the derived path without checking if it already exists. A second creation with a colliding name silently overwrites the first namespace's config.
- **Fix:** Before writing, `os.Stat(configPath)`. If file exists and this is a create (not update), return 409 with error code `NAMESPACE_EXISTS`.
- **New constant:** `ErrCodeNamespaceExists = "NAMESPACE_EXISTS"` in `api/dto.go`.

### 14a-4: Concurrent reload guard
- **File:** `internal/daemon/routes.go` (`handleReloadNamespace`)
- **Problem:** Two simultaneous reload requests both perform network I/O (bundle resolution), then both call `runtime.Regenerate()`. The `cmdCh` (capacity 1) drops the second command silently. The first reload succeeds but the second returns success to the caller while its regeneration was actually dropped.
- **Fix:** Add `reloadMu sync.Mutex` to `Daemon`. Use `reloadMu.TryLock()` at handler entry. If already locked, return 409 `RELOAD_IN_PROGRESS`. Unlock in defer after the handler completes.
- **New constant:** `ErrCodeReloadInProgress = "RELOAD_IN_PROGRESS"` in `api/dto.go`.

---

## Sub-Phase 14b: Web UI Reliability (HIGH)

**6 issues, ~5 files**

### 14b-1: Logs page O(n) string re-split on every streaming chunk
- **File:** `web/src/pages/Logs.tsx`
- **Problem:** `logs` stored as a single string. Every streaming chunk: `setLogs(prev + chunk)`. Then `useMemo` calls `logs.split('\n')` + 7-pattern level detection on up to 50k lines — O(50k * 7) regex operations blocking the main thread on every append. Causes visible jank and dropped frames during active log streaming.
- **Fix:** Store `logs` as `string[]` (line array). Append new lines via `setLogs(prev => [...prev, ...newLines])`. Level detection: maintain a parallel `levels: string[]` array, run detection only on new lines. Ring-buffer cap: `if (lines.length > maxLines) lines = lines.slice(-maxLines)`. Initial load: `setLogs(text.split('\n'))`.

### 14b-2: ReDoS via user-supplied regex in Logs search
- **File:** `web/src/pages/Logs.tsx` (`highlightSearch`, `searchMatches`)
- **Problem:** `new RegExp(userInput, 'gi')` without sanitization. Patterns like `(a+)+b` on a 500-char log line cause catastrophic backtracking — hangs the browser tab for seconds or indefinitely. The `try/catch` only catches `SyntaxError`, not exponential backtracking.
- **Fix:** Before applying user regex to full log set, test it on a synthetic 500-char string with a 50ms `setTimeout` guard. If the test doesn't complete in 50ms, show "regex too complex" warning and fall back to literal match (escaping special chars with `escapeRegex()`). Alternative: use `RE2` semantics via a safe-regex library that rejects backtracking patterns.

### 14b-3: `fetchJSON` ignores server error body
- **File:** `web/src/lib/api.ts` (`fetchJSON`)
- **Problem:** On non-2xx: `throw new Error(\`HTTP ${res.status}: ${res.statusText}\`)`. Server returns structured `ErrorDto` with `message` field (e.g. "proxy port must be 1-65535") — completely discarded. User sees "HTTP 400: Bad Request" everywhere.
- **Fix:**
  ```typescript
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = await res.json()
      if (body.message) msg = body.message
    } catch { /* not JSON, use statusText */ }
    throw new Error(msg)
  }
  ```

### 14b-4: No fetch timeout — UI hangs forever
- **File:** `web/src/lib/api.ts`
- **Problem:** All `fetch()` calls have no `AbortController` / `signal`. If daemon stops responding but TCP connection stays open (common with NAT, keep-alive), the browser waits indefinitely. Loading spinners spin forever, user must reload.
- **Fix:** Create a `fetchWithTimeout(url, opts, timeoutMs = 30_000)` wrapper that creates an `AbortController`, sets `signal` on the request, and calls `setTimeout(controller.abort, timeoutMs)`. Use for all non-streaming requests. Streaming endpoints (log follow, SSE) remain unlimited.

### 14b-5: `fetchData` flashes skeleton on every SSE event
- **File:** `web/src/lib/store.ts` (`fetchData`)
- **Problem:** `set({ loading: true, error: null })` runs on every call. SSE events trigger debounced `fetchData()` every 100ms during activity. Each call sets `loading: true` for a few hundred milliseconds — dashboard skeleton appears and disappears, causing visible flicker during pull/start sequences.
- **Fix:** Only set `loading: true` when `namespace` is currently null (initial load). For refresh-while-data-exists:
  ```typescript
  fetchData: async () => {
    const isInitial = get().namespace === null
    if (isInitial) set({ loading: true })
    set({ error: null })
    // ... fetch ...
  }
  ```

### 14b-6: DaemonLogs page — unbounded 3s polling without visibility check
- **File:** `web/src/pages/DaemonLogs.tsx`
- **Problem:** `setInterval(fetchLogs, 3000)` runs permanently, even when the browser tab is hidden (user switched to another app). At 10k concurrent browser tabs = 3,333 requests/second to the daemon's log endpoint. Each request reads and returns up to 500 lines, causing needless I/O.
- **Fix:** Use `document.addEventListener('visibilitychange', ...)` to pause/resume the interval. Also increase interval to 5s (3s is aggressive for daemon logs). Clear interval on unmount (already done via `useEffect` cleanup, but add the visibility listener).

---

## Sub-Phase 14c: Test Coverage & CI (MEDIUM)

**5 issues, ~5 files**

### 14c-1: Runtime state machine behavioral tests
- **File:** new `internal/namespace/runtime_behavior_test.go`
- **Problem:** The core namespace lifecycle (`doStart`, `doStop`, `waitForDeps`, `Regenerate`, reconciler retry) has zero behavioral tests. The runtime is the most complex and most critical code in the project — 1400+ lines of concurrent state machine logic — and all correctness relies on manual testing.
- **Fix:** Create a `mockDockerClient` implementing the Docker interface. Write tests:
  - `TestStartAndStop` — start namespace with 3 apps (one with deps), verify all reach RUNNING, stop, verify all reach STOPPED
  - `TestWaitForDeps` — app B depends on A, verify B stays in DEPS_WAITING until A reaches RUNNING
  - `TestRegeneratePreservesRunning` — regenerate with unchanged app → container NOT restarted (hash match)
  - `TestRegenerateRestartsChanged` — regenerate with changed image → container restarted
  - `TestStopWhileStarting` — stop during start sequence, verify no deadlock, all goroutines exit

### 14c-2: RotatingWriter tests
- **File:** new `internal/fsutil/rotating_test.go`
- **Problem:** `RotatingWriter` is used for daemon log rotation (50MB/3 files). No tests for rotation threshold, file rename sequence (.1→.2→.3), concurrent write safety, or `Close()`. A rotation bug means silent log loss.
- **Fix:** Tests:
  - `TestRotation` — write > maxBytes, verify .1 created with old content, new file starts fresh
  - `TestMultipleRotations` — write 3x maxBytes, verify .1/.2/.3 exist, .3 has oldest data
  - `TestConcurrentWrites` — 10 goroutines writing simultaneously, verify no panics or data corruption
  - `TestClose` — close writer, verify subsequent writes return error

### 14c-3: Add `-race` to CI and Makefile
- **Files:** `.github/workflows/release-go.yml`, `Makefile`
- **Problem:** Race detector not in CI. The project has 1400+ lines of lock-protected concurrent state machine code. Races can only be reliably detected with `-race`.
- **Fix:** `go test -race ./internal/...` in both CI workflow and Makefile `test` target.

### 14c-4: Remove stale Kotlin workflow
- **File:** `.github/workflows/release.yml`
- **Problem:** References `./gradlew` which doesn't exist in the Go codebase. Fires on every `v*.*.*` tag, fails noisily, creates confusion in CI dashboard.
- **Fix:** Delete the file.

### 14c-5: Add pre-merge CI workflow
- **File:** new `.github/workflows/ci.yml`
- **Problem:** Tests run only at release tag time. A broken commit can be merged to master and only discovered when cutting a release.
- **Fix:** Workflow triggered on push to master + PR to master:
  ```yaml
  on:
    push: { branches: [master] }
    pull_request: { branches: [master] }
  jobs:
    test:
      runs-on: ubuntu-latest
      steps:
        - uses: actions/checkout@v4
        - uses: actions/setup-go@v5
          with: { go-version-file: go.mod }
        - run: go vet ./...
        - run: go test -race ./internal/...
        - uses: actions/setup-node@v4
          with: { node-version: 22 }
        - run: cd web && npm ci && npx vitest run
  ```

---

## Sub-Phase 14d: UX Polish (LOW)

**5 issues, ~5 files**

### 14d-1: AppDetail — stale responses on rapid tab switch
- **File:** `web/src/pages/AppDetail.tsx`
- **Problem:** `load()` fires 4 concurrent API calls on mount with no `AbortController`. Switching quickly between app tabs: responses from old app overwrite state for the newly selected app.
- **Fix:** Add `AbortController` in `useEffect`. Pass `signal` to all 4 fetch calls. Abort on cleanup return.

### 14d-2: Follow mode + initial fetch duplicate log lines
- **File:** `web/src/pages/Logs.tsx`
- **Problem:** `fetchInitialLogs` and streaming follow both fire on mount when `follow === true`. First streaming chunk contains the same tail lines already fetched — user sees duplicated content.
- **Fix:** When `follow` is true on mount, skip `fetchInitialLogs`. The stream provides initial data.

### 14d-3: Restart error silently discarded
- **File:** `web/src/pages/AppDetail.tsx` (`handleRestart`)
- **Problem:** `try { await postAppRestart(name) } finally { setRestarting(false) }` — no `catch`. A 500 or network error vanishes. User clicks "Restart", button returns to normal, nothing happened.
- **Fix:** Add `.catch(e => toast(e.message, 'error'))`.

### 14d-4: Config fetch failure silently swallowed
- **File:** `web/src/pages/Config.tsx`
- **Problem:** `.catch(() => null)` on config fetches. On 500 error, user sees "No configuration file found" instead of the real error.
- **Fix:** Track error state separately. Show error banner when fetch fails, show "not found" only when response is 404.

### 14d-5: `lastSeq` not reset on SSE reconnect — spurious toast
- **File:** `web/src/lib/store.ts`
- **Problem:** `stopEventStream` resets `lastSeq: 0`. After reconnect, the first event has `seq > 0 + 1` → gap detection fires → "Connection restored" toast even though no events were missed.
- **Fix:** In the SSE `onOpen` callback, reset `lastSeq` to 0. Gap detection only fires when `lastSeq > 0`, so the first event after reconnect is always accepted cleanly.

---

## Implicit Rules

- All new tests run with `-race` flag.
- All new error responses use `writeErrorCode` with constants from `api/dto.go`.
- Web UI fetch calls use `fetchWithTimeout` wrapper (14b-4) by default.

---

## Execution Order

```
14a (backend reliability) → 14b (web UI reliability) → deploy + test on server
→ 14c (tests + CI) → 14d (UX polish)
```

Each sub-phase: implement → `go test -race ./internal/...` → code review → fix issues.

After 14b: build, deploy to server, test SSE heartbeat via load balancer, verify Logs page with 50k lines.

---

## Verification

1. `go test -race ./internal/...` — all pass, no races
2. `curl` SSE endpoint → receives `: keepalive` comment every 15s
3. Kill git server during reclone → old bundle directory preserved, daemon continues
4. Create two namespaces with colliding names → second gets 409 `NAMESPACE_EXISTS`
5. Send two simultaneous reload requests → second gets 409 `RELOAD_IN_PROGRESS`
6. Browser Logs page with 50k lines in follow mode → smooth scrolling, no jank
7. Type `(a+)+` in Logs regex search → "regex too complex" warning, no hang
8. Daemon returns 400 with validation message → UI shows the actual message, not "HTTP 400"
9. Stop daemon while UI is open → fetch timeout fires in 30s, error shown
10. CI workflow runs on PR → `go vet`, `go test -race`, `vitest` all pass
11. Runtime behavior tests pass → start, stop, deps wait, regenerate hash match

## Not in scope (deferred)

These were flagged in the audit but are not production-blocking:

- **Shared shutdown context** (LOW) — both servers drain instantly in practice
- **Rate limiter under DDoS** (LOW) — daemon runs on localhost or behind mTLS
- **CLI CSRF for localhost TCP** (LOW) — CLI defaults to Unix socket; TCP path is for browsers
- **Prometheus lock during scrape** (LOW) — sub-millisecond contention after warmup
- **SQLite plaintext secrets** (desktop only) — matches OS keychain threat model (same-user access)
- **Accessibility** (P2) — important but not blocking initial production release
- **Mobile responsive** (P2) — primary use is desktop browser for server management
