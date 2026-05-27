# Remaining Kotlin-parity items (after 2026-05-26 session)

**Session of 2026-05-27 — final status:** All numbered items 1–26 are closed,
plus Doubtful A/B/C/F. The only outstanding work is platform-specific visual
verification that cannot be performed on this Linux dev box:

- **macOS Retina tray icon scaling** — needs a Mac to capture 2× / 3× tray
  screenshots and confirm the embedded 256×256 PNG renders cleanly under
  `SetTemplateIcon`. See Doubtful D below for hand-off detail.
- **GTK tray fallback (`AppIndicator`)** — needs a stock GNOME 45+ or KDE
  shell box (without `gnome-shell-extension-appindicator` installed) to
  confirm whether the Wails tray menu still renders or whether we need a
  hidden-window + keyboard-shortcut fallback. See Doubtful E.

Everything else listed below — items 1–26, multi-workspace polish 11a–11d,
and Doubtful A/B/C/F — landed in this session or was already in HEAD.

> **For continuing in a fresh session:** read this file plus the relevant
> sections of `docs/porting/0X-*.md` referenced below. The bulk of the
> migration is done — see `docs/porting/10-2x-status-and-porting-checklist.md`
> for the wider picture and `git log --oneline -30` for what was just landed.

**Project state at hand-off:**
- Wails v3.0.0-alpha.95 (alpha.96 SIGSEGVs on this dev box — see
  memory `project_wails_alpha96_crash.md`).
- Linux desktop build requires `-tags gtk3`. `Makefile` already sets it.
- All web tests pass (38/38), all Go tests pass (`go test -tags gtk3 ./internal/...`).
- Both binaries build clean (`make build` and `make build-desktop`).

## What's done already

Multi-window architecture, License port, FormDialog/JournalDialog upgraded,
4 modal dialogs (Volumes, Snapshots, Secrets, Namespace), MasterPasswordDialog
(3 modes + Reset), LogViewer Kotlin shortcuts, AppConfigEditor with Reset +
validate + CodeMirror highlighting, DockerNotAvailable screen,
GitPullErrorDialog, RegistryCredentialsDialog (auto-detected on PULL_FAILED),
LoadingHint 30s, tray "Dump System Info", editor RMB context menu for files,
namespace edit via ConfigEditor bottom tab, multi-quickStart Welcome,
sidebar links with categories + per-link SVG icons, container stats SSE
streaming, snapshot create-with-name / delete / .zip strip.

## Remaining items

Numbered 1–10 in priority order. Each block is self-contained — a fresh
session can pick any item and execute it.

---

### 1. CompactResourceRow in sidebar (aggregate CPU / MEM bars) 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.6 + Kotlin `ContainerStatViews.kt:166-228`.

Render two rows under the namespace status indicator in `Dashboard.tsx`
sidebar: `CPU x.x% / max%  [bar 80dp]` and `MEM x.xG / y.yG  [bar 80dp]`.
Color thresholds:

- `progressPercent ≥ 90` → red `#E53935`
- `progressPercent ≥ 70` → orange `#FFA726`
- otherwise → green `#66BB6A`

Note: these aggregate thresholds are MORE sensitive than the per-app
`StatsCell` thresholds (95 / 90).

**Data source:** sum of `app.cpu` + `app.memory` across running apps. The
`AppDto.cpu` is a string like `"2.3%"`; `app.memory` is `"128M / 256M"`.
Parse both into numbers.

**Files:**
- Create `web/src/components/CompactResourceRow.tsx` — props `{ label,
  used, total?, percent, throttled? }`.
- Modify `web/src/pages/Dashboard.tsx` — compute aggregate values from
  `namespace.apps`, render two rows after `StatusBadge` block (around
  line 364, before `dockerError` banner).

**Verification:**
- Web build clean.
- Manual: open Dashboard, observe two compact bars with sensible values.

---

### 2. Status hex colors (Kotlin parity) 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.2 + `AppRuntimeStatus.kt`.

Exact colors per status category:

| Category | Hex |
|---|---|
| Running | `#33AB50` |
| Stopping / Starting (transient yellow) | `#F4E909` |
| Stalled (`PULL_FAILED` / `START_FAILED` / `STOPPING_FAILED`) | `#DB831D` |
| Stopped | `#424242` |

**Files:**
- `web/src/components/StatusBadge.tsx` — replace tailwind tokens
  (`bg-destructive`, `bg-success`, etc.) with explicit `style={{...}}`
  using the hex values above. Keep the dot pattern.

**Verification:** all 38 web tests pass; visual check matches Kotlin
screenshots.

---

### 3. COG count badge for edited volume files 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.4 (COG decorators).

When an app has > 0 user-edited mounted files, show a small count badge
at bottom-right of the COG icon (in addition to the existing blue dot for
edited ApplicationDef).

**Data source:** there is no per-app "edited file count" in `AppDto` yet.
Add to the daemon:

- New field `AppDto.editedFilesCount int` in `internal/api/dto.go`.
- In `internal/namespace/runtime_dto.go` `appToDto`, count entries in the
  `changedRtFiles` repo whose key starts with `<app>/`.
- Bump version of `web/src/lib/types.ts` `AppDto` interface.

**Files:**
- `internal/api/dto.go`
- `internal/namespace/runtime_dto.go`
- `web/src/lib/types.ts`
- `web/src/components/AppTable.tsx` — render `<span className="absolute
  -bottom-0.5 -right-0.5 text-[9px] ...">{count}</span>` when
  `app.editedFilesCount > 0`.

**Verification:** `go test ./internal/...` passes, web build clean.

---

### 4. "Edited" marker in file list (AppConfigEditor) 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.4 (`COG RMB context menu` → "blue vertical
bar if edited").

In `AppConfigEditor.tsx` mounted-files section, prepend a blue vertical
bar (e.g. `<span className="inline-block w-0.5 h-3 bg-primary mr-1.5"/>`)
to file names that have been user-edited.

**Data source:** call `GET /api/v1/apps/{app}/files` already returns the
list; we need an "edited" flag per file. Backend already tracks per-file
edits via `changedRtFiles`. Extend the file list response or add a new
endpoint:

- Option A: change `handleListAppFiles` to return `[]FileInfoDto{Path,
  Edited bool}` instead of `[]string`. Breaking change — also fix the
  RMB context menu in `AppTable.tsx` that uses the same endpoint.
- Option B: add `GET /api/v1/apps/{name}/files-info` returning the
  enriched list and keep the simple list endpoint as-is.

Recommended: **Option A** — keep the API surface small. The RMB menu
needs the same enriched info anyway to decide which files to show in
bold etc.

**Files:**
- `internal/api/dto.go` — new `AppFileDto` struct.
- `internal/daemon/routes_apps.go` — `handleListAppFiles` returns
  `[]AppFileDto`.
- `web/src/lib/api.ts` — `getAppFiles` returns `AppFileDto[]`.
- `web/src/components/AppConfigEditor.tsx` — render edited marker.
- `web/src/components/AppTable.tsx` — `buildCogMenu` uses the new shape.

**Verification:** `go test ./internal/...`, web build, web tests.

---

### 5. Per-file Reset button in AppConfigEditor 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/03` §2 (`AppCfgEditWindow` — Reset semantics).

The current AppConfigEditor has Reset on the YAML config but NOT on
individual mounted files. Kotlin uses the same Reset path
(`nsRuntime.resetEditedFile(path)`).

Backend likely already supports this — check
`internal/daemon/routes_apps.go` for `handlePutAppFile` and friends; add
`POST /api/v1/apps/{name}/files/reset?path=...` if missing.

**Files:**
- `internal/daemon/routes_apps.go` — new `handleResetAppFile`.
- `internal/daemon/server.go` — register the route.
- `internal/namespace/runtime_commands.go` — `Runtime.ResetEditedFile(app,
  path)` removes the entry from `changedRtFiles` + triggers regenerate.
- `web/src/lib/api.ts` — `resetAppFile(name, path)` client.
- `web/src/components/AppConfigEditor.tsx` — Reset button in the per-file
  editor pane (next to Save / Cancel).

**Verification:** `go test`, manual reset of a mounted file, file revert
visible after refresh.

---

### 6. "Force Update And Start" RMB on Update&Start button 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.2 — RMB on start button shows "Force Update
And Start" menu item.

Currently `NamespaceControls.tsx` has a single Update&Start button.
Add a right-click handler that opens a one-item context menu "Force Update
And Start" which calls the same start endpoint with `?force=true`.

Backend:
- `internal/daemon/routes_ns.go` `handleStartNamespace` — accept
  `force=true` query param; pass through to
  `Runtime.UpdateAndStart(forceUpdate=true)`.
- `internal/namespace/runtime_commands.go` — `UpdateAndStart` should
  already accept `forceUpdate bool` (Kotlin parity).

**Files:**
- `internal/daemon/routes_ns.go`
- `internal/namespace/runtime_commands.go` (if not already wired)
- `web/src/components/NamespaceControls.tsx` — `onContextMenu` handler
  with `useContextMenu` hook + ContextMenu render.
- `web/src/lib/api.ts` — `postNamespaceStart(force?: boolean)`.

**Verification:** smoke test that force-update triggers a fresh git
pull (check daemon logs for "Pulling repository").

---

### 7. Open in Browser tooltip with admin/admin hint 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.2.

The "Open In Browser" button in Dashboard sidebar currently has
`title={t('dashboard.openInBrowser.tooltip')}`. The translation key
already exists but the content is shorter than Kotlin. Update the
English value to:

```
Open Citeck in your browser.
 Default username: admin
 Default password: admin
```

(Note the leading space on lines 2-3 — Kotlin parity.)

Repeat for the disabled / starting / stopping / stalled states with the
Kotlin strings from `docs/porting/02` §7.2.

**Files:**
- `web/src/locales/en.ts` — update `dashboard.openInBrowser.tooltip`.
- `web/src/locales/ru.ts` — same translation.
- 6 other locales — mirror English (use the python3 inline pattern in
  prior commits).
- `web/src/pages/Dashboard.tsx` — surface different tooltips per status
  (running / starting / stopping / stopped / stalled).

**Verification:** locales test passes.

---

### 8. Quick start variant creates namespace directly (no wizard detour) 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §4.3 case B.

Today the Welcome screen's quick-start buttons all route to `/wizard`.
Kotlin calls `workspaceServices.entitiesService.createWithData(namespaceConfig)`
directly and then starts the namespace.

For the web port: build a `NamespaceCreateDto` from the QuickStart variant
and POST to `/api/v1/namespaces`, then navigate to `/`.

**Data needed from `QuickStartDto`:** the existing DTO has only `{name,
template, snapshot?}`. Kotlin's variant carries a full pre-filled
`NamespaceConfig`. Decide:

- **Cheap path:** treat each variant as a template id. Daemon already
  exposes `/api/v1/templates` returning the resolved namespace config.
  Map variant.template → template body → POST as create.
- **Proper path:** add `QuickStartDto.namespaceConfig` field — daemon
  serializes the workspace-config variant as a `NamespaceConfig` blob.
  Frontend POSTs that blob.

Recommended: **cheap path** since `QuickStartDto.template` already exists.

**Files:**
- `internal/daemon/routes_*.go` — likely no change if `/templates`
  already returns sufficient data.
- `web/src/pages/Welcome.tsx` — replace `handleCreateNew` on quickstart
  buttons with a `handleQuickStart(variant)` that:
  1. Fetches template by `variant.template`.
  2. Calls `createNamespace({...templateBody, name: variant.name})`.
  3. Calls `fetchData()` + `startEventStream()`.
  4. `navigate('/')`.

**Verification:** click a quickstart variant — namespace created and
dashboard opens. Manual smoke test.

---

### 9. FAST_ACCESS_NAMESPACES_LIMIT = 3 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §4.3 case C.

Welcome shows up to 3 namespace cards. If there are more, the remainder
is accessible via the existing "More" button (already opens
`NamespaceDialog`).

**Files:**
- `web/src/pages/Welcome.tsx` — slice the `namespaces` map to first 3:
  `namespaces.slice(0, 3).map(...)`. The "More" button visibility
  condition already accepts `namespaces.length > 3`.

**Verification:** seed >3 namespaces (or mock in test), confirm only 3
shown + More button.

---

### 10. Locked indicator in main app table 🟢 DONE (2026-05-27)

**Spec:** `docs/porting/02` §7.4 — `editedAndLockedApps` survive bundle
updates. Today the lock icon is only visible inside `AppConfigEditor`
(when `appMeta.locked === true`). The Kotlin spec doesn't actually
require a dedicated row marker, but for parity with the COG blue-dot
pattern: add a small lock icon next to the blue dot when
`app.locked && app.edited`.

**Files:**
- `web/src/components/AppTable.tsx` — render a `<Lock size={6}/>` next
  to the existing `<Circle/>` when `app.locked`.

**Verification:** lock an app via `putAppLock(name, true)`, observe the
icon in the row.

---

### 11. Multi-workspace support for desktop mode

**Status (2026-05-27):** first slice landed — CRUD + activate backend + Welcome
dropdown picker + namespace filter. Not yet done: auto-loading a namespace
after switch, wizard pre-filling active workspace, dashboard sidebar switcher.
See "What landed" / "Still missing" below.

**Spec:** `docs/porting/05` (Workspaces) + `docs/porting/10` §5.10 + Kotlin
`core/workspace/*.kt`, `view/screen/WelcomeScreen.kt` (workspace picker).

**Scope decision (2026-05-26):**
- **Server mode** — single-workspace, stays out-of-scope.
- **Desktop mode (Wails)** — REQUIRED for Kotlin parity.

**Current state (`internal/config/workspace.go`):** filesystem layout
`ws/{wsID}/ns/{nsID}/` is plumbed, `WorkspaceInfo` + `ListWorkspaces()` work,
`NamespaceInfo.WorkspaceID` exists. **Missing:**

- Workspace CRUD (create / delete / rename) — backend ops + persistence.
- Active workspace selection + persistence in desktop SQLite store.
- Workspace picker UI on Welcome screen (mirror Kotlin `WelcomeScreen.kt`).
- Workspace switcher in dashboard sidebar / top bar.
- Per-workspace bundle/namespace lists (currently `ListAllNamespaces`
  aggregates across all workspaces — desktop needs filtering by active ws).
- Gate workspace endpoints behind `--desktop` so server binary stays
  single-workspace.

**Files (anticipated):**
- `internal/config/workspace.go` — add `CreateWorkspace`, `DeleteWorkspace`,
  `RenameWorkspace`.
- `internal/storage/sqlitestore.go` — add `selectedWorkspace` key (mirror
  Kotlin `launcher!state`).
- `internal/daemon/routes_workspaces.go` (new) — desktop-only routes.
- `web/src/pages/Welcome.tsx` — workspace picker step before namespace list.
- `web/src/components/WorkspaceSwitcher.tsx` (new) — sidebar switcher.
- `web/src/lib/store.ts` — `activeWorkspaceId` in Zustand store.

**Verification:**
- Server binary should expose no workspace CRUD endpoints.
- Desktop: create 2 workspaces, switch between them, verify namespaces are
  scoped correctly, restart desktop — selected workspace persists.

**What landed (2026-05-27):**
- `internal/api/{dto,paths}.go` — `WorkspaceDto`, `WorkspaceCreateDto`,
  `WorkspaceUpdateDto`, `Workspaces` path, error codes.
- `internal/daemon/routes_workspaces.go` — `GET/POST/PUT/DELETE
  /api/v1/workspaces[/{id}]` and `POST /workspaces/{id}/activate`, each
  gated by `requireDesktop` returning 404 + `DESKTOP_ONLY` in server mode.
- `Daemon.SwitchWorkspace` tears down runtime + docker client, recreates the
  client for the new workspace, persists `launcher_state.workspace_id`, and
  clears all per-namespace state. Refuses to switch while a namespace is
  running (`ErrCodeNamespaceRunning`).
- `internal/storage/sqlitestore.go` — `GetWorkspace` now returns `(nil, nil)`
  on miss to match FileStore semantics (previously returned a synthetic
  error, which made handler "not found" branches unreachable).
- `internal/daemon/routes_workspaces_test.go` — 4 test suites covering mode
  gating, full CRUD happy path, validation errors, and the active-workspace
  delete refusal.
- `web/src/lib/api.ts` + `types.ts` — client + DTOs.
- `web/src/components/WorkspaceSelector.tsx` — dropdown selector + create /
  edit / delete dialog. Hidden in server mode (auto-collapses to nothing when
  `/workspaces` returns 404).
- `web/src/pages/Welcome.tsx` — selector slotted into existing header row;
  namespaces and quick-start fallback both filter by active `workspaceId`.
- All 8 locale files updated with 12 new strings each
  (`welcome.workspace.{create,edit,delete,deleteConfirm,switched,
  switchFailed,form.*}`).

**Out of scope (matches Kotlin original — do NOT add):**
- No auto-loading of a namespace after workspace switch — Kotlin returns
  the user to Welcome on switch, and there are no controls to change
  workspace from outside Welcome, so there's no scenario where the launcher
  has to decide which namespace to open automatically.
- No workspace switcher anywhere except the Welcome screen — the original
  Kotlin launcher has no sidebar at all. The `WorkspaceSelector` component
  lives only on Welcome.

---

---

## Audit 2026-05-27 — deep parity findings

A four-agent cross-reference against Kotlin v1.3.8 found the following gaps
**beyond** items 1–11 above. Items 1–10 are landed; item 11 is partially
landed (see "Multi-workspace polish" sub-section). Items 12–25 are net-new.

Source: 4 parallel agent reports — UI (Welcome/Dashboard/dialogs/forms),
core services (workspaces/secrets/license/git/migration/cloud-config),
runtime+docker (state machine, generator, appfiles), and lifecycle
(entry point, tray, single-instance, system-dump). See git log around
2026-05-27 for the session that generated this list.

### Multi-workspace polish — extends item 11

These were discovered when auditing iteration 1 of multi-workspace:

**11a. `resolver.resolveWorkspace()` ignores per-workspace `RepoURL` / `Branch` / `PullPeriod`. 🟢 DONE (2026-05-27)**
- Kotlin source: `core/workspace/WorkspacesService.kt:80-125` (`loadWorkspaceConfig` uses workspace.repoUrl/repoBranch/repoPullPeriod).
- Go location: `internal/bundle/resolver.go:262-273` always uses hardcoded `defaultBundlesRepo`.
- Effect: secondary workspaces with custom repos are **unreachable** during normal startup / wizard flow. Only the Force Update path (`routes_workspace.go:resolveActiveRepoOpts`) uses per-workspace values.
- Fix: `Resolver` needs to accept the active workspace's metadata (or look it up via the store) and plumb URL/branch/pullPeriod into `git.RepoOpts`.

**11b. H2 migration drops workspace `authType` and `repoPullPeriod`. 🟢 DONE (2026-05-27)**
- Kotlin source: `core/workspace/WorkspaceDto.kt:8-15` has both fields.
- Fix landed: `internal/h2migrate/migrate.go::parseWorkspaceJSON` now reads
  `authType` and `repoPullPeriod` (Jackson DurationSerializer ISO-8601 string,
  with integer-seconds fallback for legacy hand-edits). The unified
  import pipeline in `internal/h2migrate/imports.go::importWorkspaces` shares
  the same parser. SQLite migration v2 already accepts the columns.
- Tests: `TestParseWorkspaceJSON_AuthAndPullPeriod`,
  `TestParseWorkspaceJSON_PullPeriodAsSeconds`,
  `TestParseWorkspaceJSON_OmittedFieldsDefaultEmpty`,
  `TestImports_RoundTripsAuthAndPullPeriod`.

**11c. `LauncherState.NamespaceID` is global, not per-workspace. 🟢 DONE (2026-05-27)**
- Kotlin source: `core/WorkspaceServices.kt:60-86` (`workspaceStateRepo[SELECTED_NS_PROP]` scoped to `workspace-state/{wsId}` map).
- Fix landed:
  - `LauncherState` is now `{WorkspaceID string, SelectedNs map[string]string}` with a `NamespaceID()` accessor for the active workspace's selection (`internal/storage/store.go`).
  - SQLite migration v4 folds the legacy global `namespace_id` row into a `selected_ns` JSON keyed by `workspace_id`, then drops the old row.
  - FileStore reads legacy `namespaceId` JSON and folds it into `SelectedNs[WorkspaceID]` on first read; next write upgrades the on-disk shape.
  - `Daemon.SwitchWorkspace` now records the outgoing workspace's selection in `SelectedNs[oldWsID]` instead of clearing — re-activating restores it.
- Tests: `TestSQLiteMigrationV4_FoldsLegacyNamespaceID`, `TestSwitchWorkspaceRoundTrip`
  (`internal/storage/state_migration_test.go`) and the updated `testStoreState`
  which now asserts ws1's selection survives a switch to ws2.

**11d. Second-instance desktop launch shows error instead of focusing existing window.** 🟢 DONE (2026-05-27)
- Kotlin source: `core/utils/AppLock.kt:21-37` + `core/socket/AppLocalSocket.kt` — second launch sends `TakeFocus` over loopback TCP; first instance brings its window to the foreground.
- Fix landed:
  - `POST /desktop/focus` route registered on the daemon mux only when `config.IsDesktopMode()` (`internal/daemon/server.go` + `internal/daemon/desktop_focus.go`).
  - Process-global `daemon.SetDesktopFocusHandler(func())` lets `cmd/citeck-desktop/main.go` register a Wails `window.Show()`+`Focus()` callback (wrapped in `application.InvokeAsync` to stay on the UI thread) after Wails is wired.
  - `internal/desktop/focus.go::NotifyExistingInstance` dials the daemon Unix socket with a 2 s timeout and POSTs `/desktop/focus`.
  - `instance_unix.go` / `instance_windows.go`: on lock conflict, call `NotifyExistingInstance` first; on success `os.Exit(0)`, on failure fall through to the original error (preserves stale-lock recovery).
- Tests: `internal/desktop/focus_test.go` (stub Unix socket — success, error status, no-daemon paths) + `internal/daemon/desktop_focus_test.go` (handler invokes callback, 503 when none registered).

---

### 12. Volume namespace isolation broken in desktop mode 🟢 DONE (2026-05-27)

- Kotlin source: `core/namespace/runtime/actions/AppStartAction.kt:799-834` (`prepareVolume` → `createVolumeIfNotExists` → `DockerConstants.getVolumeName` → docker named volume with `LabelOrigName`).
- Go location: `internal/docker/client.go:240-247` — desktop-mode plain-named volume reference goes to Docker as-is.
- Effect: a plain volume `mongo2:/data/db` in two namespaces ends up as **one shared Docker volume** → data corruption. `LabelOrigName` is never written, so the Kotlin volume-lookup pattern (find volume by original name + namespace label) doesn't work.
- **Direct violation of byte-exact contract from `docs/porting/10` §6.**
- Fix: in `internal/docker/client.go` add `CreateVolume(originalName) → citeck_volume_{orig}_{ns}_{ws}` and `GetVolumeByOriginalName` helpers (mirroring Kotlin `DockerApi`); update generator/runtime to use them for desktop mode.

### 13. Volumes UI page is empty in desktop mode 🟢 DONE (2026-05-27)

- Kotlin source: `core/namespace/volume/VolumesRepo.kt` + `DockerApi.getVolumes(nsRef)` + `getVolumesSize()` (via `/system/df?type=volume`).
- Go location: `internal/daemon/routes_volumes.go::handleListVolumes` only reads the filesystem `volumesDir` (server-mode bind-mount layout).
- Effect: in desktop mode the Volumes UI is always empty even when Docker named volumes exist.
- Fix: when `config.IsDesktopMode()`, list via `cli.VolumeList` with label filter `LabelNamespace`+`LabelWorkspace`, size via Docker `/system/df`. Delete via Docker volume remove.

### 14. License canonical date serialization mismatch 🟢 DONE (2026-05-27)

- Kotlin source: `core/license/LicenseInstance.kt:153-160` (`LicenseDateSerializer` strips `T00:00:00Z` for midnight-UTC dates).
- Go location: `internal/license/license.go:30-32` uses `time.Time` with default `MarshalJSON` → always full RFC3339.
- Effect: any Kotlin-signed license whose `issuedAt`/`validFrom`/`validUntil` lands at midnight UTC fails signature verification under Go.
- Fix: introduce a JSON marshaler that emits `"2025-01-01"` for midnight UTC dates and full RFC3339 otherwise. Add a fixture test against a known-good Kotlin-produced canonical blob.

### 15. BASIC_AUTH secret stored as `username:password` 🟢 DONE (2026-05-27)

- Kotlin source: `core/secrets/auth/AuthSecret.kt:34-44` — typed `Basic(username, password)`.
- Fix landed:
  - `Secret.Username` field added (`storage/store.go`), JSON tag `username,omitempty`.
  - SQLite migration v3 (`ALTER TABLE secrets ADD COLUMN username TEXT NOT NULL DEFAULT ''`)
    plus a one-shot in-place split of legacy `BASIC_AUTH` / `REGISTRY_AUTH` rows; base64
    ciphertext is colon-free so the split never touches encrypted values, and
    `GIT_TOKEN` is excluded (PATs can contain ':').
  - `FileStore.readSecret` splits legacy on-disk packed values once on load (same
    type filter, encrypted base64 immune).
  - `h2migrate/decrypt.go` writes `Username` + `Value` as typed fields.
  - `internal/daemon/server.go::buildRegistryAuthCache` reads `sec.Username` / `sec.Value`
    directly; legacy "user:pass" packed values still split as last-resort fallback.
  - `internal/cli/registry_auth.go` + `saveRegistrySecret` (install.go) use typed fields.
  - `SecretCreateDto` gained `username` (optional); `SecretsDialog` renders a Username
    input when type is BASIC_AUTH or REGISTRY_AUTH.
- Tests: `internal/storage/secret_legacy_test.go` (SQLite v3 migration + FileStore
  legacy split), `internal/daemon/registry_auth_cache_test.go` (registry auth cache
  with typed Basic + colon-in-password), `internal/h2migrate/decrypt_test.go`
  (`TestImportDecryptedSecrets_BasicPasswordWithColon`).

### 16. Detached Alfresco still set as `proxyTarget` 🟢 DONE (2026-05-27)

- Kotlin source: `NamespaceGenerator.kt:329-340` — proxy target = `alfresco:8080` only when alfresco is **not** in `context.detachedApps`.
- Go location: `internal/namespace/generator.go:924` checks `ctx.Applications[AppAlfresco] != nil` but not `r.manualStoppedApps[AppAlfresco]`.
- Effect: `citeck stop alfresco` leaves proxy pointing at a dead container → 502 on the UI.
- Fix: in `alfrescoEnabled` calculation, exclude apps listed in `manualStoppedApps`.

### 17. `RETRIES_COUNT_FOR_EXISTING_IMAGE=3` fallback for offline pull 🟢 DONE (already in HEAD — REMAINING.md was stale)

- Kotlin source: `core/namespace/runtime/actions/AppImagePullAction.kt:73-95` — if pull fails but image exists locally, after 3 retries treat as success.
- Go location: `internal/namespace/nsactions/` or `runtime_workers.go` — not confirmed present.
- Effect: offline / disconnected-registry scenario gets stuck in `PULL_FAILED` retry loop instead of using the local image. Common dev workflow.
- Fix: in the pull worker, after N retries check `dockerClient.ImageExists(ref)` and short-circuit success if true.

### 18. System dump missing goroutine dump 🟢 DONE (2026-05-27)

- Kotlin source: `view/utils/SystemDumpUtils.kt:140-172` writes `thread-dump.txt` via `ThreadMXBean`.
- Fix landed: `internal/daemon/routes_system.go::writeSystemDumpZip` adds a
  `goroutine-dump.txt` entry written via `pprof.Lookup("goroutine").WriteTo(fw, 2)`.
  `debug=2` is the closest pprof analogue to Kotlin's ThreadMXBean output —
  it emits per-goroutine state, locks held, and full frames (richer than the
  flat `runtime.Stack(buf, true)`).
- Tests: `TestWriteSystemDumpZip_IncludesGoroutineDump`
  (`internal/daemon/routes_system_test.go`).

### 19. System-dump UX silent in desktop tray 🟢 DONE (2026-05-27)

- Kotlin source: `SystemDumpUtils.kt:88` opens containing folder; `:40-47` wraps in `LoadingDialog`.
- Fix landed:
  - `cmd/citeck-desktop/app.go::dumpSystemInfo` now returns `(zipPath, error)`
    instead of swallowing failures; the timeout went from 30 s to 5 min to
    match `writeSystemDumpZip`'s server-side write deadline.
  - `cmd/citeck-desktop/main.go` tray handler runs the dump on a goroutine
    and on completion:
    - opens the containing folder via `desktop.OpenBrowser("file://" + filepath.Dir(zipPath))`
      (existing helper already wraps `xdg-open`/`open`/`rundll32`),
    - shows a Wails `app.Dialog.Info()` with the on-disk path (success) or
      `app.Dialog.Error()` (failure),
    - flips an `atomic.Bool` + `SetEnabled(false)`/`SetLabel("...running...")`
      so a second click while the first dump is in flight is a no-op (matches
      Kotlin's LoadingDialog modal semantics).
  - All Wails UI mutations are wrapped in `application.InvokeAsync` per the
    GTK-thread rule already documented for the focus handler.

### 20. Welcome context-menu missing "Edit" 🟢 DONE (2026-05-27)

- Kotlin source: `WelcomeScreen.kt` per-namespace context menu has Edit / Delete entries.
- Go location: `web/src/pages/Welcome.tsx:179-184` (`nsContextItems`) — only Open / Delete.
- Effect: cannot edit a namespace from Welcome — must open it first then go to Dashboard.
- Fix: add Edit entry to `nsContextItems` that opens `NamespaceEditDialog` directly.

### 21. RMB "Force Update And Start" on Update&Start button 🟢 DONE (already in HEAD — REMAINING.md was stale; same as item #6)

- Kotlin source: `NamespaceScreen.kt:176-187` — `forceUpdate=true` start path.
- Go location: `web/src/components/NamespaceControls.tsx` — no `onContextMenu`. Already item #6 in the original list above.
- Backend `force=true` query param: confirmed exists.

### 22. Unified ErrorDialog with "Export System Info" button 🟢 DONE (2026-05-27)

- Kotlin source: `view/commons/dialog/ErrorDialog.kt:105-113` — every error has a quick dump button.
- Go location: errors currently surface via `toast()` / inline `setError()`. Dump only reachable from Dashboard sidebar / tray.
- Fix: introduce `ErrorModal` component; wrap unhandled action errors; include "Download System Dump" CTA.

### 23. SnapshotsDialog missing "Open NS Directory" button 🟢 DONE (2026-05-27)

- Kotlin source: `SnapshotsDialog.kt:101-104` — third footer button.
- Go location: `web/src/components/SnapshotsDialog.tsx` — only Create / Import.
- Fix: add a third button that POSTs `/api/v1/system/open-dir { kind: "volumes" }`.

### 24. `exec` prefix missing in init action shell command 🟢 DONE (2026-05-27)

- Kotlin source: `AppStartAction.kt` — `withCmd("/bin/sh", "-c", "exec " + command)`.
- Go location: `internal/namespace/generator.go:454` — `Exec: []string{"sh", "-c", "..."}` (no `exec`).
- Effect: init process is a child shell rather than replacing it; PID and signaling differ. Low practical impact today but contract diverges.
- Fix: prepend `"exec "` to the command.

### 25. `AppInitAction.Trigger` field exists in Go but not Kotlin 🟢 DONE (2026-05-27)

- Go location: `internal/appdef/appdef.go:72`.
- Effect: dead field in JSON wire format if not consumed; if consumed, it is a 2.x-only extension that should be documented.
- Fix: grep for `Trigger` usage; remove if dead, document in CLAUDE.md if alive.

### 26. CloudConfig nested map flattening — DONE

- Kotlin source: `core/config/cloud/CloudConfigImpl.kt` flattens nested maps to Spring dot-notation (`spring.datasource.url`).
- Go location: `internal/daemon/cloudconfig.go` now applies `flattenCloudConfig` inside `UpdateConfig`, mirroring Kotlin's `buildFlattenedMap` (depth-first, `.`-joined keys, `[idx]` bracket notation for lists, empty list → `""`, non-string scalars passed through).
- Coverage: `internal/daemon/cloudconfig_test.go` exercises nested maps, already-flat input (no-op), bracket-indexed lists, empty lists, list-of-maps, non-string scalars, and end-to-end HTTP serving.

### Doubtful — verify manually

The 2026-05-27 verification pass closed four of the six items below; the two
remaining ones are platform-specific and require macOS / GNOME / KDE access.

- **Volume content-hash recompute on file edit. 🟢 DONE (2026-05-27)** Gap was
  real: `Generate()` hashed against embedded defaults from
  `appfiles.GetFiles()`, so UI edits never bumped `VolumesContentHash` and the
  state machine kept the stale container. Fix adds `GenerateOpts.EditedFileOverlay`,
  populated by `Runtime.EditedFileOverlay(volumesBase)` on reload and by
  `readEditedFileOverlay()` at daemon startup (from persisted state).
  `SetFileEdited(edited=true)` now also enqueues `cmdRegenerate` so the
  edit takes effect without a manual reload — matches Kotlin
  `NamespaceRuntime.pushEditedFile`. Tests:
  `TestGenerate_EditedFileOverlayChangesHash`,
  `TestRuntime_EditedFileOverlay_ReadsDiskContent`,
  `TestRuntime_EditedFileOverlay_EmptyWhenNoEdits`.
- **eapps init-container memory cap.** No gap.
  `internal/namespace/runtime_loop.go:246-253` (`buildInitContainerDef`) sets
  `Resources.Limits.Memory = "100m"` on every init container; both
  call-sites (`beginStartingUnderLock` and the chained T11 step in
  `handleInitResult`) go through this builder.
- **Pull-stuck watchdog.** No gap. `internal/namespace/runtime_workers.go:104-233`
  (`runPullTask`) wraps `PullImageWithProgress` in a per-attempt watchdog with
  `pullStallTimeout=5m` / `pullStallPollInterval=30s`; on no-progress the
  watchdog cancels `stallCtx`, the loop reports `"no progress for 5m0s"`, and
  retries (or falls back to a locally-present image after
  `PullRetriesForExistingImage`). Covered by `runtime_phase8_test.go`.
- **macOS Retina tray icon scaling — CANNOT VERIFY HERE (needs Mac).**
  Current state: `cmd/citeck-desktop/main.go:238-245` calls
  `tray.SetTemplateIcon(citeckLogo)` on darwin with the embedded
  `cmd/citeck-desktop/logo.png` (256×256, 8-bit RGBA). Kotlin
  (`view/tray/CiteckSystemTray.kt:75-89`) rasterizes `logo.svg` at
  `max(SystemTray.trayIconSize.width, 64)` and adds a transparent border of
  `size/8` for visual padding under macOS Retina scaling. Hand-off: on a Mac,
  capture a 2× / 3× tray screenshot; if the icon clips or looks pixelated,
  either pre-bake a 2× / 3× PNG into the embed or generate from a vector
  source at runtime (Wails `application.NewIconFromBytes` accepts
  size-specific PNGs).
- **GTK tray fallback (`AppIndicator`) — CANNOT VERIFY HERE (needs GNOME/KDE
  shell test).** Wails v3 alpha.95 uses libappindicator under the hood on
  Linux; GNOME Shell since 41 dropped legacy tray support entirely so users
  need `gnome-shell-extension-appindicator`. There is no fallback in
  `cmd/citeck-desktop/main.go:238` — `app.SystemTray.New()` returns the same
  object on every platform. Hand-off: on a GNOME 45+ box without the
  AppIndicator extension installed, confirm whether the tray menu still
  renders (Wails may have its own fallback) or whether we need to fall back
  to a hidden window + keyboard shortcut path.
- **Windows single-instance fallback.** No gap.
  `internal/desktop/instance_windows.go` uses a named kernel Mutex
  (`Global\CiteckLauncher`) which the OS releases on process death, so stale-
  PID is structurally impossible (unlike a file-based lock). The
  `NotifyExistingInstance` focus hand-off (item 11d) is wired identically to
  the Unix path: on `ERROR_ALREADY_EXISTS` we POST `/desktop/focus` to the
  daemon Unix socket, fall back to the original error only on dial failure.
  Covered by `internal/desktop/focus_test.go`.

---

## Out of scope (do NOT do)

- **Multi-workspace in server mode** — single daemon serves one workspace.
  Desktop is different (see item 11).
- **Tray on macOS / Windows** — Wails handles this automatically.
- **`Compose Desktop` window state persistence** (size/pos) — Wails-side
  concern, not Kotlin parity.
- **Generic Entity Framework** (`EntitiesService` / `EntityDef`) — 5 entity
  types in Go (workspace/namespace/secret/license/snapshot), each with
  hardcoded routes + UI. Migration to a registry costs ~1500-2000 LOC
  and only pays off at ≥8 entity types. Not on the parity path.
- **ActionsService thread-pool with `getRetryAfterErrorDelay`** — replaced
  by state machine + reconciler + dispatcher. Functionally equivalent;
  observability gap (no "show me in-flight actions") tracked separately
  if it becomes a real diagnostic blocker.
- **System SVG icon catalog (20+)** — using Lucide for system icons,
  9 app-specific SVGs in `/web/public/icons/`. Cosmetic.
- **`CiteckSelect` branded component** — native `<select>` + Lucide
  affordances. Cosmetic.

## Cross-cutting verification before declaring done

```bash
cd /home/spk/IdeaProjects/citeck-launcher2
go test -tags gtk3 ./internal/...    # all green
cd web && pnpm test --run              # 38/38 green
cd web && pnpm run build               # clean
cd .. && make build-desktop            # produces ~25 MB binary
cd .. && make build                    # produces ~24 MB binary
```

If a sub-agent is dispatched, brief it with:
- The specific section above (1–10).
- The reminder that locales need a Python inline script (see prior
  commits): the `re.sub` replacement string MUST use a `lambda m: m.group(1)
  + block` form, otherwise backslash sequences in `block` are
  re-interpreted as regex backreferences and the locale files end up
  with raw newlines instead of `\n` escapes.
- `cd web && pnpm test --run` always before / after locale changes
  because the `locales.test.ts` validates key parity across all 8
  locales.

## Files modified during this session

For quick orientation when continuing:

- New: `internal/license/` (Instance, Signature, Service), Web
  Licenses page, `internal/desktop/wailswin/`, `web/src/lib/desktop.ts`,
  `web/src/pages/Window{Logs,Editor}.tsx`,
  `web/src/components/{VolumesDialog,SnapshotsDialog,SecretsDialog,
  NamespaceDialog,MasterPasswordDialog,LoadingHint,CodeEditor,
  DockerNotAvailable,GitPullErrorDialog,RegistryCredentialsDialog}.tsx`.
- New backend routes: `/api/v1/licenses` (CRUD), `/api/v1/secrets/reset`,
  `/api/v1/apps/{name}/config/reset`, `/api/v1/snapshots/{name}` DELETE,
  `/desktop/windows/*` (Wails-only).
- Modified: `cmd/citeck-desktop/main.go` (tray + WindowManager),
  `internal/namespace/runtime_loop.go` (`app_stats` SSE), `web/src/App.tsx`
  (DockerNotAvailable, /window/* routes), `web/src/pages/Dashboard.tsx`
  (sidebar dialog wiring + master-password dialog), and every locale
  file in `web/src/locales/`.
