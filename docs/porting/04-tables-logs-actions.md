# 04 — Таблицы, логи, селекты, action-система

## 1. Table framework

Файлы:
- `view/table/table/DataTable.kt`
- `view/table/table/TableDslBuilder.kt`
- `view/table/table/Divider.kt`
- `view/table/CiteckIconButton.kt`

### 1.1 DSL

`DataTable` — composable, две lambda-параметра: `columns` и `rowsContent`. Нет explicit row count и data source; caller капчурит данные внутри lambda.

```kotlin
DataTable(
    columns = {
        column(modifier = Modifier.width(200.dp)) { Text("Name") }
        column(contentAlignment = Alignment.CenterEnd) { Text("Status") }
    },
    footer = { Text("${rows.size} items") }
) {
    items.forEach { item ->
        row(
            modifier = Modifier
                .clickable { onRowClick(item) }
                .background(if (hovering) hoverColor else Color.Transparent)
        ) {
            cell { Text(item.name) }
            cell(contentAlignment = Alignment.CenterEnd) { Text(item.status) }
        }
    }
}
```

`@DslMarker` — `@TableDslBuilder` на `ColumnBuilder`, `RowsBuilder`, `RowBuilder` для предотвращения accidental nesting.

### 1.2 Column features

| Param | Поведение |
|---|---|
| `modifier: Modifier` | Width / flex. Fixed via `Modifier.width()`. Нет встроенного `weight`/`fillMaxWidth` в DSL — caller использует Compose modifiers. |
| `contentAlignment: Alignment` | Default `CenterStart`. Любой `Alignment`. |
| `composable: @Composable BoxScope.() -> Unit` | Header cell content. |
| `headerBackground(composable)` | Опциональная декорация под header row. |

Sort: **не встроен в DSL**. Sortable flag нет, sort model нет. Любые sort affordances — caller сам.

### 1.3 Row features

| Param | Поведение |
|---|---|
| `modifier` | Click handler, hover, context menu. Caller responsibility. |
| `cell { ... }` | Один на column, с `modifier` и `contentAlignment`. |

Нет selection model. Нет managed hover. Нет row identity. Context menu — `Modifier.contextMenu(Button.RMB, items)`.

### 1.4 Layout algorithm (`SubcomposeLayout`)

Два прохода:
1. "content" — все cells (header + data) измеряются без constraints для natural widths/heights.
2. Scale factor: если total natural width < container — proportional scale up. Если > container — `scale=1f` (нужен parent-уровень horizontal scroll).
3. "scaledContent" — повторная композиция с exact scaled widths.

**Нет виртуализации!** Каждая row измеряется и размещается. Practical limit — сотни строк; для тысяч — заметно медленно.

### 1.5 Divider

`view/table/table/Divider.kt`:
- `Box(Modifier.fillMaxWidth().height(thickness).background(color))`.
- Default: `thickness=1dp`, `color=LightGray`.
- `Dp.Hairline` — 1 физический пиксель через `1f / density`.

`DataTable` ставит divider между каждой парой data rows (и после header) через `divider: @Composable ((rowIndex: Int) -> Unit)?`. Default `@Composable { Divider() }`. Если `headerBackground` задан — divider под header подавлен. Caller может передать `divider = null` или использовать `rowIndex` для group boundaries.

### 1.6 `CiteckIconButton`

`view/table/CiteckIconButton.kt`. Минималистичная icon button:
- `Box` + `Modifier.clickable(role = Role.Button)`.
- Ripple commented out (no visual press feedback).
- `enabled = false` → `ContentAlpha.disabled` через `CompositionLocalProvider`.
- Принимает любой `@Composable` content.
- Size — modifier. Используется `CiteckIconAction` как button wrapper.

**Disabled icon visual — разный для SVG vs Material EDIT**:
- SVG-backed `ActionIcon` (START/STOP/COG_6_TOOTH/...): когда `enabled=false`, иконка получает **explicit `Color.Gray` tint** (`CiteckIconAction.kt:113-124`), независимо от ContentAlpha provider'а.
- `ActionIcon.EDIT` (Material `Icons.Outlined.Edit` vector, line 86): использует `renderDefaultIcon` без `enabled` параметра → получает только ContentAlpha dimming из `CompositionLocalProvider` от `CiteckIconButton`.
- Результат: EDIT-иконка слегка тускнеет; SVG-иконки становятся **сплошным серым**. Они выглядят по-разному в disabled-состоянии.

**Нет `loading=true` спиннера** на icon buttons. Прогресс долгих action'ов поверхуется через отдельный `LoadingDialog`, не на самой кнопке.

---

## 2. LogsViewer

Файлы:
- `view/logs/LogsWindow.kt`
- `view/logs/LogsViewer.kt`
- `view/logs/LogsToolbar.kt`
- `view/logs/LogsContent.kt`
- `view/logs/LogsState.kt`
- `view/logs/LogsComponents.kt`

### 2.1 `LogsWindow`

Наследник `CiteckWindow`. **Отдельное OS-окно**, не embedded panel. Одно окно per `show()` call.

API: `LogsWindow.show(LogsDialogParams(...))`.

`LogsDialogParams`:
- `appName: String` — title: `"Logs of $appName"` или `"Logs"` если пустое.
- `limit: Int` — ring buffer size для `LogsState`.
- `listenMessages: ((String) -> Unit) -> Result<AutoCloseable>` — factory подписки на log stream. Caller предоставляет (Docker log tail).

На `beforeClose()` — `watcher.getOrNull()?.close()` отписывает stream.

Размер окна: 100% width × 90% height **того монитора, чьи bounds содержат позицию main window** (multi-monitor detection в `CiteckWindow.kt:62-83`). Centered.

**Нет multi-app multiplexing**. Каждый `show()` — одно окно для одного app'а. Три открытых лога = три независимых окна.

### 2.2 `LogsViewer` (layout)

Material 3 `Scaffold`:
- `topBar`: `LogsToolbar` always + `LogsSearchBar` conditionally + `HorizontalDivider`
- `bottomBar`: `HorizontalDivider` + `LogsStatusBar`
- `content`: `LogsContent`

Local state:
- `followLogs: MutableState<Boolean>` — auto-scroll
- `searchQuery`, `debouncedSearchQuery` (300ms debounce), `searchVisible`, `currentMatchIndex`
- `wordWrap`, `useRegex`, `copiedFeedback` (2s)
- `filterText`
- `levelFilters: SnapshotStateMap<LogLevel, Boolean>` — booleans per level

Consumer loop: `LaunchedEffect("consume-log-messages")` опрашивает `logsState.consumeMessagesQueue()` каждые 500ms если queue пустой.

### 2.3 `LogsToolbar` controls

**Left side**:
- `CompactSearchField` (300dp wide, placeholder `"Filter"`) — live text filter, min 2 chars. Wildcards `*` (конвертится в `.*`, остальное regex-escaped). Case-insensitive.
- Clear-filter `IconButton(20dp, Icons.Outlined.Clear)` — только когда filter non-empty.
- Level checkboxes: ERROR, WARN, INFO, DEBUG, TRACE (UNKNOWN исключён). Цвет = level text color.

**Right side** (все 20dp icons):
- Copy all: `Icons.Outlined.ContentCopy`. Tooltip `"Copied!"` 2s после клика, tint green `#4CAF50`. Shortcut: `Ctrl/Cmd+Shift+C`.
- Clear logs: `Icons.Outlined.Delete`. Tooltip `"Clear logs (Ctrl+L)"`.
- Export: `Icons.Outlined.SaveAlt`. Swing `JFileChooser`, default filename `{windowTitle}_{yyyyMMdd_HHmmss}.log`, filter `.log`/`.txt`. Shortcut: `Ctrl/Cmd+S`.

`LogsSearchBar` (если `searchVisible`):
- `CompactSearchField` placeholder `"Search"` — **поиск работает только при `searchQuery.length >= 2`**, иначе матчей нет (`LogsViewer.kt:294-296`).
- **Regex toggle** — `Checkbox(checked = useRegex)` + рядом `Text(".*", fontWeight = Bold)`. Только сам Checkbox кликабелен, лейбл `.*` — не интерактивный (`LogsToolbar.kt:213-221`).
- **Match counter** (60dp wide fixed) — три состояния (`LogsToolbar.kt:226-229`):
  - `searchQuery.length < 2` → `""` (пусто)
  - `matchCount == 0 && length >= 2` → `"0/0"`
  - иначе → `"${currentMatchIndex + 1}/$matchCount"`
- Prev (▲), Next (▼), Close (✕)
- Enter / Shift+Enter — next/prev

**Scaffold-level keyboard**:
- `Ctrl/Cmd+F` — открыть search
- `Escape` — close search (или window если search закрыт)
- `F3` / `Ctrl/Cmd+G` — next; `Shift+F3` / `Ctrl/Cmd+Shift+G` — prev

**Russian keyboard mapping** (`mapToLatinKey`) — транслирует кириллические физические клавиши в QWERTY positions, чтобы shortcuts работали независимо от layout.

### 2.4 `LogsContent` (rendering)

Весь output — **один** `BasicText` (в `SelectionContainer`) с computed `AnnotatedString`. Нет per-line widget. Performant для display, но **нет line virtualization**.

Два независимых `ScrollState`: vertical, horizontal. Если `wordWrap=false` — horizontal enabled + `HorizontalScrollbar`.

Follow-tail:
- **Выходит** при upward-scroll событии с delta меньше `-10` (т.е. пользователь сдвинул скролл вверх больше чем на 10px за один event) (`LogsContent.kt:63-69`). Это **не** "10px от bottom" — это амплитуда конкретного scroll-события.
- **Возвращается** когда `isAtBottom` = `maxValue - value < SCROLL_THRESHOLD = 50`.

`LogsStatusBar` (bottom):
- Left: `"Lines: N / limit"` (gray 0.8em)
- Right: Word-wrap toggle (`Icons.AutoMirrored.Filled.WrapText`, blue если active) + Follow-logs (`Icons.Default.KeyboardArrowDown`, blue если active)

### 2.5 `LogsState` (streaming model и buffer)

Double-buffering чтобы не блокировать UI:

- Два `LogsList` массива размера `limit`. Активен один; меняются местами при каждом flush.
- Producer (`addMsg`): `ArrayBlockingQueue<LogMessage>` capacity 10 000. Блокирует caller до 30 секунд если queue full. Вызывается из Docker log callback thread.
- Consumer (`consumeMessagesQueue`): из UI coroutine loop. Drain'ит весь queue одним батчем, пишет в inactive массив (сдвигает старое из front'а если full), atomically свапает `messagesState.value` → recomposition один раз per batch.
- При full buffer — старые сообщения дропаются из front'а (offset через `System.arraycopy`).
- `totalMessages` — monotonic counter, только для триггера follow-scroll `LaunchedEffect`.
- `clear()` — обнуляет оба массива и counter.

Limit задаётся `LogsDialogParams.limit`. Default в `LogsState` нет.

### 2.6 Log level detection и coloring

`LogLevelDetector.detect(message)` — 7 regex паттернов по приоритету:
1. `[ERROR]`, `[WARN]`, ... (bracketed)
2. `|-WARN`, `|-ERROR`, ... (Logback internal)
3. Timestamp + level: `10:30:45.123 ERROR`
4. Spring Boot ISO: `2024-01-15T10:30:45... INFO`
5. Python: `ERROR:`, `WARNING:`
6. Level в whitespace
7. Level at line start

Линии с `LogLevel.UNKNOWN` наследуют level предыдущей (для stack trace).

**Color scheme** (`LogLevelColors` в `LogsComponents.kt:32-49`):

| Level | Text Color | Hex |
|---|---|---|
| ERROR | Dark red | `#C62828` |
| WARN | Dark orange | `#F57C00` |
| DEBUG | Medium gray | `#757575` |
| TRACE | Light gray | `#9E9E9E` |
| INFO | Dark green | `#2E7D32` |
| UNKNOWN | Black | `#000000` |

Search highlight: yellow bg `Color.Yellow` + black text. Current match: orange bg `#FF9800` + black text.

**Level keyword** внутри line (literal token `[INFO]`) дополнительно bolded. `LogsTextBuilder.buildColoredAndHighlightedText` идёт по char'ам, interleaving level-color spans с search-highlight.

Font: **Ubuntu Mono Regular** (`fonts/ubuntu/UbuntuMono-R.ttf`), 1em.

---

## 3. `CiteckSelect`

Файл: `view/select/CiteckSelect.kt`.

### 3.1 State model

```kotlin
class CiteckSelectState(
    val options: MutableState<List<SelectOption>>,
    val selected: MutableState<String>
)
class SelectOption(
    val name: String,                              // display
    val value: String = name,                      // internal value
    val button: Boolean = false,                   // если true — action-button, не selection
    val actions: List<ActionDesc<String>> = emptyList()  // в CiteckSelect НЕ рендерится
)
```

`SelectOption.actions` определён, но `CiteckSelect` его не рендерит — используется `JournalSelectDialog`.

### 3.2 Trigger UX

30dp tall `Box` с 1dp gray border. Содержит:
- Selected option's `name` (lookup из options; fallback на `value`)
- Down-arrow icon (right)
- (Опционально) `RemoveCircleOutline` icon 30dp from right когда `mandatory=false` и есть selection (click → `onSelected("")`)

**Special case**: если ровно одна option, она `button=true` и `value` совпадает с текущим selection — click сразу fires `onSelected`, popup не открывается (single-purpose button). Иначе popup открывается при multiple options или single option с другим value.

### 3.3 Popup

Через `PopupInWindow`, pin'ится к trigger's window-coords (top-left = trigger's bottom-left). Scrollable `Column`:
- `widthIn(min=150dp)`, минимум trigger's width
- `heightIn(max = triggerHeight * 8)`
- 1dp gray border

Каждый item — 30dp Box. **Currently selected item исключён** из списка. 1dp `LightGray` bottom line. Click: close popup. Если `option.button=false` — обновить `state.selected.value`, потом `onSelected(option.value)`. Если `option.button=true` — selection не меняется, но `onSelected` всё равно.

### 3.4 Single only

Multi-select нет.

### 3.5 No search-as-you-type, no keyboard nav

Поиска внутри dropdown нет. Стрелок/Enter — тоже нет.

### 3.6 Empty state

Если options пуст и selection пуст — пустой trigger с arrow. Popup не открывается.

---

## 4. Action system

### 4.1 Два разных понятия "action"

В кодбазе два разных action-концепта; **не путать**.

**UI-level `ActionDesc`** (`view/action/ActionDesc.kt`): лёгкий descriptor для кнопки. Поля:
- `id: String` — для логов
- `ActionIcon` — enum
- `description: String` — tooltip/content-description
- `action: suspend (T) -> Any` — параметризуется по типу `T` (часто `Unit` для row-level, или row record)

Нет регистрации, нет discovery — инстансы конструируются inline там где определяются кнопки.

**Core-level `ActionsService`**: queue-backed executor для тяжёлых операций — image pull, container start/stop. Это **не та же система**. UI's `ActionDesc.action` обычно вызывает domain services (`AppStartAction.execute(actionsService, appRuntime)`), которые в свою очередь зовут `actionsService.execute(Params(...))`.

### 4.2 `ActionsService` регистрация

Нет META-INF/services discovery. Регистрация explicit в `LauncherServices.init()` (`LauncherServices.kt:123-126`):

```kotlin
actionsService = ActionsService()
actionsService.register(AppImagePullAction(dockerApi, authSecretsService))
actionsService.register(AppStartAction(dockerApi))
actionsService.register(AppStopAction(dockerApi))
```

`register()` через `ReflectUtils.getGenericArgs` извлекает `KClass` от `ActionParams` из generic-сигнатуры executor'а, потом сохраняет executor в `ConcurrentHashMap<KClass<ActionParams<Any>>, ExecutorInfo>`. Этот ключ используется для lookup в `execute()`.

### 4.3 Invocation chain (UI button → Docker)

1. Developer декларирует:
```kotlin
ActionDesc<AppRuntime>(
    id="start",
    icon=ActionIcon.START,
    description="Start",
    action = { runtime -> AppStartAction.execute(actionsService, runtime) }
)
```
2. `CiteckIconAction(coroutineScope, actionDesc, actionParam)` composable рендерит `CiteckIconButton`.
3. Click: `coroutineScope.launch { ErrorDialog.doActionSafe({ actionDesc.action.invoke(actionParam) }, ...) }`.
4. Suspend lambda зовёт `AppStartAction.execute(actionsService, appRuntime)` → `actionsService.execute(Params(appRuntime))`.
5. `ActionsService.execute()` lookup'ит `AppStartAction` по `Params::class`, создаёт `ActionInfo` + `ActionFuture`, submits в fixed thread pool (20 threads), возвращает `Promise<R>`.
6. UI lambda не await'ит `Promise`; fire-and-forget от coroutine view. Результат не propagate'ится в кнопку.

**`IconBtn`** — вариант для toolbar (не table rows): dispatch через `Thread.ofPlatform().start { runBlocking { action() } }`, не через coroutine scope. Использует `CiteckTooltipArea`.

### 4.4 `ActionStatus`: progress reporting

`core/utils/ActionStatus.kt`:

```kotlin
data class ActionStatus(val message: String, val progress: Float)  // 0.0–1.0
class Mut : MutProp<ActionStatus> { ... }
```

`doWithStatus` хранит current `Mut` в `ThreadLocal`, чтобы sub-steps звали `ActionStatus.getCurrentStatus()` без передачи reference вниз.

`LoadingDialog.show(status: ActionStatus.Mut?)` — surface progress. Но `CiteckIconAction` и `IconBtn` НЕ передают `ActionStatus` в `LoadingDialog`. Long-running actions (image pull) должны звать `LoadingDialog.show()` explicitly из action lambda или из `ActionExecutor.execute()`, если хотят progress UI. Кнопка сама loading-state не показывает.

### 4.5 Retry logic

`ActionExecutor.getRetryAfterErrorDelay` default `-1` (no retry).
- `AppStopAction` override → `1000` ms (stop retries с 1s delay).
- `AppStartAction` default (no retry).

Positive retry → `ActionsService` reschedule через `ScheduledExecutorService`.

### 4.6 Concurrency

`ActionsService` — fixed thread pool 20. Нет per-app/per-namespace queueing; все registered actions competeят за 20 threads. `actionsInfo` set (`ConcurrentHashMap`) tracks in-flight; > 1000 → throw (memory-leak guard).

Background watcher `actions-watcher` логирует warn если action работает >5 min. Action работающий >2 min без active `Future` — stalled и удаляется.

### 4.7 Error surface

- `CiteckIconAction`: `ErrorDialog.doActionSafe` — лог exception, потом `ErrorDialog.show(exception)` (модал, до 10 строк root cause stack).
- `IconBtn`: `Thread.ofPlatform.start { runBlocking { try { action() } catch (e) { ErrorDialog.show(e) } } }`.
- `ContextMenu` items: тот же `ErrorDialog.doActionSafe`.

**Нет toasts**, нет row-level badges, нет permanent status indicators после failure. ErrorDialog — единственная surface.

---

## 5. `ActionIcon` enum: каталог иконок

`view/action/ActionIcon.kt` (lines 3-25). Mapping из `CiteckIconAction.kt:81-102` и `IconBtn.kt:27-28`:

| Enum | SVG file | Rendered size |
|---|---|---|
| `DELETE` | `icons/delete.svg` | 20dp |
| `STOP` | `icons/stop.svg` | 20dp |
| `START` | `icons/start.svg` | 20dp |
| `PLUS` | `icons/plus.svg` | 20dp |
| `MINUS` | `icons/minus.svg` | 20dp |
| `EDIT` | Material `Icons.Outlined.Edit` (NOT SVG) | 30dp |
| `DEPLOY` | `icons/deploy.svg` | 28dp |
| `STOP_ALL` | `icons/stop-all.svg` | 28dp |
| `RECREATE_NS` | `icons/recreate.svg` | 28dp |
| `PORTS_ON` | `icons/ports-on.svg` | 28dp |
| `PORTS_OFF` | `icons/ports-off.svg` | 28dp |
| `DELETE_VOLUMES` | `icons/delete-volumes.svg` | 28dp |
| `LOGS` | `icons/logs.svg` | 28dp |
| `OPEN_DIR` | `icons/open-dir.svg` | 28dp |
| `ARROW_LEFT` | `icons/arrow-left.svg` | 28dp |
| `KEY` | `icons/key.svg` | 28dp |
| `STORAGE` | `icons/storage.svg` | 28dp |
| `EXCLAMATION_TRIANGLE` | `icons/exclamation-triangle.svg` | 28dp |
| `COG_6_TOOTH` | `icons/cog-6-tooth.svg` | 28dp |
| `ELLIPSIS_VERTICAL` | `icons/ellipsis-vertical.svg` | 28dp |
| `BARS_ARROW_DOWN` | `icons/bars-arrow-down.svg` | 28dp |

`IconBtn` вычисляет SVG путь динамически: `"icons/${icon.name.lowercase().replace("_", "-")}.svg"`. Так что `RECREATE_NS` дал бы `recreate-ns.svg` — но enum никогда не используется с `IconBtn` для этого варианта (только `CiteckIconAction` с explicit mapping на `recreate.svg`).

**SVG в resources БЕЗ enum'а**: `pencil.svg`, `arrow-path.svg`, `cube-transparent.svg`, `ellipsis-horizontal-circle.svg`. Доступны через `CpIcon("icons/...")`, но не через `ActionIcon`.

---

## 6. Что трудно портировать

1. **Table framework → JSON schema**: column/row нужно сериализовать. JSON-schema (label, width hint, alignment, renderer type) + row data array. Scaling algorithm — CSS `flex-grow` или `table-layout: fixed` с percentage widths. Нет built-in sort — caller-managed.

2. **No virtualization**: current `SubcomposeLayout` рендерит все rows. Web — обязательно virtualized list (`react-virtual`, `@tanstack/virtual`) с самого начала; namespace tables могут содержать сотни containers. Two-pass measure не транслируется в DOM; CSS grid / `<table>` обрабатывают column width нативно.

3. **Logs viewer — single text node vs per-line DOM**: AnnotatedString renderит весь output одной нодой. В браузере = огромный text node, трудный для selection и virtualized scroll. Web-port: каждая line — `<div>` или `<span>` с CSS class по level. CSS classes per `LogLevel`. Search highlight — split text вокруг match offsets at render time.

4. **Logs viewer — follow-tail через SSE/WS**: `LogsDialogParams.listenMessages` — callback subscription. В вебе — Go runtime exposes streaming endpoint (SSE/WS). Browser UI subscribes, append'ит в ring buffer `limit`, обрабатывает auto-scroll identically. 500ms poll loop и double-buffer swap — desktop concurrency workaround, в браузере event-loop модель push'ит сообщения directly.

5. **`CiteckSelect` — no keyboard nav**: текущий select не имеет arrow-key/Enter. Web-port должен реализовать standard `<select>` keyboard UX с нуля.

6. **Action system — HTTP API boundary**: `ActionsService` — Java executor в desktop JVM. Web-port: Go runtime exposes HTTP endpoints для каждого action type:
   - `POST /api/namespaces/{ns}/apps/{app}/start`
   - `POST /api/namespaces/{ns}/apps/{app}/stop`
   - `POST /api/namespaces/{ns}/apps/{app}/recreate`
   - и т.д.
   
   20-thread pool, retry logic (`getRetryAfterErrorDelay`), stalled-action watcher — server-side equivalents. `ActionStatus`/`LoadingDialog` ThreadLocal progress → SSE progress events on HTTP response.

7. **`ActionDesc` coroutine scope vs fetch**: `CiteckIconAction` fires action в caller-provided `CoroutineScope`. В React — async call (или mutation от react-query). Scope cancellation при leaving composition → AbortController tied to component unmount.

8. **Icon catalog**: 20 SVG в `resources/icons/` — canonical icon set. В web bundle. `EDIT` icon — единственный использующий Material Icons vector; в web-port заменить на equivalent Material icon или `pencil.svg` (уже есть в resources, не используется в `ActionIcon`).
