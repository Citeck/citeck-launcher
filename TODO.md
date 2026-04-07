# Production Readiness TODO

Задачи для выпуска серверного launcher v2.0.0 в прод.

## P0: Блокеры релиза

### ~~1. Bundle upgrade (`citeck upgrade`)~~ ✅ DONE
- [x] CLI: `citeck upgrade [bundle-ref]` — смена bundleRef в namespace.yml + reload
- [x] CLI: `citeck upgrade --list` — показать доступные версии (из workspace config)
- [x] Web UI: кнопка upgrade на Dashboard с выбором версии

### ~~2. Docker image cleanup~~ ✅ DONE
- [x] CLI: `citeck clean --images` — удалить неиспользуемые dangling-образы
- [ ] Автоочистка после upgrade (удалить образы предыдущей версии бандла)

### 3. Welcome/Wizard в серверном режиме
- [ ] Серверный режим без namespace: Web UI показывает Welcome с кнопкой "Create Namespace"
- [ ] Wizard должен показывать доступные бандл-версии (из local repo или cloned bundles)

### 4. README.md
- [ ] Quick start: скачать бинарь → `citeck install` → `citeck start` → открыть UI
- [ ] Offline quick start: скачать бинарь + workspace.zip → `citeck install --workspace` → `citeck start`
- [ ] Архитектура, CLI reference, ссылки на docs/

### ~~5. Обновить документацию под текущее состояние~~ ✅ DONE
- [x] CLAUDE.md: Phase 18 added (upgrade, image cleanup, bundle listing fix, docs)
- [x] docs/config.md: CLI flags, workspace commands, offline mode, upgrade, image cleanup
- [x] docs/operations.md: bundle upgrade, offline deployment, image cleanup
- [x] docs/api.md: POST /namespace/upgrade endpoint documented

### 6. Server deployment testing
- [ ] Чистая установка: `citeck install --workspace` → `citeck start` → все сервисы RUNNING
- [ ] Bundle upgrade: смена версии → контейнеры пересозданы
- [ ] Daemon restart: restart events переживают рестарт
- [ ] Liveness probe: убить контейнер → автоперезапуск → diagnostics файл создан
- [ ] Web UI: Dashboard, restart events panel, restart count badge

## P1: Важно, но не блокирует релиз

### 7. Self-update лончера
- [ ] `citeck self-update` или документация по ручному обновлению через systemd

### 8. Проверка совместимости при upgrade
- [ ] Bundle может декларировать `minLauncherVersion` и `migrationNotes`

### 9. Системные требования и ограничения
- [ ] Задокументировать: минимум 4 CPU / 16GB RAM / 50GB disk, Docker 24+
- [ ] Серверный режим = один namespace на инстанс

### 10. CI/CD pipeline
- [ ] Проверить release workflow (goreleaser) с pnpm
- [ ] Проверить CI на чистом runner

### 11. Desktop: E2E миграция Kotlin → Go
- [ ] Тест на реальных данных: Kotlin v1.3.x → Go v2.0.0

## P2: После релиза

- [ ] Scheduled backups (snapshot schedule + retention)
- [ ] Metrics (restart_count, liveness_failures, image_pull_duration) + Grafana template
- [ ] Multi-node (Docker Swarm или документация)
