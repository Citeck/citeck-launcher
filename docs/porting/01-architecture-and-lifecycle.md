# 01 — Архитектура, AppDir, точка входа, lifecycle

## 1. Стек

- **Язык**: Kotlin 2.x, target JVM 21 (Java 21)
- **UI**: Jetbrains Compose Desktop (Skiko + Material 3)
- **Сборка**: Gradle (Kotlin DSL)
- **Docker**: docker-java (`com.github.dockerjava` + Apache HTTP transport)
- **Git**: JGit
- **БД**: H2 MVStore (embedded, NOT SQL-режим)
- **Сеть**: Ktor (CIO client + CIO server)
- **Сериализация**: Jackson (JSON), SnakeYAML Engine (YAML)
- **Шифрование секретов**: AES-GCM (SecretsEncryptor — детали в `core/secrets/storage/`)
- **Tray на Linux fallback**: GTK через JNA (libgtk-3, libglib-2.0)
- **Редактор кода**: RSyntaxTextArea (Swing, через SwingPanel внутри Compose)

## 2. Layout сорсов

```
src/main/kotlin/ru/citeck/launcher/
├── Main.kt                              ← entry point
├── core/                                ← бизнес-логика, без UI
│   ├── LauncherServices.kt              ← top-level IoC, lazy init
│   ├── LauncherStateService.kt          ← selectedWorkspace persistence
│   ├── WorkspaceServices.kt             ← workspace-scoped IoC
│   ├── actions/                         ← ActionsService (queue + executor)
│   ├── appdef/                          ← ApplicationDef и связанные типы
│   ├── bundle/                          ← BundleDef / BundleKey / BundleRef / BundlesService
│   ├── config/                          ← AppDir + CloudConfig (port 8761)
│   ├── database/                        ← H2 MVStore wrappers
│   ├── entity/                          ← generic CRUD framework
│   ├── git/                             ← GitRepoService
│   ├── license/                         ← LicenseService + signature verification
│   ├── logs/                            ← LogbackConfigurator + AppLogUtils
│   ├── namespace/                       ← NamespaceRuntime / Generator / Docker integration
│   ├── secrets/                         ← AuthSecretsService + EncryptedStorage
│   ├── snapshot/                        ← WorkspaceSnapshots (download with resumable HTTP)
│   ├── socket/                          ← IPC for single-instance
│   ├── utils/                           ← AppLock, MutProp, Promise, DataValue, ...
│   └── workspace/                       ← WorkspaceDto/Config/Service
└── view/                                ← Compose UI
    ├── theme/LauncherTheme.kt
    ├── tray/                            ← AWT + GTK system tray
    ├── screen/                          ← Welcome / Loading / Namespace / DockerNotAvailable
    ├── dialog/, commons/dialog/         ← диалоги
    ├── form/                            ← форм-фреймворк
    ├── popup/                           ← CiteckPopup / Dialog / Window / Kind
    ├── table/                           ← DataTable + DSL
    ├── logs/                            ← LogsWindow / LogsViewer / ...
    ├── editor/EditorWindow.kt           ← RSyntaxTextArea wrapper
    ├── select/CiteckSelect.kt
    ├── action/                          ← ActionDesc / ActionIcon / IconBtn / CiteckIconAction
    ├── drawable/CpDrawable.kt           ← classpath SVG loader
    └── commons/                         ← ContextMenu / Tooltip / LimitedText / PopupInWindow
```

## 3. Layout на диске (`AppDir`)

`AppDir.PATH` (см. `core/config/AppDir.kt`):

| OS | Path |
|---|---|
| Linux/Unix | `$HOME/.citeck/launcher/` |
| macOS | `$HOME/Library/Application Support/Citeck/launcher/` |
| Windows | `%LOCALAPPDATA%\Citeck\launcher\` |

Полное дерево:

```
{AppDir}/
├── app.lock                              # текст: "{PID}|{socketPort}"; держится NIO file-lock'ом
├── storage.db                            # H2 MVStore (key-value, JSON-bytes как значения)
├── icons/tray.png                        # сгенерированный из logo.svg
├── logs/
│   ├── logfile.log                       # активный
│   └── logfile-YYYY-MM-DD.log.zip        # ротация: maxHistory=5, totalSizeCap=50mb
├── ws/{workspaceId}/                     # workspace-scoped дерево
│   ├── repo/                             # JGit clone, depth=1; содержит workspace-v1.yml
│   ├── bundles/{repoId}/                 # JGit clones bundle-репов
│   ├── snapshots/{snapId}.zip            # кэш скачанных снапшотов
│   └── ns/{namespaceId}/
│       ├── rtfiles/                      # runtime-файлы (generated + user-edited)
│       └── reports/...                   # дампы system-info по требованию
└── reports/YY-MM-DD_HH-mm/                # глобальные дампы (sysinfo, thread-dump, logs)
    └── launcher-dump_{datetime}.zip
```

**MVStore map naming** (в `storage.db`):

| Map key | Content |
|---|---|
| `launcher!state` | `{ "selectedWorkspace": "<wsId>" }` |
| `workspace-state!{wsId}` | `{ "selectedNamespace": "<nsId>" }` (legacy fallback на верхний регистр для `DEFAULT`) |
| `global!{typeId}` | глобальные сущности: `workspace`, `auth-secret` |
| `{wsId}!{typeId}` | ws-scoped сущности: `namespace`, `volume`, ... |
| `entities/{wsId}/versions!{typeId}` | история (до 10 версий для versionable entities) |
| `git-repo!instances` | состояние клонов: `{path → {props, lastSyncTime, lastCommitHash}}` |

## 4. Точка входа: `Main.kt`

### Фаза 0 — до Compose

Файл `src/main/kotlin/ru/citeck/launcher/Main.kt`. JVM вызывает `MainKt.main()`:

1. `StdOutLog.info(...)` (Logback ещё не активен; пишем в stdout с timestamp).
2. `AppLock.tryToLock()` — захват single-instance lock'а (см. §6).
3. Если ошибка при lock'е — она сохраняется в `tryToLockError`, Compose всё равно стартует и покажет `ErrorDialog`.

Logback конфигурируется через `META-INF/services/ch.qos.logback.classic.spi.Configurator` → `LogbackConfigurator.kt`:
- Root level: INFO
- Console appender (stdout) + RollingFileAppender (`{AppDir}/logs/logfile.log`)
- Pattern: `%d{yyyy-MM-dd'T'HH:mm:ss.SSS,GMT+0} [%thread] %-5level %logger{36} - %msg%n`
- Ротация: time-based, `maxHistory=5` дней, `totalSizeCap=50 mb`, gzip rolled-файлы.

### Фаза 1 — Compose `application {}` (`Main.kt:72+`)

- Tray init **в отдельном `Thread.ofPlatform()`** (не блокирует EDT) → `CiteckSystemTray.initialize(...)` → `traySupported: AtomicBoolean`.
- `WindowState(width=1200dp, height=800dp, position=centered)`.
- Логотип: `classpath:logo.svg` → `decodeToSvgPainter` (используется как window icon и для tray PNG).
- Версия: `classpath:build-info.json` (генерится Gradle'ом) → подмешивается в `title = "Citeck Launcher v${version}"`.
- Мин. размер окна: 300×400 (через `LaunchedEffect(Unit)` после первого фрейма).

### Фаза 2 — Window создан, первый фрейм = `LoadingScreen()`

`servicesValue.value` ещё `null`. В фоне:
- `Thread.ofPlatform()` строит `LauncherServices` через `initBase()` и `initDocker()`.

### Фаза 3 — `LauncherServices.init()` (`core/LauncherServices.kt`)

`initBase()` — однократный (под `@Volatile baseInitDone`):

| # | Шаг |
|---|---|
| 1 | `database.init()` — открыть `{AppDir}/storage.db`, MVStore с compression, rollback orphaned txn |
| 2 | `launcherStateService.init(this)` — читает `selectedWorkspace` |
| 3 | `secretsStorage.init(database)` |
| 4 | `entitiesService.init(this)` — scoped to `GLOBAL_WS_ID="global"` |
| 5 | `authSecretsService.init(this)` |
| 6 | `gitRepoService.init(this)` |
| 7 | `workspacesService.init(this)` |
| 8 | `entitiesService.register(authSecretEntityDef)` |
| 9 | `cloudConfigServer.init()` — Ktor CIO на **порту 8761** (на ошибке только лог, не abort) |
| 10 | JVM shutdown hook: `workspaceServices.dispose()` + `cloudConfigServer.dispose()` |

`initDocker()`:
1. Если retry — закрыть прошлый `DockerHttpClient`.
2. `DefaultDockerClientConfig.createDefaultConfigBuilder()` — читает `DOCKER_HOST`, `~/.docker/config.json`.
3. `ApacheDockerHttpClient`: `maxConnections=200`, connect-timeout 2 min, response-timeout 10 min.
4. `pingCmd()` через docker-java ping — если кидает → `DockerNotAvailableException` (помечается `isDockerNotRunning` по типу root cause: `ConnectException` / `ConnectionClosedException`).
5. `ActionsService` создаётся, регистрирует 3 экзекутора:
   - `AppImagePullAction`
   - `AppStartAction`
   - `AppStopAction`

### Фаза 4 — services готовы

`servicesValue.value = Result.success(launcherServices)`. Recomposition:
- `SystemDumpUtils.init(servicesVal)`
- `takeMainWindowFocus` lambda сохраняется (используется trayem и IPC)
- `AppLocalSocket.listenMessages(TakeFocusCommand::class) { takeMainWindowFocus() }`
- Рендерится `App(services)`.

### Фаза 5 — workspace resolve

`App(services)` запускает `Thread.ofPlatform().name("ws-svc-init")`:
1. Читает `launcherStateService.getSelectedWorkspace()`.
2. `entitiesService.getById(WorkspaceDto::class, id)` — fallback на `getFirst()` — fallback на `WorkspaceDto.DEFAULT`.
3. `services.setWorkspace(selectedWs.id)` — внутри происходит git-pull workspace-репо. На `GitPullCancelledException` — fallback на default.
4. `services.getWorkspaceServices()` — конструирует `WorkspaceServices`, `WorkspaceServices.init()` восстанавливает `selectedNamespace`.

UI:
- `selectedWorkspace.value == null` → `LoadingScreen()`
- `selectedNamespace.value == null` → `WelcomeScreen()`
- иначе → `NamespaceScreen()`

### Если Docker не поднят

`DockerNotAvailableException` → рендерится `DockerNotAvailableScreen` (`view/screen/DockerNotAvailableScreen.kt`). См. §02 для UI деталей.

Любое другое исключение в `init()` → `ErrorDialog.show(exception) { exitApplication() }`.

## 5. `LauncherServices` vs `WorkspaceServices`

| | LauncherServices | WorkspaceServices |
|---|---|---|
| Scope | Глобальный (`"global"`) | Per-workspace |
| Lifetime | Всё приложение | Создаётся при выборе workspace, dispose при смене |
| Содержит | Database, ActionsService, AuthSecretsService, EntitiesService (global), GitRepoService, CloudConfigServer, DockerApi | NamespacesService, BundlesService, EntitiesService (ws-scoped), VolumesRepo, LicenseService, WorkspaceSnapshots |
| Свитч workspace | `setWorkspace(id)` под `ReentrantLock`; persist в `launcher!state` | dispose старого, init нового |

Workspace `"global"` зарезервирован для `EntitiesService` (там живут `WorkspaceDto` и `AuthSecret`).

## 6. `AppLock` — single-instance lock

Файл: `core/utils/AppLock.kt`.

**Алгоритм** (`tryToLock()`):
1. `{AppDir}/app.lock` → `RandomAccessFile.channel.tryLock()` (non-blocking, эксклюзивный write-lock).
2. Если null → другой процесс держит → `doFallbackActions()`: читаем `{pid}|{port}` из файла, шлём `TakeFocusCommand` по локальному сокету, `exitProcess(0)`.
3. Если успех: 
   - `AppLocalSocket.run()` → `ServerSocket(0)` (OS даёт порт), запоминаем `socketPort`.
   - Перезаписываем файл строкой `{PID}|{socketPort}`.
   - Релизим write-lock, берём shared-lock (он держится до завершения процесса).
   - Регистрируем shutdown hook: shared-lock release + delete файла.

**Гарантии**: только один процесс на одном AppDir. Аварийный exit оставляет файл, но `tryLock` следующего инстанса всё равно сработает (NIO-lock освобождается ядром).

## 7. IPC (single-instance protocol)

Файлы: `core/socket/AppLocalSocket.kt` + `SocketUtils.kt`.

- **Транспорт**: TCP `ServerSocket(0)` (ephemeral port, OS назначает).
- **Address**: `127.0.0.1:{port}`; port записан в `app.lock`.
- **Wire**: custom varint encoding (1 байт для 0–127, иначе header `0x80 | (len-1) | sign` + до 4 big-endian байт).
- **Session**:
  ```
  client → server: int(0)            // API version
                    int(cmdBytes.size)
                    cmdBytes           // JSON
  server → client: int(respBytes.size)
                    respBytes          // обычно "{}"
  client → server: int(closeBytes.size)
                    closeBytes         // {"type":"close"}
  ```
- **Max message size**: 200 000 bytes.
- **Concurrency**: один поток на коннект; listener-mapping в `ConcurrentHashMap` + `CopyOnWriteArrayList`.

**Команды** (полиморфизм Jackson по `"type"`):

| `type` | Класс | Эффект |
|---|---|---|
| `"take-focus"` | `TakeFocusCommand` | Делает главное окно видимым, on top, requestFocus |
| `"close"` | `CloseConnection` | Завершает сессию (внутреннее использование) |

## 8. System tray

Файлы:
- `view/tray/CiteckSystemTray.kt` — общая логика, AWT-path
- `view/tray/CiteckTrayItem.kt` — модель пункта меню
- `view/tray/gtk/AppIndicatorApi.kt` + `GtkTrayIndicator.kt` — fallback для Linux без AWT-tray

**Алгоритм инициализации** (`CiteckSystemTray.initialize(items, lmbAction)`):
1. `SystemTray.isSupported()` → AWT tray. Возвращает `true`.
2. Иначе пытаемся `dlopen` libgtk-3 и libglib-2.0 через JNA. Если ок — GTK indicator. Возвращает `true`.
3. Иначе → tray недоступен. Возвращает `false` (логируется WARN).

**Пункты меню** (порядок и точные label'ы, из `Main.kt:45-48`):

| Label | Действие |
|---|---|
| `"Open"` | `takeMainWindowFocus?.invoke()` |
| `"Dump System Info"` | `SystemDumpUtils.dumpSystemInfo()` в новом потоке |
| `"Open Launcher Dir"` | `Desktop.getDesktop().open(AppDir.PATH.toFile())` |
| `"Exit"` | `exitApplication()` (Compose clean shutdown) |

**Иконка трея**: транскодируется из `classpath:logo.svg` через Apache Batik в PNG, кэшируется в `{AppDir}/icons/tray.png`.

- **macOS**: размер = `max(tray.size.width, 64)`, border = `size/8` прозрачный (для template-image эффекта). JVM-arg `-Dapple.awt.enableTemplateImages=true` (Light/Dark mode aware).
- **Linux AWT**: `tray.size.width`, без border.
- **Linux GTK**: 24×24 px.
- **Windows**: `tray.size.width`.

Tooltip AWT: `"Citeck Launcher"`. Состояний иконки нет (один статический PNG).

**Click handling**:
- **AWT**: `MouseListener` на TrayIcon: button 1 → `lmbAction`. RMB обрабатывается AWT-native PopupMenu.
- **GTK**: connect `"button-press-event"` на GtkStatusIcon. Byte offset 52 = button. Button 1 → `lmbAction`; 3 → `gtk_menu_popup_at_pointer`.

**GTK threading**: dedicated `Thread.ofPlatform().name("gtk-tray")` запускает `g_main_loop_run()` и не возвращается. Shutdown hook вызывает `gtk_widget_destroy` + `g_object_unref`.

## 9. Lifecycle окна

**Кнопка close главного окна** (`Main.kt:128-135`):
- Если `traySupported.get() == true`:
  - `CiteckWindow.closeAll()` (закрыть все secondary окна)
  - `windowVisible.value = false` (главное окно скрыто, JVM продолжает работать)
- Иначе: `exitApplication()` (полный shutdown).

**Восстановление из трея** (`takeMainWindowFocus`):
```kotlin
windowVisible.value = true
window.isMinimized = false
window.requestFocus()
window.toFront()
```

**Secondary windows**: `CiteckWindow` держит `mutableStateListOf<CiteckWindow>`. Каждый second window (LogsWindow, EditorWindow) — отдельный OS-Window. `CiteckWindow.renderWindows(logo)` вызывается внутри `LauncherTheme {}` (`Main.kt:228`).

**Background work**: NamespaceRuntime, Docker-операции, CloudConfigServer работают независимо от UI. Скрытое окно ≠ остановленные сервисы.

**JVM shutdown hooks** (в порядке регистрации):
1. `LauncherServices.initBase` → `workspaceServices.dispose()` + `cloudConfigServer.dispose()`. **Контейнеры НЕ останавливаются** — Docker daemon продолжает их крутить.
2. `Database` → MVStore close (commit pending).
3. `AppLock` → release shared lock + delete `app.lock`.
4. `AppLocalSocket` → interrupt listener thread + close ServerSocket.

**Crash recovery**: при следующем старте `Database.init()` детектит `openTransactions` (uncommitted) и rollback'ит их с WARN-логом.

## 10. Тема (`LauncherTheme`)

Файл: `view/theme/LauncherTheme.kt`.

- Material 3 `lightColorScheme(...)` с единственным override: `primary = Color(90, 120, 170)` (`#5A78AA`, приглушённый стальной синий).
- **Dark mode НЕ реализован**. Тоггла нет.
- Типография: дефолтная M3. Кастомные шрифты:
  - **JetBrains Mono Regular** — `resources/fonts/jetbrains/JetBrainsMono-Regular.ttf` — для EditorWindow (RSyntaxTextArea).
  - **Ubuntu Mono Regular** — `resources/fonts/ubuntu/UbuntuMono-R.ttf` — для LogsViewer.

**Drawables** (`view/drawable/CpDrawable.kt`):
- `CpImage(path)` / `CpIcon(path)` — load SVG из classpath через `CiteckFiles.getFile("classpath:$path")` → `decodeToSvgPainter`.
- Кэш байтов в process-lifetime `ConcurrentHashMap<String, ByteArray>`.
- Поддерживается ТОЛЬКО SVG; остальные форматы → `error("Unsupported file type: $path")`.

## 11. Реактивность: `MutProp`

Файл: `core/utils/prop/MutProp.kt`.

```kotlin
class MutProp<T>(initial: T) {
    @Volatile private var value: T
    private val watchers: CopyOnWriteArrayList<(T, T) -> Unit>
    private val lock = ReentrantLock()
    
    fun setValue(value: T) {
        lock.lock()
        try {
            if (value == this.value) return
            val old = this.value
            this.value = value
            watchers.forEach { it(old, value) }   // synchronous, под локом
            // update version, changedAt
        } finally { lock.unlock() }
    }
    
    fun watch(action: (T, T) -> Unit): Disposable
}
```

**Bridge в Compose** (`view/utils/ViewExtensions.kt:15-35`):

```kotlin
@Composable
fun <T> rememberMutProp(prop: MutProp<T>): MutableState<T> {
    return remember(prop) { 
        MutablePropView(prop)                    // RememberObserver
    }
    // onRemembered: subscribe watcher, then state.value <- newValue on change, recomposition
    // onForgotten: dispose watcher
}
```

Это **одностороннее** связывание: core state (`MutProp`) → Compose UI. Запись из UI в core делается явно (event handler вызывает `MutProp.setValue(...)`).

**В порте**: `MutProp` соответствует publish-subscribe на сервере (Go). В UI 2.x — SSE-канал или WebSocket, на который React-store подписывается (Zustand store sets state).

## 12. Packaging

`build.gradle.kts`. Main class: `ru.citeck.launcher.MainKt`. Class loader: standard.

**Native targets** (через `-PtargetOs=...`):

| targetOs | Skiko/Compose runtime |
|---|---|
| `linux_x64` | `compose.desktop.linux_x64` |
| `linux_arm64` | `compose.desktop.linux_arm64` |
| `linux` | оба |
| `macos_x64` / `macos_arm64` / `macos` | соответствующие |
| `windows_x64` / `windows_arm64` | соответствующие |
| `current` (default) | `compose.desktop.currentOs` |

**Native installer formats**:
- `TargetFormat.Dmg` (macOS)
- `TargetFormat.Msi` (Windows)
- `TargetFormat.Deb` (Debian/Ubuntu)

**JVM**:
- `sourceCompatibility = VERSION_21`, JVM target = JVM_21
- Heap: **`-Xmx200m`** (важно! очень тесно)
- JDK modules: `java.compiler`, `java.instrument`, `java.management`, `java.naming`, `java.scripting`, `java.security.jgss`, `java.sql`

**Метаданные**:
- `packageName = "citeck-launcher"`, vendor `"Citeck LLC"`, version из `gradle.properties`
- Linux: icon `icons/logo.png`, deb maintainer `info@citeck.ru`, category `"Utility"`
- Windows: icon `icons/logo.ico`, per-user install, MSI upgrade GUID `3fa61060-0739-4463-985e-c58d1bc4e9b2`, menu group `"Citeck Tools"`
- macOS: icon `icons/icon.icns`, category `"public.app-category.utilities"`, JVM-arg `-Dapple.awt.enableTemplateImages=true`

**`packageDist` task**: переименовывает результат в `citeck-launcher_{version}_{targetOs}.{ext}` в `build/compose/binaries/main/{dmg|msi|deb}/`.

**`build-info.json`**: генерится в `build/generated/resources/build-info.json` (`version`, `buildTime` ISO-8601 UTC, `javaVersion`), включается в JAR.

**Pre-commit hook**: `addKtlintFormatGitPreCommitHook` task устанавливает ktlint в `.git/hooks/pre-commit` при первом билде.

## 13. CloudConfigServer (порт 8761)

Файл: `core/config/cloud/CloudConfigServer.kt`. См. §05 для протокола.

**Важно для порта**: этот сервер должен быть на **точно том же порту 8761** и отдавать **байт-в-байт** совместимый Spring Cloud Config format, потому что управляемые Spring Boot контейнеры (`eapps`, `gateway`, `emodel`, ...) клиентят его при старте. Если формат сломаем — все ECOS контейнеры упадут.

## 14. Что важно для портёра

1. **AppDir layout — контракт обратной совместимости**: пути `~/.citeck/launcher/storage.db`, `~/.citeck/launcher/ws/...`, `~/.citeck/launcher/app.lock` лучше сохранить, чтобы апгрейд с 1.x на 2.x не терял состояние. (Альтернатива: migration tool.)
2. **CloudConfig 8761** — критичный protocol-level контракт; не менять.
3. **DockerLabels** (см. §09) — критичный discovery-контракт.
4. **Single-instance в вебе**: в 2.x demon — это серверный процесс, single-instance делает сам сервис через bind на TCP-порт API. Tray в браузере не нужен — окно браузера = окно приложения.
5. **MutProp → SSE**: каждый `MutProp<T>` в Kotlin соответствует одной подписке во фронте. Хороший mapping:
   - `nsRuntime.nsStatus` → SSE event `namespace.status`
   - `application.containerStats` → SSE batch `containers.stats` (мультиплексирует все app'ы)
   - `application.appRuntimeStatus` → SSE `app.status`
6. **Lifecycle сервисов**: init-порядок (см. §4, Фаза 3) — не случайный. Например, `cloudConfigServer.init()` идёт после `entitiesService` потому, что ему нужна БД для cache; и до того, как пользователь может запустить namespace.
