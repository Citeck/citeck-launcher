## New features

- Webapp companion settings (`webapps[].companions`): workspaces can wire environment variables and startup dependencies to webapps only while a companion service (such as an optional LLM gateway) is present on the stand, while keeping namespace overrides in priority.
- Stand PostgreSQL databases for additional apps (`additionalApps[].postgresDatabases`): custom services can request databases in the stand's main PostgreSQL cluster so they participate in migrations, snapshots and pgAdmin without recreating PostgreSQL when toggling features.
- Localized tooltips for custom sidebar links (`links[].descriptions`): workspace links now support per-language tooltips with backward-compatible fallback, and the desktop UI refreshes them immediately when switching language.
- Added the LiteLLM sidebar icon and updated the Mailpit icon.
