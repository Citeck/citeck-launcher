# Self-Healing Runtime

Bring the launcher daemon closer to Kubernetes-level autonomy: detect unhealthy services, collect diagnostics, restart automatically, and surface what happened.

## Current State

The launcher already has:
- **Startup probes** — block until app is ready (HTTP or exec). Default threshold 360 attempts (~1 hour). `DefaultProbe()` in appdef is dead code (never called).
- **LivenessProbe field** on `ApplicationDef` — checked by `checkLiveness()` in reconciler every 30s.
- **Reconciler** — detects missing containers, restarts with exponential backoff (1m→30m).
- **Docker `unless-stopped`** restart policy on all containers.
- **OOM detection** — inspects `OOMKilled` flag on missing containers, emits `app_oom` SSE event.
- **Health API** — `GET /api/v1/health` returns healthy/degraded/unhealthy with per-app checks.

What's missing:
- No service defines a `LivenessProbe` — the mechanism exists but is not wired.
- `checkLiveness` restarts on **first** failed probe — no failure counting.
- `runLivenessProbe` uses `curl` inside the container for HTTP probes — not all images have curl.
- Startup probe threshold is 1 hour (360 × 10s) — too long for detecting a genuinely broken deploy.
- `checkLiveness` only runs in RUNNING state — if one app dies (→ STALLED), liveness stops for ALL apps. Other apps can hang undetected.
- No restart counter visible in API/UI.
- No event log for restarts (only slog output).
- No pre-restart diagnostics (logs, thread dumps).
- `DefaultProbe()` in appdef is dead code (never called).

## Design

### 1. Liveness Probes in Generator

Add `LivenessProbe` to all service definitions in `generator.go`.

**Java webapps** (all apps from bundle — eapps, emodel, gateway, etc.):
```go
app.LivenessProbe = &appdef.AppProbeDef{
    HTTP: &appdef.HTTPProbeDef{
        Path: "/management/health",
        Port: port,
    },
    FailureThreshold: 3,
    TimeoutSeconds:   5,
}
```

Note: `PeriodSeconds` is NOT used by `checkLiveness` — the liveness cycle period is global via `ReconcilerConfig.LivenessPeriod` (30s). `PeriodSeconds` is only used by startup probes in `waitForProbe`. Setting it on liveness probes would be misleading, so we omit it.

**Third-party infrastructure:**

| Service | Probe type | Command / Path |
|---------|-----------|----------------|
| postgres | Exec | `pg_isready -U postgres` |
| observer-postgres | Exec | `pg_isready -U observer` |
| zookeeper | HTTP | `/commands/ruok` on port 8080 (ZK admin server, no nc/curl dependency) |
| rabbitmq | Exec | `rabbitmq-diagnostics check_running -q` |
| mongo | Exec | `mongo --quiet --eval "db.adminCommand('ping')"` (mongo 4.0 has `mongo`, not `mongosh`) |
| keycloak | HTTP | `/health/live` on port 8080 (`--health-enabled=true` already set in cmd) |
| observer | HTTP | `/health` on 17016 |
| mailhog | — | Skip (non-critical) |
| pgadmin | — | Skip (non-critical) |
| onlyoffice | — | Skip (complex, has own restart) |
| proxy | — | Skip (nginx, restarts instantly) |

Default liveness probe parameters:
- `FailureThreshold`: 3 (3 consecutive failures at 30s period = 90s of unhealthy before restart)
- `TimeoutSeconds`: 5

### 2. Failure Counting + STALLED Fix

Add `livenessFailures map[string]int` to `Runtime` (protected by `r.mu`). In `checkLiveness`:

- Probe succeeds → reset counter to 0.
- Probe fails → increment counter. If `counter >= FailureThreshold` → capture diagnostics → restart + reset counter.
- On app restart (status change away from RUNNING) → delete from map.

This prevents restarting on a single transient failure (GC pause, network hiccup).

**Fix: run `checkLiveness` in STALLED state too.** Current code returns early if `r.status != NsStatusRunning`. Change to also allow `NsStatusStalled` — same as `reconcile()` does. Otherwise, when one app fails (namespace → STALLED), liveness probes stop for ALL apps, and other hung apps go undetected.

### 3. Fix `runLivenessProbe` for HTTP

Replace curl-in-container with direct HTTP from daemon (same as startup probe):

```go
func (r *Runtime) runLivenessProbe(ctx context.Context, containerID string, probe *appdef.AppProbeDef) bool {
    timeout := probe.TimeoutSeconds
    if timeout <= 0 {
        timeout = 5
    }
    probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
    defer cancel()

    if probe.Exec != nil {
        _, exitCode, err := r.docker.ExecInContainer(probeCtx, containerID, probe.Exec.Command)
        return err == nil && exitCode == 0
    }
    if probe.HTTP != nil {
        host := r.docker.GetContainerIP(probeCtx, containerID)
        return httpProbeCheck(probeCtx, host, probe.HTTP.Port, probe.HTTP.Path, timeout)
    }
    return true
}
```

- Uses container IP on Docker network — works for both server and desktop modes, no curl dependency.
- Exec probes get a timeout context (currently they run without timeout — a stuck `pg_isready` would block the liveness loop forever).

### 4. Startup Timeout

Change default `FailureThreshold` for startup probes. Current: 0 in generator (→ 360 in `waitForProbe` fallback = 1 hour). Set explicitly to a reasonable value.

In `generateWebapp`:
```go
app.StartupConditions = []appdef.StartupCondition{
    {Probe: &appdef.AppProbeDef{
        HTTP: &appdef.HTTPProbeDef{Path: "/management/health", Port: port},
        PeriodSeconds:    10,
        FailureThreshold: 30,  // 30 × 10s = 5 minutes max startup time
        TimeoutSeconds:   5,
    }},
}
```

For infrastructure services, keep generous timeouts (postgres might need recovery):
- postgres: 60 attempts × 10s = 10 minutes
- keycloak: 60 attempts × 10s = 10 minutes
- others: 30 attempts × 10s = 5 minutes

When startup probe exhausts the threshold, the container is stopped and retried with exponential backoff (existing reconciler logic handles this).

### 5. Restart Events

#### 5a. Restart Counter

Add `RestartCount int` to `AppRuntime`. Incremented every time an app transitions from a running/failed state back to `ReadyToPull` due to:
- Liveness probe failure
- Container crash (missing in reconcile)
- OOM kill
- Startup timeout

Exposed in status API (`GET /api/v1/namespace/{id}/status`) as `restartCount` per app.
Reset to 0 on namespace stop/start. NOT reset on `citeck reload` (reload is a config change, not a clean restart — restart history should survive it).

#### 5b. Restart Event Log

New type:
```go
type RestartEvent struct {
    Timestamp   string `json:"ts"`
    App         string `json:"app"`
    Reason      string `json:"reason"`      // "liveness", "oom", "crash", "startup_timeout"
    Detail      string `json:"detail"`       // e.g. "3/3 liveness probes failed (HTTP /management/health)"
    Diagnostics string `json:"diagnostics"`  // path to diagnostics file, or "" if none
}
```

Storage: in-memory ring buffer (last 100 events per namespace) inside `Runtime`. Persisted to `state-{nsID}.json` alongside existing fields — survives daemon restart.

API: `GET /api/v1/namespace/{id}/restart-events` — returns the event list.

SSE: emit `restart_event` type for real-time UI updates.

### 6. Pre-Restart Diagnostics

Before stopping a container for liveness failure or startup timeout, capture diagnostics while the container is still running. Diagnostics are captured in `checkLiveness` after `FailureThreshold` is reached but BEFORE `setAppStatus(app, AppStatusReadyToPull)`. For crash/OOM, container is already gone — diagnostics are skipped (only the restart event is recorded).

**Step 1: Thread dump (Java services only)**

Detect Java by checking `ApplicationKind.IsCiteckApp()` (all Citeck webapps are Java).

```go
// jcmd is more reliable than kill -3: works regardless of PID, outputs to exec stdout
output, exitCode, _ := r.docker.ExecInContainer(ctx, containerID,
    []string{"jcmd", "1", "Thread.print"})
```

`jcmd 1 Thread.print` sends a thread dump request to PID 1 via JVM attach API.
Output goes to exec stdout (not container stdout), so we capture it directly.
Falls back gracefully: if jcmd fails (exit != 0), diagnostics file gets logs only.

Note: `kill -3` (SIGQUIT) also works but writes to container stdout, requiring a
separate log fetch with timing uncertainty. `jcmd` is synchronous and captures directly.

**Step 2: Capture last logs**

```go
logs := r.docker.GetContainerLogs(ctx, containerID, 500) // last 500 lines
```

**Step 3: Save to file**

```
conf/diagnostics/{app}/{timestamp}.txt
```

Contents:
```
=== RESTART DIAGNOSTICS ===
App:       emodel
Reason:    liveness probe failed (3/3)
Time:      2026-04-06T12:34:56Z
Container: abc123def456

=== THREAD DUMP ===
<jcmd Thread.print output>

=== LAST 500 LOG LINES ===
<container logs>
```

File path stored in `RestartEvent.Diagnostics` — accessible via API for download.

Cleanup: diagnostics files older than 7 days deleted by a periodic cleanup (piggyback on existing reconciler cycle).

### 7. Configuration

All settings use sensible defaults. No daemon.yml changes required to enable liveness probes — they're enabled by default via the generator.

Override points:
- `daemon.yml` → `reconciler.livenessPeriod` (ms, default 30000) — already exists in `config.ReconcilerConfig`
- `daemon.yml` → `reconciler.livenessEnabled` (bool) — add to `config.ReconcilerConfig`, default true
- Per-app in `namespace.yml` → `webapps.{name}.livenessDisabled` (bool, default false) — add to `WebappProps`, disables the auto-generated liveness probe for a specific app.

State persistence: add `RestartEvents []RestartEvent` and `RestartCounts map[string]int` to `NsPersistedState` (backward compatible — JSON `omitempty`, old state files without these fields load cleanly).

### 8. What This Does NOT Include

- **Readiness probes** — no load balancer between instances, not needed.
- **Resource autoscaling** — out of scope.
- **Automatic memory limit increase after OOM** — out of scope (would require config mutation).
- **Alerting/notifications** — out of scope (observer handles this via metrics).

## Files to Change

| File | Change |
|------|--------|
| `internal/namespace/generator.go` | Add `LivenessProbe` to all services, reduce startup threshold |
| `internal/namespace/reconciler.go` | Failure counting, fix `runLivenessProbe`, pre-restart diagnostics, `captureDiagnostics()` |
| `internal/namespace/runtime.go` | `RestartCount` in `AppRuntime`, `livenessFailures` map |
| `internal/namespace/state.go` | Add `RestartEvents`, `RestartCounts` to `NsPersistedState` |
| `internal/namespace/config.go` | Add `LivenessProbe *appdef.AppProbeDef` to `WebappProps` |
| `internal/daemon/routes.go` | `GET /restart-events` endpoint, `restartCount` in status DTO, diagnostics file download |
| `internal/api/dto.go` | `RestartEventDto`, add `restartCount` to app status DTO |
| `internal/config/daemon.go` | Add `LivenessEnabled` to `ReconcilerConfig` |
| `internal/appdef/appdef.go` | Remove dead `DefaultProbe()` |
| Web UI | Show restart count badge, restart events panel |
