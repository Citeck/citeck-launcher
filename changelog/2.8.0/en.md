## New features
- **Custom links in the sidebar.** Declare your own quick links in the workspace config (`workspace-v1.yml` → `links:`), each with an optional `dependsOn` list of apps. A link is hidden while a dependency is not part of the namespace, disabled while a dependency is not running, and enabled otherwise. Custom links appear at the bottom of the sidebar.
- **Edit app configuration from the command line.** `citeck edit <app>` opens an app's effective configuration in your `$EDITOR` and saves it as a per-app override (like `kubectl edit`). `--file <path>` edits a mounted config file such as `application-launcher.yml` instead, `--list-files` lists the editable files, `--reset` restores the generated default, and `--from <file|->` sets the content without opening an editor.

## Changes
- **Edited mounted config files now reach the running app.** Saving a change to a mounted configuration file (for example `application-launcher.yml`) now recreates the affected container so the new content is applied; previously the edit was saved but did not take effect until the next unrelated change. Upgrading recreates the web-application containers once (their deployment hash changes) — no action needed.
- The Keycloak memory limit was raised from 1 GB to 1.5 GB.
- Memory limits now accept fractional units (for example `1.5g`).
