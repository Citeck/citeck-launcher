# Plan: Phase 9 — Production Hardening for Scale

## Context

Launcher deployed and tested on server. Deep analysis for production deployment revealed issues across security, data integrity, and concurrency. After cross-referencing with actual code, **6 false positives eliminated**, priorities corrected. Final: **3 critical, 4 high, 5 medium** verified real issues.

---

## Phase 9a: Data Integrity — Atomic Writes (4 issues)

All use the same pattern: `os.WriteFile` directly → crash mid-write = corrupt file. Fix: shared `AtomicWriteFile` helper.

| # | Issue | File:Line | Fix |
|---|-------|-----------|-----|
| 1 | **Non-atomic cert/key write** — `O_TRUNC` destroys old cert before new data written; crash = empty cert, HTTPS broken | `acme/client.go:177-200` | Write to temp file, fsync, rename. Write cert+key as a pair (key first, then cert — cert presence signals completion) |
| 2 | **Non-atomic state.json** — `SetState` uses direct `os.WriteFile`; crash = corrupt JSON, wrong workspace on restart | `storage/filestore.go:186` | Use `AtomicWriteFile` |
| 3 | **Non-atomic daemon.yml** — `SaveDaemonConfig` uses direct `os.WriteFile`; crash = config lost | `config/daemon.go:90` | Use `AtomicWriteFile` |
| 4 | **NsState missing fsync** — `SaveNsState` already uses temp+rename (good!) but no fsync before rename; power failure on `data=writeback` ext4 = empty file | `namespace/state.go:34` | Replace manual temp+rename with `AtomicWriteFile` (which includes fsync) |

**Shared helper** — `internal/fsutil/atomic.go`:
```go
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error
```
Pattern: CreateTemp in same dir → Write → Chmod → Sync → Close → Rename.

---

## Phase 9b: Security (2 issues)

| # | Issue | File:Line | Fix |
|---|-------|-----------|-----|
| 5 | **Shell injection in snapshot import** — `vol.DataFile` from untrusted meta.json interpolated into shell `sh -c` command. Crafted snapshot can execute arbitrary commands in utils container with host bind mounts | `snapshot/snapshot.go:269-274` | Apply `sanitizeFileName()` to `vol.DataFile` before using in shell command. Reject if contains path separators after sanitization |
| 6 | **`io.ReadAll` in TTY log fallback** — fallback path for TTY containers uses unbounded `io.ReadAll(reader)`, no limit | `docker/client.go:488` | Wrap with `io.LimitReader(reader, 50*1024*1024)` (50MB cap) |

---

## Phase 9c: Concurrency & Robustness (6 issues)

| # | Issue | File:Line | Fix |
|---|-------|-----------|-----|
| 7 | **Docker InspectContainer under write lock** — reconciler holds `r.mu.Lock()` during Docker API calls (up to 5s × N apps). All readers (API, stats) blocked | `reconciler.go:108-147` | Split into 2 phases: (a) collect container IDs + names under lock; (b) release lock, inspect outside; (c) re-acquire lock, update state |
| 8 | **StopApp has no timeout** — uses `context.Background()`, handler blocked indefinitely if Docker unresponsive | `runtime.go:497` | `context.WithTimeout(context.Background(), 2*time.Minute)` |
| 9 | **Snapshot auto-import no timeout** — `context.Background()` for download blocks startup indefinitely on slow network | `server.go:809` | `context.WithTimeout(context.Background(), 30*time.Minute)` |
| 10 | **ACME rate limit not persisted** — daemon restart within backoff window immediately retries LE, risks account lockout | `acme/renewal.go:124-131` | Write `{dataDir}/acme/rate-limit-until` file with timestamp. Check before attempting. Simple: `time.Now().Before(rateLimitUntil)` → skip |
| 11 | **Probe failureThreshold=0 → 10000** — effectively 27h wait, app stuck STARTING | `runtime.go:1177` | Default to 360 (1 hour with 10s period) instead of 10000 |
| 12 | **OIDC client secret hardcoded** — same UUID across all deployments. Low-risk (containers on isolated Docker network), but audit red flag | `generator.go:441`, `ecos-app-realm.json:311`, `lua_oidc_full_access.lua:7` | Generate per-namespace via `OIDCSecret()` (like JWTSecret). Substitute in realm JSON before first import, proxy env, lua file. For existing Keycloak DB, update via `kcadm.sh` init action (like redirect URIs) |

---

## Implementation Order

**Day 1 — Atomic writes + security:**
1. Create `internal/fsutil/atomic.go` with `AtomicWriteFile`
2. Apply to `filestore.go`, `daemon.go`, `state.go`
3. Fix ACME cert write to use atomic pattern (cert+key pair)
4. Sanitize `vol.DataFile` in snapshot import
5. Add `io.LimitReader` to TTY log fallback

**Day 2 — Concurrency:**
6. Refactor reconciler to inspect outside lock
7. Add timeout to `StopApp`
8. Add timeout to snapshot auto-import
9. Fix probe failureThreshold default

**Day 3 — ACME + OIDC:**
10. Persist ACME rate limit state
11. Generate per-namespace OIDC client secret

## Files to Modify

| File | Issues |
|------|--------|
| `internal/fsutil/atomic.go` | NEW — shared AtomicWriteFile helper |
| `internal/storage/filestore.go` | #2 atomic SetState |
| `internal/config/daemon.go` | #3 atomic SaveDaemonConfig |
| `internal/namespace/state.go` | #4 add fsync |
| `internal/acme/client.go` | #1 atomic cert+key write |
| `internal/acme/renewal.go` | #10 rate limit persistence |
| `internal/snapshot/snapshot.go` | #5 sanitize vol.DataFile |
| `internal/docker/client.go` | #6 LimitReader for TTY logs |
| `internal/namespace/reconciler.go` | #7 inspect outside lock |
| `internal/namespace/runtime.go` | #8 StopApp timeout, #11 probe threshold |
| `internal/daemon/server.go` | #9 auto-import timeout |
| `internal/namespace/generator.go` | #12 OIDC secret generation |
| `internal/namespace/context.go` | #12 OIDCSecret() alongside JWTSecret() |

## Verification

1. `go test ./...` — all pass
2. `go vet ./...` — clean
3. **Crash test:** `kill -9` daemon during state write → restart → state intact
4. **Security test:** craft snapshot with `; echo PWNED > /dest/pwned` in meta.json dataFile → verify sanitized
5. **Lock test:** `docker kill` a running container, observe reconciler restarts it without blocking API
6. **Timeout test:** stop Docker daemon, call `citeck stop app` → verify returns after 2min, not hang
7. **ACME test:** trigger rate limit → restart daemon → verify no immediate retry
8. Server deploy: rebuild, copy to server, verify all 22 services start
