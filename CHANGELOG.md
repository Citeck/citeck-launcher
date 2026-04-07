# Release 2.0.0

## Major changes

* Complete rewrite from Kotlin/Compose to Go + React (single 14MB binary)
* Embedded React Web UI served on `http://127.0.0.1:7088`

## Bundle version upgrade support

* Hot-reload bundle version via `citeck reload` (e.g. 2025.12 → 2026.1)
* Smart regeneration: only recreated containers whose config/image changed, infrastructure reused via hash matching
* New apps from updated bundles auto-created with database init actions
* Auth type switching (KEYCLOAK ↔ BASIC) with minimal container churn
* Host switching (domain ↔ IP) with automatic TLS cert handling

## Cached bundle fallback

* Last successfully resolved bundle persisted in namespace state file
* On startup/reload: if bundle resolution fails (file deleted, network down), cached bundle used automatically
* Matches Kotlin launcher behavior — platform survives bundle repo changes

## CLI per-app commands

* `citeck start [app]` — start a single app (removes from detached) or the full namespace
* `citeck stop [app]` — stop a single app (marks as detached, won't auto-start) or the full namespace
* `citeck logs [app]` — show daemon logs (no arg) or container logs (with app name)

## Bug fixes

* Fixed workspace config fallback: `_workspace` repo now used when no local `repo/` dir exists
* Fixed `.sh` file permissions on reload (was 0644, now 0755 — matching initial startup)
* Fixed bundle ref display not updating after reload
* Fixed daemon process not exiting after `stop --shutdown`

## Web UI

* Lens-inspired UI: right overlay drawer for app details, bottom panel with tabs for logs/config
* Drag-to-resize bottom panel, lazy tab mounting, active-only streaming
* i18n: 8 languages (English, Russian, Chinese, Spanish, German, French, Portuguese, Japanese)
* Language selector with flags in tab bar, auto-detection from browser
* Full service catalog visible even when namespace is stopped
* Human-readable status names (Waiting, Queued, Ready instead of raw enums)
* Toast notifications on all actions (start/stop/restart, config save, secret create/delete, etc.)
* Loading skeletons on Volumes and Secrets pages
* Refined Darcula palette with active tab accent lines, status dot indicators
* Server mode: Dashboard shown directly at root (no Welcome screen)

## Bundle upgrade

* `citeck upgrade [ref]` — change bundle version and reload
* `citeck upgrade --list` — show available versions with current marked
* Web UI upgrade button in Dashboard sidebar with version picker
* `POST /api/v1/namespace/upgrade` API endpoint

## Snapshot improvements

* `citeck snapshot export` auto-stops namespace, exports, then auto-starts
* `citeck snapshot export --dir /mnt/backup/` — write directly to specified directory
* Interactive prompts for output directory and stop confirmation (`--yes` skips)

## Docker image cleanup

* `citeck clean --images --execute` — prune dangling Docker images after confirmation

## CLI

* Interactive install wizard with language selection, port availability check
* Shell completion (bash/zsh/fish/powershell)
* `citeck validate` command for offline config validation
* Server mode: `citeck start` requires namespace.yml (run `citeck install` first)
* `citeck workspace import|update` for offline workspace management

## Security

* mTLS for non-localhost Web UI access (client certificates)
* CSRF protection for localhost TCP (X-Citeck-CSRF header)
* Unix socket + TCP two-mux security architecture
* HTTP security headers (X-Frame-Options, CSP, X-Content-Type-Options, HSTS)
* TLS 1.3 minimum for mTLS connections
* Server-side secret masking in API responses
* Internal error suppression (generic 500 to client, full error in daemon log)

## Infrastructure

* ACME/Let's Encrypt auto-renewal
* Prometheus metrics endpoint (`/api/v1/metrics`)
* HTTP request metrics (counter + latency histogram)
* SSE heartbeat, event sequence numbers, gap detection
* System dump with daemon logs
* Runtime log level control
* SQLite schema versioning with transactional migrations
* Snapshot export with SHA256 integrity sidecar
* Locale field in daemon.yml for server-wide language preference

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

