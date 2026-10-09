# Аудит снапшотов — 2026-10-09

Область: ZIP format, daemon export/import, создание namespace из workspace snapshot,
server install, namespace secrets, dependency state и связь данных с конфигурацией.
Существующие незакоммиченные S3/Seaweed/review изменения сохранены. Релиз/публикация
Launcher в рамках этого изменения не выполнялись.

## Исправлено в этой фазе

| Пропуск/дефект | Изменение |
| --- | --- |
| Generated namespace credentials отсутствовали в архиве | secrets.json с сохранёнными именами/значениями; восстановление в нужный destination scope и его vault |
| Pins не сохранялись, поколения выводились только из томов | meta.json.dependencies: image + volumeGen; явный metadata pin имеет приоритет над эвристикой |
| Rollback target мог ссылаться на отсутствующее поколение | prevImage/prevVolumeGen переносятся только с retained volume |
| Create/install могли продолжить запуск после ошибки восстановления | Ошибка теперь возвращается до auto-activation/start |
| Partial server import уносил все volumes, включая невыбранные | Backup только заменяемых томов, остальные остаются на месте |
| Ошибка tar на экспорте маскировалась успешным stat | tar && stat; нет pipeline с потерей exit code и verbose listing содержимого |
| Недостающий/повреждённый поздний data file обнаруживался после замены предыдущих томов | Все файлы и tar/compression проверяются до backup/clear |
| Успех ZIP не учитывал ошибки финализации | Проверяются zip writer/file close |
| ZIP мог читаться другими пользователями | Export/download/partial download используют 0600 |
| Старая recovery history относилась к уже заменённым данным | Для импортированных dependencies очищаются journal/last result; export при pending recovery запрещён |

Точки применения: `internal/snapshot`, `internal/daemon/routes_snapshots.go`,
`internal/daemon/routes_ns.go`, `internal/daemon/snapshot_state.go`,
`internal/cli/install.go`, `internal/namespace/deps_state.go`.

## Результаты и принятые границы

### 1. Системные ключи, связанные с БД — реализовано

При опциональном экспорте `--include-secrets` JWT/OIDC/SA/admin теперь входят
в `secrets.json`. Импорт сохраняет namespace overrides в vault, не меняет глобальные
`_sys_*` и другие стенды. Loader/reload читают overrides, admin rotation сохраняет
импортированный пароль в том же scope. Без параметра secrets.json отсутствует,
импорт использует прежние ключи целевого сервера.

OIDC используется между proxy и Keycloak, но конфигурация proxy генерируется
Launcher, а секрет клиента Keycloak хранится в PostgreSQL. Сохранение обеих БД
не переносит генерируемую proxy-конфигурацию; поэтому перенос OIDC тоже нужен.

### 2. Конфигурация стенда и локальные overrides — принятая граница

Нет исходного `bundleRef`/версии workspace, namespace contentStorage/s3 settings,
локальных app image/env overrides и edited file deltas. При создании из snapshot
используется конфигурация выбранного target template. Она может описывать другой
storage provider, bucket, набор приложений или схему данных ECOS.

Рекомендуется отдельный manifest исходного стенда: bundle/workspace revision,
storage mode/provider/bucket, app images и portable overrides. Применять его нужно
с выбором/сравнением, сохраняя destination id, hostname, ports и локальные пути.
Дамп всего runtime state целиком не подходит: там cache, transient status,
история restart/migration и машинные пути.

Отдельно не переносится `contentStorageDefaultSet`: это история подтверждения
автоматического default selection. При восстановлении в существующий namespace
история target может описывать прежнюю БД. Нужна согласованная политика переноса
этого флага и source storage mode, сохраняющая выбор администратора.

### 3. Данные внешнего S3 — принятая граница

Экспорт включает только launcher-managed volumes/bind dirs. Во внешнем S3 лежат
объекты, на которые ссылаются dataKey из PostgreSQL, и ZIP их не содержит.
Даже для managed S3 частичный архив PostgreSQL без тома SeaweedFS не является
полной копией контента. Новая выгрузка ключей решает credentials, но не перенос
самих внешних объектов.

Нужен manifest внешних ресурсов и политика: сохранять ссылку на тот же backend
либо выполнять отдельный export/import объектов. Это не заменяется pin metadata.
`secret:<id>` ссылки внешнего S3/других settings на глобальные пользовательские
secrets также не входят в опциональный namespace/system export.

### 4. Размер распакованных данных и восстановление после I/O ошибки

Проверка места пока использует приблизительные 3× ZIP size. Inner tar.zst может
распаковываться значительно сильнее. Следует сохранять размер несжатых данных
томов в metadata и проверять свободное место до restore. Legacy archives требуют
отдельной оценки. Проверка tar integrity не доказывает наличие места для записи.

Server backup сохраняется; при ошибке извлечения его возврат выполняется вручную.
Desktop named volume очищается перед извлечением и не имеет аналогичного backup.
Импорт всегда извлекает данные на чистое место. Пользователь не запросил staged
rollback в этой фазе. Оценка 3× ZIP остаётся приближённой: чистые тома не решают
нехватку диска при сильном сжатии. Конфигурация/overrides и external S3 objects
также приняты пользователем как границы текущего snapshot, без расширения scope.

### 5. Частичный архив и все namespace secrets

Выбор томов ограничивает данные и dependency pins. При --include-secrets secrets.json включает все
сохранённые namespace keys. При импорте в уже существующий namespace замена ключа
может влиять и на невыбранный сервис. Автоматической связи secret → volume сейчас
нет; нужна явная политика partial restore или dependency-aware grouping.

### 6. Обычный CLI import и транзакция метаданных — исправлено

CLI больше не запускает namespace после ошибки импорта. Все production import
paths (обычный, create/download, server install) используют SnapshotDestination:
после проверки архива сохраняется pending marker, затем распаковываются тома,
после чего secrets + namespace state/pins фиксируются вместе. Для SQLite это
одна DB transaction; для FileStore — before-image journal, durable commit marker
и recovery. При ошибке Runtime сохраняет старые pins и start gate, незавершённый
import не автозапускается на boot. Отдельные start/restart/export/deps операции
также отказывают, retry import остаётся разрешённым.

Это транзакция метаданных, не файлов томов. После ошибки извлечения тома могут
быть частичными. Старые выбранные server volumes сохранены в backup, но полный
автоматический rollback данных не добавлен. Legacy pin inference вычисляется
без промежуточных persist и включается в тот же commit; явные metadata pins
имеют прежний приоритет.

## Что не следует переносить автоматически

Global registry/git credentials, master password/key vault, SSH credentials,
лицензии машины, TLS private keys/ACME account и абсолютные host paths. Не следует
засевать target runtime status/автозапуск из source, импортировать открытый journal
миграции или выдавать наличие ZIP за гарантию совместимости с любым target bundle.

## Проверка

Добавлены portable encrypted-vault round-trip, namespace/workspace isolation,
empty constants, отказ malformed/null/duplicate secret values без утечки значений,
locked-vault preflight до изменения томов, rollback неуспешной записи, legacy ZIP,
partial server restore, pins/поколения/retained rollback и сохранение импортированного
generated key после первой генерации. Отдельный Docker integration использует
только scratch bind directories и temporary utils containers, без нового daemon
или изменения существующего стенда.

Итоги: Docker round-trip данных/ключей/pins и отказ повреждённого tar до
изменения данных/ключей — PASS. Vet, golangci-lint (0 issues), race-тесты и
coverage floors — PASS; snapshot coverage 78.1%, daemon 67.6%, storage 66.2%.
Deadcode — PASS (три прежних allowlisted функции). 446 web tests, web audit/eslint,
server/desktop build, linux/arm64 compile, Windows vet и integration-tag vet — PASS.
Общий check НЕ прошёл security gate, см. отдельный блокер ниже.
Evidence: .tmp/snapshot-secrets/{full-check-pass.log,web-check.log,
server-build.log,desktop-build.log,docker-roundtrip-corruption.log}.
Фаза реализации/аудита проверена; готовность к публикации не заявляется.

## История общего release gate

Первый прогон 2026-10-09, до обновления toolchain, прошёл vet, lint, race и
coverage floors, но остановился на stdlib findings Go 1.26.8 (fixed 1.26.9).
Тогда go.mod/go.sum и allowlist оставались без изменений, полный gate был красным.
Исторические evidence: .tmp/snapshot-secrets/full-check-pass.log, vuln-scan.json,
vuln-summary.json. После согласованного обновления этот блокер устранён;
текущий результат приведён ниже.


## Следующая согласованная фаза (2026-10-09)

Пользователь выбрал опциональный экспорт секретов (default off), перенос SA/admin,
проверку связи OIDC и обновление Go. Минимальный Go pin поднят до 1.26.9; все CI
workflows используют go-version-file: go.mod. Пользователь подтвердил глобальную замену /usr/local/go на 1.26.9. После
системной авторизации pkexec замена выполнена; обычный go version подтверждает
go1.26.9 linux/amd64. SHA256 официального архива проверен. Прежняя установка
сохранена в .tmp/snapshot-followup/global-go-1.26.8-backup.
Сканирование на глобальном Go 1.26.9: 0 reachable findings; прежний security
блокер устранён без изменения allowlist. Docker round-trip включает SA/admin/OIDC
через другой vault, сохранение ключей при импорте default-архива без secrets.json
и отказ corrupted tar до изменения данных/ключей (PASS, 5.50s).
Проверены экспорт без чтения vault, зарезервированные system names, scope isolation,
reload/import cache refresh, admin rotation и admin lookup при install.

Повторный full gate выявил два существующих нестабильных теста, без изменения
production state machine:
- TestDispatcherDispatchRuns проверял Flush сразу после buffered result; теперь
  ждёт завершения worker. 1000 повторов с race detector проходят.
- TestStopGroupTimeoutProceedsToNextGroup зависел от real-time бюджета двух фаз
  остановки; теперь явно двигает FakeClock на каждой фазе. 30 повторов с race проходят.

Итоговый повторный full gate: **10/10 PASS**. Vet, lint (0 issues), race all
internal packages, coverage floors, govulncheck (0 reachable), deadcode, 446 web
тестов, npm audit, eslint, server build, linux/arm64 compile, windows vet и
integration-tag vet прошли. Snapshot coverage 78.1%, daemon 67.6%, storage 66.2%.
Desktop build и реальный Docker round-trip также прошли отдельно.

Команда gate использовала копию Makefile в Solution .tmp: единственные отличия
от штатного gate — пути cross-compile scratch artifacts и testing overlay,
сокращающий имя Go TempDir из-за ограничения Unix socket path. Product/runtime
логика тестов не подменялась, GOROOT source не редактировался.

Evidence: .tmp/snapshot-followup/full-check-verified.log, desktop-build.log,
docker-roundtrip.log, dispatcher-repeat.log, stop-group-repeat.log,
final-daemon.log, final-focused.log и govulncheck.log. Временные Docker fixtures,
headless browser и локальный docs server завершены. TEST не изменялся в этой фазе.




## Транзакционное восстановление — следующая согласованная фаза

Пользователь попросил транзакцию для secrets/pins. Добавлены SQL/FileStore atomic
commit, pending marker и защита от запуска незавершённого restore. Shared FileStore
lock координирует vault/state handles внутри процесса. File journal не попадает в
snapshot, имеет 0600, ограничен 64 MiB и проверяет namespace/credential targets.
При encrypted vault он содержит ciphertext, не переносит master key.

Проверки: SQLite trigger abort после secret writes; FileStore I/O failure с
блокировкой чтения до recovery; prepared/committed crash journals, новая/прежняя
строка; Runtime pins при отказе commit; API start/export/per-app gates; CLI не
перезапускает namespace при import error. Реальный Docker tar/ZIP + другой vault
подтвердил failure-after-data-write, retry, pins и corrupted tar preflight
(PASS, 3.02 s). Evidence: .tmp/snapshot-transaction/{storage-tests.log,
focused-final.log,docker-transaction-final.log}. Итог нового полного gate
зафиксирован: **10/10 PASS** в .tmp/snapshot-transaction/full-check-verified.log.
Vet/lint (0 issues), race all internal, coverage floors, govulncheck (0 reachable),
deadcode, 446 web tests, npm audit/eslint, server build, linux/arm64 compile,
windows/integration-tag vet прошли. Storage coverage 68.1%, daemon 67.7%,
namespace 84.2%. Проверена совместимость с прежним ID namespace secret: update
не создаёт дубликат того же имени. Секреты, не входящие в import, сохраняются.

Документация ecos-docs обновлена и собрана Sphinx без предупреждений; section
snapshot просмотрен на screenshot .tmp/snapshot-transaction/docs-transaction.png.
Новые проверки не запускали Launcher daemon, не трогали TEST или существующие
Docker volumes. Транзакция данных volumes, оценка raw size и новые публикации
в эту фазу не входили.

Финальная desktop-сборка также прошла: .tmp/snapshot-transaction/desktop-build-verified.log.
Все собственные test/HTTP/browser процессы завершены; публикация/коммиты не выполнялись.
Фаза транзакционного импорта завершена и проверена; следующий независимый этап не начат.
