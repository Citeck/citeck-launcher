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

