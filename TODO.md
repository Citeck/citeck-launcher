# Production Readiness TODO

## P0: Server deployment testing — DONE (2026-04-07)

- [x] Clean install: `citeck install --workspace` -> `citeck start` -> all services RUNNING
- [x] Bundle upgrade: version change -> containers recreated
- [x] Daemon restart: restart events survive restart
- [x] Liveness probe: kill container -> auto-restart -> diagnostics file created
- [x] Web UI: Dashboard, restart events panel, restart count badge
- [x] Snapshot export/import cycle: auto-stop -> export -> start -> stop -> import -> start
- [x] Self-update: `citeck self-update --file` -> binary replace -> rollback
- [x] mTLS: `citeck webui cert --name admin` -> .p12 import in browser -> Web UI accessible
- [x] Keycloak 26+ liveness probe on management port 9000
- [x] Let's Encrypt with IP address (shortlived profile, verified on test server)

## P1: Important but not release-blocking

- [ ] Upgrade compatibility check: `minLauncherVersion` and `migrationNotes` in bundle
- [ ] Document system requirements: 4 CPU / 16GB RAM / 50GB disk, Docker 24+
- [ ] Verify CI/CD: release workflow + CI on release/2.0.0
- [ ] Auto-cleanup images after upgrade
- [ ] `citeck clean --images` dry-run: show how much space can be freed

## P2: Post-release

- [ ] Scheduled backups (snapshot schedule + retention)
- [ ] Metrics (restart_count, liveness_failures, image_pull_duration) + Grafana template
- [ ] Multi-node (Docker Swarm or documentation)
