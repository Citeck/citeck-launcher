# MCP OAuth / SQLite: ревью избыточной сложности

## Результат последующей реализации

После аудита пользователь разрешил упрощение и завершение фичи, с ревью после полностью закрытого блока. **Реализация завершена и принята на уровне кода после независимого ревью: APPROVE WITH NOTES, незакрытых замечаний по исходникам нет.** Это не подтверждение нативного запуска или выкладки.

- Сохранены отдельная SQLite и Fosite; убраны reservations/квоты истории, unused receipt/revision/confirmation, exact-DDL fingerprints и пустые секундные commits. Обычный upgrade схемы v3 сохраняет существующие SQLite root, manual tokens, grants и refresh history. Импорт manual/settings JSON сохранён; неподдерживаемый экспериментальный OAuth JSON отклоняется без изменения источника. Минимальный marker против stale JSON fallback/missing root оставлен обоснованно.
- Завершены публичный OAuth flow и подтверждение владельцем через trusted transport; в Settings добавлены pending requests, Observer/Developer, Allow/Deny, connections/revoke и Last used. Переводы — все восемь языков. Сквозной тест проходит через реальные HTTP и Unix management handlers, а не прямое одобрение в Store.
- Полное ревью нашло три дефекта. Одним пакетом закрыты: cancellation-induced блокировка всего Store; ошибочное превращение Fosite-wrapped SQL failure в invalid_grant; отображение старых revoked-строк без timestamp как активных. Регрессии и повторное независимое ревью подтвердили закрытие всех трёх. Дополнительная архитектура не вводилась.
- До исправлений прошли полные internal race/coverage, floors, vet, pinned lint и desktop deadcode. После исправлений прошли полные race-тесты затронутых Go-пакетов, 563 frontend-теста, ESLint, TypeScript/Vite и Windows/Darwin cross checks. Повторный полный govulncheck завершился успешно; прежний OS-killed запуск не считается успешным задним числом. Это совокупность проверок двух этапов, **не новый запуск make check**.
- Нативные macOS/Windows runtime/DACL и реальный Pi/Lima flow не проверены. Известный development-only minimatch GHSA-g3hv-75wg-m249 не изменён и не подавлен. Установленный Launcher не обновлялся; staging/commit/deploy не выполнялись.

Артефакты в каталоге сессии Pi `subagent-artifacts/outputs/`: реализация `8d2d7788-1584-4b6d-aa73-636e44cc27da`, полное ревью `a3db380b-250a-4d70-a7a8-294d9fa793f4`, единый пакет исправлений `59086145-d8d8-4e6a-a0bb-48ef8737c5c0`, итоговый recheck `fee50264-2d33-4cd0-9923-24a546b87969`. Пользовательская документация: [MCP](../mcp.md).

## Исторический статус аудита

Проведены три независимых параллельных read-only ревью: SQLite, OAuth и план/процесс разработки. На момент аудита реализация была остановлена пользователем, исправления **ещё не выполнялись**. Ниже сохранены исходные выводы и рекомендации; актуальный результат исполнения приведён выше.

**Общий вывод: отдельная SQLite и существующий OAuth framework разумны. Раздуты дополнительные обязательства вокруг них. Нужно упростить текущую реализацию, а не переписывать OAuth или менять БД.**

Предыдущие code review закрыли конкретные дефекты, но не доказывали соразмерность архитектуры. Утверждение чрезмерного плана также не делало его разумным. Разрастание требований, экспериментов и согласований — ответственность оркестрации, а не пользователя.

### Область проверки

- Проверены фактические `internal/launchermcp`, связанные daemon management routes, оба плана и краткие отчёты предыдущих этапов.
- Включён незавершённый OAuth transport остановленного worker `1fa08ba4`; его нельзя считать готовой или развёрнутой функциональностью.
- В `web/src` ещё нет интеграции OAuth pending/connections в Settings. Приёмка SQL-хранилища не означает работающий Sign in.
- Пакет `internal/launchermcp` в проверенном дереве untracked. Обычный `git diff --stat` его не учитывает. Его 7 145 строк включают тесты и исходный MCP/tools/artifacts, а не только новую production-реализацию OAuth.
- Нативные Windows/macOS и реальный Pi/Lima flow не проверялись. Аудит не запускал тесты, сборки, сервисы или миграцию пользовательских данных.

## 1. Что оставить

Это необходимая корректность и безопасность, не «космолёт»:

1. Один приватный `mcp.db` с уже подключённым SQLite-драйвером; main `launcher.db`, server FileStore и vault не менять.
2. Точечные SQL-запросы и обновления, обычные ограничения целостности, исходные сроки токенов, компактная история использованных refresh-токенов.
3. Существующий selectively composed Fosite: authorization code, PKCE-S256, refresh, revoke и внутренний introspection. Не заменять его сейчас самописным OAuth или новым framework.
4. Outer SQL transaction **до** Fosite parsing/раннего удаления PKCE; не возвращать code/token до успешного commit.
5. Полная проверка credential/client/resource/expiry до обработки reuse; authenticated family revocation не должна теряться при rollback неуспешной выдачи.
6. Правдивый результат revoke: при ошибке записи не сообщать успех; сохранить live deny и небольшой retry с backoff.
7. Одобрение владельцем через доверенный Launcher transport, Observer по умолчанию и явный Developer. Browser/localhost/cookie/PKCE сами по себе не доказывают владельца.
8. Browser nonce/CSRF/Origin binding, escaped unverified client name, один completion, короткий TTL.
9. Приватные DB/sidecars, обычная политика SQLite sync, checked errors, один owner и корректный close/drain. Никакого удаления WAL для «восстановления» auth state.
10. Постоянные manual tokens, роли, Last used, stable GrantID для artifacts, disable/port invalidation и нормальный restart.

## 2. Что удалить или упростить

| Действие | Где | Минимальная замена и последствия |
|---|---|---|
| **Убрать** гарантии будущей renewable-ёмкости и резервирование слотов | SQLite-план §§5–7; `schema.sql`; `oauth_pending_schema.sql`; `oauth_pending.go`; cleanup в `oauth_transaction.go` | Независимые простые ограничения входных данных, регистрации и pending + expiry GC. Не обещать заранее зарезервированные 30 дней хранения. **Не удалять живую replay history**, чтобы уложиться в квоту. |
| **Убрать** `receipt`, `revision`, `confirmation`, если у них нет действующего потребителя | `schema.sql:2`; `oauth_transaction.go:76–112`; `sqlite_migration.go` | Обычные SQL commit/error и fail-closed при неясном исходе. Не строить reconciliation engine ради уже добавленных колонок. |
| **Исправить** пустые фоновые commits каждую секунду | `oauth_transaction.go:222–248` | Retry только для реального revoke debt; cleanup при необходимости или на редком таймере, без безусловной мутации metadata. |
| **Убрать** точный fingerprint текстов DDL/индексов | `sqlite_schema.go:11–53` | Версия схемы, транзакционные upgrades, constraints и проверки критичных данных. Вспомогательный индекс не должен становиться доказательством auth authority. |
| **Сузить** миграции до реального обязательства совместимости | `sqlite_migration.go`; legacy JSON-модель в `oauth_state.go`; `testdata/legacyreader` | Сохранить используемые manual credentials/settings. Конвертацию экспериментальных OAuth JSON/SQL checkpoint-форматов и сложные provisional/fingerprint/fence/activation-ветки удалять после выяснения фактического использования. Не сбрасывать существующие данные наугад. |
| **Упростить** pending workflow | `oauth_pending.go`; `oauth_pending_schema.sql` | Короткоживущий request → approve/deny → один completion → expiry. Не связывать browser ceremonies с резервами durable grants. Незавершённый вход может отменяться при restart. Небольшую SQL-таблицу можно оставить; переписывать её в RAM только ради принципа не нужно. |
| **Упростить** срок жизни client registration | `oauth_provider.go`; `oauth_storage.go` | Не смешивать регистрацию public client с 30-дневным grant и не создавать лишний срок, ломающий cached client IDs. Оставить cleanup действительно неиспользуемых регистраций и обычные bounds. |
| **Не возвращать** исследовательскую обвязку | Старые TEMP-программы, resource gates и quantitative части плана | Без custom I/O monitors, physical page/WAL allocator, сотен cold opens, saturation-программ и обязательных p99-профилей для этой фичи. |

Важно: упрощение лишних миграций не означает разрешение fallback на устаревшие credentials, root reset при ошибке БД или удаление пользовательских токенов. Поддержку конкретного старого формата сначала обосновываем реальным использованием.

## 3. Два конкретных дефекта и одно ошибочное требование

### A. Idle service пишет в БД без полезных изменений

Два ревью независимо проследили путь: секундный worker вызывает `oauthWrite(collectOAuth)`, а каждый успешный UOW меняет receipt/revision, даже если cleanup ничего не удалил. Worker остаётся жив после disable.

Это вывод из кода, не замер количества физических записей. Исправление простое: убрать неиспользуемые metadata mutations и не выполнять пустой maintenance commit. Не нужна новая система планирования.

### B. Возможен deadlock при shutdown частичного OAuth transport

`Service.Close` держит `s.mu`, пока ждёт `s.requests` (`server.go:188–215`). Уже допущенный browser request затем пытается получить этот же mutex в `oauthTranslator` (`oauth_browser.go:20–25,50`). Close ждёт request, request ждёт Close.

Разделить синхронизацию locale и shutdown либо не удерживать mutex во время drain. Проверить одним направленным concurrency-тестом, не вводить lifecycle framework.

### C. Излишняя «строгость» нарушает совместимость OAuth

- `adaptOAuth` отвергает все неизвестные параметры (`oauth_provider.go:78–103`). RFC 6749 §§3.1,3.2 требует игнорировать нераспознанные request parameters. Неизвестное расширение можно отбросить, продолжая проверять все известные security-поля и запрещённые дубликаты.
- `exactRedirect` требует зарегистрированный callback port (`oauth_provider.go:123–131`). Для **loopback IP redirect URI native-клиента** RFC 8252 §7.3 требует позволять порт, выбранный в момент authorization request.
- Правильная граница: standards-aware matching зарегистрированных loopback host/path при authorization, затем точная привязка code и exchange к **фактическому валидированному redirect** этой попытки. Это не разрешение arbitrary redirect и не изменение фиксированного MCP issuer/resource.

Фиксированный зарегистрированный порт был нашим ошибочным требованием плана. Тесты, закрепляющие его отказ, тоже требуют изменения. Эти пункты родитель дополнительно сверил с первичными RFC, не только с мнением reviewer.

Источники: [RFC 8252 §7.3](https://www.rfc-editor.org/rfc/rfc8252.html#section-7.3), [RFC 6749 §3.1](https://www.rfc-editor.org/rfc/rfc6749.html#section-3.1), [RFC 6749 §3.2](https://www.rfc-editor.org/rfc/rfc6749.html#section-3.2).

## 4. Минимальное целевое решение

- Один приватный SQLite-файл, существующий драйвер, один connection/mutex/owner lock, обычное версионирование схемы.
- Settings/manual tokens/root, clients/grants и необходимые Fosite code/PKCE/access/refresh indexes. Без мини-аллокатора, unused mutation receipts и exact-DDL authority.
- Небольшой pending request с TTL, выбор роли владельцем через доверенный transport, одно browser completion с nonce/CSRF.
- Стандартный flow: metadata → DCR → browser wait → trusted approval → callback → token → MCP → refresh/revoke.
- Компактный Settings UI для pending/connections, сохранённый manual-token UI и Last used. Никаких новых аккаунтов, ACL, IdP или дополнительных OAuth flows.
- Fosite и SQLite не переписывать. Упрощать обещания и собственную обвязку, а не заменять их другой платформой.

## 5. Достаточные проверки

Один сквозной happy path через **реальный trusted management handler**, а не только прямой вызов Store; deny/expiry; S256 и binding negatives; spoofed approval/nonce/CSRF; code/refresh replay и concurrent completion; failed transaction без выдачи credential; revoke/restart; manual never-expiry; disable/port/artifact identity; private-file/dump exclusion; UI role/approve/deny/error/cancel; shutdown interleaving.

Маленькие fixtures и направленные race-тесты. Обычный gate репозитория перед завершением/релизом, не после каждой строки. Native Pi/macOS/Windows проверять отдельно и честно помечать невыполненное. Ни один reviewer не предложил отменить необходимые security-тесты.

## 6. Процесс: что было лишним

- Отдельная полномасштабная TEMP-реализация до работы над фичей дублировала код и отодвинула первый рабочий Sign in.
- Самописный монитор записей компилятора стал самостоятельным источником сбоев и запросов разрешения.
- 100 cold opens на каждый размер под race измеряли инструментированный тестовый стенд; затянутый тест успел дождаться истечения своего настоящего access token.
- Архивирование сотен мегабайт generated/ignored-файлов и повторные whole-tree hashes не требовались для сохранения небольшого dirty diff.
- Отдельный worker/патч/перехеширование 1 839 файлов/ещё одно ревью ради удаления одного `?` в тестовом пути — несоразмерная церемония.

Сохранить точный рабочий diff, один осмысленный независимый security/code review и обычные regression-тесты. Не возвращать прежнюю бюрократию под новым названием.

## 7. Порядок будущих исправлений

1. Подтвердить единственный существенный вопрос совместимости: надо ли сохранять **экспериментальные OAuth grants/форматы**, и поддерживается ли откат на экспериментальную JSON-версию. Manual tokens/settings сохраняем.
2. Убрать unused mutation bookkeeping и пустые секундные commits; исправить shutdown deadlock и стандартное redirect/extension matching.
3. Снять reservation/lifecycle-гарантии; разделить простые pending/admission bounds и обычный grant/replay lifecycle.
4. Удалить неподтверждённые OAuth migration/fence-ветки и exact DDL fingerprints; сохранить минимальный необходимый импорт и отсутствие stale fallback.
5. Закончить один сквозной flow и небольшой Settings UI; выполнить перечисленные functional/race-проверки и одно coherent review.

Для server-only режима нужен доступный trusted CLI/Unix способ approval — без включения запрещённого server WebUI. Это вопрос поверхности незавершённой фичи, не основание строить новую админскую систему.

**Состояние на момент аудита: исправления не были начаты, код не откатывался, реализация не возобновлялась, пользовательские данные не открывались, коммитов не было. Последующая реализация описана в начале документа.**

## Первичные отчёты

Workflow: `1c9e4444-58f3-4f25-a0ac-ab764bc842bd`, три независимых свежих reviewer-контекста:

- SQLite: `4d9bdc65-7d87-4678-b1cd-a72133dc3ce8`, `mcp-sqlite-complexity-review.md`.
- OAuth: `bd61496d-3d8a-4807-b433-42fcb0da1991`, `mcp-oauth-complexity-review.md`.
- План/процесс: `5eac661c-0656-4f09-9b05-0c635c480cf4`, `mcp-plan-process-complexity-review.md`.

Отчёты сохранены в каталоге артефактов этой сессии Pi: `subagent-artifacts/outputs/1c9e4444-58f3-4f25-a0ac-ab764bc842bd/`. Выводы — результат чтения исходников и документов, не новых runtime-экспериментов. Признак untracked сам по себе не доказывает отсутствие любых прошлых пользователей; удаление реальной совместимости без выяснения не рекомендовано.
