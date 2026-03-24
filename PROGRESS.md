# Progress Log

## V1 — COMPLETE (2026-03-24)

All priorities done. 11 commits on `release/1.4.0`.

- **Bug fixes:** TLS startup probe, proxyBaseUrl port, install script restart, lua bind mount, shadow JAR resources, appfiles prefix, daemon auto-start
- **CLI commands (8):** logs, restart, inspect, exec, version, health, config show, config validate
- **E2E tests (5/5):** BASIC+HTTP, BASIC+TLS, KC+custom+TLS, KC+HTTP, BASIC+custom+8443
- **Unit tests:** NsGenContext.proxyBaseUrl, NamespaceConfig YAML

## V2 — Plan: `AGENT_PLAN_V2.md`

### Phase 1: `--output json` + structured errors + exit codes
- [ ] 1.1 Global `--output` flag
- [ ] 1.2 Structured errors in DaemonClient
- [ ] 1.3 Machine-readable exit codes
- [ ] 1.4 `--dry-run` on all mutating commands
