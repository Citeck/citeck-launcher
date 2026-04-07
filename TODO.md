# Production Readiness TODO

Задачи для выпуска серверного launcher v2.0.0 в прод.

## P0: Блокеры релиза

### ~~1. Bundle upgrade~~ ✅ DONE
### ~~2. Docker image cleanup~~ ✅ DONE
### ~~3. CLI namespace guard + Wizard~~ ✅ DONE
### ~~4. README.md~~ ✅ DONE
### ~~5. Документация~~ ✅ DONE
### ~~7. Self-update~~ ✅ DONE

### 6. Server deployment testing
- [ ] Чистая установка: `citeck install --workspace` → `citeck start` → все сервисы RUNNING
- [ ] Bundle upgrade: смена версии → контейнеры пересозданы
- [ ] Daemon restart: restart events переживают рестарт
- [ ] Liveness probe: убить контейнер → автоперезапуск → diagnostics файл создан
- [ ] Web UI: Dashboard, restart events panel, restart count badge
- [ ] Snapshot export/import cycle: auto-stop → export → start → stop → import → start
- [ ] Self-update: `citeck self-update --file` → замена бинаря → rollback
- [ ] mTLS: `citeck webui cert --name admin` → .p12 import в браузер → Web UI доступен

## P1: Важно, но не блокирует релиз

### 8. Проверка совместимости при upgrade
- [ ] Bundle может декларировать `minLauncherVersion` и `migrationNotes`

### 9. Системные требования
- [ ] Задокументировать: минимум 4 CPU / 16GB RAM / 50GB disk, Docker 24+

### 10. CI/CD pipeline
- [ ] Проверить release workflow на чистом runner
- [ ] Проверить CI на release/2.0.0 ветке

### 11. Desktop: E2E миграция Kotlin → Go
- [ ] Тест на реальных данных: Kotlin v1.3.x → Go v2.0.0

### 12. Автоочистка образов после upgrade
- [ ] Удалять образы предыдущей версии бандла после успешного upgrade

### 13. Image dry-run в `citeck clean`
- [ ] `citeck clean --images` без `--execute` показывает сколько места можно освободить

## P2: После релиза

- [ ] Scheduled backups (snapshot schedule + retention)
- [ ] Metrics (restart_count, liveness_failures, image_pull_duration) + Grafana template
- [ ] Multi-node (Docker Swarm или документация)
- [ ] Hot backup (pg_dump + ZK snapshot без остановки namespace)
