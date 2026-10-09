# S3: проверка на тестовом сервере

Даты: 2026-10-07–08. Сервер: `45.15.158.227`. Владелец разрешил изменение и очистку тестовых данных.
**Итог: default/custom bucket/явный и пустой region проверены; ecos-content
1.7.2 опубликован, TEST восстановлен и очищен. После финального cleanup
прошли 25 ECOS readbacks, два multipart readbacks и 24 физических S3-объекта.**

В первом прогоне 2026-10-07 проверена незакоммиченная сборка `feature/content-storage-s3`,
`dev-20261007-072924`, SHA-256
`a0ed29f84fe85635a828701bd51c27d4a7433ed1afa2f95404adaee47a737517`.
Хэш локальной сборки и установленного бинарника совпал, в том числе после перезапуска процесса агента.

## Исходная конфигурация первого прогона

Исходно: launcher 2.15.2, Community 2026.2.1, 16 GiB RAM, ecos-content и S3 отсутствовали.
Для теста остановлены необязательные приложения. Добавлен отдельный тестовый
bundle `enterprise:2026.2.1-s3-live`: прежние образы платформы плюс ecos-content 1.7.0
и SeaweedFS 4.48. Это локальная тестовая конфигурация, а не опубликованный bundle.
Workspace дополнен провайдером из fixture через сохраняемый workspace delta;
исходный tracked workspace-файл восстановлен. Приватный образ получен с уже
имеющимися Docker-учётными данными и credential привязан в launcher.
Пароли не включены в отчёт или логи.

## Результаты

| Проверка | Результат |
|---|---|
| DB до переключения | Загрузка и чтение через ECOS, SHA-256 совпал |
| Настройка через CLI | `citeck setup content-storage`: выбран S3, показан diff, сохранение через daemon; провайдер запущен, content пересоздан с native env |
| Штатное S3 | Используется `content/storage@content-storage-s3`, endpoint `content-storage-s3-endpoint`, bucket `ecos-content` |
| Дополнительные записи | `emodel/secret@seaweedfs-s3-credentials`, `emodel/endpoint@seaweedfs-s3`, `content/storage@seaweedfs-s3` отсутствуют |
| Загрузка и чтение | Бинарные файлы 64 KiB, 1 MiB, 8 MiB; UTF-8; четыре параллельных файла по 256 KiB — SHA-256 совпали |
| Физическое хранение | 14 S3-файлов прочитаны напрямую из bucket с SigV4; байты совпали с загруженными через ECOS |
| Старые данные | Файлы из DB читаются после выбора S3, перезапусков и при остановленном провайдере |
| Ошибка мутации и автоповтор | `500 → 500 → 200` с интервалами около 5 минут, без ручного Retry |
| Остановка провайдера | CLI предупредил; новая S3-загрузка получила HTTP 500; после запуска все файлы прочитаны и новая загрузка прошла |
| Смена S3-ключа | Новый generated secret через workspace delta; оба контейнера пересозданы, env совпали; старые файлы прочитаны, новый записан. Исходный secret reference восстановлен |
| Admin-пароль | Изменён штатным API; мутация config и S3 upload/read прошли. Исходный пароль восстановлен |
| BASIC → Keycloak | Оба режима и переход проверены: мутация config, новая загрузка и чтение прежних данных прошли. Итоговый режим — исходный KEYCLOAK |
| Ручной LOCAL и restart daemon | После ручной правки ECOS config и рестарта daemon сохранил LOCAL, показал `admin-managed`; файлы обоих хранилищ читались |
| S3 → DB в namespace.yml | CLI отказал; PUT config вернул HTTP 409 `CONTENT_STORAGE_DOWNGRADE`, файл не изменился |
| Финальный restart | Сервис systemd active, namespace RUNNING, S3 connected; все 17 файлов прочитаны, включая 3 DB и 14 S3 |

Файлы создавались штатным multipart upload ECOS (`emodel/temp-file@...`) и
скачивались через возвращаемый `_content.url`. Проверка физического bucket
дополнительно подтверждает выбор S3, а не только значение config или баннер.

## Отказы и зависимости

Временный отказ записи внедрён прозрачным локальным HTTP proxy только для
mutation `eapps/config@app/emodel$default-content-storage`; остальные запросы
проходили в реальный gateway. В 08:08:50 UTC ответ был HTTP 500; после снятия
отказа таймер сам повторил запрос в 08:13:51 UTC. На этом стенде реальный ECOS
ответил HTTP 500 `App is not available: 'eproc'`: eproc был остановлен при
освобождении памяти. После его запуска следующий автоматический запрос в
08:18:53 UTC получил HTTP 200 и подтверждение нового default. Никакой кнопки
или POST ручного повтора между этими попытками не использовалось.

Это зависимость обработки Records API в проверенной платформе, а не
зависимость от admin-пароля. Гейт четырёх основных приложений не гарантирует
готовность всех внутренних обработчиков ECOS; такие отказы показываются и повторяются.
Mailpit возвращён в работу: используемый nginx image требует DNS alias `mailhog`.

## Как аутентифицируется мутация

Daemon вызывает внутренний gateway: `POST /api/records/mutate` с JSON
Records API v1 и `X-ECOS-User: admin`. Gateway маршрутизирует `eapps/config@...`
в eapps. Изменяется config для emodel. В запросе нет admin-пароля, Basic login
или OIDC-токена, он не идёт через публичный proxy. При неизменных правах
platform user admin смена пароля или режима BASIC/KEYCLOAK не меняет этот путь;
оба случая проверены на живом сервере.

## Итоговое состояние и доказательства

Сервер оставлен со штатным сервисом `citeck` в systemd, текущим бинарником,
S3 default и исходным режимом KEYCLOAK. Namespace RUNNING, S3 connected;
13 необходимых приложений запущены. Исходный admin-пароль и исходный S3 secret
reference восстановлены. В конце доступно около 8,9 GiB RAM и 4,1 GiB диска.
Необязательные приложения остаются остановленными. Тестовый bundle/workspace
остаются для воспроизведения; исходный бинарник и namespace сохранены в backup.

Fault proxy остановлен, DNAT rule удалён, его listener отсутствует; временный
admin password backup удалён. Коммитов и push нет. Исходники Go во время этого
прогона не менялись: исправлять дефекты S3 в проверенных сценариях не потребовалось.

Артефакты в `.tmp/s3-live/` в корне solution:

- `final-verification.log`, `final-state.log`, `post-recovery-state.log` — итог;
- `s3-data-matrix.log`, `key-rotation-result.log`, `provider-down-test.log`, `provider-recovery.log` — данные и восстановление;
- `admin-password-test.log`, `keycloak-transition-result.log` — пароль и режим аутентификации;
- `persist-workspace-and-retry.log` — три попытки мутации;
- `manual-default-restart-result.log`, `one-way-api.log` — сохранение ручного выбора и запрет downgrade;
- `live.py`, `probe.py` и остальные harness scripts — воспроизведение проверок.

## Дополнительный SeaweedFS/TUI прогон

По последующему разрешению владельца на тестовый сервер установлена сборка
`dev-20261007-120301` с backup; SHA256 совпал с локальной сборкой. Реальный
изолированный Docker integration test проверил managed 4.47 → 4.48 и штатный
API rollback, включая binary/UTF-8/versioned readback и сохранность томов.
Основной SeaweedFS остаётся на 4.48, его workspace теперь имеет
`type: SEAWEEDFS`; зависимость видна после restart. Дополнительный upload
1 MiB прошёл через ECOS, итоговая проверка — 18 файлов через ECOS и 15
физических S3-файлов с совпадением SHA256. Сервер оставлен systemd active,
RUNNING/S3 connected/KEYCLOAK, backend-порт закрыт.

Проверен настоящий TUI через tmux; найденное обрезание длинного предупреждения
исправлено и проверено на 80/120 колонках со скриншотом. Полный gate новой
правки — **10/10 PASS**, **0 lint issues**, **446 web-тестов**. При подготовке
TUI второй server daemon конфликтовал с основным Docker namespace `default`;
он остановлен, основной стенд восстановлен из исходных томов, все прежние
хэши повторно проверены. Правило изоляции добавлено в Agent Testing Guide.
Подробности, границы доказательств и пути логов:
[SeaweedFS: тестовый сервер и TUI](../seaweedfs-upgrade.md#дополнительный-тестовый-сервер-и-tui).

Последующий TUI-прогон всех восьми локалей (`en/ru/zh/es/de/fr/pt/ja`) завершён:
**8/8 PASS**, 48 screenshots и исходные terminal captures сохранены. Проверены
меню, content hint при 80/120 колонках, diff/cancel, навигация, port validation,
language picker, deps/refusals и запрет S3 → DB. Новых дефектов нет; исходники
и binary не менялись, настройки основного стенда побайтово неизменны.
Доказательства и точные границы: [TUI на всех языках](../seaweedfs-upgrade.md#tui-на-всех-восьми-языках).

## Закрытие live-проверки defaults и внешнего S3 — 2026-10-08

На `45.15.158.227` проверена сборка `dev-20261008-071720`, SHA256
`c4e63810ae9392a5c8f8852bd105b74e4f37268a019d9e2f6088a9abf193b4c7`
(совпадает с локальной). Компактный workspace использует `type: SEAWEEDFS`
и обычный YAML `settings`; frame, probes, resources, command и generation volume
формируются профилем. Основной managed SeaweedFS 4.48 сохранил исходный том
`seaweedfs2` (gen1); backend-порты в server mode не опубликованы. Новый 1 MiB
файл успешно записан и прочитан через ECOS; managed-промежуточный итог —
19 ECOS-файлов и 16 физических S3-файлов с совпадением SHA256.

Внешний backend поднят вручную через Docker, вне Launcher: отдельный
SeaweedFS 4.48 без managed app labels, с одноразовыми credentials и своим
data directory. MinIO server не проверен: загрузка образов из DockerHub/Quay
была отклонена registry. Настройка выполнена через настоящий удалённый TUI
в tmux 120×40: `citeck setup s3`, проверка неверного URL, ввод endpoint,
маскированный пароль, diff, Apply/reload, повторное редактирование с пустым
полем пароля (secret reference сохранён, конфигурация не изменилась).
Managed SeaweedFS остановлен через TUI; content продолжал работать без этой
зависимости. Два новых файла (binary 1 MiB и UTF-8 16 KiB) прошли запись и
чтение через ECOS. Все 21 ECOS-файл и 18 физических внешних S3-файлов
совпали по SHA256; reload не пересоздал внешний контейнер.

Границы доказательства: использован существующий legacy `namespace.s3`;
нативный default `content/storage@content-storage-s3` уже был выбран до
внешнего теста и оставался неизменным. Bucket — `ecos-content`, region —
`us-east-1`; в проверенном native artifact bucket задан явно. Это проверка
endpoint/credentials/lifecycle/data, а не автоматического выбора S3 на свежей
БД, произвольного bucket/region или поддерживаемого TUI-перехода managed →
external. Для инициализации теста managed `contentStorage` временно убран
из копии namespace; после теста восстановлен оригинал. Новая функциональность
внешнего режима в этой фазе не добавлялась.

После mirror-back 18 объектов (16 прежних проверены без перезаписи, два новых
скопированы) оригинальный namespace восстановлен побайтово и основной daemon
перезапущен. Финальное чтение выполнено **до и после удаления внешнего backend**:
**21/21 ECOS-файлов и 18/18 физических managed S3-файлов — PASS**.
Native endpoint и credentials снова соответствуют managed provider, default
остался нативным S3. Итог: systemd `active`, namespace `RUNNING`, S3 `connected`,
исходный KEYCLOAK, SeaweedFS 4.48/исходный том, без внешнего `namespace.s3`.

Удалены только тестовый контейнер `external-s3-9bfre` (точный ID сверён), его
bind data, одноразовые credentials, созданный тестом неиспользуемый секрет
`s3.secretKey` и собственная tmux-сессия `s3-live-tui-9bfre`. Порт 19000 закрыт.
Основной сервис, backup, proof scripts, логи и снимки сохранены; чужие ресурсы
не затронуты. Четыре снимка настоящего TUI визуально проверены: текст читается,
валидация видна, пароль скрыт, diff переносится, повторное редактирование
показывает `Nothing changed`.

Доказательства в solution `.tmp/seaweed-server-check/`:

- `managed-read.log`, `managed-physical.log`, `tui-managed.log`;
- `external-upload.log`, `external-read.log`, `external-proof.log`,
  `tui-external.log`, `tui-external-edit.log`, `mirror-back.log`;
- `final-read.log`, `final-physical.log`, `final-cleanup.log`, `final-state.log`;
- `post-cleanup-read.log`, `post-cleanup-physical.log`,
  `post-cleanup-env.log`, `post-cleanup-default.log`;
- `managed-menu.png`, `external-validation.png`, `external-diff.png`,
  `external-edit-retained.png` — визуально проверенные terminal screenshots.

Исходники во время текущего live-прогона не менялись; ранее пройденные local
10/10 gate, 446 web-тестов, lint и desktop build остаются применимыми.
Все накопленные изменения сохранены незакоммиченными на
`feature/content-storage-s3` (HEAD `34fe309`); public workspace остаётся на
`local-s3-test`. Коммитов, push и публикации нет. Текущая фаза закрыта.

## Доработка внешнего default и bucket — 2026-10-08

После закрытия предыдущего прогона владелец запросил исправить ограничения
внешнего режима. Добавлены подключение default/retry/status для `namespace.s3`
без managed provider и применение namespace bucket к встроенному native
artifact через generated read-only mount. Credentials остаются в native env;
единственная Records API mutation по-прежнему default-config. Другой default
администратора не переписывается; неразрешённый secret блокирует переключение.

Через настоящий удалённый tmux/TUI на основном daemon применены endpoint,
fixture credentials и заранее созданный **`launcher-external-custom`**.
До настройки default был установлен в `LOCAL`, а история подтверждения S3
временно очищена в резервированной копии runtime state. Это проверка начального
состояния выбора на существующей БД, **не отдельный запуск с чистой БД**.
После Apply/reload Launcher сам выбрал native S3; реальные container env,
смонтированный artifact и ECOS storage record подтвердили custom bucket.
Managed provider остановлен через TUI; binary 1 MiB и UTF-8 16 KiB успешно
записаны/прочитаны через ECOS и найдены в физическом custom bucket по SHA256.
Все 23 ECOS-файла, включая 21 прежний, прочитаны во внешнем режиме.

После TUI-start managed provider выполнен mirror-back: 18 объектов стандартного
bucket сверены без перезаписи; два новых перенесены в одноимённый custom bucket
managed S3 с проверкой чтения. Оригинальные namespace/runtime history восстановлены,
основной сервис перезапущен. Native record снова имеет bucket `ecos-content`,
endpoint/credentials соответствуют managed provider. После удаления только
собственного external container/data/fixture credentials/unused `s3.secretKey`
и tmux подтверждены **23/23 ECOS и 20/20 физических S3-файлов**; основной
стенд RUNNING/S3 connected/systemd active, 4.48/исходный `seaweedfs2`, без
backend port bindings. Custom bucket с двумя проверенными файлами сохранён
в managed S3 для чтения их сохранённых content refs.

Новые terminal captures и визуально проверенные screenshots находятся в
solution `.tmp/external-completion/`: `tui-external.log`, `external-diff.png`,
`external-applied.png`, `custom-proof.log`, `external-read.log`, `mirror-back.log`,
`restored-read.log`, `restored-physical.log`, `cleanup.log`, `final-read.log`,
`final-physical.log`, `final-state.log`. Local gate —
`.tmp/seaweed-server-check/external-check.log`: **10/10 PASS**, **0 lint issues**,
**446 web tests**. Последующее упрощение external status label (обычный `S3`,
без повторения имени) проверяется focused race tests и повторными server/desktop
builds в `.tmp/external-completion/`.

**Region завершён на выпущенном ecos-content 1.7.2.** Владелец выбрал
доработку ECOS content и hotfix. DTO — `region: String = ""`; blank оставляет
автоопределение, trim explicit передаётся обоим MinIO builders. Clean verify
выпущенной версии: 50 тестов, 0 failures/errors/skipped; preflight — 0 stops.
Интеграционная оснастка использует закреплённый SeaweedFS 4.48.

Тег 1.7.1 (`e94a2ed`) сохранён: его CI не смог скачать MinIO из quay.io (401).
Владелец подтвердил следующий patch. Master и тег 1.7.2 — `de91408`, Docker
`enterprise-registry.citeck.ru/ecos-content:1.7.2` опубликован (waiter AVAILABLE).
После появления артефакта master перенесён в develop `30ceed0`, версия
`1.8.0-SNAPSHOT` сохранена, develop отправлен один раз. Своя локальная
hotfix-ветка и временный stash удалены; hotfix в origin не отправлялся.
COREDEV-629 существует; отдельная задача не создавалась. Launcher/public
остаются без commits/push/publication.

При закрытии default/bucket части обнаружена и исправлена связанная проблема:
прежний Remove внешнего S3 мог убрать доступ после автоматического выбора
native default. Добавлены shared removal policy, online/offline write gate,
ленивое чтение external блока даже при ошибках постороннего YAML, защита undo
и отказ Remove до изменения config. Реальный TUI на сервере проверен с
изолированным namespace config, без второго daemon: Remove отклонён,
namespace hash неизменен; меню корректно показывает S3. Main daemon оставался
active. `tui-removal.log`, `external-removal-refused.png`, `external-menu.png`
сохранены в `.tmp/external-completion/`; fixture config/own tmux удалены.

Финальный gate использует копию Makefile с arm64 scratch-путём внутри
solution, `TMPDIR=<solution>/.tmp/t` и Go overlay для тестов: только шаблон
имени `testing.TempDir` сокращён с 64 до 8 символов. Логика тестов, уникальность
каталогов и production-код не менялись; файл Go вне solution не редактировался.
Даже короткий абсолютный scratch-path вместе с длинными именами тестов
превышал лимит Unix socket; overlay устранил эту проблему окружения.
Итог `.tmp/external-completion/final-check.log` — **10/10 PASS**, **0 lint issues**,
**446 web tests**; final desktop build также PASS. На сервере установлена
точная финальная сборка `dev-20261008-092259`, SHA256
`937c0e3e74344c654f420f2911ef7edfa352730f67c37b73200ae05f2c935dee`.
После её restart снова подтверждены 23/23 ECOS, 20/20 physical S3 и исходный
managed namespace/default/bucket/credentials. Внешних test jobs/resources нет;
backups, proof scripts и logs сохранены. Region остаётся единственным
нерешённым вопросом до ответа владельца.

## Завершение region: TEST, 2026-10-08

На 45.15.158.227 использовались текущий Launcher с region mapper (SHA256
`521c0ae45f57f18f4465fff02e6809b77b80e4f63e54bf8e360bda38a7bfd946`) и
выпущенный CI-образ content 1.7.2. Digest registry:
`sha256:d968dad57fb48afb7870cab0c1bba266545047f8ee04f29b3f38f5d4630fd7e6`.
Docker CLI сервера не имел registry auth; скачанный CI-образ передан по SSH
через docker save/load. Source и loaded Image ID отличаются; равенство ID
не заявляется. SHA256 классов S3ConfigurationDto, S3ContentStorageFactory и
ExtendedMinioAsyncClient запущенного контейнера совпали с CI-образом.
Bytecode содержит trim и region у обоих builders. Контейнер использует
фактически загруженный release image; beta-образ для приёмки не использован.

Внешний MinIO запускался вручную вне Launcher с eu-west-2. До переключения
18 стандартных и два custom объекта скопированы с проверкой raw SHA256 и
сохранением metadata/content headers. Собственный HTTP proxy слушал только
Docker gateway; его контрольная проверка принимала верную подпись и отклоняла
location/wrong-region. Контрольный объект удалён до основной проверки.
Прокси записывал только method/path/query keys/region/phase/verdict, без Auth
и credentials. Основной daemon оставался единственным на Docker engine.

| Проверка | Явный region | Пустой region |
| --- | --- | --- |
| Настройка | настоящий TUI, padded eu-west-2 → trim | TUI Edit, region очищен, пустой secret сохранил reference |
| Native record/artifact | launcher-region-custom, eu-west-2 | тот же bucket, region отсутствует/blank |
| Sync | 1 MiB upload/download, SHA256 | 1 MiB upload/download, SHA256 |
| Async ECOS WebAPI | init + 3 parts + complete + download + abort | те же операции |
| Multipart payload | 11 MiB + 123 bytes | 11 MiB + 123 bytes |
| Подписи | все eu-west-2, 0 location, 0 denied | автоопределение; 4 location с учётом чтения старых бакетов, остальные подписи eu-west-2, 0 denied |
| Прежние данные | read-all 24 ECOS; 22 S3 objects | read-all 25 ECOS; 24 S3 objects |

Location при явном region запрещён самим proxy, поэтому SDK automatic discovery
не мог скрыть игнорирование настройки. В обоих режимах managed SeaweedFS был
реально остановлен командой из terminal/TUI; проверка ждала фактического stop,
а не только возврата асинхронной команды. Проверены все пять native env keys,
секрет в namespace хранится только reference. Native default остался S3.
Этот этап не повторял прежние auth/fault/admin/default matrices.

Все 24 объекта зеркалированы назад: ecos-content — 18,
launcher-external-custom — 2, launcher-region-custom — 4. Каждый raw payload
проверен после записи; сохранены старые и новые metadata/dataKey. После cleanup
повторно прочитаны 25 ECOS-файлов, обе multipart-загрузки через ECOS WebAPI и
24 физических S3-объекта во всех бакетах. Две multipart-загрузки имеют одинаковый
payload, поэтому это 23 distinct expected digests при 24 объектах. Ни одного
незавершённого multipart upload не осталось. Новый бакет оставлен, поскольку
существующие dataKey ссылаются на него.

Исходный namespace восстановлен byte-for-byte; authentication — KEYCLOAK,
managed endpoint/credentials и native default/bucket восстановлены. Dependency
pins и manualStoppedApps совпадают с backup. SeaweedFS 4.48 использует прежний
seaweedfs2/gen1, backend ports не опубликованы. Единственный дополнительный
app override — image content 1.7.2. YAML editor API добавил probe defaults в
livenessProbe/startupConditions; собственные лишние поля удалены из runtime
при безопасном stop/start того же systemd daemon с --leave-running. Остальные
patches сохранены. Это найденная особенность YAML round-trip, отдельная
доработка editor здесь не выполнялась.

Очищены только собственные MinIO container/data/fixture credentials,
proxy PID/listener, неиспользуемый s3.secretKey и свой tmux. Порты 19000/19010
закрыты. После cleanup/restart primary — active/RUNNING/S3 connected.
Текущий desktop build также прошёл; исходный полный Launcher gate — 10/10 PASS,
446 web tests, 0 lint issues — относится к тому же коду region mapper.

Evidence в solution .tmp/s3-region: release-1.7.2-verify-retry.log,
release-1.7.2-preflight.log, release-image-meta.json, release-image-live-proof.json,
proxy-control.log, proxy-explicit.jsonl, proxy-auto.jsonl, explicit-{sync,async,
read-all,physical}.log, auto-{sync,async,read-all,physical}.log,
region-mirror-back.log, region-cleanup.log, region-final-{read,async-read,
physical,state}.log, desktop-build.log. Реальные terminal captures отрисованы
и просмотрены: region-explicit-diff.png, region-explicit-applied.png,
region-auto-diff.png, region-auto-applied.png, region-restored.png,
region-final-status.png. Private backups с environment secrets не публикуются.
