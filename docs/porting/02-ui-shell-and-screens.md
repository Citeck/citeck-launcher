# 02 — UI shell и экраны

## 1. Общая модель UI

### 1.1 Три типа сюрфейсов

Всё что показывается пользователю — наследник абстрактного `CiteckPopup` (`view/popup/CiteckPopup.kt`):

- **`CiteckDialog`** — Compose `Dialog` (overlay внутри текущего окна, без OS title bar). Глобальный стек `activeDialogs: MutableList<CiteckPopup>`; рендерится через `renderDialogs()` в main-window composition loop. Контент — `Card` с `RoundedCornerShape(10dp)`, padding top=15, start=20, end=20.
- **`CiteckWindow`** — отдельное OS-окно (свой title bar, иконка). Список `activeWindows`. Может хостить свои nested-`CiteckDialog`. Используется только `EditorWindow`.
- **`PopupInWindow`** — `Popup` Compose; in-window overlay (dropdown'ы, context menu'и). Кастомный `PopupPositionProvider` клампит popup в пределах окна минус 10px.

### 1.2 `DialogWidth` enum (`view/popup/DialogWidth.kt`)

| Name | Dp |
|---|---|
| `EXTRA_SMALL` | 300 |
| `SMALL` | 500 |
| `SMALL_2` | 600 |
| `MEDIUM` | 700 |
| `LARGE` | 1000 |
| `EXTRA_LARGE` | 1300 |

### 1.3 `CiteckPopup` DSL контекст

```kotlin
abstract class CiteckPopup(val kind: CiteckPopupKind) {
    val actionsEnabled: MutableState<Boolean>  // false во время async-операции
    
    fun executePopupAction(desc, action) {
        // запуск на per-task platform-thread (popupActionExecutor)
        // catch-all: ErrorDialog.show(e) на uncaught exception
    }
    
    @Composable
    fun render() {
        PopupContext(this).{
            title("...")
            buttonsRow {
                button("Cancel") { ... }
                spacer()
                button("Confirm", enabledIf = { ... }) { ... }
            }
        }
    }
}
```

`CiteckPopupKind`:
- `DIALOG` — buttonsRow padding: 18dp top, 15dp bottom. Сам `Row` фиксирован `height = 40dp`.
- `WINDOW` — buttonsRow padding: 5dp top/bottom, 10dp start/end. Сам `Row` фиксирован `height = 40dp`.

**Click outside / backdrop click suppressed**: все `CiteckDialog` инстансы создаются с `onDismissRequest = {}` (пустая лямбда). Клик мимо диалога **ничего не делает** — закрыть можно только через кнопку диалога. Re-implementer'у в вебе НЕ нужно делать стандартный close-on-backdrop. (`CiteckDialog.kt:70`)

Системный трей описан в **§01 раздел 8** (отдельная подсистема: AWT/GTK; в веб-порте не нужен — браузерная вкладка = окно приложения).

### 1.4 `ContextMenu` (`view/commons/ContextMenu.kt`)

Глобальный синглтон, поддерживает **одно активное контекстное меню в любой момент**:
- `items: mutableStateOf(List<Item>)`, `dropDownOffset`, `dropDownShow`.
- `render()` вызывается раз в main composition.
- `Modifier.contextMenu(button, items)` — навешивается на любой composable. `button` ∈ `{LMB, RMB}`. На pointer-press события матчит и поднимает popup.

`ContextMenu.Item`:
```kotlin
data class Item(
    val name: String,
    val decoration: @Composable BoxScope.() -> Unit = {},
    val action: suspend () -> Unit
)
```

Click → action в platform-thread под `ErrorDialog.doActionSafe`.

**Web-порт**: RMB-меню в браузере не имеют дефолтного UI; нужен либо `oncontextmenu` + кастомный popup, либо kebab/dropdown.

### 1.5 `CiteckTooltipArea` (`view/commons/CiteckTooltipArea.kt`)

Обёртка над Compose `TooltipArea`. Defaults:
- `delayMillis = 600`
- Placement: автоматически above/below на основе anchor.top vs window.height/2. Offset 8dp.
- Контент: `Surface(shadowElevation=4dp, RoundedCornerShape(4dp), Text(padding=8dp))`.

**`enabled=false` поведение**: при `enabled=false` или `tooltip.isEmpty()` surface ВООБЩЕ не рендерится — hover событие захватывается, но ничего не показывается. (`CiteckTooltipArea.kt:37-46`) Чтобы показать tooltip на disabled-элементе, нужно обернуть его в `CiteckTooltipArea(enabled=true)` независимо от enabled-состояния child'а.

### 1.6 `LimitedText` (`view/commons/LimitedText.kt`)

`Text(maxLines=1, overflow=Ellipsis, requiredWidthIn(min, max))` + автоматический tooltip с полным текстом.

## 2. Тема и иконки

См. §01 раздел 10. Дополнительно:

**Иконки** в `resources/icons/`:

| SVG | Mapping в `ActionIcon` | Размер |
|---|---|---|
| `delete.svg` | `DELETE` | 20dp |
| `stop.svg` | `STOP` | 20dp |
| `start.svg` | `START` | 20dp |
| `plus.svg` | `PLUS` | 20dp |
| `minus.svg` | `MINUS` | 20dp |
| `deploy.svg` | `DEPLOY` | 28dp |
| `stop-all.svg` | `STOP_ALL` | 28dp |
| `recreate.svg` | `RECREATE_NS` | 28dp |
| `ports-on.svg` | `PORTS_ON` | 28dp |
| `ports-off.svg` | `PORTS_OFF` | 28dp |
| `delete-volumes.svg` | `DELETE_VOLUMES` | 28dp |
| `logs.svg` | `LOGS` | 28dp |
| `open-dir.svg` | `OPEN_DIR` | 28dp |
| `arrow-left.svg` | `ARROW_LEFT` | 28dp |
| `key.svg` | `KEY` | 28dp |
| `storage.svg` | `STORAGE` | 28dp |
| `exclamation-triangle.svg` | `EXCLAMATION_TRIANGLE` | 28dp |
| `cog-6-tooth.svg` | `COG_6_TOOTH` | 28dp |
| `ellipsis-vertical.svg` | `ELLIPSIS_VERTICAL` | 28dp |
| `bars-arrow-down.svg` | `BARS_ARROW_DOWN` | 28dp |
| `arrow-path.svg` | (не enum'нут) | manual SVG load |
| `cube-transparent.svg` | (не enum'нут) | manual |
| `ellipsis-horizontal-circle.svg` | (не enum'нут) | manual |
| `pencil.svg` | (не enum'нут) | manual |

`ActionIcon.EDIT` использует Material `Icons.Outlined.Edit` (30dp), а не SVG. Остальные приложения и сервисы имеют свои SVG в `resources/icons/app/`: `alfresco`, `keycloak`, `postgres`, `rabbitmq`, `solr4`, `mailhog`, `telegram`, `docs`, `spring-boot-admin`.

## 3. Карта навигации

```
JVM start
   │
   ▼
DockerNotAvailableScreen ←—— (Docker missing or retry failed)
   │ "Retry" button
   │
   ▼
LoadingScreen ←——————————— (selectedWorkspace == null)
   │ (workspace resolved)
   │
   ▼
WelcomeScreen ←─────────────────────────────────────────────┐
   │ выбор namespace                                          │
   │ setSelectedNamespaceSafe()                              │
   │ → LoadingDialog (модальный overlay)                     │
   │ → workspaceServices.setSelectedNamespace()              │
   ▼                                                          │
NamespaceScreen                                              │
   │ "Back to Welcome" button (только если status=STOPPED)   │
   │ services.setSelectedNamespace("") ──────────────────────┘

(внутри NamespaceScreen открываются окна и диалоги:
  AppCfgEditWindow, LogsWindow, SnapshotsDialog,
  JournalSelectDialog (volumes, secrets), Confirm/Error/Loading/Message)
```

**Навигация — это state mutation**, не URL-роутинг. Две `MutableState` в `Main.kt` определяют текущий экран:
- `selectedWorkspace: MutableState<WorkspaceDto?>` — null → `LoadingScreen`
- `selectedNamespace: MutableState<NamespaceConfig?>` — non-null → `NamespaceScreen`

В вебе **обязательно** ввести URL-роутинг (`/workspace/{id}/namespace/{nsId}/...`), иначе deeplinks не работают.

---

## 4. Screen: Welcome

Файл: `view/screen/WelcomeScreen.kt` (lines 43-396).

**Назначение**: после старта (или возврата с NamespaceScreen). Выбор workspace, создание/выбор namespace, навигация в namespace.

### 4.1 Лейаут

```
┌──────────────────────────────────────────────────────┐
│ [WorkspaceName▾] [⋮]                                  │ TopStart Row
│                                                      │
│           Welcome To Citeck Launcher!                │ centered, vertical offset
│                                                      │
│          ┌──────────────────────────────┐            │
│          │                              │            │
│          │   Body (500dp wide, Center)  │            │
│          │                              │            │
│          └──────────────────────────────┘            │
│                                                      │
│ [slsoft logo]                      [citeck logo]     │ BottomStart / BottomEnd
└──────────────────────────────────────────────────────┘
```

### 4.2 Top-left

| Элемент | Что | Action |
|---|---|---|
| `TextButton` (workspace name, scrim color) | `selectedWsValue.name` | Открывает `JournalSelectDialog` для `WorkspaceDto::class`. На submit → `launcherServices.setWorkspace(id)` |
| `CpIcon(icons/ellipsis-vertical.svg, 20dp)` | LMB contextMenu | Один item: **"Force Update"** — показывает `LoadingDialog`, вызывает `workspaceServices.updateConfig(GitUpdatePolicy.REQUIRED)` |

### 4.3 Body — три состояния

**A. `workspaceServices == null`** (только что выбран workspace, services ещё не загружены):
- Один `Text("Workspace Is Empty", fontSize = 1.05.em)`. Никакой интерактивности.

**B. workspace без namespaces** — рендерится **Quick Start buttons** (через `renderQuickStartButtons`):
- Список вариантов из `workspaceConfig.quickStartVariants`, default `[QuickStartVariant("Quick Start")]`.
- Первый вариант — большая кнопка, weight 0.7, rounded 16dp. Две строки: `variant.name` (1.7em) + `namespaceConfig.bundleRef.toString()` (без явного fontSize — наследует default; не "small").
- Остальные варианты — меньше, weight 0.3, `variant.name` 1em.
- Click:
  - Если уже есть namespaces → `MessageDialog("Workspace already has namespaces\nQuick start is disabled.")`
  - Иначе → `LoadingDialog` + `workspaceServices.entitiesService.createWithData(namespaceConfig)` + `runtime.updateAndStart()` + `ErrorDialog` на ошибку.

**C. workspace с 1-3 namespaces** (`FAST_ACCESS_NAMESPACES_LIMIT = 3`):

Для каждого ns — высокая кнопка (60dp height, rounded 16dp, fillMaxWidth):
- Центр: имя (1.05em) + bundleRef (0.8em, gray)
- CenterEnd: `icons/ellipsis-horizontal-circle.svg` (25dp), LMB contextMenu с двумя items:
  - **"Edit"** — `workspaceServices.entitiesService.edit(namespace.entity)`. Игнорит `FormCancelledException`.
  - **"Delete"** — `workspaceServices.entitiesService.delete(namespace.entity)`.
- Body click → `setSelectedNamespaceSafe(wsServices, namespace.ref.localId)` — platform thread + LoadingDialog + ErrorDialog.
- **Важно**: на карточке **нет** `enabled` guard'а по статусу — клик доступен всегда, в отличие от sidebar-header NamespaceScreen где body-click disabled при не-STOPPED. (`WelcomeScreen.kt:135-144`)

Под карточками меньшая кнопка (35dp height):
- **"More"** — `JournalSelectDialog` для `NamespaceConfig::class` с `closeWhenAllRecordsDeleted=true`. На submit → переход в namespace.

**ВНИЗУ BODY (всегда показывается когда `workspaceServices != null`** — и в state B, и в state C):
- **"Create New Namespace"** (60dp, rounded 16dp) — `workspaceServices.entitiesService.create(NamespaceConfig::class, {}, {})` — открывает create form.

### 4.4 Footer

- `BottomStart`: `logo/slsoft_full_logo.svg`, 100dp height, padding 10dp start / 5dp top
- `BottomEnd`: `logo/citeck_full_logo.svg`, 50dp height, padding 29dp bottom / 10dp end

### 4.5 Какие диалоги поднимаются

| Trigger | Dialog |
|---|---|
| Workspace TextButton | `JournalSelectDialog(WorkspaceDto)` |
| "Force Update" | `LoadingDialog` |
| Quick Start | `LoadingDialog` / `MessageDialog` / `ErrorDialog` |
| Namespace card body | `LoadingDialog` / `ErrorDialog` |
| Card "Edit" | Entity edit form (FormDialog) |
| "More" | `JournalSelectDialog(NamespaceConfig)` |
| "Create New Namespace" | Entity create form |

---

## 5. Screen: Loading

Файл: `view/screen/LoadingScreen.kt`.

**Назначение**: fullscreen placeholder во время блокирующих операций. Используется в двух местах:
1. Внутри `WelcomeScreen` если `selectedWorkspace == null` (line 49-51 WelcomeScreen).
2. Между фазами на app-level (workspace/namespace loading).

### 5.1 Лейаут

```
┌────────────────────────────────────────┐
│                                        │
│              Loading...                │ Center, 2em
│                                        │
│  (после 30s показывается доп. текст:)  │
│  Still loading... This is taking       │
│  longer than expected.                 │
│  To help us diagnose the issue,        │
│  please click the "Dump System Info"   │
│  button at the bottom and send the     │
│  data to the maintainers.              │
│                                        │
│ Show Logs | Dump System Info           │ BottomStart, 40dp row
└────────────────────────────────────────┘
```

### 5.2 Timing logic

`LaunchedEffect` опрашивает каждые 5 сек:
- Если `CiteckDialog.hasActiveDialogs()` — сбрасывает таймер.
- После 30s непрерывного loading'а БЕЗ активных диалогов → `longDelay.value = true`, доп. текст появляется, лог `warn { "Loading takes too long" }`.

### 5.3 Bottom bar

| Label | Action |
|---|---|
| **"Show Logs"** (0.8em, clickable Text) | `LogsWindow.show(title="Launcher Logs", limit=5000, listenMessages=AppLogUtils.watchAppLogs)` |
| `VerticalDivider` | |
| **"Dump System Info"** (0.8em) | `SystemDumpUtils.dumpSystemInfo(AppDir.PATH.resolve("reports"), true)` |

Cancel-кнопки нет; экран исчезает когда parent state меняется.

---

## 6. Screen: DockerNotAvailable

Файл: `view/screen/DockerNotAvailableScreen.kt` (lines 23-70).

**Назначение**: показывается на entry-level если Docker daemon недоступен. Принимает `DockerNotAvailableException` (с `isDockerNotRunning: Boolean`) и `onRetry` lambda.

### 6.1 Лейаут

```
┌────────────────────────────────────────────┐
│        Docker is not available             │ 2em, центр
│                                            │
│   Текст A (Docker установлен, не запущен): │
│   Docker is installed but not running.    │
│   Please start Docker and click Retry.    │
│                                            │
│   ИЛИ Текст B (Docker отсутствует):       │
│   Docker does not appear to be installed  │
│   or is not running.                      │
│   If Docker is already installed, please  │
│   start it and click Retry.               │
│                                            │
│   Install Docker: https://docs.docker.com │ clickable, primary color
│         /get-docker/                      │
│                                            │
│              [ Retry ]                    │ Material Button
└────────────────────────────────────────────┘
```

Точные строки (для locale-файлов):
- Header: `"Docker is not available"`
- A: `"Docker is installed but not running.\nPlease start Docker and click Retry."`
- B: `"Docker does not appear to be installed or is not running."` + `"If Docker is already installed, please start it and click Retry."`
- Link prefix: `"Install Docker: "`, URL `DOCKER_INSTALL_URL` = `https://docs.docker.com/get-docker/`. Клик: `Desktop.getDesktop().browse(URI(DOCKER_INSTALL_URL))`.
- Button: `"Retry"` → invokes `onRetry()`.

Quit-кнопки и settings-доступа нет.

---

## 7. Screen: Namespace (главный экран)

Файл: `view/screen/NamespaceScreen.kt` (lines 71-693).

**Назначение**: всё управление жизненным циклом namespace — start/stop, per-app control, log viewing, config editing, volume и snapshot management.

### 7.1 Общая структура

Двухколоночная `Row`, fillMaxSize:

```
┌──────────────────────────────┬──────────────────────────────────────────────────┐
│   LEFT SIDEBAR (300dp)       │  RIGHT CONTENT AREA (remaining, scrollable)      │
│                              │                                                  │
│  [Namespace name + id]  [⚙]  │  == Citeck Core ==                               │
│  Bundle ref                  │  Name | Status | CPU | MEM | Ports | Tag | Actions│
│  ─────────────────────────── │  ─────────────────────────────────────────────── │
│  ● RUNNING                   │  app-name  RUNNING  1.2%  128M  8080  tag  [▶⏹⚙]│
│  ─────────────────────────── │  ...                                             │
│  CPU  x.x% / max%  [bar]     │  == Citeck Core Extensions ==                    │
│  MEM  x.xG / y.yG  [bar]     │  ...                                             │
│  ─────────────────────────── │  == Citeck Additional ==                         │
│  [▶ Update&Start] | [■ Stop] │  ...                                             │
│  [⬡ Open In Browser]         │  == Third Party ==                               │
│  ─────────────────────────── │  ...                                             │
│  [category header]           │                                                  │
│    [icon] Link 1             │                                                  │
│    [icon] Link 2             │                                                  │
│  ─────────────────────────── │                                                  │
│  ──────────── (spacer) ───── │                                                  │
│  ─────────────────────────── │                                                  │
│  [←] [📂] [≡] [💾] [🔑] [⚠] │                                                  │
└──────────────────────────────┴──────────────────────────────────────────────────┘
```

### 7.2 LEFT SIDEBAR

#### Namespace header (lines 93-143)

| Element | Behavior |
|---|---|
| Имя + id: `LimitedText(selectedNs.name, maxWidth=170dp)` + `" (" + selectedNs.id + ")"` | |
| Bundle ref: `selectedNs.bundleRef.toString()` (0.8em, gray) | |
| Body click | `JournalSelectDialog(NamespaceConfig, closeWhenAllRecordsDeleted=true)`. На submit → `services.setSelectedNamespace(newRef.localId)` |
| **Constraint** | Body click `enabled` только когда `runtimeStatus == STOPPED` |
| Gear icon (`cog-6-tooth.svg`, 28×29dp, CenterTop) | `services.entitiesService.edit(selectedNamespace.value!!)` (в platform thread); открывает форму ниже; игнорит `FormCancelledException` |

##### Форма редактирования Namespace (открывается gear иконкой)

FormSpec title `"Namespace"` (`NamespaceEntityDef.kt:22-81`). Width `MEDIUM` (FormDialog card = 700dp, label-колонка `MEDIUM * 0.3 = 240dp`):

| # | Field key | Тип | Label | Default | Mandatory | Visible / depends |
|---|---|---|---|---|---|---|
| 1 | `name` | `NameField` | `"Name"` | `""` | yes (≤50 chars) | всегда |
| 2 | `bundlesRepo` | `SelectField` | `"Bundles Repo"` | `""` | yes | всегда. Options: `workspaceConfig.bundleRepos.map { (id, name) }` |
| 3 | `bundleKey` | `SelectField` | `"Bundle"` | `""` | yes | `dependsOn(bundlesRepo)`. Options: `bundlesService.getRepoBundles(ctx.getStr("bundlesRepo")).map { (key, key) }`. **Имеет `onManualUpdate`** — справа рядом с селектом рендерится icon `arrow-path.svg` (refresh); click → `bundlesService.updateBundlesRepo(repo)` за `LoadingDialog`, потом перечитать options |
| 4 | `snapshot` | `SelectField` | `"Snapshot"` | `""` | no | `visibleWhen { mode == FormMode.CREATE }` — **видно только в форме создания, скрыто в edit**. Options: `workspaceConfig.snapshots.map { (id, name) }` |
| 5 | `authenticationType` | `SelectField` | `"Authentication Type"` | `""` | yes | всегда. Options: `NamespaceConfig.AuthenticationType.entries.map { (name, name) }` — текущие: `BASIC`, `KEYCLOAK` |
| 6 | `authenticationUsers` | `TextField` | `"Basic Auth Users"` | `""` | yes | `visibleWhen { authenticationType == "BASIC" }`, `dependsOn(authenticationType)`. Comma-separated usernames (например `pavel.simonov,fet,director,admin`) |

Cancel / Confirm — стандартные кнопки FormDialog'а.

#### Status indicator (lines 146-165)

`Row` 30dp height:
- `StatusIndicator`: круг 20dp с black-border, fill — цвет статуса.
- `Text(runtimeStatus.value.name)` — текстовое имя enum'а.

Цвета `NsRuntimeStatus`:

| Status | Hex | Описание |
|---|---|---|
| `STOPPING` | `#F4E909` (yellow) | переход |
| `STARTING` | `#F4E909` | переход |
| `STOPPED` | `#424242` (gray) | финал |
| `STALLED` | `#DB831D` (orange) | ошибка |
| `RUNNING` | `#33AB50` (green) | финал |

#### Namespace stats summary (lines 168-169)

`NamespaceStatsSummary(nsStats.value)` — см. §7.6.

#### Update&Start / Stop row (lines 172-231)

`Row` 30dp:

| Слот | Label / Icon | Weight | Enabled when | Click |
|---|---|---|---|---|
| Левый | `start.svg` + `"Update&Start"` | 0.7 | `!nsActionInProgress` | `nsActionInProgress = true` (Compose thread), потом `Thread.ofPlatform` → `nsRuntime.updateAndStart(false)` через `ErrorDialog.doActionSafe` → `nsActionInProgress = false` |
| Левый (RMB) | context menu **"Force Update And Start"** | — | — | То же что Update&Start, но `updateAndStart(true)`; reset происходит внутри popup-action executor'а |
| `VerticalDivider` | | | | |
| Правый | `stop.svg` + `"Stop"` | 0.3 | `!nsActionInProgress && runtimeStatus != STOPPED` | `nsRuntime.stop()` |

**`nsActionInProgress` — общий флаг между Start и Stop**: пока одна из этих операций активна, обе кнопки disabled. (`NamespaceScreen.kt:179-197`)

#### Open In Browser button (lines 233-261)

Bordered `Box` (1dp LightGray, fillSidebarWidth), с tooltip:

| Status | Tooltip |
|---|---|
| `STARTING` | `"The application is starting. Please wait..."` |
| `STOPPING` / `STOPPED` | `"The application is not running. Start it to open in the browser."` |
| `STALLED` | `"The application is stalled. Please try to start it again."` |
| `RUNNING` | `"Open Citeck in your browser.\n Default username: admin\n Default password: admin"` |

- `logo.svg` 40×40dp (start), padding 7dp/5dp
- Label `"Open In Browser"`, 55dp left padding
- Enabled только при `RUNNING`. Click: `Desktop.getDesktop().browse(URI.create("http://localhost"))`.

#### Link list (lines 264-302)

`(nsGenRes.value?.links ?: emptyList()) + GlobalLinks.LINKS`.

Категоризация: если `link.category` отличается от предыдущей категории — вставляется заголовок (`labelMedium`, `onSurfaceVariant`, padding 12dp start / 4dp vertical).

Каждый link — `Box` fullSidebarWidth:
- Icon 30dp (12dp start / 5dp vertical padding). **`link.icon` — classpath-relative resource path** (например `"icons/app/spring-boot-admin.svg"`, `"icons/app/postgres.svg"`, `"icons/app/docs.svg"`), загружается через `CpImage()` из `resources/icons/app/*.svg`. (`NamespaceLink.kt:7`, `GlobalLinks.kt:12,20`) Web-port должен забандлить эти SVG как static assets.
- Label `link.name` (55dp left padding)
- Tooltip `link.description`
- Click: `Desktop.getDesktop().browse(URI.create(link.url))`. Enabled только при `RUNNING`, кроме `alwaysEnabled=true`.
- `HorizontalDivider` после.

**GlobalLinks** (всегда присутствуют, `alwaysEnabled=true`):

| Name | URL | Category | Tooltip |
|---|---|---|---|
| `"Documentation"` | `https://citeck-ecos.readthedocs.io/` | `"Resources"` | `"Citeck documentation"` |
| `"AI Documentation Bot"` | `https://t.me/haski_citeck_bot` | `"Resources"` | `"Telegram bot for AI documentation assistance"` |

#### Bottom toolbar (lines 306-406)

`Row` 30dp, 10dp start padding, 4dp spacedBy:

| Icon | ActionIcon | Tooltip | Enabled | Click |
|---|---|---|---|---|
| ← | `ARROW_LEFT` | STOPPED: `"Back to Welcome Screen"`. Иначе: `"Please stop all running apps before returning to the welcome screen"` | `runtimeStatus == STOPPED` | `services.setSelectedNamespace("")` |
| 📂 | `OPEN_DIR` | `"Open Namespace Dir"` | always | `Desktop.getDesktop().open(nsDir)` |
| ≡↓ | `BARS_ARROW_DOWN` | `"Show Launcher Logs"` | always | `LogsWindow.show(title="Launcher Logs", limit=5000, source=AppLogUtils.watchAppLogs)` |
| 💾 | `STORAGE` | `"Show And Manage Volumes"` | always | `JournalSelectDialog(VolumeInfo)` columns Name(200-450dp) + Size(50-100dp), not selectable. Custom buttons: **"Snapshots"** и **"Delete All"** |
| 🔑 | `KEY` | `"Show Auth Secrets"` | always | `JournalSelectDialog(AuthSecret)` params: `selectable=false`, `multiple=false`, `closeWhenAllRecordsDeleted=true`, default columns (NAME 500dp). Per-row Actions и доступность "Create" определяются `AuthSecretsService.getSecretEntityDef()` (in restricted code `core/secrets/auth/`). Минимально наблюдаемо: кнопка слева — `"Close"`, никаких custom buttons. (`NamespaceScreen.kt:390-401`) |
| ⚠ | `EXCLAMATION_TRIANGLE` | `"Export System Info"` | always | `SystemDumpUtils.dumpSystemInfo(nsDir.resolve("reports"))` |

**Volumes dialog custom buttons**:
- **"Snapshots"** → `SnapshotsDialog.showSuspended(Params(nsRuntime, services))`
- **"Delete All"** — `loading = true` (`JournalButton` сам оборачивает action в `LoadingDialog.show()`). `enabledIf` только при `runtimeStatus == STOPPED`. `ConfirmDialog("All your data in volumes will be lost")` → итерирует до 100 раз, удаляя `VolumeInfo` entities. (`NamespaceScreen.kt:360-362`)

### 7.3 RIGHT CONTENT AREA (lines 410-445)

Вертикальный scrollable `Column`. Apps группируются по `ApplicationKind` в фиксированном порядке:

1. `"Citeck Core"` (`CITECK_CORE`)
2. `"Citeck Core Extensions"` (`CITECK_CORE_EXTENSION`)
3. `"Citeck Additional"` (`CITECK_ADDITIONAL`)
4. `"Third Party"` (`THIRD_PARTY`)

Пустые группы скипаются. Внутри группы apps сортируются по имени.

#### Group header (lines 458-464)

`Text` 1.1em, bold, padding 5dp start / 10dp top+bottom.

#### Table header row (lines 467-475)

Один `Row` перед каждой группой:

| Header | Layout |
|---|---|
| `"Name"` | `weight(NAME_WEIGHT = 0.8f)` |
| `"Status"` | `weight(STATUS_WEIGHT = 0.6f)` |
| `"CPU"` | `width(CPU_WIDTH = 100dp)` |
| `"MEM"` | `width(MEM_WIDTH = 100dp)` |
| `"Ports"` | `width(PORTS_WIDTH = 80dp)` |
| `"Tag"` | `width(TAG_WIDTH = 175dp)` |
| `"Actions"` | `width(ACTIONS_WIDTH = 100dp)` |

(`AppTableColumns.kt`)

### 7.4 Per-app data row (lines 479-675)

`Row` 30dp height.

**Name** (`weight 0.8f`): `application.name`, `maxLines=1`.

**Status** (`weight 0.6f`):
- Текст: `appStatus.name`
- Цвет:
  - Stalled (`PULL_FAILED`, `START_FAILED`, `STOPPING_FAILED`): `#DB831D` (orange)
  - `STOPPED`: `#424242` (gray)
  - `RUNNING`: `#33AB50` (green)
  - Transient: `#F4E909` (yellow)
- Плюс `statusText.value` (freeform, ellipsized, maxLines=1).

**CPU** (100dp) / **MEM** (100dp): `AppStatsCells` (§7.6).

**Ports** (80dp):
- Парсятся из `appDef.ports` (до `:`, без leading `!`).
- 0 ports → пусто.
- 1 port → `Text`.
- 2+ → `CiteckTooltipArea` с tooltip = все ports через `\n`; display = first + `" .."`.

**Tag** (175dp):
- Tooltip = full `application.image`.
- Display = `image.substringAfterLast(":", "unknown")`.
- Click: copy full image string в системный clipboard через `Toolkit.getDefaultToolkit().systemClipboard`.

**Actions** (100dp):

| Icon | Tooltip | Enabled | Click |
|---|---|---|---|
| START/STOP (toggle) | `"Start Application"` / `"Stop Application"` | always | Если `isStoppingState()`: START → `application.start()`. Иначе STOP → `application.stop(manual=true)` (manual=true добавляет в detachedApps). |
| `BARS_ARROW_DOWN` | `"Show Logs"` | `appStatus != STOPPED` | `LogsWindow.show(title=application.name, limit=5000, source=application.watchLogs)` |
| `COG_6_TOOTH` (в 33dp Box) | LMB: см. ниже | always | LMB → config edit. RMB → volume files menu |

**COG Tooltip (LMB)**:
```
Left Click - Edit App Docker Config
Right Click - Edit Volume Files
A blue marker means this app has a manual config that
won't be managed by the launcher
To reset manual changes, open the editor and click 'Reset'
```

**COG decorators**:
- 6dp blue dot at TopEnd — если `editedDef.value || anyVolumeFilesEdited.value`.
- Count label at BottomEnd — `volumeFilesItems.value.size` (12sp), показывается при volume files > 0.

**COG LMB** (lines 639-653): открывает `AppCfgEditWindow.show(appDefToEdit)`.
- `null` (Reset) → `nsRuntime.resetAppDef(appDefToEdit.name)`.
- Non-null → `nsRuntime.updateAppDef(before, editRes.appDef, editRes.locked)`.

**COG RMB context menu** (lines 581-627): построен из `volumeFiles`. Только файлы с расширениями `EDITABLE_FILE_EXTENSIONS = {"yaml","yml","json","kt","java","js","lua","Dockerfile","sh","txt","conf"}`:
- Имя файла (с blue vertical bar если edited)
- Click: `nsRuntime.runtimeFiles.getFileContent(path)` → `AppCfgEditWindow.show(filename, content)`:
  - `null` → `nsRuntime.resetEditedFile(path)`
  - Non-null → `nsRuntime.pushEditedFile(path, content.toByteArray())`

Каждая row закрывается `HorizontalDivider`.

### 7.5 AppRuntimeStatus — полный state machine

Файл: `core/namespace/runtime/AppRuntimeStatus.kt`.

| Status | Категория | Цвет |
|---|---|---|
| `READY_TO_STOP` | Stopping | yellow |
| `STOPPING` | Stopping | yellow |
| `STOPPING_FAILED` | Stalled | orange |
| `STOPPED` | Финал | gray |
| `READY_TO_PULL` | Starting | yellow |
| `PULLING` | Starting | yellow |
| `PULL_FAILED` | Stalled | orange |
| `READY_TO_START` | Starting | yellow |
| `DEPS_WAITING` | Starting | yellow |
| `STARTING` | Starting | yellow |
| `START_FAILED` | Stalled | orange |
| `RUNNING` | Финал | green |

Помощники:
- `isStoppingState()` = `READY_TO_STOP || STOPPING || STOPPED`
- `isStartingState()` = `READY_TO_PULL || PULLING || DEPS_WAITING || READY_TO_START || STARTING || RUNNING`
- `isStalledState()` = `PULL_FAILED || START_FAILED || STOPPING_FAILED`

### 7.6 ContainerStatViews

Файл: `view/screen/ContainerStatViews.kt`.

#### `AppStatsCells` (lines 42-106)

Рендерит CPU + MEM ячейки.

**CPU** (100dp):
- Inactive: `"-"` gray.
- Active: `StatsCell` с `value=cpuPercent`, text=`ContainerStats.formatCpuPercent(cpuPercent)`, warning если `isCpuThrottled`.
- Tooltip (RUNNING + throttled): `"Throttled: N periods\nThrottle time: X.Xms"`.

**MEM** (100dp):
- Inactive: `"-"` gray. Условие inactive: `!RUNNING || !hasMemoryData`, где `hasMemoryData = containerStats.memoryUsage > 0 || containerStats.memoryLimit > 0`. То есть даже у RUNNING-контейнера со стримом без данных (на старте) показывается `-`. (`ContainerStatViews.kt:96-101`)
- Active: `StatsCell` с `value=memoryPercent`, text=`ContainerStats.formatMemoryShort(usage)` (B/K/M/G).
- Tooltip: `"X.XM / Y.YG (Z.Z%)"` + optional `"\nCRITICAL: Near OOM limit!"` / `"\nWarning: High memory usage"` / `"\nCache: X.XM"`.

#### `StatsCell` (lines 109-163)

| Часть | Размер |
|---|---|
| Текст | width 55dp |
| Progress bar | 30dp × 6dp, gray bg (alpha 0.3), rounded 3dp |

Цвета — **bar и text управляются раздельно**:

| Условие | Bar color | Text color |
|---|---|---|
| `isCritical` (mem ≥ 95%) | `#E53935` (red) | `#E53935` (red) |
| `isWarning` (CPU throttled, mem ≥ 90%) | `#FFA726` (orange) | `#FFA726` (orange) |
| Default | `#66BB6A` (green) | **`Color.Unspecified`** — наследует default text color темы (т.е. чёрный/тёмный); НЕ зелёный |

(`ContainerStatViews.kt:122-137`)

Animation: `tween(300ms)`.

#### `NamespaceStatsSummary` (lines 166-213) и `CompactResourceRow`

В sidebar под status indicator. Две aggregate-строки (20dp каждая):

**CPU**: `"CPU"` (35dp wide, gray, 0.85em) + value `"X.X% / Y.Y%"` или `"X.X%"` + progress bar 80dp × 6dp. Tooltip: `"N CPUs available"` если cpuCores>0.

**MEM**: `"MEM"` + value `"X.XG / Y.YG"` или просто usage + progress bar.

**Color thresholds для `CompactResourceRow` ОТЛИЧАЮТСЯ от `StatsCell`** (`ContainerStatViews.kt:224-228`):

| Условие | Цвет |
|---|---|
| `progressPercent ≥ 90` | `#E53935` (red) |
| `progressPercent ≥ 70` | `#FFA726` (orange) |
| иначе | `#66BB6A` (green) |

То есть aggregate-индикаторы более чувствительные чем per-app (90/70% vs 95/90%).

### 7.7 Refresh / streaming behavior

Stats — **streaming**, не polling. `AppRuntime.startStatsStream()` открывает `DockerApi.watchContainerStats` subscription когда app входит в RUNNING. Каждый emit обновляет `application.containerStats` (MutProp). UI через `rememberMutProp` рекомпозит. Поток обрывается при выходе из RUNNING.

**Web-порт**: одна SSE-стрима на namespace, мультиплексирующая все container stats. Frequency определяется Docker daemon (нативный rate).

---

## 8. Что трудно портировать

1. **OS-native действия**: `Desktop.getDesktop().open(dir)` и `.browse(URI)` не имеют веб-эквивалента. Замена:
   - "Open Namespace Dir" → копировать путь в clipboard, либо download .zip.
   - "Open in Browser" → просто `<a href>` или `window.open()`.
2. **Continuous Docker stats streaming**: Docker daemon push'ит сам, частоту не контролируем. На вебе — мультиплексировать в один SSE-канал и **самим** регулировать частоту обновления стора.
3. **Two-column weighted layout**: Compose `weight(0.8f)` / `weight(0.6f)` плюс fixed dp columns. В CSS — flex/grid. Hi-DPI: Compose dp ≈ CSS px @1×, на retina нужен корректный rem-scaling.
4. **Separate OS windows**: `AppCfgEditWindow`, `LogsWindow` открываются как независимые OS-окна. Пользователь может иметь несколько просмотрщиков логов и редакторов одновременно с главным экраном. Веб-эквивалент — модалы / drawer'ы / новые browser-tab'ы. Дизайн должен учитывать конкурентные панели.
5. **Context menus (RMB)**: браузер по умолчанию имеет свой RMB. Нужен `oncontextmenu={preventDefault; show menu}` или kebab/dropdown.
6. **Clipboard**: `Clipboard Web API` требует HTTPS + user permission. Tag-cell copy: `navigator.clipboard.writeText(image)`.
7. **Back-navigation guard**: кнопка "Back to Welcome" disabled при не-STOPPED. В SPA нужен и UI-disable, и server-side проверка, потому что history back / URL change могут обойти UI-флаг.
8. **Quick Start logic**: `prepareNsDataToCreate` (lines 268-306 WelcomeScreen) ресолвит `bundleRef` в render time через `bundlesService.getLatestRepoBundle`. В вебе — async API call с loading state.
