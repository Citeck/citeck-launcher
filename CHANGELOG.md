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

