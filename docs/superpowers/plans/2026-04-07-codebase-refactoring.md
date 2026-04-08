# Codebase Refactoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split god files, eliminate code duplication, and normalize error handling patterns across the codebase — pure structural refactoring with zero behavior changes.

**Architecture:** File-level splits along domain boundaries. No new packages, no interface changes, no API changes. Every task produces identical behavior before and after — verified by existing tests + `go vet` + `golangci-lint`.

**Tech Stack:** Go 1.24, golangci-lint v2.11.4, `make test-unit`, `make lint`

**Critical rule:** Every task is a pure move/extract. If tests break, the refactoring is wrong — never fix tests to accommodate a "refactoring."

---

## File Structure

### Splits

| Current File | Lines | Split Into | Responsibility |
|---|---|---|---|
| `internal/namespace/runtime.go` | 1,755 | `runtime.go` | Runtime struct, state machine, command loop, public API |
| | | `runtime_app.go` | Per-app lifecycle: pull, start, deps, init containers, startup probes |
| | | `runtime_probes.go` | Liveness probes, stats collection, HTTP probe check, formatting helpers |
| `internal/daemon/routes.go` | 1,471 | `routes_apps.go` | App CRUD: logs, restart, stop, start, inspect, exec, config, files, lock |
| | | `routes_config.go` | Namespace config GET/PUT, events SSE, daemon logs |
| | | `routes_system.go` | Health, metrics, Prometheus, system dump, log level, restart events, diagnostics file |
| | | `routes_volumes.go` | Volume list, delete |
| `internal/daemon/routes_p2.go` | 1,418 | `routes_ns.go` | Namespace CRUD: list, delete, create, templates, quick starts, bundles, forms |
| | | `routes_secrets.go` | Secrets CRUD, encryption status, unlock, password setup, migration |
| | | `routes_snapshots.go` | Snapshot list, export, import, download, rename, workspace snapshots |
| | | `routes_diagnostics.go` | Diagnostics list, fix actions |

### Extractions

| Current Location | Extract To | What |
|---|---|---|
| `routes.go:207` | `server.go` | `doReload()` — business logic, not an HTTP handler |
| `routes.go:772` | `routes_system.go` | `buildDumpData()` — belongs with system dump handler |
| `routes.go:1392` | stays in `routes_apps.go` | `findApp()` — used by app handlers |
| `routes_p2.go:391` | stays in `routes_ns.go` | `resolveBundleDir()` — used by bundle handler |
| `routes_p2.go:831-839` | stays in `routes_snapshots.go` | `snapshotsDir()`, `activeNsID()` |

### Helper Extractions (dedup)

| Pattern | Current | Extract To |
|---|---|---|
| `tail` param parsing | `routes.go:324`, `routes.go:659` | `func parseTailParam(r, default, max) int` in `routes_helpers.go` |
| `d.runtime == nil` guard | 7 handlers | `func (d *Daemon) requireRuntime(w) bool` in `routes_helpers.go` |
| `volumesDir()` | `routes.go:900` | Move to `routes_helpers.go` (used by volumes + system dump) |

### Cleanup

| Item | File | Action |
|---|---|---|
| `GracefulShutdownOrder` | `reconciler.go:404` | Unexport → `gracefulShutdownOrder`, keep for test |
| `doReload` in routes.go | `routes.go:207` | Move to `server.go` (business logic, not handler) |

---

### Task 1: Extract `routes_helpers.go` — shared handler utilities

**Files:**
- Create: `internal/daemon/routes_helpers.go`
- Modify: `internal/daemon/routes.go`

- [ ] **Step 1: Create `routes_helpers.go` with `parseTailParam` and `requireRuntime`**

```go
package daemon

import (
	"net/http"
	"strconv"

	"github.com/citeck/citeck-launcher/internal/api"
)

// parseTailParam reads the "tail" query parameter with a default and max cap.
func parseTailParam(r *http.Request, defaultVal, maxVal int) int {
	tailStr := r.URL.Query().Get("tail")
	tail := defaultVal
	if tailStr != "" {
		if n, err := strconv.Atoi(tailStr); err == nil {
			tail = n
		}
	}
	if tail > maxVal {
		tail = maxVal
	}
	return tail
}

// requireRuntime checks that a namespace runtime exists. Returns false and writes
// an error response if not configured. Callers should return immediately when false.
func (d *Daemon) requireRuntime(w http.ResponseWriter) bool {
	if d.runtime == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return false
	}
	return true
}
```

- [ ] **Step 2: Move `volumesDir()` (routes.go:900) to `routes_helpers.go`**

This helper is used by both `handleListVolumes` (→ routes_volumes.go) and `buildDumpData` (→ routes_system.go), so it belongs in the shared helpers file.

- [ ] **Step 3: Replace duplicated `tail` parsing in `handleAppLogs` (routes.go:324-333)**

Replace the 8-line block with:
```go
tail := parseTailParam(r, 100, 10000)
```

- [ ] **Step 4: Replace duplicated `tail` parsing in `handleDaemonLogs` (routes.go:659-668)**

Replace the 8-line block with:
```go
tail := parseTailParam(r, 200, 10000)
```

- [ ] **Step 5: Replace all 7 `if d.runtime == nil` guards in routes.go**

In each handler (`handleReloadNamespace:124`, `handleUpgradeNamespace:157`, `handleAppRestart:409`, `handleAppStop:431`, `handleAppStart:452`, `handlePutAppConfig:995`, `handleAppLockToggle:1169`), replace:
```go
if d.runtime == nil {
    writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
    return
}
```
with:
```go
if !d.requireRuntime(w) {
    return
}
```

- [ ] **Step 6: Build and test**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" go build ./internal/... && go test -race ./internal/daemon/... && golangci-lint run ./internal/daemon/...`

Expected: 0 errors, 0 lint issues, all tests pass.

- [ ] **Step 7: Commit**

```
git add internal/daemon/routes_helpers.go internal/daemon/routes.go
git commit -m "refactor: extract parseTailParam + requireRuntime + volumesDir to routes_helpers.go"
```

---

### Task 2: Split `runtime.go` → `runtime.go` + `runtime_app.go` + `runtime_probes.go`

**Files:**
- Create: `internal/namespace/runtime_app.go`
- Create: `internal/namespace/runtime_probes.go`
- Modify: `internal/namespace/runtime.go`

**Split boundaries (by line ranges in current runtime.go):**

`runtime_app.go` — per-app lifecycle (move these functions):
- `pullAndStartApp` (line 1094-1232)
- `waitForDeps` (line 1234-1273)
- `runInitContainers` (line 1275-1322)
- `waitForStartup` (line 1324-1339)
- `waitForLogPattern` (line 1341-1386)
- `waitForProbe` (line 1388-1454)
- `shouldPullImage` (line 1456-1462)
- `buildExistingContainerMap` (line 1471-1492)
- `existingContainer` struct (line 1464-1468)

`runtime_probes.go` — stats, probes, formatting (move these functions):
- `updateStats` (line 1605-1664)
- `checkStatus` (line 1666-1693)
- `httpProbeCheck` (line 1713-1732)
- `formatMemory` (line 1696-1710)
- `formatBytes` (line 1734-1747)
- `truncateID` (line 1749-1755)
- `probeClient` var (line 1705)

Everything else stays in `runtime.go`.

- [ ] **Step 1: Create `runtime_app.go`**

Move the functions listed above. File header:
```go
package namespace

import (
	// only imports actually used by moved functions
)
```

- [ ] **Step 2: Create `runtime_probes.go`**

Move the functions listed above. File header:
```go
package namespace

import (
	// only imports actually used by moved functions
)
```

- [ ] **Step 3: Remove moved functions from `runtime.go`**

Delete the function bodies that were moved. Do NOT change any function signatures, types, or logic.

- [ ] **Step 4: Build and test**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" go build ./internal/... && go test -race ./internal/namespace/... && golangci-lint run ./internal/namespace/...`

Expected: all tests pass, 0 lint issues. If any test fails, the move was incorrect — a function references something that wasn't moved together.

- [ ] **Step 5: Commit**

```
git add internal/namespace/runtime.go internal/namespace/runtime_app.go internal/namespace/runtime_probes.go
git commit -m "refactor: split runtime.go into runtime_app.go + runtime_probes.go"
```

---

### Task 3: Split `routes.go` → `routes_apps.go` + `routes_config.go` + `routes_system.go` + `routes_volumes.go`

**Files:**
- Create: `internal/daemon/routes_apps.go`
- Create: `internal/daemon/routes_config.go`
- Create: `internal/daemon/routes_system.go`
- Create: `internal/daemon/routes_volumes.go`
- Modify: `internal/daemon/routes.go` (becomes empty or deleted)

**Split plan (by handler → target file):**

`routes_apps.go`:
- `handleAppLogs` (319)
- `handleAppLogsFollow` (361)
- `handleAppRestart` (404)
- `handleAppStop` (426)
- `handleAppStart` (447)
- `handleAppInspect` (473)
- `handleAppExec` (542)
- `handleGetAppConfig` (970)
- `handlePutAppConfig` (990)
- `handleListAppFiles` (1048)
- `handleGetAppFile` (1079)
- `handlePutAppFile` (1112)
- `handleAppLockToggle` (1164)
- `findApp` helper (1392)

`routes_config.go`:
- `handleDaemonStatus` (30)
- `handleDaemonShutdown` (43)
- `handleGetNamespace` (51)
- `handleStartNamespace` (92)
- `handleStopNamespace` (104)
- `handleReloadNamespace` (116)
- `handleUpgradeNamespace` (138)
- `handleGetConfig` (581)
- `handlePutConfig` (592)
- `handleEvents` (615)
- `handleDaemonLogs` (656)
- `handleSetLogLevel` (1361)

`routes_system.go`:
- `handleHealth` (1191)
- `handleMetrics` (1275)
- `handleSystemDump` (810)
- `handleRestartEvents` (1427)
- `handleDiagnosticsFile` (1449)
- `buildDumpData` helper (772)
- `writeSystemDumpZip` helper (833)

`routes_volumes.go`:
- `handleListVolumes` (904)
- `handleDeleteVolume` (945)

- [ ] **Step 1: Create all 4 target files by moving handlers**

Each file: `package daemon` + only the imports needed by its functions.

- [ ] **Step 2: Move `doReload` (routes.go:207) to `server.go`**

This is business logic, not an HTTP handler. Place it near the other daemon lifecycle methods.

- [ ] **Step 3: Delete `routes.go`**

After moving everything, `routes.go` should be empty. Delete it.

- [ ] **Step 4: Build and test**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" go build ./internal/... && go test -race ./internal/daemon/... && golangci-lint run ./internal/daemon/...`

- [ ] **Step 5: Commit**

```
git add internal/daemon/
git commit -m "refactor: split routes.go into domain-specific route files"
```

---

### Task 4: Split `routes_p2.go` → `routes_ns.go` + `routes_secrets.go` + `routes_snapshots.go` + `routes_diagnostics.go`

**Files:**
- Create: `internal/daemon/routes_ns.go`
- Create: `internal/daemon/routes_secrets.go`
- Create: `internal/daemon/routes_snapshots.go`
- Create: `internal/daemon/routes_diagnostics.go`
- Delete: `internal/daemon/routes_p2.go`

**Split plan:**

`routes_ns.go`:
- `handleListNamespaces` (63)
- `handleDeleteNamespace` (118)
- `handleGetTemplates` (151)
- `handleGetQuickStarts` (172)
- `handleGetForm` (195)
- `handleCreateNamespace` (208)
- `handleListBundles` (370)
- `resolveBundleDir` helper (391)

`routes_secrets.go`:
- `handleListSecrets` (407)
- `handleCreateSecret` (432)
- `handleDeleteSecret` (476)
- `handleTestSecret` (497)
- `handleGetMigrationStatus` (552)
- `handleSubmitMasterPassword` (582)
- `handleGetSecretsStatus` (645)
- `handleUnlockSecrets` (653)
- `handleSetupPassword` (680)

`routes_snapshots.go`:
- `handleListSnapshots` (848)
- `handleExportSnapshot` (878)
- `handleImportSnapshot` (961)
- `handleDownloadSnapshot` (1069)
- `handleWorkspaceSnapshots` (1273)
- `handleRenameSnapshot` (1285)
- `snapshotsDir` helper (831)
- `activeNsID` helper (839)
- `downloadAndImportSnapshot` helper (1162)

`routes_diagnostics.go`:
- `handleGetDiagnostics` (713)
- `handleDiagnosticsFix` (803)

- [ ] **Step 1: Create all 4 target files by moving handlers**

- [ ] **Step 2: Delete `routes_p2.go`**

- [ ] **Step 3: Build and test**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" go build ./internal/... && go test -race ./internal/daemon/... && golangci-lint run ./internal/daemon/...`

- [ ] **Step 4: Commit**

```
git add internal/daemon/
git commit -m "refactor: split routes_p2.go into domain-specific route files"
```

---

### Task 5: Cleanup — unexport `GracefulShutdownOrder`, normalize HTTP status

**Files:**
- Modify: `internal/namespace/reconciler.go`
- Modify: `internal/namespace/reconciler_test.go`

- [ ] **Step 1: Unexport `GracefulShutdownOrder` in reconciler.go (line 404)**

Rename `GracefulShutdownOrder` → `gracefulShutdownOrder`. It has no production callers — only used in `reconciler_test.go`.

- [ ] **Step 2: Update the test in reconciler_test.go**

Change the test to call `gracefulShutdownOrder` (unexported but accessible from `_test.go` in same package).

- [ ] **Step 3: Build and test**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" go build ./internal/... && go test -race ./internal/namespace/... && golangci-lint run ./internal/namespace/...`

- [ ] **Step 4: Commit**

```
git add internal/namespace/reconciler.go internal/namespace/reconciler_test.go
git commit -m "refactor: unexport GracefulShutdownOrder (no production callers)"
```

---

### Task 6: Final verification

- [ ] **Step 1: Full build**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" make build-fast`

- [ ] **Step 2: Full test suite**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" make test-unit`

- [ ] **Step 3: Full lint**

Run: `PATH="/usr/local/go/bin:$HOME/go/bin:$PATH" golangci-lint run ./internal/... ./cmd/citeck/...`

- [ ] **Step 4: Verify line counts improved**

Run: `wc -l internal/namespace/runtime*.go internal/daemon/routes*.go`

Expected: no single file over ~800 lines. `runtime.go` should be ~700-800, `runtime_app.go` ~500, `runtime_probes.go` ~200. Each route file should be 200-500 lines.

- [ ] **Step 5: Update CLAUDE.md**

Add a "Phase 20: Codebase Refactoring" section documenting the file splits and dedup changes.

- [ ] **Step 6: Commit**

```
git add CLAUDE.md
git commit -m "docs: add Phase 20 codebase refactoring summary to CLAUDE.md"
```
