![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English version](README.md)

Citeck Launcher управляет пространствами имён Citeck и Docker-контейнерами. Один Go-бинарник (~24 МБ) выступает и как CLI, и как демон.

> **Полная документация:** https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server.html

## Быстрый старт

Требования: Docker (запущен).

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

Скрипт скачивает последний релиз для вашей платформы и устанавливает в `/usr/local/bin/`. Мастер установки настроит пространство имён и запустит платформу.

> **Примечание:** Если `citeck` уже есть в `PATH` (например, предварительно доставлен CI/CD или системой управления конфигурацией), пропустите скачивание и сразу выполните `citeck install`.

> **Важно:** Команда `citeck install` — это **интерактивный TUI-мастер**, требующий настоящего терминала. В конце мастер **один раз** выводит сгенерированный пароль администратора — обязательно скопируйте и сохраните его, после закрытия экрана его нельзя будет восстановить. Если потеряли, сбросьте через `citeck setup admin-password` (см. [справочник команд](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server/commands.html)). Нажатие `Ctrl+C` до шага «запись конфигурации» завершает мастер без внесения изменений; при прерывании позже проверьте `/opt/citeck/conf/` на частичные файлы.
>
> Автоматическая (неинтерактивная) установка — планируемая функция. Если она вам нужна, создайте issue.

Для **обновления** уже установленной версии выполните тот же one-liner — скрипт определит установленную версию, спросит подтверждение, остановит демон и заменит бинарник (бэкап сохраняется в `/usr/local/bin/citeck.bak` и восстанавливается через `bash install.sh --rollback`).

### Офлайн-установка

Для серверов без доступа в интернет заранее скачайте два файла:

1. **Бинарник** — со [страницы релизов](https://github.com/Citeck/citeck-launcher/releases).
2. **Архив workspace** — с [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (раздел Releases или кнопка «Download ZIP»). Архив содержит определения
   бандлов, которые лончер обычно подтягивает из git.

Затем на целевом сервере:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

Флаг `--workspace` распаковывает репозитории бандлов локально — интернет при запуске не нужен.
Чтобы позже обновить workspace из нового архива без переустановки: `citeck update -f <zip>`.

## Возможности

- **Интерактивный установщик** с авто-определением TLS (Let's Encrypt / самоподписанный / свой сертификат)
- **Обновление CLI и демона** — `install.sh` заменяет бинарь демона, не останавливая контейнеры платформы; новый демон подхватывает их по deployment-hash (k8s-style restart control plane). Приложения с неизменённым hash продолжают работать без простоя; приложения с изменённым hash пересоздаются (типичный простой 1–5 мин на приложение, Java-сервисы дольше). После замены бинарника выполните `citeck reload --dry-run`, чтобы увидеть список контейнеров, которые будут пересозданы.
- **8 языков интерфейса**: английский, русский, китайский, испанский, немецкий, французский, португальский, японский
- **Обновления в реальном времени** через SSE (статус приложений, потребление ресурсов)
- **Снапшоты томов** с экспортом/импортом (ZIP + tar.xz), авто-остановка/запуск
- **Let's Encrypt** с авто-обновлением (домены и IP-адреса)
- **Самовосстановление** с liveness-пробами, отслеживанием перезапусков и диагностикой
- **Автодополнение** для bash, zsh, fish, powershell

## Команды CLI

```
citeck install [--workspace <zip>]        Мастер установки (офлайн с --workspace)
citeck start [app] [-d|--detach]          Запуск демона/пространства (--detach = в фоне)
citeck stop [app] [-d|--detach]           Остановка (--detach = в фоне)
citeck restart [app] [--wait]             Перезапуск приложения или всего пространства
citeck reload [--dry-run] [-d|--detach]   Перечитать конфиг и пересоздать только изменённые контейнеры
citeck status [-w|--watch]                Статус пространства имён
citeck describe <app>                     Подробная информация о контейнере (образ, порты, env, тома)
citeck health                             Проверка здоровья (exit 0=ok, 1=демон недоступен, 8=проблема)
citeck diagnose [--fix] [--dry-run]       Диагностика (с авто-исправлением при --fix)
citeck logs [app] [-f|--follow]           Логи (демона или контейнера)
citeck exec <app> -- <команда>            Выполнить команду в контейнере
citeck update [-f|--file <zip>]           Подтянуть workspace/бандлы (или импорт из ZIP)
citeck upgrade [bundle:version] [--yes]   Переключиться на другую версию бандла
citeck snapshot list|export|import|delete Снапшоты томов (авто-остановка/запуск)
citeck config view|validate|edit          Показать, проверить или отредактировать namespace.yml
citeck setup [настройка]                  Настройка (TUI-меню или по ID)
citeck setup history                      История изменений конфигурации
citeck clean [--force] [--volumes] [--images]  Очистка сирот / удаление образов
citeck version [--short]                  Информация о версии
citeck completion bash|zsh|fish           Автодополнение
citeck uninstall [--delete-data]          Удалить systemd-сервис, бинарь и (по желанию) данные
```

Глобальные флаги: `--format (text|json)`, `--yes/-y`.

## Конфигурация

Описание `daemon.yml` и `namespace.yml` — в [справочнике конфигурации](https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher_server/configuration.html).

## Лицензия

См. [LICENSE](LICENSE).
