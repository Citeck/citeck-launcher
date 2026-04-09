# Release 2.1.0

## Zero-downtime binary upgrade
- New `--leave-running` mode on `citeck stop --shutdown` exits the daemon without touching platform containers
- The replacement daemon adopts the live containers via the existing deployment-hash matching path (k8s-style control-plane restart)
- ACME renewal and cloud-config server are now stopped before the runtime so a late renewal callback cannot tear down the proxy during a detach
- Daemon shutdown HTTP endpoint accepts `?leave_running=true` (strict bool parse, 400 on invalid input)
- Upgrades from v2.0.0 also preserve the platform: the new binary SIGKILLs the old daemon (Docker owns the containers, so they stay alive) and then adopts them — `ApplicationDef.GetHashInput` is byte-identical between v2.0.0 and v2.1.0, so hash matching works across versions
- When running under systemd, the unit is masked before the kill so `Restart=on-failure` doesn't respawn the old binary during the swap window; the start phase unmasks and starts the new binary

## `citeck install` binary lifecycle (replaces `citeck self-update`)
- `citeck install` now handles its own binary bootstrap: when invoked from a binary outside `/usr/local/bin/citeck`, it auto-detects whether to do a fresh install, an in-place upgrade, or a no-op (same version), then hands off to the setup wizard for configuration
- Fresh install: atomic copy self → `/usr/local/bin/citeck`, then `syscall.Exec` to re-exec from the installed path so `forkDaemon` uses the right location
- Upgrade: confirm → backup current binary to `citeck.bak` → stop old daemon preserving platform (detach for v2.1.0+, SIGKILL for v2.0.0) → atomic swap via `fsutil.AtomicWriteFile` → start new daemon via systemd or detached fork
- Rollback: `citeck install --rollback` restores from `citeck.bak`, stops the current daemon and starts the restored one — covers the case where an upgrade went wrong
- `versionAtLeast` semver helper handles the v2.1.0 feature-detection for picking between clean detach and SIGKILL
- `copyBinaryAtomic` uses `rename(2)` via `fsutil.AtomicWriteFile` — safe even when the destination is currently being executed (Linux preserves the running process's inode; only the directory entry changes)
- All stop-and-swap coordination (systemd mask/unmask, PID polling, socket cleanup) lives in `internal/cli/installer_lifecycle.go` with unit tests, replacing ~370 lines of untestable shell

## install.sh (minimal bootstrap)
- Shrunk from ~420 to ~180 lines — only does what must happen before the binary exists on disk: detect platform, fetch latest stable v2.x tag from GitHub, download to temp, exec `<new-binary> install`
- Pinned to v2.x releases, skips semver pre-release identifiers (`v2.1.0-rc1`) via a `*-*` pattern that's independent of GitHub's own "prerelease" flag
- Detects v2.0.0 binaries via fallback parser when `citeck version --short` is unavailable
- If installed version already matches the latest, skips the download entirely and execs `citeck install` on the already-installed binary
- `--file <path>` for offline / local-binary installs
- `--rollback` delegates straight to `citeck install --rollback` — no shell logic
- Same one-liner works for installs and upgrades (documented in README)

## huh TUI migration (all CLI user interactions)
- Replaced `bufio.Scanner`-based prompts with `charmbracelet/huh` Select / Input / Confirm wrappers
- Arrow-key navigation, validation, and TTY-aware rendering across the install wizard, registry auth, password reset, snapshot/clean/uninstall/workspace prompts
- New `promptPassword` (huh `EchoModePassword`) for registry credentials; master-password input still uses `term.ReadPassword`

## `citeck setup` interactive config editor
- TUI-based settings editor with arrow-key navigation, history, and rollback
- Reload integrates with live status streaming and a 3-option confirm dialog
- Per-setting `CurrentValue` strings localized across all 8 locales (en, ru, de, es, fr, ja, pt, zh)

## Install wizard
- New snapshot selection step for demo-data deployment
- Already-installed message shows version + build date and points to `citeck setup`

## Output / CLI polish
- Single `FormatAppTable` in the `output` package with ANSI-aware column alignment
- Shared TTY helpers (`output.IsTTY` / `output.ClearLines`)
- Synchronous stop with live progress, `--detach` mode
- `citeck status --watch` no longer leaks the pre-watch frame above the live table and no longer stacks duplicate rows each redraw (fixed off-by-one in `ClearLines` + moved the initial render into `watchEvents` itself)

## Tooling
- Makefile and CI now pin `golangci-lint` to v2.11.4 (was split between v2.7.2 and v2.11.4, which caused CI failures on taint-analysis warnings the older local version didn't catch)
- Three remaining gosec G703 false positives on already-validated paths suppressed with `//nolint` comments and explicit justifications (`routes_snapshots.go` rename, `routes_volumes.go` delete)

## Tests
- Behavioral tests for the detach + adopt cycle: `TestDetachLeavesContainersRunning`, `TestDetachThenAdopt` (asserts the new daemon does not recreate containers), `TestDetachWhileStopping`
- `mockDocker` now tracks stop/remove calls and actually mutates its container map

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

