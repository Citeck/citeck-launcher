# Plan: Secrets Encryption + Desktop Bug Fixes

## Context

Go launcher v2 хранит секреты **plaintext** в SQLite. Kotlin v1 шифровал AES-256-GCM + PBKDF2-HMAC-SHA256 (1M iterations). Критическая регрессия безопасности.

---

## P0: Шифрование секретов

### Архитектура

```
  API handlers ────> SecretService ──── derived key (in memory, volatile)
                     Encrypt/Decrypt
                     Lock/Unlock
                         │
                     storage.Store ──── SQLiteStore (desktop) / FileStore (server)
                     (raw read/write)
```

- `SecretService` — обёртка над `Store`, держит derived key в `sync.RWMutex`-protected поле
- Desktop: каждый секрет хранится как `base64(IV || AES-GCM(value))` в колонке `value` таблицы `secrets`
- Server: passthrough (без шифрования, file perms 0o600 достаточно)
- `Store` интерфейс НЕ меняется

### Криптография

- AES-256-GCM
- PBKDF2-HMAC-SHA256, **1M iterations** (как Kotlin)
- 16-byte random salt (хранится в `launcher_state`)
- 12-byte random IV per secret (prepended к ciphertext)
- Каждый секрет шифруется индивидуально — без перешифровки всех при изменении одного

### Состояние в `launcher_state`

| Key | Value |
|-----|-------|
| `secrets_encrypted` | `"true"` или отсутствует |
| `secrets_key_params` | JSON: `{"salt":"base64","iterations":1000000,"keySize":256}` |
| `secrets_verify` | `base64(IV + AES-GCM("citeck-secrets-v1"))` — проверка пароля без расшифровки секретов |

### API

- `GET /api/v1/secrets/status` → `{ encrypted: bool, locked: bool }`
- `POST /api/v1/secrets/unlock` → `{ password }` — derive key, validate через verify token
- `POST /api/v1/secrets/setup-password` → `{ password }` — первичная установка, шифрует plaintext в транзакции

Без Lock API в v1 (нет UX-смысла — залочишь → bundle сломается).

### Поведение при locked

- Daemon стартует нормально, UI доступен
- `GetSecret()` → `ErrSecretsLocked` → token lookup пустой → `bundleError`
- Dashboard: overlay "Введите мастер-пароль"
- После unlock: rebuild token lookup + registry auth, retry bundle + pull-failed apps

### Миграция Kotlin → Go encrypted

**Объединённый flow (одна сессия, один диалог):**
1. Dashboard: `hasPendingSecrets` (Kotlin blob) → диалог "Введите мастер-пароль"
2. User вводит Kotlin пароль → decrypt blob → import secrets
3. **Сразу** предлагаем: "Использовать этот же пароль для защиты?" с кнопками [Да] / [Задать другой]
4. При [Да] → `SetMasterPassword` с тем же паролем → шифруем все в транзакции
5. При [Задать другой] → ещё одно поле ввода → `SetMasterPassword`

**Fresh install:** prompt появляется при первом добавлении секрета, не раньше.

### НЕ меняется

- `Store` interface
- `FileStore` (server, без шифрования)
- `SQLiteStore` (raw read/write, шифрование в слое выше)
- Kotlin migration flow (as-is, но расширен шагами 3-5)
- `ChangeMasterPassword` — defer (Kotlin тоже не имел)

### Файлы

| Файл | Действие |
|------|----------|
| `internal/storage/crypto.go` | Новый: SecretService, DeriveKey, EncryptValue, DecryptValue |
| `internal/storage/crypto_test.go` | Новый: round-trip, wrong password, plaintext→encrypted |
| `internal/daemon/server.go` | Mod: SecretService field, wiring в makeTokenLookup/registryAuth |
| `internal/daemon/routes_p2.go` | Mod: secret handlers через secretService; endpoints unlock/setup/status |
| `web/src/lib/api.ts` | Mod: getSecretsStatus, unlockSecrets, setupPassword |
| `web/src/pages/Dashboard.tsx` | Mod: объединённый unlock/migration dialog |
| `web/src/pages/Secrets.tsx` | Mod: "Set Password" при !encrypted, lock indicator |

---

## P1: Desktop баги

### 1. LogViewer пуст при первом открытии
**Файл:** `web/src/components/LogViewer.tsx`
**Проблема:** `follow=true` → REST fetch skip → stream без данных → пусто
**Фикс:** Fetch initial logs по REST при mount, затем follow stream

### 2. SSE через Wails proxy буферизуется
**Файл:** `cmd/citeck-desktop/main.go`
**Проблема:** `httputil.ReverseProxy` буферизует SSE chunks
**Фикс:** `FlushInterval: -1` на proxy transport

### 3. TCP listener не нужен в desktop mode
**Файлы:** `internal/daemon/server.go`, `cmd/citeck-desktop/main.go`
**Проблема:** Proxy через TCP, Unix socket доступен
**Фикс:** Proxy через Unix socket, skip TCP listener в desktop mode

### 4. Bottom panel stale data при collapse/expand
**Файл:** `web/src/components/BottomPanel.tsx`
**Фикс:** Reconnect stream при expand (active=true)

### 5. DaemonLogsViewer polling → streaming
**Файл:** `web/src/components/DaemonLogsViewer.tsx`
**Проблема:** `setInterval(5000)` polling
**Фикс:** Follow-stream как в LogViewer

### 6. Citeck logo для tray и window icon
**Файлы:** `cmd/citeck-desktop/main.go`, `icons/logo.png` (уже есть в репо)
**Проблема:** Дефолтные Wails icons
**Фикс:** Embed `icons/logo.png`, использовать для `app.Icon` и `tray.SetIcon`

---

## Implementation Order

1. P0 Crypto layer — crypto.go + тесты
2. P0 Daemon integration — SecretService wiring, degraded mode
3. P0 API endpoints — unlock/setup/status
4. P0 Web UI — объединённый dialog, Secrets page
5. P1 #6 — Citeck logo (быстро)
6. P1 #1 — LogViewer first-open
7. P1 #2 — SSE proxy flush
8. P1 #3 — Unix socket proxy
9. P1 #4-5 — Bottom panel + DaemonLogs

## Verification

- `go test ./internal/storage/...` — encrypt/decrypt, wrong password, plaintext→encrypted
- `npx vitest run`
- Manual: Kotlin migration → same password → encrypted. Restart → unlock → secrets work
- Manual: `sqlite3 launcher.db "SELECT value FROM secrets"` → base64 ciphertext only
- Manual: fresh install → add secret → prompt set password → encrypted
- Manual: логи с первого клика, SSE через proxy, Citeck иконки
