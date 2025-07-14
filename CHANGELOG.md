
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

