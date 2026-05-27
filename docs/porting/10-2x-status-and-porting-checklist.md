# 10 — Состояние 2.x порта + порт-чеклист

## 1. Где сейчас 2.x

**Status after commit `6fe02d1` (2026-05-27 — "close all 1.x→2.x parity gaps
+ rewrite migration on pure-Go"):**

- Все numbered items 1–26 + multi-workspace polish 11a–11d + Doubtful A/B/C/F
  закрыты. Остаются ТОЛЬКО два platform-specific verification теста
  (macOS Retina tray + GTK tray fallback) — нужны Mac / стоковый GNOME для
  проверки. Полный список — см. `REMAINING.md`.
- Kotlin 1.x → Go 2.x миграция теперь **pure Go**: `tools/h2-export/`,
  embedded `h2-export.jar`, `jarmigrate.go` и JRE-download path удалены.
  `internal/h2migrate/mvstore.go` читает H2 MVStore напрямую. Desktop
  binary -1.1 MB. См. `docs/porting/07` §5 для деталей.
- Кодовая база Go-демона лежит в `internal/`.
- Web UI — React 19 + Vite + Tailwind v4.2.2 + Zustand, лежит в `web/`.
- На исторической `release/1.4.1` ветке `internal/` и `web/` присутствуют
  только в виде build-артефактов (embedded webdist, без исходников).

## 2. Что уже сделано — Go server (`internal/`)

### 2.1 Структура (из audit + memory)

| Package | Файлы | Состояние |
|---|---|---|
| `internal/daemon/` | `server.go` (1771 LOC), `routes_apps.go` (516 LOC), `routes_snapshots.go` (601 LOC), `routes_ns.go` (363), `routes_config.go` (404), `routes_system.go` (379), `routes_secrets.go` (322), `routes_volumes.go` (66), `routes_diagnostics.go` (134), `routes_helpers.go` (76), `routes_admin_password.go`, `webdist/` (embed) | **Complete**, zero handler-level tests |
| `internal/namespace/` | `runtime_loop.go` (1171 LOC, T1–T33 transitions), `runtime_commands.go` (301), `runtime_orchestration.go` (348), `runtime_app.go` (415), `runtime_state.go` (169), `runtime_probes.go` (160), `runtime_workers.go`, `dispatcher.go`, `generator.go` (1465 LOC), `reconciler.go`, `config.go`, `context.go` | **Complete**, full lifecycle |
| `internal/bundle/` | `resolver.go` (с Kotlin-parity test) | **Complete** |
| `internal/acme/` | `client.go` (367), `profile.go` (169) | **Implemented, undertested** |
| `internal/cli/` | `install.go`, `prompt/`, `bundlepicker/`, `setup/`, `start.go`, `status.go`, `uninstall.go` | **Complete CLI** |
| `internal/appdef/` | `appdef.go` (canonical constants) | **Complete** |
| `internal/config/` | `paths.go` (`DetectOutboundIP`, `DetectDisplayIP`) | **Complete** |
| `internal/secrets/` | `FileStore` + `SecretService` + AES-256-GCM | **Complete** |
| `internal/appfiles/` | `embedded/keycloak/init.sh.tmpl`, etc. | **Complete** |

### 2.2 HTTP API surface

Подтверждён скан compiled JS bundle + memory:

| Group | Route patterns |
|---|---|
| Namespace | `/namespace/{id}/start`, `/stop`, `/status`, `/reload`, `/upgrade` |
| Apps | `/apps/{name}/start`, `/stop`, `/recreate`, `/restart`, `/logs?follow=true&tail=N` |
| List | `/namespaces`, `/bundles` |
| Secrets | `/secrets` (list/get/set/delete) |
| Snapshots | `/snapshots` (full CRUD: 10 handlers) |
| Workspace | `/workspace` (config) |
| Live | SSE stream — 1 `EventSource` per active namespace |

### 2.3 Persistence

- **Server mode**: flat JSON/YAML files (`state-{nsID}.json`, `daemon.yml`,
  `namespace.yml`) — `FileStore` в `internal/storage/`.
- **Desktop mode**: SQLite (pure-Go `modernc.org/sqlite`) в `launcher.db`.
  Schema versions v2 (workspace `auth_type` / `repo_pull_period`), v3
  (`secrets.username`), v4 (per-workspace `selected_ns` map), v5
  (`git_repo_state` table). H2 → SQLite one-time migration через
  `internal/h2migrate/`.
- **Secrets**: encrypted via `SecretService` (AES-256-GCM, per-secret).
  Master password derives через PBKDF2-HMAC-SHA256 (1M iterations,
  16-byte salt) — совместимо с Kotlin `SecretsEncryptor`.

### 2.4 Tray / desktop

- **Server mode** (default): Web UI served directly daemon'ом через embedded
  `webdist/`. Системного трея нет — браузер ИС application.
- **Desktop mode** (`--desktop` flag, hidden от server help): Wails v3
  webview hosts the same Web UI; system tray с "Open Window", "Dump System
  Info", "Quit" пунктами; single-instance lock + `/desktop/focus`
  hand-off (см. REMAINING item 11d). `cmd/citeck-desktop/main.go` входная
  точка.
- Нет отдельного Electron / tray process в Go rewrite.

## 3. Что уже сделано — Web (`web/`)

### 3.1 Stack

- **React 19** (`react`, `React` в bundle; `@types/react` в node_modules)
- **Build**: Vite (hashed asset filenames; `index-{hash}.js`/`{hash}.css`)
- **State**: **Zustand** (бандл line 16; `useDashboardStore`, `usePanelStore`)
- **Design**: Tailwind v4.2.2 (`/*! tailwindcss v4.2.2 */` в CSS); ссылки на MUI (Material Icons only, не full MUI)
- **Routing**: React Router v6+ (`useNavigate`, `useLocation`, `RouterProvider`)
- **Forms**: `react-hook-form` + `yup`
- **API client**: plain `fetch` с configurable base URL. **Нет** OpenAPI generated client, tRPC, GraphQL
- **Live updates**: `EventSource` (SSE) — 1 per active namespace. **Нет** WebSocket.

### 3.2 Confirmed screens / components

- **Wizard** — multi-step: hostname → TLS (5 options) → release (community/enterprise tabs).
- **Loading screen** — transition wizard → dashboard.
- **Dashboard** (= NamespaceScreen) — main app table с health, upgrade. `useDashboardStore`. `RightDrawer` для detail panel.
- **App actions** — start/stop/recreate/restart, per-app log streaming.
- **Logs viewer** — `DaemonLogsViewer.tsx`. Streaming через `response.body.getReader()`.
- **Snapshots** — full CRUD dialog.
- **Secrets** — CRUD dialog для registry auth + system secrets.
- **Settings wizard** — in-browser equivalent `citeck setup`: hostname, TLS, email, language, resources.
- **App config dialog** — per-app (memory, JVM heap; пример валидации "Heap (1200m) is at or above the container memory limit").
- **Diagnostics**.
- **i18n** — `ru.`, `en.` (8 locales в CLI).

### 3.3 Schema / API contract

- **Нет** `openapi.yaml`, `.proto`, generated types. Hand-written fetch calls с inline URL construction.

## 4. Coverage matrix

| Kotlin 1.x feature | 2.x status | Comment |
|---|---|---|
| App entry / single-instance lock | **Done** | Socket-based replaced via Go HTTP bind / IPC lock |
| System tray (macOS/Win/Linux GTK) | **Not planned (server)** | Go daemon = headless; `--desktop` exists но tray absent |
| Desktop Compose UI | **Not applicable** | Replaced by browser UI |
| Welcome screen / setup wizard | **Done** | Multi-step wizard в browser UI |
| Namespace screen / dashboard | **Done** | React + Zustand + SSE |
| App table (status, health) | **Done** | Full dashboard |
| App start/stop/restart/recreate | **Done** | `routes_apps.go` 13 handlers |
| Per-app detach/attach | **Done** | `manualStoppedApps`; CLI + UI |
| Per-app log streaming | **Done** | `GET /apps/{name}/logs?follow=true` + reader |
| Namespace start/stop/reload/upgrade | **Done** | `routes_ns.go`; bundle upgrade в wizard |
| Snapshots (CRUD/import/export) | **Done** | 10 handlers + UI |
| Secrets / registry auth | **Done** | `routes_secrets.go` + encrypted FileStore + UI |
| Workspace management | **Done** | Server: single-workspace by design ✓. Desktop: full CRUD via `/api/v1/workspaces` (gated by `requireDesktop`), `WorkspaceSelector` UI on Welcome, per-workspace `SelectedNs` map, second-instance focus hand-off (`/desktop/focus`); see REMAINING items 11, 11a–11d |
| Bundle versioning (BundleKey) | **Done** | `compareBundleVersions` + parity test |
| Bundle picker UI | **Done** | Community/enterprise tabs |
| Docker runtime | **Done** | `runtime_orchestration.go` + docker client |
| NamespaceGenerator (~24 apps) | **Done** | `generator.go` 1465 LOC |
| Init actions (post-start scripts) | **Done** | `AppInitAction` / `init.sh.tmpl` |
| HTTP startup probes | **Done** | `runtime_probes.go`; hits `127.0.0.1:hostPort` |
| Liveness probes / auto-restart | **Done** | `reconciler.go` |
| **License validation** | **Done** | `internal/license/` (Instance, Signature, Service) ported; `LicenseTime` wrapper emits midnight-UTC dates as `YYYY-MM-DD` for Kotlin canonical-form parity; CRUD via `/api/v1/licenses` (`routes_licenses.go`) |
| Git pull (bundle repo) | **Done** | "Pulling repository..." в wizard output |
| TLS / ACME / Let's Encrypt | **Done** | `internal/acme/client.go` + 5 TLS modes |
| Systemd service install | **Done** | Auto в `install.go` wizard |
| H2 database | **Migrated** | Pure-Go reader (`internal/h2migrate/`); writes go to SQLite (desktop) / flat files (server). One-time H2 → SQLite migration, `storage.db` opened read-only, atomic `.kotlin-bak` backup for rollback |
| Entity framework | **Not ported (out of scope)** | 5 entity types → direct config structs + hardcoded routes; migration к registry pays off только начиная с ≥8 types (см. §7) |
| CloudConfigServer (8761) | **Done** | `internal/daemon/cloudconfig.go`; `flattenCloudConfig` mirrors Kotlin `buildFlattenedMap` (depth-first `.`-joined keys + `[idx]` bracket notation for lists). Skipped в server mode (webapps disable via env vars). Coverage: `cloudconfig_test.go` |
| CPU/memory stats per container | **Done** | `app_stats` SSE event stream через `runtime_loop.go`; UI renders в `StatsCell` + sidebar `CompactResourceRow` |
| Spring properties / file editor (`AppCfgEditWindow`) | **Done** | `AppConfigEditor.tsx` (per-app YAML config + mounted files editor с CodeMirror highlighting + per-file Reset + edited markers); backend `routes_apps.go` файловые endpoints |
| Keycloak SA / admin password mgmt | **Done** | `routes_admin_password.go`, `_launcher_sa`, `init.sh.tmpl` |
| Observer integration | **Done (partial)** | ZK works; gateway 403 unresolved |
| STT sidecar (AI WS proxy) | **Done** | Generator + `stt_models` volume |
| i18n / multi-locale | **Done** | 8 locales в CLI; ru/en в web |
| Docker availability check | **Done** | Pre-launch в `start.go` |

## 5. Gap analysis — top 10 для спецификации

### 5.1 Handler-level test coverage (highest priority)

`routes_apps.go` (13 handlers), `routes_snapshots.go` (10), все шесть `routes_{ns,volumes,config,diagnostics,system,helpers}.go` — **zero** unit/integration tests (`.audit-backlog.md:28-31`). Behavior проверен только live e2e. Спецификация должна определить:
- Expected request/response contracts (с примерами)
- Error cases + status codes
- Edge conditions

Это **самая большая** untested surface в server'е.

### 5.2 ACME/TLS testing strategy

`internal/acme/client.go` (367 LOC) + `profile.go` (169) — zero tests. Спецификация:
- RFC8555 golden JSON fixtures с fixed nonces
- Staging vs production mode separation
- Rate-limit gate invariant: `ObtainCertificate` gated на `IsRateLimited` каждый Start + reload (не только во время active renewal)

### 5.3 `server.go` god-object split

1771 LOC + 650 LOC `Start()` method. Backlog defers как "design choice". Спецификация:
- Partition: storage+migration / bundle-resolve / TLS+ACME / HTTP-listener phases
- Сохранить nested `defer` cleanup chain + `ReadyCh` signalling integrity при extraction

### 5.4 `dispatcher.go` parentCtx gap

`parentCtx = context.Background()` → `doDetach`'s 5s poll может оставить workers permanently stuck на full 128-cap `resultCh`. Спецификация определит fix (cancelable `parentCtx` → dispatcher → Shutdown cancel) + goroutine-lifetime contract.

### 5.5 `generator.go` partitioning

1465 LOC god-object. Спецификация:
- Per-app-generator interface (shared `NsGenContext` vs per-app isolated state)
- Какие apps можно extract'нуть без breaking dependency graph computations spanning whole context

### 5.6 `flushEvents` blocking contract

SSE event delivery intentionally blocking (send в `eventCh` cap 256). Любой новый SSE subscriber / event callback **обязан** быть non-blocking — иначе wedge'ит `runtimeLoop`. Спецификация — это hard constraint.

### 5.7 License subsystem — DONE

Ported в `internal/license/` (Instance, Signature, Service). `LicenseTime`
wrapper решает midnight-UTC canonical-form parity с Kotlin
`LicenseDateSerializer` (item 14). CRUD via `/api/v1/licenses`. См.
`docs/porting/06` §5.1.

### 5.8 Container stats + config file editor — DONE

Container stats — `app_stats` SSE event stream from `runtime_loop.go`,
UI consumes в `StatsCell` (per-app) + sidebar `CompactResourceRow`
(aggregate, REMAINING item 1). Config file editor — `AppConfigEditor.tsx`
с CodeMirror highlighting, per-file Reset, edited markers, `EditedFileOverlay`
volume content hash recompute (Doubtful A).

### 5.9 Observer gateway 403

Observer integration работает на ZK registration + gateway health level, но dashboard `/gateway/observer/` returns 403 от gateway's permission filter. Спецификация:
- Gateway-side config change? Или
- Dedicated observer UI в launcher'е?

### 5.10 Multi-workspace divergence — DONE for desktop

**Решение (2026-05-26):**
- **Server mode** — single-workspace, out of scope (current state correct).
- **Desktop mode (Wails)** — multi-workspace REQUIRED, Kotlin parity достигнут
  в `6fe02d1` (REMAINING items 11, 11a–11d):
  - Workspace CRUD via `GET/POST/PUT/DELETE /api/v1/workspaces[/{id}]` +
    `POST /workspaces/{id}/activate`, все gated by `requireDesktop` (404
    в server mode).
  - `WorkspaceSelector.tsx` dropdown + create/edit/delete dialog on Welcome.
  - `Daemon.SwitchWorkspace` tears down runtime + docker client, recreates
    для нового workspace.
  - Active workspace persisted в SQLite `launcher_state.workspace_id`;
    per-workspace last namespace в `selected_ns` JSON map (v4 migration).
  - Bundle resolver honours per-workspace `RepoURL` / `Branch` /
    `PullPeriod` / `AuthType` (item 11a).
  - Second-instance focus hand-off via `POST /desktop/focus` (item 11d).

---

## 6. Чек-лист критичных вещей которые НЕЛЬЗЯ сломать

### Контракты что должны быть byte-exact compatible

| Контракт | Где | Тест |
|---|---|---|
| **DockerLabels** (все 7 keys) | `core/namespace/runtime/docker/DockerLabels.kt` | Parity test: existing v1.x containers/volumes discoverable Go runtime'ом |
| **DockerConstants** naming pattern | `core/namespace/runtime/docker/DockerConstants.kt` | Same: existing names discoverable |
| **`ApplicationDef.getHash()`** SHA-256 input | `core/appdef/ApplicationDef.kt` | Kotlin and Go produce SAME hash for same input |
| **`BundleKey.compareTo`** ordering | `core/bundle/BundleKey.kt` | `TestCompareBundleVersions_KotlinParity` (Go has it) |
| **CloudConfig response format** | `core/config/cloud/CloudConfigImpl.kt` flatten + `CloudConfigServer.kt` JSON | Spring Cloud Config client compatibility test |
| **CloudConfig port** = `8761` | `CloudConfigServer.kt` PORT constant | Если изменим — все ECOS контейнеры в running deployments сломаются |
| **AppDir layout**: `~/.citeck/launcher/storage.db`, `~/.citeck/launcher/ws/...`, `~/.citeck/launcher/app.lock` | `core/config/AppDir.kt` | Backward compat при upgrade |
| **License canonical signing form** (lexicographic key ordering) | `core/license/LicenseInstance.kt#getContentForSign` | Existing licenses still verify |
| **Init action contract**: `docker exec /bin/sh -c {cmd}` post-start | `core/namespace/runtime/actions/AppStartAction.kt` | Existing init scripts still work |
| **Volume scoping**: `citeck_volume_{name}_{ns}_{ws}` | `DockerConstants.getVolumeName` | Existing volumes discoverable |
| **`citeck.launcher.original-name` label** на volumes | `DockerLabels.kt` | Volume lookup по plain name работает |

### UX contracts что должны быть preserved (для consistency)

| UI Aspect | Где | Tradeoff |
|---|---|---|
| Status colors (green/yellow/orange/gray/red) | `NamespaceScreen.kt`, `ContainerStatViews.kt` | Иначе пользователи 1.x пугаются |
| Icon set (20+ SVG) | `resources/icons/` + `ActionIcon` enum | Reuse в Web port |
| Status names (RUNNING/STOPPED/STARTING/STALLED/...) | `AppRuntimeStatus.kt`, `NsRuntimeStatus.kt` | Должны match runtime status поскольку часть SSE payload |
| Default credentials в UI (`admin`/`admin`) | Open in Browser tooltip | Сохранить hint в UI |
| GlobalLinks (Documentation, AI Bot) | `GlobalLinks.kt` | Должны появляться в sidebar |
| Snapshot name validation regex `[\w-.]+` | `CreateOrEditSnapshotDialog.kt` | Reject invalid uploaded names |
| Master password input semantics | `AskMasterPasswordDialog.kt` | Web equivalent через server session |

---

## 7. Что НЕ нужно портировать дословно

| Aspect | Reason |
|---|---|
| **System tray** | Server-only model; web tab IS application |
| **Single-instance lock через app.lock** | Go server binds HTTP port = single-instance |
| **AWT + GTK tray** | Browser-only UI |
| **`Desktop.getDesktop().open()/.browse()`** | Browser security model; replace через `<a href>` и/или server-side action |
| **Swing-based RSyntaxTextArea** | Replace на Monaco / CodeMirror 6 |
| **OS-native file chooser (JFileChooser)** | Browser `<input type="file">` / download API |
| **Compose `weight()` layout DSL** | CSS Grid / Flexbox |
| **`SubcomposeLayout` без virtualization** | React virtual list обязательно для namespace tables |
| **`MutProp` reactive bridge** | SSE / WebSocket → Zustand store updates |
| **Coroutine scopes** | `useEffect` cleanup / `AbortController` |
| **H2 MVStore** | SQLite (или bbolt) + JSON values |
| **JGit** | go-git |
| **Compose's `Window` per dialog** | Modal / drawer / new tab |
| **`MasterPassword` через CharArray** | Server-side credentials store |

---

## 8. Read-list для каждой роли

### Developer пишет Go server

1. [09 — Docker + appfiles](09-docker-and-appfiles.md) — для DockerLabels + Pull/Start/Stop
2. [08 — Namespace runtime + generator](08-namespace-runtime-and-generator.md) — state machine + generation
3. [05 — Workspaces + bundles + cloud](05-workspaces-bundles-cloud.md) — CloudConfig contract
4. [07 — Git + DB + snapshots](07-git-database-snapshots.md) — go-git + SQLite mapping
5. [06 — Entities + secrets + license](06-entities-secrets-license.md) — generic entity framework decision
6. [01 — Architecture + lifecycle](01-architecture-and-lifecycle.md) — AppDir, init order

### Developer пишет React UI

1. [02 — UI shell + screens](02-ui-shell-and-screens.md) — все экраны
2. [03 — Dialogs + forms + editor](03-dialogs-forms-editor.md) — modals
3. [04 — Tables + logs + actions](04-tables-logs-actions.md) — data display + icon catalog
4. [01 §11 — MutProp](01-architecture-and-lifecycle.md#11-реактивность-mutprop) — для SSE mapping

### End-to-end reviewer

Этот документ (10) сначала, потом весь список в TOC.

---

## 9. Open вопросы (для product/tech-lead)

Большинство закрыты после `6fe02d1`. Открытыми остаются:

1. **Observer dashboard 403** — fix в gateway или separate UI? (Не входит
   в Kotlin parity scope.)
2. **macOS Retina tray icon scaling** — нужен Mac для verification (см.
   REMAINING Doubtful D).
3. **GTK tray fallback** на стоковом GNOME 45+ / KDE без
   `gnome-shell-extension-appindicator` — нужна тестовая машина (см.
   REMAINING Doubtful E).

### Закрытые

- **Upgrade path 1.x → 2.x** — pure-Go migrator (`internal/h2migrate/`)
  читает существующий `storage.db` read-only, пишет в SQLite (desktop) или
  flat files (server); atomic `.kotlin-bak` backup делает rollback дешёвым
  (см. `docs/porting/ROLLBACK.md`).
- **Multi-workspace** — done для desktop (§5.10), out-of-scope для server.
- **License subsystem** — ported (см. §5.7 + `docs/porting/06` §5.1).
- **CloudConfig 8761** — done с flatten parity (см. coverage matrix).
- **`AppCfgEditWindow`** — done (`AppConfigEditor.tsx` + backend endpoints).
- **Manual override locking** (`editedAndLockedApps`) — preserved через
  миграцию (`internal/h2migrate/runtimestate.go`); locked apps survive
  bundle updates (REMAINING item 10).
- **Container stats stream** — done (`app_stats` SSE).
- **`UTILS_IMAGE`** — pinned в `internal/snapshot/` + Zookeeper generator.

---

## 10. Ключевые файлы для верификации (1.x source-of-truth)

| Тема | Files |
|---|---|
| Entry/Lifecycle | `src/main/kotlin/ru/citeck/launcher/Main.kt`, `core/LauncherServices.kt`, `core/LauncherStateService.kt`, `core/utils/AppLock.kt`, `core/socket/AppLocalSocket.kt`, `core/config/AppDir.kt` |
| Tray | `view/tray/CiteckSystemTray.kt`, `view/tray/gtk/GtkTrayIndicator.kt` |
| Theme | `view/theme/LauncherTheme.kt`, `view/drawable/CpDrawable.kt` |
| Reactive | `core/utils/prop/MutProp.kt`, `view/utils/ViewExtensions.kt` |
| Screens | `view/screen/WelcomeScreen.kt`, `LoadingScreen.kt`, `NamespaceScreen.kt`, `DockerNotAvailableScreen.kt`, `AppTableColumns.kt`, `ContainerStatViews.kt` |
| Dialogs | `view/dialog/AppCfgEditWindow.kt`, `SnapshotsDialog.kt`, `CreateOrEditSnapshotDialog.kt`, `view/commons/dialog/*.kt` |
| Forms | `view/form/FormDialog.kt`, `FormContext.kt`, `spec/FormSpec.kt`, `spec/ComponentSpec.kt`, `components/select/SelectComponent.kt`, `components/journal/*.kt` |
| Editor | `view/editor/EditorWindow.kt` |
| Tables/Logs/Select | `view/table/table/DataTable.kt`, `TableDslBuilder.kt`, `view/logs/*.kt`, `view/select/CiteckSelect.kt` |
| Actions/Icons | `view/action/*.kt`, `core/actions/*.kt`, `resources/icons/*.svg` |
| Workspaces | `core/workspace/*.kt`, `core/WorkspaceServices.kt` |
| Bundles | `core/bundle/*.kt` |
| CloudConfig | `core/config/cloud/*.kt` |
| Entities | `core/entity/*.kt` |
| Secrets/License | `core/secrets/*.kt`, `core/license/*.kt` |
| Git | `core/git/*.kt` |
| Database | `core/database/*.kt` |
| Logs (core) | `core/logs/*.kt` |
| Snapshots | `core/snapshot/*.kt` |
| Namespace concepts | `core/namespace/*.kt`, `core/namespace/gen/*.kt`, `core/namespace/init/*.kt` |
| Runtime | `core/namespace/runtime/*.kt`, `core/namespace/runtime/actions/*.kt`, `core/namespace/volume/*.kt` |
| Docker | `core/namespace/runtime/docker/*.kt`, exceptions |
| AppDef | `core/appdef/*.kt` |
| Appfiles | `src/main/resources/appfiles/{alfresco,keycloak,pgadmin,postgres,proxy}/*` |
