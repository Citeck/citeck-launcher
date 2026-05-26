# 06 — Entity framework, Secrets, License

## 1. Entity Framework

Кастомный embedded CRUD framework, который вместо schema-migration'а сериализует всё как JSON blobs. Самая нетривиальная часть для портирования.

### 1.1 `EntityDef<K, T>`

`entity/EntityDef.kt` — type descriptor, параметризуется по `K` (id type) и `T` (value type):

| Field | Описание |
|---|---|
| `idType: EntityIdType<K>` | `Long` или `String` |
| `valueType: KClass<T>` | Kotlin class сущности |
| `typeName: String` | Human-readable (`"Workspace"`) |
| `typeId: String` | Machine id; используется как DB scope key и как первый сегмент `EntityRef` (`"workspace"`) |
| `getId: (T) -> K` | Извлечь id |
| `getName: (T) -> String` | Извлечь display name |
| `getCustomProp: (T, String) -> DataValue` | Опционально; для arbitrary attributes (используется table columns) |
| `createForm: FormSpec?` | Form в "create" dialog; `null` = non-creatable |
| `editForm: FormSpec?` | Form в "edit"; если null и createForm != null — createForm используется и для edit |
| `defaultEntities: List<T>` | Pre-seeded read-only entities (default workspace); не хранятся в БД, но возвращаются queries |
| `actions: List<ActionDef<T>>` | Доп. context actions помимо built-in edit/delete |
| `customRepo: Repository<K, T>?` | Override DB repo (используется `VolumeInfo` — читает live из Docker) |
| `toFormData: (T?) -> DataValue` | Serializer для pre-populating edit form (default Jackson) |
| `fromFormData: (DataValue) -> T` | Deserializer из submitted form data (default Jackson) |
| `versionable: Boolean` | Если `true`, previous value сохраняется в versions repo перед update/delete (до `HISTORY_MAX_COUNT = 10` versions) |

### 1.2 `EntityIdType`

`entity/EntityIdType.kt` — sealed class, два singleton'а:

**`EntityIdType.Long`**:
- ids — `kotlin.Long`
- Empty sentinel = `-1L`
- Valid range: `>= 0`
- New ids — auto-incrementing counter в `Repository<String, Long>` под `("entities-service", "idCounters")`, keyed by `typeId`. Counter стартует с 100.

**`EntityIdType.String`**:
- ids — `kotlin.String`
- Empty = `""`
- Valid pattern: `[\w-.:$@]+`
- New ids — `IdUtils.createStrId` (short random alphanumeric; switch на longer strings после 10 collisions).

### 1.3 `EntityRef`

`entity/EntityRef.kt`:
- Wire format: `"<typeId>@<localId>"` (serialized through `@JsonValue` / `@JsonCreator`)
- Examples: `"workspace@default"`, `"namespace@my-ns"`, `"auth-secret@ws:default:repo"`
- `EMPTY` = `EntityRef("", "")`
- `isEmpty()` = both parts blank

### 1.4 `EntityInfo`

`entity/EntityInfo.kt` — read-side envelope, возвращаемый всеми query methods:

```kotlin
data class EntityInfo<T>(
    val ref: EntityRef,
    val name: String,                                  // display name
    val getCustomProp: (String) -> DataValue,          // property accessor
    val actions: List<ActionDesc<EntityInfo<T>>>,     // standard edit/delete + type-specific
    val entity: T                                      // raw entity
)
```

### 1.5 `EntitiesService` CRUD

`entity/EntitiesService.kt`:

**Registry**: `ConcurrentHashMap<KClass<*>, EntityDefWithRepo>` + `ConcurrentHashMap<String, EntityDefWithRepo>`. Все регистрации идут в обе мапы. `register(EntityDef)` создаёт `Repository` (или использует custom), создаёт versions repo, wrap'ает в `EntityDefWithRepo`.

**Lookup chain**: каждый `getDefWithRepo` сначала checks workspace-scoped service; если не найдено — делегирует на `launcherEntities` (global `EntitiesService` инициализированный в `LauncherServices`). Так workspace-scoped entity types (namespace, bundle, snapshot) сосуществуют с launcher-wide (workspace, auth secret).

**Scope naming**: db scope = `"entities/<workspaceId>"` (с legacy fallback на uppercase для `"default"` и `"global"`).

**`create`** (async, form-driven):
1. Показывает `createForm` в `FormDialog`.
2. На submit: определяет id (из form data если present + valid, иначе генерит).
3. `database.doWithinTxn { repo[id] = entity; events.fireEntityCreatedEvent(...) }`.
4. `onSubmit(EntityRef)`.

**`createWithData`** (synchronous, no form):
Программное использование. Генерит id если empty, валидит id, проверяет duplicate, store, fire event.

**`edit`** (async, form-driven):
1. Сериализует current entity в `DataValue` через `toFormData`.
2. Показывает `editForm` (или `createForm`) в `FormDialog` mode=`EDIT`.
3. На submit: `storeValueToVersions` если versionable, потом `repo[id] = newEntity`, fire `EntityUpdatedEvent`.

**`delete`**:
`storeValueToVersions` если versionable, потом `repo.delete(id)`, fire `EntityDeletedEvent`, всё внутри txn.

**`find(type, max)`**: `defaultEntitiesInfo` (always in-memory, no DB) ++ DB results (до `max`).

**`getAll(type)`**: то же что `find` но limit = `100_000`.

**`getById` / `getByRef`**: сначала `defaultEntitiesByRef` (O(1) map lookup), потом DB.

### 1.6 Events

`entity/events/EntitiesEvents.kt`:
- Три `ConcurrentHashMap<KClass<*>, CopyOnWriteArrayList<listener>>` — для created/updated/deleted.
- `addEntityCreatedListener`, `addEntityUpdatedListener`, `addEntityDeletedListener` — все возвращают `Disposable`.
- `fireEntityCreatedEvent` etc — синхронно вызывают всех listeners для типа; если listener возвращает `Promise<*>`, all promises собираются в `Promises.all(...)`.
- Каждый `Event` несёт уникальный `UUID id`.

**Зарегистрированные listeners** на startup:
- `WorkspacesService` слушает `EntityCreatedEvent<WorkspaceDto>` — клонит/pull'ит workspace repo.
- `WorkspacesService` слушает `EntityDeletedEvent<WorkspaceDto>` — удаляет workspace directory.

### 1.7 Persistence

Все entities — в H2 MVStore (см. 07). Каждый entity type — свой `TransactionMap`, keyed by `"<scope>!<typeId>"`. Values — JSON bytes (Jackson). Versions — отдельный map `"<scope>/versions!<typeId>"` хранит `List<T>` (до 10 previous versions).

### 1.8 Кто регистрирует entity types

- `WorkspacesService.init` — `WorkspaceEntityDef.definition` на launcher-level И workspace-level `EntitiesService`.
- `LauncherServices.initBase` — `authSecretsService.getSecretEntityDef()` на launcher-level.
- `WorkspaceServices.init` — `getVolumeEntityDef()` (с `customRepo = VolumesRepo`) на workspace-level; потом `bundlesService.init`, `namespacesService.init` регистрируют свои типы (`NamespaceConfig`, etc.).

### 1.9 ActionDef (entity actions)

`entity/EntityActionDef.kt`:

```kotlin
data class EntityActionDef<T>(
    val icon: ActionIcon,
    val description: String,
    val action: suspend (T) -> Any,
    val enabledIf: (T) -> Boolean = { true }
)
```

Добавляются как третий column в `JournalSelectDialog` через `EntityInfo.actions`. Built-in edit + delete всегда есть, эти добавочные — type-specific (например, у `AuthSecret` — refresh, у `Snapshot` — restore).

---

## 2. Secrets

Часть `core/secrets/` имеет access-restricted (auth/storage subpackages). Описание ниже — reconstructed из cross-references в non-restricted файлах.

### 2.1 `AuthType`

Известные значения (из `WorkspaceEntityDef.kt` и `git/GitRepoService.kt`):
- `AuthType.NONE` — no authentication
- `AuthType.TOKEN` — token-based (показывается в workspace form)
- `AuthType.BASIC` — username/password (используется в `AppImagePullAction.kt:165`)

### 2.2 `AuthSecret`

Sealed class с минимум двумя subtypes:
- `AuthSecret.Token(token: String)` — для Git repos с `AuthType.TOKEN`; маппится на JGit `UsernamePasswordCredentialsProvider("", token)`
- `AuthSecret.Basic(username: String, password: String)` — для image registries с `AuthType.BASIC`; маппится на Docker `AuthConfig`

### 2.3 `SecretDef`

```kotlin
data class SecretDef(val authId: String, val authType: AuthType)
```

Declarative requirement: pair stable storage key (`authId`) + expected auth type. Используется чтобы запросить credentials у `AuthSecretsService`.

**Известные `authId` patterns**:
- `"ws:<workspaceId>:repo"` — workspace git repo
- `"images-repo:<host>"` — Docker registry для image pull

### 2.4 `AuthSecretsService`

API:
- `init(services: LauncherServices)`
- `getSecret(secretDef, context, resetSecret): AuthSecret` — retrieves stored credentials; если `resetSecret=true` или нет stored — prompt user через UI dialog
- `getSecretEntityDef()` — `EntityDef` для управления через UI

### 2.5 `SecretsStorage`

Initialized с `database` в `LauncherServices.initBase`. Используется и `LicenseService` и `AuthSecretsService`.

API:
- `put(key, value)` — store encrypted
- `getList(key, KClass)` — retrieve JSON list (используется для `"licenses"`)

### 2.6 Encryption

- `KeyParams` — key derivation parameters (salt, iteration count). Likely PBKDF2.
- `SecretsEncryptor` — AES-GCM encrypt/decrypt.
- `EncryptedStorage` — on-disk envelope format.

### 2.7 Master password

`AuthSecretsService` использует `SecretsStorage` который зависит от encryption. Master password у пользователя запрашивается через `AskMasterPasswordDialog` (см. 03 §1.5) при первом обращении; в случае утери — `CreateMasterPwdDialog` с reset flow.

Cached в process memory; cleared при close (детали в restricted code).

---

## 3. License

### 3.1 `LicenseService`

`license/LicenseService.kt`:

- Initialized `WorkspaceServices.init` через `licenseService.init(launcherServices)`.
- **Storage**: licenses — JSON list в `SecretsStorage` под key `"licenses"`.
- **Lazy load**: первый `getLicenses()` триггерит read из `SecretsStorage.getList("licenses", LicenseInstance::class)`. Дальнейшие calls — in-memory list (guarded `AtomicBoolean`).
- `hasValidEntLicense()` — `true` если any license проходит `LicenseInstance.isValid()`.
- `addLicense(license)` — append in-memory + write full list back в `SecretsStorage`.

### 3.2 `LicenseInstance` fields

`license/LicenseInstance.kt`:

| Field | Type |
|---|---|
| `id` | `String` |
| `tenant` | `String` |
| `priority` | `Long` |
| `issuedTo` | `String` |
| `issuedAt` | `Instant` |
| `validFrom` | `Instant` |
| `validUntil` | `Instant` |
| `content` | `DataValue` — arbitrary feature flags / module grants (opaque JSON object) |
| `signatures` | `List<LicenseSignature>` |

**Дата сериализация**: `Instant` → ISO-8601; `T00:00:00Z` суффикс strip'ится для date-only licenses.

**`isValid()`**:
1. `signatures.isNotEmpty()`.
2. `validFrom <= now <= validUntil`.
3. `checkSign(getContentForSign(), sig)` для каждой signature; `true` если any passes.

**`getContentForSign()`**: serialize license в JSON, remove `"signatures"` key, потом **normalize all object keys в лексикографический порядок на every nesting level** перед конвертацией в bytes. Эта canonical form — что signed.

### 3.3 `LicenseSignature`

`license/LicenseSignature.kt`:

| Field | Type | Описание |
|---|---|---|
| `time` | `String` | ISO timestamp; добавляется в signed data |
| `issuer` | `String` | Expected X.500 issuer DN signing certificate |
| `signature` | `ByteArray` | Raw signature bytes |
| `certificates` | `List<ByteArray>` | DER-encoded X.509 chain |

**Verification**:
1. Первый certificate через `CertificateFactory.getInstance("X.509")`.
2. Issuer DN проверяется против `citeckSignature.issuer`.
3. `Signature.getInstance(signCert.sigAlgName)` — алгоритм определяется самим сертификатом (на практике RSA + SHA-256 или похожее).
4. Data signed = `getContentForSign()` bytes ++ `signature.time.toByteArray(UTF_8)`.

### 3.4 License source

Loadable из:
1. `WorkspaceConfig.licenses` (embedded в workspace YAML)
2. `LicenseService.addLicense` runtime call — persisted в `SecretsStorage` независимо от workspace config.

После `addLicense` — survives application restarts.

### 3.5 Behavior on missing / invalid / expired

`hasValidEntLicense()` returns `false`. Callers (namespace runtime + UI) gate enterprise features. **Нет hard shutdown**; UI показывает feature gates.

---

## 4. Что важно для портёра

### Entity framework

1. **Type registry** — mapping type-id strings на typed descriptors (CRUD callbacks, form schema).
2. **Form schema представление** — `FormSpec` + `ComponentSpec` это subset JSON Schema. Чистейший Go equivalent: form как JSON Schema (или custom struct) → отправляется browser UI; UI рендерит generically.
3. **Event bus** — три типа events (created/updated/deleted) с listener registration. В Go: `sync.Map` of `[]func(event)` per type-id. Fire синхронно в той же goroutine (либо через channel для async).
4. **Version history** — до 10 previous versions per entity, secondary KV namespace.
5. **Default entities** — pre-seeded read-only (default workspace) которые returned by queries но не stored в DB. Должны в type descriptor.
6. **ID generation** — auto-increment for `Long` (counter в DB); short random alphanumeric для `String`.

### Secrets

7. На сервере (Go) — secrets server-side, не в browser. Варианты:
   - OS keyring через `go-keyring` (per-platform: macOS Keychain, Linux Secret Service, Windows Credential Manager)
   - AES-GCM encrypted файл с master password
   - Dedicated secrets manager (Vault, 1Password SDK)
8. **Browser UI никогда не получает raw secrets** — только flag "secret set" + triggers re-entry flows.
9. **Три `AuthType` values** — keep enum как есть, маппятся на Git/registry types directly.

### License

10. `LicenseService` — есть в Kotlin, **не найден явно в Go 2.x**. Спецификация должна определить:
    - Inscope или out-of-scope для 2.x?
    - Если inscope: signature algorithm, validation алгоритм, где в startup sequence гейтится.
11. Canonical signing form (lexicographic key ordering на every nesting level) — критично для interoperability с уже-выписанными лицензиями.
12. Hardcoded issuer DN — нужно сохранить как константу, либо параметризовать через конфиг.

### Mapping table

| Kotlin | Go |
|---|---|
| `EntitiesService` | `internal/entity/service.go` |
| `EntityDef` | `EntityDef` struct + registration |
| `EntityRef` | `EntityRef` struct |
| `AuthSecretsService` | `internal/secrets/service.go` |
| `SecretsStorage` | `internal/secrets/store.go` (encrypted FileStore) |
| `SecretsEncryptor` | crypto/aes + crypto/cipher (AES-GCM) |
| `LicenseService` | TBD (см. gap analysis) |
