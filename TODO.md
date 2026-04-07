# Production Readiness TODO

Задачи для выпуска серверного launcher v2.0.0 в прод.

## P0: Блокеры релиза

### 1. Bundle upgrade (`citeck upgrade`)
- [ ] CLI: `citeck upgrade [bundle-ref]` — смена bundleRef в namespace.yml + reload
- [ ] CLI: `citeck upgrade --list` — показать доступные версии (из workspace config)
- [ ] Web UI: кнопка upgrade на Dashboard с выбором версии
- [ ] При upgrade: smart regenerate (пересоздать только изменившиеся контейнеры)
- [ ] Уже работает: `citeck reload` пересоздаёт контейнеры с изменённым hash

### 2. Docker image cleanup
- [ ] CLI: `citeck clean images` — удалить неиспользуемые образы (dangling + старые версии)
- [ ] Автоочистка после upgrade (удалить образы предыдущей версии бандла)
- [ ] Показывать сколько места занимают образы в `citeck status` или diagnostics

### 3. Welcome/Wizard в серверном режиме
- [ ] Серверный режим без namespace: Web UI показывает Welcome с кнопкой "Create Namespace"
- [ ] Wizard создаёт namespace через тот же API что и `citeck install`
- [ ] Проверить что wizard получает bundleRepos/templates из workspace config (уже починено: daemon грузит wsCfg до namespace)
- [ ] Wizard должен показывать доступные бандл-версии (из local repo или cloned bundles)

### 4. README.md
- [ ] Quick start: скачать бинарь → `citeck install` → `citeck start` → открыть UI
- [ ] Offline quick start: скачать бинарь + workspace.zip → `citeck install --workspace` → `citeck start`
- [ ] Архитектура (один абзац + диаграмма)
- [ ] CLI reference (таблица команд)
- [ ] Ссылки на docs/ (api.md, config.md, operations.md)

### 5. Обновить документацию под текущее состояние
- [ ] CLAUDE.md: добавить workspace import/update, offline mode, secrets default password, docker naming без workspace
- [ ] docs/config.md: добавить `--offline`, `--workspace` флаги, `citeck workspace` команды
- [ ] docs/operations.md: добавить offline deployment flow, workspace import, workspace update
- [ ] docs/api.md: проверить актуальность всех эндпоинтов

### 6. Server deployment testing
- [ ] Чистая установка: `citeck install --workspace` → `citeck start` → все сервисы RUNNING
- [ ] Bundle upgrade: смена версии → контейнеры пересозданы
- [ ] Daemon restart: контейнеры сохраняются, restart events переживают рестарт
- [ ] Liveness probe: убить контейнер → автоперезапуск → diagnostics файл создан
- [ ] Web UI: Dashboard, restart events panel, AppTable restart count badge

## P1: Важно, но не блокирует релиз

### 7. Self-update лончера
- [ ] `citeck self-update` — скачать последний релиз с GitHub, заменить бинарь, перезапустить
- [ ] Или: документация по ручному обновлению через systemd (`systemctl stop citeck && cp ... && systemctl start citeck`)
- [ ] Проверка наличия новой версии при старте (info лог, не блокирует)

### 8. Проверка совместимости при upgrade
- [ ] Bundle может декларировать `minLauncherVersion` — launcher проверяет перед upgrade
- [ ] Bundle может декларировать `migrationNotes` — показывать пользователю перед upgrade
- [ ] Без этого upgrade всё равно работает, просто без safety net

### 9. Документация серверного ограничения
- [ ] Задокументировать: серверный режим = один namespace на инстанс
- [ ] Для multi-namespace: запускать несколько инстансов с разным CITECK_HOME

### 10. Системные требования
- [ ] Минимум: 4 CPU, 16GB RAM, 50GB disk (для community бандла ~20 сервисов)
- [ ] Рекомендуемые: 8 CPU, 32GB RAM, 100GB disk
- [ ] Docker 24+, Linux x64/arm64
- [ ] Сетевые требования: порт для proxy (80/443), опционально 7088 для Web UI

### 11. CI/CD pipeline
- [ ] Проверить что release workflow (goreleaser) работает с pnpm
- [ ] Проверить что CI workflow проходит на чистом runner
- [ ] Добавить E2E smoke test в CI (опционально)

### 12. Прозрачная миграция с Kotlin launcher (v1.x) — только desktop
Серверного Kotlin launcher не было в релизе. Миграция актуальна только для desktop.
Пользователь не должен знать что рантайм сменился. Просто обновил бинарь — всё работает.
- [x] H2→SQLite миграция — автоматическая (desktop mode, при старте)
- [x] Docker naming — desktop сохраняет формат `citeck_{app}_{ns}_{ws}` (Kotlin compat), контейнеры не пересоздаются
- [x] Secrets: Kotlin шифровал blob → автоимпорт + диалог для мастер-пароля в desktop UI
- [ ] E2E тест: Kotlin v1.3.x desktop → Go v2.0.0 desktop на реальных данных

## P2: Улучшения после релиза

### 13. Scheduled backups
- [ ] `citeck snapshot schedule` — cron-like расписание для автоматического экспорта
- [ ] Retention policy (хранить N последних)
- [ ] Настройка через daemon.yml

### 14. Metrics и alerting
- [ ] Prometheus endpoint уже есть (`/api/v1/metrics`)
- [ ] Добавить метрики: restart_count, liveness_failures, image_pull_duration
- [ ] Grafana dashboard template

### 15. Multi-node (future)
- [ ] Docker Swarm mode support
- [ ] Или: документация по ручному распределению сервисов

---

## Завершённые задачи

- [x] Self-healing runtime (liveness probes, failure counting, diagnostics, restart events)
- [x] Offline workspace import (`citeck workspace import`, `citeck install --workspace`)
- [x] Local bundles (empty URL = use workspace repo dir)
- [x] Server mode: auto-encrypt secrets with default password
- [x] Server mode: resolver offline (no auto-pull, `citeck workspace update` for manual sync)
- [x] Remove workspace ID from Docker resource names (server mode only; desktop keeps it for Kotlin compat)
- [x] Dependency upgrades (Go + Web)
- [x] pnpm migration
- [x] `--offline` flag for citeck start
