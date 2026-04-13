# Release 2.1.0

## Architecture: unified service account

- Unified `citeck` service account across Keycloak (master realm, admin role) and RabbitMQ (monitoring tag, vhost `/` full perms). Webapps connect to RabbitMQ as the stable SA instead of the user-facing `admin` user.
- `citeck setup admin-password` rotates passwords in **all four** admin UIs: Keycloak `ecos-app` + `master` realms, RabbitMQ admin, PgAdmin — **without** recreating webapp containers (SA password is stable, so env vars don't change).
- Auth-mode switch (`citeck setup auth` Keycloak ↔ Basic) recreates only `proxy` / `emodel` / `keycloak` — not all webapps (fixed hash-input: webapps' `DependsOn(keycloak)` is now unconditional).
- Migration: `_launcher_sa` secret auto-migrates to `_citeck_sa` on daemon start; legacy `citeck-launcher` Keycloak user deleted on next init cycle. No user action needed.
- Reused-container Phase 2 liveness probe removed — the reconciler's threshold-based liveness loop handles hung containers without the single-shot flake cycle.

## New CLI

- `citeck dump-system-info` — collects full diagnostics into `./citeck-dump-<timestamp>.zip` in the current directory (status JSON, health, diagnose, logs, docker inspect, journalctl, trimmed container logs). `--full` keeps container logs untrimmed. Replaces the manual "collect these 10 commands" instruction in troubleshooting.
- `citeck stop [app...]` — accepts multiple app names in a single command.
- `citeck start <app>` / `citeck restart` / `citeck snapshot import` — wait for RUNNING by default with live status; `-d/--detach` to skip wait; Ctrl+C → "continues in background".
- `citeck upgrade` — tabbed picker per bundle repo (community / enterprise / …), cross-repo switch prompts for registry credentials if missing, confirmation prompt before applying (default Yes), `--yes/-y` for scripts, fail-fast on non-TTY without version arg. Accepts explicit `<bundle>:<version>` arg.
- `citeck start` — delegates to `systemctl start citeck` when the systemd unit is installed (for proper journald logging and auto-restart); `-d/--detach` forces the direct-fork path.

## Per-app detach / adopt

- `citeck stop <app>` / `citeck start <app>` with persistence across restart/reload (k8s desired-state-first pattern)
- Template `detachedApps` from workspace config — apps can be pre-detached by bundle definition
- Detached apps excluded from start/reload/regenerate, skipped by reconciler + liveness, treated as satisfied in `waitForDeps`

## Zero-downtime binary upgrade

- New `--leave-running` mode on `citeck stop --shutdown` exits the daemon without touching platform containers
- The replacement daemon adopts the live containers via the existing deployment-hash matching path (k8s-style control-plane restart)
- ACME renewal and cloud-config server stopped before the runtime so a late renewal callback cannot tear down the proxy during a detach
- Daemon shutdown HTTP endpoint accepts `?leave_running=true` (strict bool parse, 400 on invalid input)
- Upgrades from v2.0.0 preserve the platform: the new binary SIGKILLs the old daemon (Docker owns containers, they stay alive) and then adopts them — `ApplicationDef.GetHashInput` is byte-identical between v2.0.0 and v2.1.0, so hash matching works across versions
- When running under systemd, the unit is masked before the kill so `Restart=on-failure` doesn't respawn the old binary during the swap window

## `citeck install` binary lifecycle

- `citeck install` handles its own binary bootstrap: when invoked from a binary outside `/usr/local/bin/citeck`, it auto-detects fresh install / in-place upgrade / no-op, then hands off to the setup wizard.
- Fresh install: atomic copy self → `/usr/local/bin/citeck`, then `syscall.Exec` to re-exec from the installed path.
- Upgrade: confirm → backup to `citeck.bak` → stop old daemon preserving platform → atomic swap → start new daemon via systemd or detached fork.
- Rollback: `citeck install --rollback` restores from `citeck.bak` and restarts.
- `copyBinaryAtomic` uses `rename(2)` — safe even when the destination is currently being executed (Linux preserves the running process's inode).
- All stop-and-swap coordination lives in `internal/cli/installer_lifecycle.go` with unit tests, replacing ~370 lines of untestable shell.

## install.sh (minimal bootstrap)

- ~180 lines: detect platform, fetch latest stable v2.x tag from GitHub, download tar.gz + `.sha256`, **verify SHA256**, extract `citeck` binary, exec `citeck install`.
- Pinned to v2.x releases, skips semver pre-release identifiers via `*-*` pattern.
- `sha256sum` / `shasum -a 256` auto-detection (Linux + macOS).
- `--file <path>` for offline installs; `--rollback` delegates to `citeck install --rollback`.
- Same one-liner works for installs and upgrades.

## TUI and UX polish

- huh migration: all CLI prompts use `charmbracelet/huh` Select / Input / Confirm with arrow-key navigation and validation.
- **Esc cancels inputs** (huh default keymap binds only Ctrl+C — we wrap forms with a keymap that binds both).
- **Viewport fix** for huh Select: works around a bug where the option list collapses to the cursor row when `Height` is set on short lists (only set Height for >12 options).
- **Tabbed bundle picker** (bubbletea + lipgloss) for `citeck install` and `citeck upgrade` — cyan-background active tab, ←/→ switch tab, ↑/↓ move, uses alt-screen so the picker frame disappears cleanly on exit.
- **Confirmation prompt** on `citeck upgrade` (default Yes), with readable button colors (pink bg + dark fg for focused, dim for blurred).
- **Heap guard** in `citeck setup resources` — validates heap format (`^\d+(\.\d+)?[mMgG]$`) AND enforces memory-limit headroom (no OOM loop). Hard-block via `huh.NewNote` (survives alt-screen repaint).
- **Port prefill** for `setup email` uses `Placeholder` instead of `Value` — typing replaces without needing backspace.
- **NO_COLOR + non-TTY detection** drops ANSI from output, so `citeck status | grep -c RUNNING` works in pipes.
- **Honest LE messaging** in install wizard — detects rate-limit via `acme.IsRateLimited` before claiming "trusted cert will be used".
- **Shell-safe template quoting** (`shquote`) for Keycloak init script; hostname validation rejects shell metacharacters.

## `citeck setup` interactive config editor

- TUI-based settings editor with arrow-key navigation, history, and rollback.
- Reload integrates with live status streaming and a 3-option confirm dialog.
- Per-setting `CurrentValue` strings localized across all 8 locales.

## Install wizard

- 9 unique-numbered steps (duplicate "Step 4" fixed); numbering matches `quick_start.rst`.
- New snapshot selection step for demo-data deployment.
- Already-installed message shows version + build date and points to `citeck setup`.
- TLS "Auto" mode resolves at install time to a **concrete** choice (LE or self-signed based on probe + rate-limit) — no `Auto` flag persisted in `namespace.yml`.

## Config

- **Secret refs**: s3/email passwords stored as `secret:s3.secretKey` / `secret:email.password` refs in `namespace.yml` (plain values encrypted in `/opt/citeck/conf/secrets/*.json`).
- `stopTimeout` default: **10s → 15s** (better grace for heavy webapps).
- Reconciler max backoff: **30m → 10m** (faster retry after transient failures).
- Logger injected into `bundle.Resolver` — no more `slog.SetDefault` global mutation.

## Diagnose / health

- `citeck diagnose` elevates FAILED / START_FAILED apps to ERROR (was only WARN); prints `→ see docs:` pointer into `troubleshooting.rst`.
- Port 443 check: **OK** when the port is held by our own proxy container (label `citeck.launcher=true`), not a spurious WARN.
- `citeck health` banner matches exit code (0 HEALTHY / 1 DAEMON DOWN / 8 UNHEALTHY).
- Stale socket file distinguished from missing socket — clearer message, more actionable fix.

## Build / CI

- **Prod-grade release pipeline** (`.github/workflows/release-go.yml`):
  - Matrix build: `linux/{amd64,arm64}` + `darwin/{amd64,arm64}`
  - Artifacts packaged as `citeck_<ver>_<os>_<arch>.tar.gz` (with `citeck` binary inside) + `<asset>.sha256` sidecar
  - `install.sh` uploaded as release asset (for the one-liner)
  - `version`, `gitCommit`, `buildDate` all stamped via `-ldflags -X`
  - Releases published directly (no `draft: true`)
- **Cross-compile check in CI** (`ci.yml`) — catches platform-specific bugs on PR/push (caught a real `syscall.Statfs_t.Bsize` int64/uint32 mismatch on Darwin, now split into `diskspace_{linux,darwin}.go` with build tags).
- **h2migrate JAR** gated to `desktop` build tag — server binary is ~988 KB smaller (24.6 MB → 23.6 MB).
- Keycloak init script extracted to `text/template` with golden tests; fails loud on render error (no silent fallback).
- Makefile and CI pin `golangci-lint` to v2.11.4.

## Brand: Citeck ECOS → Citeck

- User-facing strings, UI labels, Wails app description, desktop `.desktop` file, tests, quick-links renamed from "Citeck ECOS" / "ECOS" → "Citeck".
- External contract names **kept** for compatibility: `ECOS_*` env vars (Spring Boot contract), Docker image names (`ecos-apps`, `ecos-model`, …), Keycloak realm (`ecos-app`).

## Docs and i18n

- Removed internal `docs/` folder from repo (moved internal api/config/operations refs to `ecos-docs` on RTD; working-session `docs/superpowers/` moved outside git).
- READMEs link to canonical RTD: `https://citeck.ru/docs/admin/launch_setup/launcher_server/`.
- `ecos-docs` launcher_server section completely refreshed: drift fixes (30+ findings), new `dump-system-info` entry, secret-refs format section, Citeck Launcher split into "локальный режим" / "серверный режим" articles with mutual `.. seealso::` cross-refs.
- Native-quality re-translation for de / es / fr / ja / pt / zh locales (ru / en reviewed).

## Breaking changes

- `stop --detach` / `-d` unified: **`--no-wait` removed**; all long-running commands use `-d`/`--detach` consistently (`stop`, `reload`, `start`, `restart`, `snapshot import`).
- `restart --wait` flag removed — waits by default; use `-d/--detach` to skip.
- `clean --execute` — **deprecated alias of `--force`** (alias kept for back-compat).
- `start --desktop` / `--no-ui` / `_daemon` hidden from server binary help.
- `citeck status -a/--apps` **removed** (was a no-op; the app table is always shown).

## Fixes

- Keycloak `ecos-app` admin password not applied on fresh install
- Email SMTP config via env vars (broken CloudConfig path)
- Proxy crash when email configured (stale mailhog container reference)
- Snapshot import name normalization, pre-flight validation, event mismatch
- Detach not respected during reload/regenerate
- Proxy DEPS_WAITING when onlyoffice detached
- `citeck install` idempotent message shows real version (not `vdev (unknown)`)
- Keycloak init script username check: exact-match (awk) instead of substring `grep` — upgrade path from `citeck-launcher` SA no longer skips new-SA creation
- Warnings silenced on `citeck update` when a bundle repo has no bundle yet (`ErrNoBundles` sentinel) — community-rc/enterprise-rc no longer spew "no such file or directory"
- Install script injection defense: all user-derived values (BaseURL, OIDCSecret, hostnames, passwords) pass through `shquote`

## Tests

- Behavioral tests for detach + adopt cycle
- Phase 3 reconciler tests: liveness restart triggered; race-free assertions for post-trigger app status
- Golden-file tests for Keycloak init script (fresh / configured / no-sa / malicious-hostname)
- 128 new i18n entries validated for placeholder consistency across 8 locales
- `mockDocker` tracks stop/remove calls and mutates its container map

# Release 2.0.0

Complete rewrite from Kotlin/Compose to Go + React. Single binary — CLI, daemon, and embedded Web UI.

## Core
- Daemon: Docker SDK, declarative reconciler (k8s-style), liveness probes with auto-restart and diagnostics
- 24 CLI commands: install, start/stop/restart, upgrade, snapshot, self-update, webui cert, etc.
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

# Release 1.3.9

## Fixes

* Fixed NPE in deleteNetwork when Network.getContainers() returns null

# Release 1.3.8

## New features

* Added Docker availability check before application startup with a user-friendly screen and retry option

# Release 1.3.7

## New features

* Added bundle info to citeck-apps

# Release 1.3.6

## Fixes

* Fixed startup error in logs 'Cannot construct instance of BundleKey'

# Release 1.3.5

## New features

* Added links to Citeck documentation and AI documentation bot (hAski Citeck)

# Release 1.3.4

## Updates

* Updated sorting rules for bundle keys

# Release 1.3.3

## Fixes

* Fixed permission denied error for script init_db_and_user.sh

# Release 1.3.2

## Fixes

* Fixed Citeck logo
* Resolved issues with Chrome’s dev-mobile mode

# Release 1.3.1

## New features

* Added tray actions: Open Launcher Directory and Dump System Info

## Fixes

* Fixed launcher freeze when pulling from git without internet connection
* Fixed incorrect dropdown width in select controls

# Release 1.3.0

## New features

* Edit Spring properties and other external configuration files directly from the UI
* View container and namespace CPU/memory statistics
* Advanced logs viewer with better navigation and filtering
* Tooltips for all actions to improve usability

## Fixes

* Launcher logs now update correctly in real time
* Fixed various UI issues

# Release 1.2.4

## Fixes

* Fixed bundle for quick start buttons
* Resolved incorrect behavior in automatic bundle selection after repository updates
* Fixed an issue where detached apps started automatically after launcher restart

# Release 1.2.3

## New features

* Added support for closing the Invalid Password dialog with the Enter key

## Fixes

* Fixed interface freeze that occurred when switching workspaces

# Release 1.2.2

## New features

* Added advanced editor for proc def

# Release 1.2.1

## New features

* Reordered bundles to improve consistency
* Refactored the select control for better usability

## Fixes

* Added error handling for namespace generation failure
* Corrected the logs window size

# Release 1.2.0

## Updates

* Update keycloak 26.3.1 -> 26.4.5
* Update zookeeper 3.9.3 -> 3.9.4
* Update pgadmin 8.13.0 -> 9.10.0
* Update onlyoffice 9.0.3.1 -> 9.1.0.1

## New features

* Add snapshots dialog
* Add support of bundles with config under 'ecos' key
* Add ability to configure postgres, pgadmin, zookeeper, keycloak version

## Fixes

* Fixed an issue where pulling the image could hang
* Fix permissions issue with restoring pgadmin from snapshot

# Release 1.1.10

## New features

* Added ability to pull latest workspace changes
* Added support for numpad Enter when submitting

## Fixes

* Fixed merge conflicts on git pull
* Fixed stalled namespace state in some cases
* Increased shared memory size for Postgres

# Release 1.1.9

## New features

* Added support for editing and deleting namespaces directly from the welcome screen

# Release 1.1.8

## New features

* Added Release Github Workflow

# Release 1.1.7

## New features

* Added 'Open' action in tray menu

## Fixes

* Removed unnecessary borders on namespace screen
* Fixed "Already resumed, but proposed with update"
* Fixed macos tray icon

# Release 1.1.6

## Fixes

* Fixed "HTTPS required" error when using local Keycloak
* Fixed "rememberCoroutineScope left the composition" error in UI

# Release 1.1.5

## New features

* Updated OnlyOffice to version **9.0.3.1**

## Fixes

* Increased default memory limit for OnlyOffice from **1 GB** to **3 GB**

# Release 1.1.4

## New features

* Introduced a new dialog system: less boilerplate, unified and consistent design.

## Fixes

* Fixed issue causing unnecessary database restart when switching authentication method from Basic to Keycloak.
* Removed duplicate tooltip on namespace name.

# Release 1.1.3

## New features

* Added the ability to edit a namespace without stopping all services
* Added the option to update kits from the repository directly in the namespace edit form
* Renamed page title to **“Citeck Launcher”**

## Fixes

* Fixed scrolling issue in the app definition editor
* Fixed DockerImageNotFound error handling
* Fixed loading of bundles that differ only by the RC suffix

# Release 1.1.2

## New features

* Added project name to group all containers into a single collection in Docker Desktop
* Added ability to cancel git pull operation
* Added default name for newly created namespaces

## Fixes

* Updated dependencies to remove known vulnerabilities

# Release 1.1.1

## New features

- Added ability to start individual applications even when the namespace is stopped
- Improved namespace form for better user experience

## Fixes

- Fixed issue where applications could start in the wrong order
- Fixed problem with pgAdmin after creating a namespace from backup
- Fixed incorrect state of the welcome screen when quick start buttons didn’t update after switching workspaces

# Release 1.1.0

## New features
- Added ability to start the system with demo data
- Added links to administration tools: **Keycloak**, **Mailhog**, **RabbitMQ**, **Spring Boot Admin**, **PG Admin**
- Added **OnlyOffice** integration
- Added Keycloak support and option to switch between **Basic Auth** and **Keycloak**
- Added ability to configure detached apps in workspace (apps that don’t start by default but can be started manually)
- Added **ports** column to the applications table

## Updates
- PostgreSQL upgraded from `13.17.0` → `17.5`
- RabbitMQ upgraded from `4.0.3` → `4.1.2`

## Fixes
- Fixed "port already in use" issue
- Fixed issues with **STALLED** state
- Fixed docker images repository authentication problem

