# 03 — Диалоги, форм-фреймворк, редактор

## 1. Common dialogs (reusable)

Все живут в `view/commons/dialog/`. Все наследники `CiteckDialog`, открываются через `CiteckDialog.showDialog()`, добавляются в глобальный `activeDialogs` список. Все модальные, без OS title bar. Стэкуются.

### 1.1 `ConfirmDialog`

Файл: `view/commons/dialog/ConfirmDialog.kt`.

**Params** `ConfirmDialogParams(title="Are you sure?", message, width=SMALL_2, onConfirm, onCancel={})`.

**Layout**: Title (опционально) + `Text(message, fontSize=1.2em)` + buttons `"Cancel"` (left) + spacer + `"Confirm"` (right).

**API**:
```kotlin
ConfirmDialog.show(params)
ConfirmDialog.showSuspended(message): Boolean          // true=confirmed
ConfirmDialog.showSuspended(title, message): Boolean
```

### 1.2 `ErrorDialog`

Файл: `view/commons/dialog/ErrorDialog.kt`.

**Два варианта**:
- `prepareParams(error)` — root cause, до 10 строк стектрейса (упрощённые class names).
- `prepareParamsWithoutTrace(error)` — только `localizedMessage` root cause (или class simple name).

**Layout**: Title `"Exception occurred"`. Сообщение в `SelectionContainer` (copyable). Width: `MEDIUM` (no trace) или `EXTRA_LARGE` (with trace). Buttons: spacer + `"Export System Info"` (`SystemDumpUtils.dumpSystemInfo` в `AppDir/reports`) + `"Close"`.

**API**:
```kotlin
ErrorDialog.show(error, onClose={})
ErrorDialog.showSuspend(error): Unit  // resume after close
ErrorDialog.doActionSafe(action, errorMsg="", onSuccess={})
    // запускает suspend block, на failure показывает ErrorDialog, на success вызывает onSuccess
    // CancellationException — игнорится
```

**Gotcha**: исключения, брошенные ВНУТРИ `onSuccess` callback'а, **тоже** ловятся и показываются как `ErrorDialog` — с log-сообщением `"onSuccess failed. ..."`. Re-implementer, который надеется на чистое разделение success vs failure, будет удивлён — `onSuccess` тоже под защитой. (`ErrorDialog.kt:41-47`)

### 1.3 `MessageDialog`

Params: `GlobalMsgDialogParams(title, text, width=MEDIUM)`.

Layout: Title (if not blank) + `Text(bodyMedium, onSurfaceVariant)` + spacer + `"Ok"` button. Enter — также закрывает.

API: `MessageDialog.show(...)` — fire-and-forget.

### 1.4 `LoadingDialog`

Params: optional `ActionStatus.Mut` (progress tracker).

Layout: width `SMALL`. `Box` 130dp height, content centered. Text `"Please, wait..."` 1.2em padding 30dp start/end. Если есть status и message — вторая строка: `"(${progressInPercent}%) ${message}"`. **No buttons**.

```kotlin
val close: () -> Unit = LoadingDialog.show(status)
try { ... } finally { close() }
```

Никаких кнопок — caller **обязан** вызвать close lambda.

### 1.5 `AskMasterPasswordDialog`

Params: `AskMasterPwdParams(onSubmit: (CharArray) -> Boolean, onReset: () -> Unit)`. `onSubmit` возвращает `false` чтобы оставить диалог открытым (wrong password).

**Trigger**: lazy, на первое обращение к `authSecretsService.getSecret()` в жизни процесса — НЕ на старте приложения. Типичные триггеры:
- Git pull workspace repo с `AuthType.TOKEN` (вызывается из `setWorkspace` в `App()`)
- Docker pull где registry требует BASIC auth
- Любой другой запрос к ранее сохранённому секрету (например, повторное чтение лицензии из `SecretsStorage`)

Фьюрит когда: master password **существует** на диске, но в session memory ещё не cached. После успешного ввода password остаётся в памяти до завершения процесса.

Width: `MEDIUM` (700dp) — default, явно не задаётся в `dialog {}`. (`AskMasterPasswordDialog.kt:30`)

Layout:
- Title `"Enter your personal master password"`
- `OutlinedTextField` single-line, password masking, show/hide toggle, immediate focus
- Enter — вызывает `onSubmit`
- Buttons:
  - **"Reset Master Password and Drop All Secrets"** (left) — открывает nested `ConfirmDialog("All your secrets will be deleted from local storage")`. На confirm — `onReset()` и закрыть.
  - spacer
  - **"Confirm"** (right) — `onSubmit(charArray)`. Закрыть только если return true.

Только callback-based, suspending варианта нет.

### 1.6 `CreateMasterPwdDialog`

Params: `CreateMasterPwdParams(onSubmit: (CharArray) -> Boolean)`.

**Trigger**: lazy, на первое обращение `authSecretsService.getSecret()` когда **master password ещё не создан** (свежая установка, либо после Reset Master Password). Триггеры те же что для `AskMasterPasswordDialog`, но фьюрится create-вариант если на диске нет stored encrypted-storage envelope.

Width: `MEDIUM` (default).

Layout:
- Title `"Create your personal master password"`
- Info: `"This password will be used to protect your secrets used by the launcher."`
- Spacer 20dp
- Два `OutlinedTextField` (password + confirm), show/hide toggle, 10dp spacer между. **Enter в любом из двух полей** триггерит `executePopupAction("Create master pwd -> Enter press") { onSubmit() }` — то есть полная submit-validation сразу же. (`CreateMasterPwdDialog.kt:84-87`)
- Spacer + `"Confirm"` button

**Validation (inline)**: passwords match (`MessageDialog("Passwords do not match!")`); not blank (`MessageDialog("Password is empty!")`).

API:
```kotlin
CreateMasterPwdDialog.show(params)
CreateMasterPwdDialog.showSuspend(): CharArray
```

### 1.7 `GitPullErrorDialog`

Params: `(repoUrl, errorMessage, skipAvailable, cancelAvailable, onSubmit: (GitPullRepoDialogRes) -> Unit)`.

Layout:
- Title `"Git Repo Pull Failed"`
- Error message (0.8em, left)
- Spacer 10dp
- Repo URL
- Spacer 10dp
- Advisory:
  - `skipAvailable=true` → `"You can skip this pull or try again"`
  - иначе → `"You can't skip this step because repo doesn't cloned before"`
- Buttons:
  - **"Cancel"** (if cancelAvailable) → `CANCEL`
  - spacer
  - **"Skip Pulling"** (if skipAvailable) → `SKIP_IF_POSSIBLE`
  - **"Try Again"** (всегда) → `REPEAT`

API:
```kotlin
GitPullErrorDialog.show(params)
GitPullErrorDialog.showSuspend(...): GitPullRepoDialogRes
```

### 1.8 Registry Credentials Dialog (Docker pull auth)

Триггерится не из UI напрямую, а из `AppImagePullAction` когда Docker registry возвращает 401 (или image repo сконфигурён с `AuthType.BASIC` в `WorkspaceConfig.imageReposByHost`). Диалог рендерится `AuthSecretsService.getSecret()` — implementation в restricted-package `core/secrets/auth/`, но trigger semantics полностью наблюдаемы из `AppImagePullAction.kt:155-178`:

**Trigger conditions**:
- `lastError is RepoUnauthorizedException` (прошлая попытка вернула 401), ИЛИ
- `WorkspaceConfig.imageReposByHost[host]?.authType == AuthType.BASIC` (registry заранее помечен basic-auth).

**Cancel semantics**:
- User dismisses dialog → `AuthenticationCancelled` exception → `AppImagePullAction.getRetryAfterErrorDelay` returns `-1` → pull aborts entirely, no retries. App status → `PULL_FAILED`.

**Wrong-password retry**:
- `RepoUnauthorizedException` re-thrown с bumped `secretVersion`. Action рестартится immediately (`retryDelay = 0`), `secretDef = null` принудит re-prompt на следующей итерации.

**Stored secret format**: `SecretDef(authId = "images-repo:<host>", type = AuthType.BASIC)` → `AuthSecret.Basic(username, password)`. После успешного логина secret cached в encrypted storage; следующие pulls с того же host идут без prompt'а.

**Field layout** (минимально наблюдаемо): username + password fields (из `AuthSecret.Basic` структуры). Confirm/Cancel кнопки. Точный layout — в restricted коде.

**Web-порт**: при 401 в pull endpoint API возвращает 401 с указанием `host`; UI открывает credentials modal; submit вызывает endpoint типа `POST /api/secrets/registry/{host}` с creds; retry pull.

---

## 2. `AppCfgEditWindow`

Файл: `view/dialog/AppCfgEditWindow.kt`.

**Не window-class сам по себе** — `object` (singleton), фабрика-координатор. Делегирует на `EditorWindow` для рендеринга.

**Use cases**:

1. `show(appDef: ApplicationDef): AppEditResponse?` — сериализует `ApplicationDef` в YAML (`Yaml.toString(appDef)`), открывает редактор с filename `"app-def.yml"`. На submit десериализует обратно. Returns `AppEditResponse(appDef, locked=true)` или `null` (Reset pressed).

2. `show(filename, content): FileEditResponse<String>?` и `show(filename, content, conv: (String) -> T): FileEditResponse<T>?` — open any file content with caller-supplied conversion/validation.

**Buttons (вставляются в EditorWindow's buttons row)**:
- **"Reset"** → resume с `null`, закрыть окно.
- **"Cancel"** → resume с `FormCancelledException`, закрыть.
- **"Submit"** → если `NOOP_CONV`: `ctx.validate()` (YAML/JSON parse check). Потом `conv(ctx.getText())`, resume с результатом, закрыть. Parse errors → `ctx.showError(e)` без закрытия.

**Cancellation semantics**: `null` означает Reset (вернуть default config), `FormCancelledException` — true cancel.

---

## 3. Snapshots dialogs

### 3.1 `SnapshotsDialog`

Файл: `view/dialog/SnapshotsDialog.kt`.

**API**: `showSuspended(params: Params): Boolean`. Params содержат `NamespaceRuntime` и `WorkspaceServices`.

**Layout**: width `MEDIUM`. Две секции рендерятся через `renderSnapshots()`:

**Workspace Snapshots** (только если non-empty) — из `workspaceConfig.snapshots`. Колонки: Name, Size, Actions. Created столбца нет (created=EPOCH для глобальных). Action: только import (cube-transparent SVG).

**Namespace Snapshots** — локальные .zip в `<namespace-dir>/snapshots/`. Sorted by creation time desc. Колонки: Name, Created (`HH:mm dd.MM.yyyy`), Size, Actions.

**Per-row Actions (namespace snapshots only)**:
- **Pencil**: `CreateOrEditSnapshotDialog.showEdit(name)`, переименовывает файл, перезагружает.
- **Cube-transparent (import)** — проверки идут по порядку (`SnapshotsDialog.kt:204-212`):
  1. **Если ns НЕ stopped** → `MessageDialog(title="Namespace is running", text="You should stop namespace before import snapshot", width=SMALL)`, возврат. Никакой ConfirmDialog после.
  2. Иначе если есть volumes → `ConfirmDialog("Current namespace $nsName has active volumes. Do you want to delete existing volumes and import selected snapshot?")`.
  3. Validates snapshot, `LoadingDialog` with `ActionStatus`, удаляет volumes, импортит, `MessageDialog("Import completed"/"Nothing imported")`.
- **Delete**: `ConfirmDialog("Are you sure to delete snapshot '${name}'?")`, удаляет файл.

**Import workspace snapshot — отличается от namespace snapshot import**: cube-transparent на workspace-row сначала запускает скачивание (`workspaceService.snapshotsService.getSnapshot(it.id, loadingStatus)` за `LoadingDialog` с % прогресса — HTTP fetch + SHA-256 verify) **до** проверок ns-stopped и volume-confirm. На SHA-256 mismatch — `validateSnapshot` throws → `doActionSafe` catch → `ErrorDialog`. (`SnapshotsDialog.kt:325-336`)

**Bottom bar**:
- **"Cancel"** — close, `onClose(false)`.
- **"Create Snapshot"** — enabled только при STOPPED. `CreateOrEditSnapshotDialog.showCreate(dir, defaultName)`, экспорт через `dockerApi.exportSnapshot` с XZ compression, `LoadingDialog` с прогрессом.
- **"Open NS Directory"** — `Desktop.getDesktop().open(snapshotsDir.toFile())`.

**Size display**: `(file.length * 10 / 1024 / 1024).roundToInt() / 10f` → `"X.X mb"`.

### 3.2 `CreateOrEditSnapshotDialog`

Файл: `view/dialog/CreateOrEditSnapshotDialog.kt`.

**Two modes**:
- `showCreate(baseDirPath, name): String`
- `showEdit(name): String`

Cancel → empty string.

**Layout**: width `MEDIUM`. Title `"Create New Snapshot"` / `"Edit Snapshot"`. Label `"Snapshot name"`. `OutlinedTextField` prefilled.

**Validation on submit**:
- `.zip` суффикс автоматически отрезается.
- Regex `[\\w-.]+` — `MessageDialog(title="Invalid snapshot name", text="Name '$name' doesn't allowed\nPlease, enter name using characters, digits, dots, dash or underscore.")`.
- В create: проверка `baseDirPath/$name.zip` не существует. Иначе `MessageDialog(title="Already exists", text="Snapshot with this name already exists. Please, enter other name.")`.

**Buttons**:
- **"Cancel"** → `""`
- **"Create"** / **"Save"** (mode-dependent) — enabled if non-blank.

---

## 4. Форм-фреймворк

Кастомный, тщательно документируем для решения: оставить server-side, портировать на JSON schema, или переписать.

### 4.1 `FormSpec`

Файл: `view/form/spec/FormSpec.kt`.

```kotlin
data class FormSpec(
    val label: String,                     // dialog title; empty = не рендерится
    val width: Width,                      // SMALL=600dp, MEDIUM=800dp, LARGE=1000dp
    val components: List<ComponentSpec>
) {
    fun forEachField(action) { /* iterates ComponentSpec.Field<*> только */ }
}
```

### 4.2 `ComponentSpec`

Файл: `view/form/spec/ComponentSpec.kt`.

Sealed hierarchy:

```
ComponentSpec
├── NonField
│   ├── Button(text, onClick: suspend (FormContext) -> Unit)
│   └── Text(text)
└── Field<T>(key, label, defaultValue, valueType)
    ├── IdField                                                   // key="id", label="Identifier", String
    ├── TextField(key, label, placeholder, defaultValue)
    │   ├── NameField                                             // key="name", label="Name"; mandatory + length≤50
    │   ├── PasswordField(key, label, placeholder)
    │   ├── SelectField(key, label, defaultValue,
    │                   options: (FormContext) -> List<Option>,
    │                   onManualUpdate: ((FormContext) -> Unit)?)
    │   └── JournalSelect(key, label, entityType: KClass<*>, multiple: Boolean)
    └── IntField(key, label, defaultValue: Long)
```

Каждый `ComponentSpec`:
- `visibleConditions: MutableList<(FormContext) -> Boolean>` — `visibleWhen(cond)`. Все условия должны пройти.
- `dependsOn: MutableSet<String>` — keys полей, при изменении которых пересчитать видимость.

Каждый `Field<T>`:
- `enabledConditions` — `enableWhen(cond)`.
- `validations: MutableList<(FormContext, T?) -> String>` — empty string = valid, иначе message.

Встроенные validations:
- `IdField`: not null/blank; length ≤ 30; `EntityIdType.String.isValidId()`. Автоматически disabled в EDIT mode.
- `NameField`: not null/blank; length ≤ 50.

**Jackson annotations**: `@JsonTypeInfo` + `@JsonSubTypes` на sealed class. Registered: `"text"` (TextField), `"password"` (PasswordField). Остальные subtypes — программные only.

### 4.3 `FormContext`

Файл: `view/form/FormContext.kt`.

Lifetime: одна инстанция per `FormDialog.render()` через `remember {}`. Содержит:
- `values: ConcurrentHashMap<String, MutableState<Any?>>` — reactive per-field state.
- `fieldsByKey: HashMap<String, ComponentData>` — `ComponentData(field, valid: MutableState<String>)`.
- `onChangedListeners: ConcurrentHashMap<String, MutableList<(String, Any?) -> Unit>>` — listeners для change cascade.

API:
- `setValue(key, value)` — обновить, run validations, fire listeners.
- `getInvalidFields(): Map<String, String>` — label→message пары для всех invalid.
- `getValues(): DataValue` — snapshot всех значений как JSON-tree.
- `mode: FormMode` — для conditions/validations (IdField проверяет CREATE vs EDIT).
- `workspaceServices: WorkspaceServices?` — доступ компонентам что нужно service-связи.

### 4.4 `FormMode`

```kotlin
enum class FormMode { CREATE, EDIT }
```

VIEW mode нет.

### 4.5 `FormDialog`

Файл: `view/form/FormDialog.kt`. Наследник `CiteckDialog`.

Multiple `show()` overloads принимают `LauncherServices`, `WorkspaceServices`, или `EntitiesService`. Suspending вариант резюмит с `DataValue` или кидает `FormCancelledException`.

**Rendering** (`renderComponents`, line 238): для каждого component — `Row`. Если `Field`, label в `LimitedText` ширины `dialogWidth * 0.3f` (left). Виджет занимает остальное.

**`renderComponent`** (line 256):
- Visibility reactive: `MutableState<Boolean>` через `remember`, подписан на `dependsOn` через `formContext.listenChanges`. Если `false` — ничего не рендерится.
- `IdField`, `NameField` → `OutlinedTextField`, single-line, fillMaxWidth.
- `IntField` → `OutlinedTextField` keyboard=Number, parsed Long.
- `TextField` → `OutlinedTextField`, optional placeholder.
- `PasswordField` → `OutlinedTextField` password masking, show/hide toggle; Enter → `formContext.submit()`.
- `Button` → Material3 `Button`.
- `SelectField` → `SelectComponent`.
- `JournalSelect` → `JournalSelectComponent`.
- `Text` → `MaterialTheme.typography.bodyLarge` + `onSurfaceVariant`.

**Submit flow**: `formContext.submit()` → `getInvalidFields()`. Если non-empty: `MessageDialog(title="Invalid form fields:", text=...)` где text — каждая ошибка в формате `"$label: $message"`, joined через `"\n"` (`FormDialog.kt:182-184`). `label` — это field's display label (не key). Если valid: `params.onSubmit(getValues()) { closeDialog() }` на platform thread.

**Dialog buttons**: `"Cancel"` (left, `onCancel()` + close), `"Confirm"` (right, `submit()`). **Confirm всегда visually enabled** (за исключением активной async-операции через `actionsEnabled`). НЕТ real-time field highlighting или red-border — невалидные поля surfacятся ТОЛЬКО через MessageDialog summary после клика Confirm.

**Invisible field + mandatory gotcha**: когда `visibleWhen` возвращает false, поле не рендерится, но его текущее значение в `FormContext.values` НЕ очищается, и validations всё равно выполняются на submit. Mandatory invisible field с пустым значением заблокирует submit (`"<label>: ..."` в MessageDialog), хотя поле невидимо. Form authors должны гарантировать что invisible mandatory fields имеют valid defaults. (`FormDialog.kt:263-272`; `FormContext.getInvalidFields` итерирует без visibility check.)

**Width mapping** — важно: 600/800/1000dp от `FormSpec.Width` используется ТОЛЬКО для вычисления ширины label-колонки (`dialogWidth * 0.3f`). Сама `Card` диалога рендерится через `dialog {}` без явного `width` параметра — значит всегда `DialogWidth.MEDIUM` = **700dp**. Re-implementer не должен делать `Card width = 600/800/1000dp` — это сломает layout. (`FormDialog.kt:217-235`)

### 4.6 `SelectComponent`

Файл: `view/form/components/select/SelectComponent.kt`.

Только single-select. Data source: `component.options.invoke(formContext)` — функция, опции могут зависеть от значений других полей. `formContext.listenChanges(component.dependsOn)` триггерит `updateOptions()` при изменении dependencies. Auto-selects first option если current не в списке.

**Auto-select каскад**: auto-selection вызывает `formContext.setValue(key, firstOption.value)`, что в свою очередь fires весь change-listener chain. Поля, которые `dependsOn` этого селекта, рефрешатся снова. Возможен каскад refresh'ей (например, смена `bundlesRepo` → auto-reset `bundleKey` → если `bundleKey` dep'ит другие поля, они тоже обновятся). (`SelectComponent.kt:19-37`)

Рендерится через `CiteckSelect` — кастомный dropdown открывающий `PopupInWindow` под селектором. Popup — scrollable `Column` max height = 8 items × item height. **Currently selected value excluded** from dropdown list. Non-mandatory — показывают clear (×) icon.

Опциональный `onManualUpdate` callback: если задан — иконка refresh (`arrow-path.svg`) рядом с select. Click → `onManualUpdate(formContext)` на platform thread за `LoadingDialog`, потом refresh options.

### 4.7 `JournalSelectComponent` + `JournalSelectDialog`

**`JournalSelectComponent`** (`view/form/components/journal/JournalSelectComponent.kt`):
- Рендерит current selection как `LimitedText` labels (или `"(No Value)"` primary color).
- Кнопка `"Select"` — открывает `JournalSelectDialog`.

**`JournalSelectDialog`** (`view/form/components/journal/JournalSelectDialog.kt`):

Generic entity-picker. Не paged в текущей реализации — вызывает `entitiesService.getAll(entityType)` и грузит все записи в `DataTable`. "Journal" в этой кодбазе = generic entity list/picker.

`Params`:
- `entityType: KClass<*>`
- `selected: List<EntityRef>` — pre-checked
- `multiple: Boolean`
- `entitiesService: EntitiesService`
- `closeWhenAllRecordsDeleted: Boolean = false` — auto-close если таблица пустеет
- `selectable: Boolean = true` — если false, нет чекбоксов, кнопка `"Close"` вместо `"Cancel"`
- `columns: List<JournalColumn> = [JournalColumn.NAME]` — каждый column = `(name: String, property: String, sizeMin: Dp, sizeMax: Dp)`. **`JournalColumn.NAME` default = `("Name", "name", 500.dp, 500.dp)`** — фиксированная 500dp колонка. (`JournalSelectDialog.kt:332`)
- `customButtons: List<JournalButton>` — caller-injected buttons; `JournalButton(label, enabledIf, loading)`. Если `loading = true` — диалог автоматически оборачивает action в `LoadingDialog.show()` (`JournalSelectDialog.kt:280-291`).

Title: `"Select " + entitiesService.getTypeName(entityType)`.

**Table behavior**:
- Колонки в header: (опционально) Checkbox-плейсхолдер → пользовательские columns (с bold-text label'ами) → `"Actions"` (bold). Header-checkbox **disabled и не интерактивен** (`Checkbox(false, onCheckedChange = {}, enabled = false)`) — это чисто визуальное выравнивание с data-rows, НЕ select-all. (`JournalSelectDialog.kt:160`)
- Per-row чекбоксы — только если `selectable=true`. В single-select check одной строки снимает все остальные.
- `"name"` cell — двойной клик: в single-select submit'ит немедленно; в multi-select только ставит галку.
- Actions column: entity-defined icon actions через `CiteckIconAction`. **`defaultEntities` (например, `WorkspaceDto.DEFAULT`) рендерятся БЕЗ edit/delete actions** — у них пустой actions list, в отличие от пользовательских записей у которых стандартные edit (pencil) + delete (trash) добавляются автоматически.

**Actions column**: всегда рендерится (даже если у entity 0 actions). Для default-entities (как `WorkspaceDto.DEFAULT`) cell — пустой `Row`, column остаётся видимой но пустой. Иконки actions рендерятся в `Row` через `CiteckIconAction`. (`JournalSelectDialog.kt:172-178, 239-249`)

**Buttons**:
- `"Cancel"` / `"Close"`
- `"Create"` (если `entitiesService.isEntityCreatable(type)`) → `entitiesService.create(type)` → open create form → refresh. **После успешного create**: в single-select mode созданная entity автоматически отмечается checkbox'ом, прошлая selection очищается; в multi-select mode — добавляется к существующему набору. **Confirm всё равно требуется** (auto-submit не происходит). (`JournalSelectDialog.kt:83-105`)
- Custom buttons
- spacer
- `"Confirm"` (если selectable)

Returns `List<EntityRef>` (callback или suspending).

---

## 5. `EditorWindow`

Файл: `view/editor/EditorWindow.kt`. Наследник `CiteckWindow`.

API: `EditorWindow.show(filename, text, onClose, buttonsRow)`.

### 5.1 Что редактирует

Любой текст. Синтаксис определяется по расширению:

| Ext | Syntax |
|---|---|
| `yml`, `yaml` | YAML |
| `kt` | Kotlin |
| `java` | Java |
| `js` | JavaScript |
| `json` | JSON |
| `lua` | Lua |
| `Dockerfile` | Dockerfile |
| `sh` | Unix shell |
| иное | None (plain text) |

### 5.2 Editor engine

`RSyntaxTextArea` (Swing, через `SwingPanel`). Настройки:
- Code folding enabled
- Anti-aliasing enabled
- Tab size 2
- Theme: VS из classpath `org/fife/ui/rsyntaxtextarea/themes/vs.xml`
- Font: JetBrainsMono-Regular.ttf @ size 14
- Scrollbar custom (`RoundedScrollBarUI`): 8dp wide/tall, no arrows, rounded thumb.

### 5.3 Window size + title

1200dp wide × 900dp tall, clamped to 90% screen size (detected по position относительно main window). Centered.

**Заголовок OS-окна**: дефолтный `CiteckWindow.title = "Citeck Launcher"` (`CiteckWindow.kt:93`). EditorWindow его НЕ переопределяет — то есть в title bar всегда `"Citeck Launcher"`, а имя редактируемого файла (`app-def.yml` и т.п.) внутри окна не отображается. (Re-implementer на вебе может улучшить — показать filename в заголовке вкладки/модала.)

### 5.4 Search bar (top row, 25dp)

Inline text field (min 300dp) + `"Next"` + `"Prev"`. **Ctrl+F фокусирует поле И селектит весь его текст** (`searchText.value = searchText.value.copy(selection = TextRange(0, length))` — `EditorWindow.kt:151-157`); следующий ввод сразу заменяет старый запрос. Enter в поле — forward search. `markAll=true` (highlight всех matches в RSTA). `searchWrap=true`.

### 5.5 Shortcuts

- Ctrl+Z — undo (built-in RSTA)
- Ctrl+Shift+Z — redo (`textArea.redoLastAction()`)

### 5.6 Buttons row

Предоставляется caller'ом через lambda `(EditorContext) -> Unit`. `EditorContext`:
- `closeWindow()`
- `getText(): String`
- `setText(text: String)`
- `validate()` — parse согласно syntax (YAML → `Yaml.read`, JSON → `Json.read`); throws.
- `showError(e: Throwable)` — `ErrorDialog` с `prepareParamsWithoutTrace`.

### 5.7 Read-only

Не реализовано. `RSyntaxTextArea` always editable. Caller может `textArea.isEditable = false` сам, но текущих usage'ей нет.

### 5.8 Nested dialogs

Когда `dialogs.isNotEmpty()`, `SwingPanel` заменяется на пустой `Box` (AWT/Compose layering conflicts).

---

## 6. Что трудно портировать

1. **Форм-фреймворк** — на JSON schema: `visibleConditions`/`enabledConditions` лямбды → декларативные predicate-expressions; `options: (FormContext) -> List<Option>` → static list ИЛИ server endpoint принимающий current form state; `validations` → JSON Schema validators или custom rule language; `dependsOn` для reactive re-eval → field-watch subscriptions в React.

2. **Password dialogs** — `CharArray` интерфейс позволяет caller'у обнулять память. В браузере OS-keyring недоступен из JS. Варианты: Web Credential Management API, derived key в session storage, или master password на сервере. `onSubmit: (CharArray) -> Boolean` → async server round-trip.

3. **Modal stacking**: глобальный `activeDialogs` поддерживает stacking. Браузер `<dialog>` тоже поддерживает nested, но backdrop/focus-trap нужно explicitly per layer. `LoadingDialog` без кнопок — нужен server-sent close или polling.

4. **EditorWindow** — `RSyntaxTextArea` нет аналога в браузере. Замена: CodeMirror 6 или Monaco. Оба поддерживают YAML/JSON/shell/Dockerfile. VS theme аппроксимируется light CodeMirror theme. `validate()` → CodeMirror linting plugin + server-side YAML/JSON validation.

5. **SnapshotsDialog** — `dockerApi.exportSnapshot` (XZ ZIP) и `importSnapshot` server-side. `LoadingDialog` progress (0-100%) → SSE/WS push для browser. `"Open NS Directory"` → download link.

6. **JournalSelectDialog** — `entitiesService.getAll(type)` синхронный, loads all in memory. В вебе → paginated REST. Double-click submit маппится на row `ondblclick`. `customButtons` → action-buttons slot.

7. **`AppCfgEditWindow` cancellation semantics**: `null` (Reset) vs `FormCancelledException` (Cancel) — preserve через explicit `{ "action": "reset" | "cancel" | "submit", "content": "..." }` API.
