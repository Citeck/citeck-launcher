# Remaining Kotlin-parity items (after 2026-05-26 session)

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

### 1. CompactResourceRow in sidebar (aggregate CPU / MEM bars)

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

### 2. Status hex colors (Kotlin parity)

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

### 3. COG count badge for edited volume files

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

### 4. "Edited" marker in file list (AppConfigEditor)

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

### 5. Per-file Reset button in AppConfigEditor

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

### 6. "Force Update And Start" RMB on Update&Start button

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

### 7. Open in Browser tooltip with admin/admin hint

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

### 8. Quick start variant creates namespace directly (no wizard detour)

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

### 9. FAST_ACCESS_NAMESPACES_LIMIT = 3

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

### 10. Locked indicator in main app table

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

## Out of scope (do NOT do)

- **Multi-workspace** — `docs/porting/10` §7 lists this as out-of-scope.
- **Tray on macOS / Windows** — Wails handles this automatically.
- **`Compose Desktop` window state persistence** (size/pos) — Wails-side
  concern, not Kotlin parity.

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
