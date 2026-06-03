# 2.0.0
Complete rewrite from Kotlin/Compose to Go + React. Single binary — CLI, daemon, and embedded Web UI.

## Core
- Daemon: Docker SDK, declarative reconciler (k8s-style), liveness probes with auto-restart and diagnostics
- 21 CLI commands: install, start/stop/restart, upgrade, snapshot, setup, diagnose, completion, etc.
- Web UI: React 19, Lens-inspired, 8 languages, SSE real-time, 50K-line log viewer
- Security: mTLS, Let's Encrypt with auto-renewal (domains + IPs), AES-256-GCM secrets encryption, CSRF

## Install wizard
- Friendly interactive setup: language, hostname, TLS, port, auth, release, systemd, start
- TLS auto-detection: tries Let's Encrypt staging, falls back to self-signed automatically
- Let's Encrypt works with IP addresses (shortlived profile, ~6 day certs, auto-renewed)
- Multi-level release picker: latest per repo at top, "Other version..." for full version list
- Offline mode: `--offline` flag or `--workspace` for air-gapped deployments
- Localized CLI wizard: 8 languages (en, ru, zh, es, de, fr, pt, ja) with JSON locale files
- Final "Citeck is ready!" block with platform URL and login credentials

## DevOps
- Synchronous stop with live progress, --detach mode, snapshot auto-stop/start
- Self-update with SHA256 verification and rollback
- Bundle upgrade command with dry-run
- Image cleanup (dangling prune)
- Keycloak 26+ liveness probe on management port 9000
