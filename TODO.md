# Production Readiness TODO

## P0: Server deployment testing

- [ ] Чистая установка: `citeck install --workspace` → `citeck start` → все сервисы RUNNING
- [ ] Bundle upgrade: смена версии → контейнеры пересозданы
- [ ] Daemon restart: restart events переживают рестарт
- [ ] Liveness probe: убить контейнер → автоперезапуск → diagnostics файл создан
- [ ] Web UI: Dashboard, restart events panel, restart count badge
- [ ] Snapshot export/import cycle: auto-stop → export → start → stop → import → start
- [ ] Self-update: `citeck self-update --file` → замена бинаря → rollback
- [ ] mTLS: `citeck webui cert --name admin` → .p12 import в браузер → Web UI доступен

## P1: Важно, но не блокирует релиз

- [ ] Проверка совместимости при upgrade: `minLauncherVersion` и `migrationNotes` в бандле
- [ ] Документировать системные требования: 4 CPU / 16GB RAM / 50GB disk, Docker 24+
- [ ] Проверить CI/CD: release workflow + CI на release/2.0.0
- [ ] Автоочистка образов после upgrade
- [ ] `citeck clean --images` dry-run: показать сколько места можно освободить

## P2: После релиза

- [ ] Scheduled backups (snapshot schedule + retention)
- [ ] Metrics (restart_count, liveness_failures, image_pull_duration) + Grafana template
- [ ] Multi-node (Docker Swarm или документация)

