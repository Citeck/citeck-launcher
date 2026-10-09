# Аудит документации — 2026-10-09

Сверка текущих operator docs Launcher, README на восьми языках, AGENTS.md,
заголовков датированных designs/plans/reviews и серверного справочника member
`ecos-docs`. Проверка опирается на текущий код рабочей ветки; публикация не выполнялась.
Changelog и датированные live-результаты сохранены как история, не переписаны
в результаты нового прогона. Текст всех старых планов не является актуальной
спецификацией: проверены их назначение, статус и ссылки, а не каждый исторический шаг.

## Подтверждённые расхождения и исправления

| Документ | Расхождение | Исправление |
|---|---|---|
| 7 переводов README | `snapshot export <name>` при отсутствии позиционного имени | Поддерживаемая команда без `<name>` |
| README ×8 | Backup на другой host описан без условий переноса ключей | Default без secrets, opt-in `--include-secrets`, plaintext и границы архива |
| README ×8 | Пароль объявлен технически невосстановимым, хотя сохраняется Launcher | Оставлены сохранение пароля и поддерживаемый reset; абсолютное утверждение удалено |
| content-storage.md | Новый namespace после snapshot всегда получает новые ключи; secrets не экспортируются | Условия opt-in transfer, сохранение имеющихся ключей, pins и ссылка на snapshots.md |
| content-storage.md | Desktop admin всегда `admin`, без imported override | Уточнено действие импортированного admin-пароля |
| config-layers.md / databases.md | SEAWEEDFS отсутствует в описании typed dependency protection | Добавлен в перечень и дана ссылка на upgrade guide |
| AGENTS.md | ZIP + tar.xz назван текущим форматом; Go pin не указан | tar.zst по умолчанию, legacy tar.xz import; Go >=1.26.9 |
| 3 designs / 2 plans | Уже реализованные RAG, minLauncherVersion и image ladder названы незавершёнными | Исторический статус, ссылки на текущие operator docs, запрет повторного исполнения плана |
| workspace secrets design | Исходное ограничение «только константы» выглядит текущим | Пометка последующих generated secrets / snapshot transfer и ссылки |
| .audit-backlog.md | Июньские H2/signature/security/test findings выглядят открытым backlog | Явно обозначен архив; текущие H2 fixture и active signature подтверждены кодом |
| ecos-docs commands.rst | Нет secret-export flag и состава/pins/границ архива | Актуальные флаги, пример и restore semantics |
| ecos-docs commands.rst | После setup s3 обязательно вручную менять default | Описано автоматическое первоначальное переключение и сохранение выбора администратора; region требует content 1.7.2 |
| ecos-docs architecture.rst | Все system secrets названы encrypted vault rows | Разделены global plain `_sys_*` и vault namespace overrides |
| ecos-docs configuration.rst | Не упомянут generated формат workspace secrets | Добавлен текущий формат |

Для остальных current guides (startup/detach, observer, MongoDB, PostgreSQL,
SeaweedFS, additional apps) проверены основные контракты по соответствующему
коду и ссылки; подтверждённых новых противоречий в проверенной части не найдено. Старые
specs/plans сохраняют дизайн-контекст и не определяют release readiness.

## Проверка

- Относительные Markdown file links в README/AGENTS/readme/docs проверены
  локально: в итоговых 50 файлах проверены 167 относительных ссылок, отсутствующих
  файлов нет. Результат: `.tmp/docs-audit/links.json`.
- `ecos-docs` собран Sphinx 8.0.2 с зависимостями из docs/requirements.txt;
  HTML build succeeded, предупреждений/ошибок нет.
- Сборочный venv, HTML, логи и screenshots находятся только в Solution `.tmp/docs-audit`.
- HTML раздела snapshot просмотрен на реальных screenshots: flags/examples и
  состав/ограничения архива читаются без перекрытия и обрезания текста. Evidence:
  `.tmp/docs-audit/snapshot.png`, `snapshot-archive.png`, `visual-check.json`.

Состояние полного code gate и фактические проверки snapshot записаны отдельно
в [аудите снапшотов](snapshots-audit.md). Архив Go 1.26.8 findings не является
текущим блокером после обновления toolchain и нового clean scan.

Итог текущего code gate: 10/10 PASS; полный лог —
`.tmp/snapshot-followup/full-check-verified.log`. Публикация/коммиты не выполнялись.
