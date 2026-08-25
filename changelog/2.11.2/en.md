## Fixes
- **Upgrading from 1.x now brings over all your workspaces and namespaces.** Only one entry per storage page was being read, so an installation with 19 namespaces migrated a single one — and reported the migration as clean. Workspaces, namespaces, git repositories, stopped-app state and per-namespace edited files are all restored now. This fixes the upgrade itself; data lost by an upgrade you already ran is not recovered automatically — your original 1.x database is untouched at `storage.db.kotlin-bak`, so contact us and it can be re-imported.
- **macOS: updating no longer freezes the launcher.** After pressing Update the window kept showing "Updating…" with every button — including Cancel — disabled, and the launcher stayed unusable until it was quit and started again. The window now refreshes itself once the new version is running, Cancel always works, and the buttons no longer shift around when the update starts.

## Updates
- Mailpit 1.31.0, pgAdmin 9.17 and ZooKeeper 3.9.5.
