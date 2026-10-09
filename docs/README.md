# Документация Launcher

Документы ниже описывают текущий код рабочей ветки. Наличие функции в документации
не означает публикацию этой ветки или изменение установленного стенда.

## Работа со стендом

- [Запуск приложений и detach](app-startup-and-detach.md)
- [Приоритет конфигурации и образов](config-layers.md)
- [Версии формата workspace](workspace-formats.md)
- [Секреты workspace](workspace-secrets.md)
- [Дополнительные приложения](additional-apps.md)
- [Объявленные базы и хранилища](databases.md)
- [Observer](observer.md)
- [Совместимость MongoDB](mongodb-compatibility.md)
- [Контент в DB/S3](content-storage.md)
- [Обновление PostgreSQL и зависимостей](postgres-major-upgrade.md)
- [Обновление SeaweedFS](seaweedfs-upgrade.md)
- [Снапшоты: формат, ключи и ограничения](snapshots.md)

Справочник серверных команд и архитектуры также находится в member `ecos-docs`,
в `docs/admin/launch_setup/launcher_server/`. Для разработки см. [AGENTS.md](../AGENTS.md).

## История и проверки

`specs/` и `plans/` хранят датированные дизайны и последовательности реализации.
Их исходные проблемы, имена веток и незакрытые чекбоксы не являются текущим
списком задач. Локальная, игнорируемая git директория `superpowers/` также содержит
исторические материалы и не является источником текущих требований.

- [Аудит снапшотов](reviews/snapshots-audit.md)
- [Ревью S3](reviews/content-storage-s3.md)
- [Датированные live-проверки S3](reviews/content-storage-s3-live.md)
- [Сверка документации](reviews/documentation-audit.md)

Live-отчёты фиксируют состояние на дату проверки. Они не гарантируют, что
стенд с тех пор не менялся. `.audit-backlog.md` — архив аудита 2026-06-10,
а не актуальный security/release gate.
