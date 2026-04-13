# Production Readiness TODO

## P0: Server deployment testing — DONE (2026-04-07)

- [x] Clean install: `citeck install --workspace` -> `citeck start` -> all services RUNNING
- [x] Bundle upgrade: version change -> containers recreated
- [x] Daemon restart: restart events survive restart
- [x] Liveness probe: kill container -> auto-restart -> diagnostics file created
- [x] Web UI: Dashboard, restart events panel, restart count badge
- [x] Snapshot export/import cycle: auto-stop -> export -> start -> stop -> import -> start
- [x] Binary upgrade: `install.sh` one-liner -> zero-downtime swap -> rollback (`citeck self-update` removed in 2.1.0, replaced by `install.sh` which preserves running platform containers via detach/SIGKILL path)
- [x] mTLS: `citeck webui cert --name admin` -> .p12 import in browser -> Web UI accessible
- [x] Keycloak 26+ liveness probe on management port 9000
- [x] Let's Encrypt with IP address (shortlived profile, verified on test server)

## P0.5: Server Testing Phase 2 — DONE (2026-04-11/12)

- [x] Enterprise deployment (registry auth, 24 apps)
- [x] S3 storage end-to-end (Fake-S3 -> content service -> bucket verified)
- [x] SMTP email delivery verified (MailPit)
- [x] Per-app detach/attach with persistence across restart/reload
- [x] Unified `citeck` service account in Keycloak master realm + RabbitMQ (webapps no longer restart on admin-password change)
- [x] Snapshot import name normalization, pre-flight validation
- [x] 14 bugs found and fixed, 2 code review rounds
- [x] CLI flag cleanup (stop --no-wait, clean --force)
- [x] Documentation: 7 RST pages in ecos-docs + CLAUDE.md update

## P1: Important but not release-blocking

- [ ] Upgrade compatibility check: `minLauncherVersion` and `migrationNotes` in bundle
- [x] Document system requirements: 4 CPU / 16GB RAM / 50GB disk, Docker 24+ (done in ecos-docs)
- [ ] Verify CI/CD: release workflow + CI on release/2.0.0
- [ ] Auto-cleanup images after upgrade
- [ ] `citeck clean --images` dry-run: show how much space can be freed

## P2: Post-release

- [ ] Scheduled backups (snapshot schedule + retention)
- [ ] Metrics (restart_count, liveness_failures, image_pull_duration) + Grafana template
- [ ] Multi-node (Docker Swarm or documentation)
