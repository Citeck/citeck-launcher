# 07 — Git, Database, Snapshots

## 1. Git

### 1.1 `GitRepoService` API

`git/GitRepoService.kt` — единственный public метод:

```kotlin
fun initRepo(
    repoProps: GitRepoProps,
    updatePolicy: GitUpdatePolicy,
    cancelAvailable: Boolean
): GitRepoInfo
```

Полностью идемпотентный "ensure-in-sync": clone если не present, pull если pull period истёк, иначе skip.

### 1.2 `GitRepoProps`

`git/GitRepoProps.kt`:

| Field | Type | Описание |
|---|---|---|
| `path: Path` | local clone dir |
| `url: String` | remote git URL |
| `branch: String` | branch name |
| `pullPeriod: Duration` | минимум между pull'ами |
| `authId: String` | key для `AuthSecretsService` |
| `authType: AuthType` | `NONE`, `TOKEN`, `BASIC` |

### 1.3 `GitRepoInfo`

`git/GitRepoInfo.kt`:

| Field | Type | Описание |
|---|---|---|
| `root: Path` | local clone root (= `repoProps.path`) |
| `hash: String` | SHA1 most recent commit |
| `nextUpdateMs: Long` | `lastSyncTimeMs + pullPeriod.toMillis()` |

### 1.4 `GitUpdatePolicy`

`git/GitUpdatePolicy.kt`:

| Value | Поведение |
|---|---|
| `REQUIRED` | Always pull/clone, regardless of pull period |
| `ALLOWED` | Pull если period истёк |
| `ALLOWED_IF_NOT_EXISTS` | Только clone если не present; никогда не pull existing |
| `DISABLED` | Error если repo не существует; никогда не clone/pull |

### 1.5 Clone behavior

- JGit `Git.cloneRepository()`:
  - `setDepth(1)` — shallow
  - `setNoTags()`
  - 15-секундный timeout
- Single branch: `setBranchesToClone(["refs/heads/<branch>"])`
- После clone — store `GitRepoInstance(props, now, lastCommitHash)` в `Repository<String, GitRepoInstance>` под `("git-repo", "instances")` keyed by relative path from `AppDir.PATH`.

### 1.6 Pull behavior

- `Git.open(dir)`.
- Hard reset to `origin/<branch>` перед pull.
- Pull с `MergeStrategy.THEIRS` и `ContentMergeStrategy.THEIRS` (local changes ВСЕГДА discarded).
- 15-секундный timeout.
- Update `lastSyncTimeMs` + `lastCommitHash` в repo instance.

### 1.7 Re-clone trigger

`isRepoShouldBeRecreated`: fire если `url` или `branch` изменились с last init. Удаляет local dir и stored instance перед re-clone.

### 1.8 Credential resolution

Для `authType != NONE`:
```
authSecretsService.getSecret(SecretDef(authId, authType), url, resetSecret)
```
Wrap'ается в JGit `UsernamePasswordCredentialsProvider`:
- token → `("", token)`
- basic → `(username, password)`

### 1.9 Error handling + retry loop

`initRepo` wrap'ает `initRepoImpl` в retry loop с feedback:
- До `FEEDBACK_REPEATS_COUNT = 5` silent retries на auth failures перед показом dialog'а.
- `GitPullErrorDialog` (UI) предлагает: **Retry**, **Skip** (если repo синхронился раньше), **Cancel**.
- **Skip** — устанавливает `updatePolicy = ALLOWED_IF_NOT_EXISTS` для этого call'а и записывает host в `skipPullForRepoDecisionAt` на 1 час (subsequent calls с того же host тоже skip'ятся).
- **Cancel** — `throw GitPullCancelledException(url)`.
- 1-секундный sleep между non-auth-failure retries.

### 1.10 Где clones живут

Все под `AppDir.PATH`:
- Workspace repo: `<AppDir>/ws/<workspaceId>/repo/`
- Bundle repos: `<AppDir>/ws/<workspaceId>/bundles/<repoId>/`

---

## 2. Database

### 2.1 Engine

**H2 MVStore** (не H2 SQL!). Файл: `<AppDir>/storage.db`. (Legacy путь был `storage3.db`; migrated automatically.) Opened с `.compress()`. Store — single-file B-tree с MVCC.

### 2.2 `DataRepo`

`database/DataRepo.kt`:
- Extends `Repository<String, DataValue>`.
- Override `get(id)` — возвращает `DataValue.NULL` вместо `null` на miss.
- Override `set(id, value: Any)` — принимает any object, wrap'ит в `DataValue.of(value)`.

Используется для untyped key-value (workspace state, launcher state, cloud config cache).

### 2.3 `Repository` interface

`database/Repository.kt`:

| Method | Поведение |
|---|---|
| `set(id, value)` | upsert |
| `get(id)` | null on miss |
| `delete(id)` | no-op если absent |
| `find(max)` | до `max` values в insertion order |
| `getFirst()` | first value или null |
| `forEach(action: (K, T) -> Boolean)` | iteration; stops когда action returns `true` |

### 2.4 `RepoImpl` serialization

Values — `ByteArray` (JSON bytes от Jackson). Keys — native:
- `String` keys → `StringDataType.INSTANCE` от H2.
- `Long` keys → `LongDataType.INSTANCE`.

MVStore maps — `TransactionMap<String|Long, ByteArray>`.

### 2.5 Transaction model

`Database.transactionContext: ThreadLocal<TxnContextImpl>`:

- `doWithinTxn { ctx -> ... }` — если txn уже active на thread, join; иначе start new.
- `doWithinNewTxn { ctx -> ... }` — всегда start новую (nested) txn (до 10 levels deep перед warn+skip для after-txn actions).
- `getTxnContext()` — active context или `EmptyTxnContext`.

`TxnContext` hooks:
- `doBeforeCommit(action)` — перед commit
- `doAfterCommit(action)` — в новой nested txn после commit
- `doAfterRollback(action)` — в новой nested txn после rollback

`EmptyTxnContext` (singleton): `doBeforeCommit` + `doAfterCommit` fire immediately; `doAfterRollback` — no-op. Используется когда нет active txn (config reads).

### 2.6 Open transaction recovery

На startup `database.init()` сканит open (uncommitted) transactions от prior crash и rollback'ит их.

### 2.7 Schema evolution

**Нет migration framework**. Все values — Jackson JSON blobs; schema implicit в Kotlin data classes. Forward compat зависит от Jackson lenient defaults (unknown fields игнорятся, missing — use defaults). Explicit migrations:
1. Legacy file rename: `storage3.db` → `storage.db`.
2. Workspace state alias: `DEFAULT` → `default`.

### 2.8 Logs (core-side)

`logs/LogbackConfigurator.kt`:
- Registered как Logback `Configurator` с `@ConfiguratorRank(CUSTOM_TOP_PRIORITY)`. Loaded через `ServiceLoader` из `META-INF/services/ch.qos.logback.classic.spi.Configurator`.
- Returns `ExecutionStatus.DO_NOT_INVOKE_NEXT_IF_ANY` — no other configurator runs.
- Root level INFO.
- Two appenders: `CONSOLE` + `logfile`.

**Console**: stdout, pattern `%d{yyyy-MM-dd'T'HH:mm:ss.SSS,GMT+0} [%thread] %-5level %logger{36} - %msg%n`, UTF-8.

**File**: `RollingFileAppender` + `TimeBasedRollingPolicy`:
- Active: `<AppDir>/logs/logfile.log`
- Rolled pattern: `<AppDir>/logs/logfile-%d{yyyy-MM-dd}.log.zip` (gzip-compressed)
- `maxHistory = 5` (5 дней rolled files)
- `totalSizeCap = "50 mb"` (cumulative)

`logs/AppLogUtils.kt`:
- `getAppLogFilePath()` → `<AppDir>/logs/logfile.log`
- `watchAppLogs(action: (String) -> Unit): AutoCloseable` — Apache Commons IO `Tailer` на active log file, на virtual thread. `action` lambda получает каждую новую line. Caller обязан close.

Используется UI log viewer для display live launcher logs.

---

## 3. Snapshots

### 3.1 Two-level metadata

**`NamespaceSnapshotMeta`** (`snapshot/NamespaceSnapshotMeta.kt`) — top-level snapshot running namespace:

```kotlin
data class NamespaceSnapshotMeta(
    val volumes: List<VolumeSnapshotMeta>,
    val createdAt: Instant
)
```

**`VolumeSnapshotMeta`** (`snapshot/VolumeSnapshotMeta.kt`) — per-Docker-volume record:

```kotlin
data class VolumeSnapshotMeta(
    val name: String,        // Docker volume name
    val rootStat: String,    // stat fingerprint volume root; empty = unknown
    val dataFile: String     // filename compressed archive внутри snapshot ZIP
)
```

### 3.2 `WorkspaceSnapshots`

`snapshot/WorkspaceSnapshots.kt`:

- Initialized `WorkspaceServices.init`.
- Единственный public method: `getSnapshot(snapshotId, status): Promise<Path>`.

**Download flow**:
1. Lookup `snapshotId` в `workspaceConfig.snapshots` (`List<Snapshot>` с `id, name, url, size, sha256`).
2. Local cache path: `<AppDir>/ws/<workspaceId>/snapshots/<snapshotId>.zip`.
3. Если файл существует — verify SHA-256. Match → return cached. Mismatch → rename old в `<name>_outdated_<datetime>.zip` и re-download.
4. **Download — resumable ranged HTTP GET**:
   - Chunk size: 10 MB
   - Ktor CIO client
   - Timeouts: connect 10s, socket 5 min, request 6 min
5. Write в `.part` файл; atomic `Files.move` в final path при completion.
6. Verify SHA-256 после download; mismatch → error.
7. **Retry loop**:
   - До `REPEATS_LIMIT_TOTAL = 100` total retries
   - В "progress window" — до `REPEATS_LIMIT_WITHOUT_PROGRESS = 3` retries без какого-либо progress
   - `REPEAT_DELAY_MS = 3000` между retries
   - Progress трекается через `@Volatile Double`

`Promise<Path>` resolved на platform thread `"snapshot-loader"`. Virtual thread `"download-status-updater"` обновляет `ActionStatus.progress` каждую секунду из `DownloadProgress` volatile.

**Нет create/delete/restore API в этом классе**. Это download-and-cache only. Реальный Docker volume restore (apply downloaded zip к containers) делается namespace runtime через `DockerApi.importSnapshot` (см. §09).

### 3.3 Snapshot zip формат

(детали в `DockerApi` и `WorkspaceSnapshots`)

```
snapshot.zip
├── meta.json                    # NamespaceSnapshotMeta serialized
├── volumes/
│   ├── postgres2.tar.xz         # per-volume compressed archive
│   ├── rabbitmq2.tar.xz
│   └── ...
```

Compression: XZ (через docker-utils container, см. §09).

---

## 4. Что важно для портёра

### Git → go-git

1. `JGit` → `github.com/go-git/go-git/v5`. Маппинг:
   - `Git.cloneRepository()` → `git.PlainClone(path, false, &git.CloneOptions{ ... })` с `Depth: 1`.
   - `Git.open()` → `git.PlainOpen(dir)`.
   - Hard reset → `worktree.Reset(&git.ResetOptions{ Mode: git.HardReset, Commit: <hash> })`.
   - Pull MERGE_STRATEGY_THEIRS → в go-git complicated; обычно после reset просто `Fetch` + `Reset` на `FETCH_HEAD`.
2. Per-repo sync state persist'ить в БД (url, branch, last sync time, last commit hash). 
3. Host-level pull suppression на 1 hour после user cancel — replicate как in-memory map.

### Database → SQLite or bbolt

4. H2 MVStore (embedded JVM) → SQLite или bbolt:
   - **SQLite** (`modernc.org/sqlite` pure-Go или `mattn/go-sqlite3` cgo): мапит KV pattern на `(scope TEXT, key TEXT, value BLOB)` с composite PK.
   - **bbolt** (pure Go BoltDB): архитектурно ближе к MVStore.
5. Transaction model с `doWithinTxn` / `doAfterCommit` hooks — реплицировать через `database/sql` `Tx` + deferred callbacks.
6. **Нет schema migrations**, если сохраним same JSON-blob-per-entity стратегию.

### Snapshots

7. Resumable download с SHA-256 verification + `.part` → final rename — straightforward в Go с `net/http` Range requests.
8. Retry logic (3 retries without progress, 100 total, 3s delay) — replicate exactly.
9. Workspace-level snapshots — URLs из workspace config; namespace-level snapshots — local .zip в `<ns-dir>/snapshots/`.

### Logs

10. Logback rolling file → Go `lumberjack` library:
    ```go
    logger := &lumberjack.Logger{
        Filename:   "<AppDir>/logs/logfile.log",
        MaxBackups: 5,
        MaxSize:    50,  // mb
        Compress:   true,
    }
    ```
11. `watchAppLogs` для UI log viewer — server-side tail (Tailer pattern); push в SSE channel который UI клиент консюмит.

### Mapping table

| Kotlin | Go |
|---|---|
| `GitRepoService` | `internal/git/service.go` |
| `JGit` | `github.com/go-git/go-git/v5` |
| `H2 MVStore` | SQLite (`modernc.org/sqlite`) или bbolt |
| `Repository<K, V>` | generic Go interface, sqlite-backed |
| `DataRepo` | shorthand для `Repository[string, DataValue]` |
| `TxnContext` | `database/sql.Tx` + slice of after-commit callbacks |
| `LogbackConfigurator` | `slog` + `lumberjack` |
| `WorkspaceSnapshots.getSnapshot` | `internal/snapshot/downloader.go` |
| `AppLogUtils.watchAppLogs` | server-side tail + SSE endpoint |
