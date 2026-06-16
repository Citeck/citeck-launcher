## New features
- Zoom the interface with the native webview zoom controls.
- Restart a single app from a right-click menu, available in any app state.

## Fixes
- Server install with demo data now actually imports the demo snapshot — previously the namespace was created but started with empty data.
- Server install now saves the private-registry login you enter during setup, instead of silently discarding it.
- Closer parity with the 1.x launcher for migrated or hand-edited configs: default namespace users (admin + fet), bundle/workspace settings, and probe/log-startup defaults.
- You're now asked to set a master password before adding your first secret, instead of getting an error.
- Quick Start pins the newest version to a concrete release, refreshes the available versions after you unlock secrets, and opens the namespace panel as soon as it starts.
- Custom workspace repositories recover automatically after you add or fix their access token — no restart needed.
- Saved registry credentials are bound to their registry host immediately.
- Secret values are clearly labelled Token or Password.
- Snapshots dialog: one shared header and an automatic refresh when an export or import finishes.
- Higher-contrast app detail panel; the configuration editor now follows the app theme.
- More readable log text, and a hint explaining when "Delete all" is disabled.
- Deleting a workspace now also removes its saved registry credential bindings.
